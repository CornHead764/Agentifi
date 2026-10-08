package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The alert sweep. Gathering the facts is all this does; deciding is the pure
// domain.EvaluateAlerts. Running twice is safe: every hit carries a dedupe key
// behind a unique constraint, so a second sweep over the same facts writes
// nothing.

// AlertWindowDays is how far back a sweep looks for transaction-shaped alerts:
// not the whole ledger, or a first sweep would announce years of history.
const AlertWindowDays = 7

// BillWindowDays is how far ahead the upcoming-bills alert looks.
const BillWindowDays = 7

// Mailer is the email channel, or nil where the install has no relay. An alert
// still reaches the feed when this is nil.
type Mailer interface {
	Send(ctx context.Context, email provider.Email) error
}

// Pusher is the web push channel, or nil where no VAPID keypair is set.
// Sending is per browser subscription.
type Pusher interface {
	Send(ctx context.Context, sub provider.PushSubscription, payload any) error
}

type Alerts struct {
	base
	store *store.Store
	Mail  Mailer
	Push  Pusher
	Now   func() time.Time
}

func NewAlerts(st *store.Store) *Alerts {
	return &Alerts{base: newBase(st), store: st}
}

func (a *Alerts) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

type AlertRun struct {
	// Fired is what the evaluation decided; Written is how much was new. The gap
	// is the dedupe working.
	Fired   int
	Written int
	// A mail failure is not a failed sweep: the alert is in the feed either way.
	Mailed     int
	MailFailed int
	// PushDropped counts subscriptions the push service said were gone, which
	// are deleted rather than retried.
	Pushed      int
	PushDropped int
}

func (a *Alerts) Run(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (AlertRun, error) {
	inputs, err := a.gather(ctx, spaceID, userID)
	if err != nil {
		return AlertRun{}, err
	}
	hits := domain.EvaluateAlerts(inputs)

	recipient, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return AlertRun{}, err
	}
	channels, err := a.channels(ctx, spaceID, userID)
	if err != nil {
		return AlertRun{}, err
	}

	audience := AlertAudience{Email: recipient.Email, Channels: channels}

	run := AlertRun{Fired: len(hits)}
	var holding []string
	for _, hit := range hits {
		delivered, err := a.Deliver(ctx, spaceID, userID, hit, audience)
		if err != nil {
			return run, err
		}
		run.add(delivered)
		if hit.Ongoing {
			holding = append(holding, conditionKey(spaceID, hit.DedupeKey))
		}
	}
	// A state this sweep did not find again has ended, and its card leaves the
	// feed so the next occurrence is news again.
	if _, err := a.store.ResolveConditionsExcept(
		ctx, spaceID, userID, domain.SweptConditions, holding); err != nil {
		return run, err
	}
	return run, nil
}

// conditionKey is a state's key as the feed stores it, scoped to the space
// like the dedupe key.
func conditionKey(spaceID store.SpaceID, condition string) string {
	return spaceID.UUID().String() + ":" + condition
}

// ResolveCondition ends a state a connector reported, for everybody in the
// space. condition is the hit's DedupeKey, or a prefix of it.
func (a *Alerts) ResolveCondition(ctx context.Context, spaceID store.SpaceID, condition string) error {
	_, err := a.store.ResolveConditions(ctx, spaceID, conditionKey(spaceID, condition))
	return err
}

// AlertAudience is who a sweep writes to and which channels each alert may
// reach them on, read once per sweep.
type AlertAudience struct {
	Email    string
	Channels map[domain.AlertType]AlertChannels
}

// Delivery is AlertRun's counts for one alert.
type Delivery struct {
	Written     bool
	Mailed      int
	MailFailed  int
	Pushed      int
	PushDropped int
}

func (r *AlertRun) add(one Delivery) {
	if one.Written {
		r.Written++
	}
	r.Mailed += one.Mailed
	r.MailFailed += one.MailFailed
	r.Pushed += one.Pushed
	r.PushDropped += one.PushDropped
}

// Deliver writes one alert to the feed and sends it wherever the rules ask.
// Nothing is sent unless the feed write was new, or the dedupe would not stop
// the same warning going out every hour.
func (a *Alerts) Deliver(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
	hit domain.AlertHit, to AlertAudience,
) (Delivery, error) {
	notification := store.Notification{
		AlertType: hit.Type,
		Title:     hit.Title,
		Body:      hit.Body,
		URL:       hit.URL,
		// The "fires once" constraint is per person, so without the space somebody
		// in two households hears about the first one only.
		DedupeKey: spaceID.UUID().String() + ":" + hit.DedupeKey,
	}
	if hit.Ongoing {
		// The open-condition index keeps the state unique while it lasts; the
		// dedupe key only tells this stretch of it from the last.
		notification.ConditionKey = conditionKey(spaceID, hit.DedupeKey)
		notification.DedupeKey = notification.ConditionKey + ":" +
			a.now().UTC().Format(time.RFC3339Nano)
	}
	written, err := a.store.RecordNotification(ctx, spaceID, userID, &notification)
	if err != nil {
		return Delivery{}, err
	}
	if !written {
		return Delivery{}, nil
	}

	out := Delivery{Written: true}
	if a.Push != nil && to.Channels[hit.Type].Push {
		out.Pushed, out.PushDropped = a.push(ctx, spaceID, userID, hit)
	}
	if a.Mail != nil && to.Channels[hit.Type].Email && to.Email != "" {
		if err := a.Mail.Send(ctx, provider.Email{
			To: to.Email, Subject: hit.Title, Body: emailBody(hit),
		}); err != nil {
			// A relay that is down does not fail the sweep, and nothing retries: a
			// retry queue would eventually send a week-old warning.
			out.MailFailed++
		} else {
			out.Mailed++
		}
	}
	return out, nil
}

// DeliverToSpace writes one alert to every member who could act on it, each
// by their own channel choices, for a connector alert that has a space but no
// person. Viewers are left out: they cannot act on it.
func (a *Alerts) DeliverToSpace(
	ctx context.Context, spaceID store.SpaceID, hit domain.AlertHit,
) (AlertRun, error) {
	members, err := a.store.ListMemberships(ctx, spaceID)
	if err != nil {
		return AlertRun{}, err
	}
	run := AlertRun{Fired: 1}
	for _, member := range members {
		if !member.IsAccepted() || !member.Role.CanWrite() {
			continue
		}
		recipient, err := a.store.GetUser(ctx, member.UserID)
		if err != nil {
			return run, err
		}
		channels, err := a.channels(ctx, spaceID, member.UserID)
		if err != nil {
			return run, err
		}
		delivered, err := a.Deliver(ctx, spaceID, member.UserID, hit,
			AlertAudience{Email: recipient.Email, Channels: channels})
		if err != nil {
			return run, err
		}
		run.add(delivered)
	}
	return run, nil
}

// push sends one alert to every browser this person has allowed. A
// subscription the push service says is gone is deleted rather than retried;
// any other failure is counted and ignored.
func (a *Alerts) push(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID, hit domain.AlertHit,
) (sent, dropped int) {
	subscriptions, err := a.store.ListPushSubscriptions(ctx, spaceID, userID)
	if err != nil {
		return 0, 0
	}
	payload := map[string]any{"title": hit.Title, "body": hit.Body, "url": hit.URL}
	for _, one := range subscriptions {
		err := a.Push.Send(ctx, one.Credentials(), payload)
		switch {
		case err == nil:
			sent++
			_ = a.store.TouchPushSubscription(ctx, one.ID)
		case errors.Is(err, provider.ErrPushSubscriptionGone):
			dropped++
			_ = a.store.DeletePushSubscriptionByEndpoint(ctx, spaceID, one.Endpoint)
		}
	}
	return sent, dropped
}

// emailBody is the alert plus where to look at it, as plain text.
func emailBody(hit domain.AlertHit) string {
	body := hit.Body
	if hit.URL != "" {
		body += "\n\nOpen Agentifi at " + hit.URL + " to see it."
	}
	return body
}

// RunForEveryone sweeps per accepted member, because alert rules and
// thresholds are per person.
func (a *Alerts) RunForEveryone(ctx context.Context, spaceID store.SpaceID) (AlertRun, error) {
	members, err := a.store.ListMemberships(ctx, spaceID)
	if err != nil {
		return AlertRun{}, err
	}
	var total AlertRun
	for _, member := range members {
		if member.AcceptedAt == nil {
			continue
		}
		run, err := a.Run(ctx, spaceID, member.UserID)
		if err != nil {
			return total, err
		}
		total.Fired += run.Fired
		total.Written += run.Written
		total.Mailed += run.Mailed
		total.MailFailed += run.MailFailed
		total.Pushed += run.Pushed
		total.PushDropped += run.PushDropped
	}
	return total, nil
}

func (a *Alerts) gather(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (domain.AlertInputs, error) {
	today := domain.DateOf(a.now())
	rules, err := a.rules(ctx, spaceID, userID)
	if err != nil {
		return domain.AlertInputs{}, err
	}

	// The whole ledger, because a balance is the sum of all of it; the window
	// narrows only what is reported. Through LoadPostings, so a linked refund is
	// filed where every screen files it.
	postings, byID, err := LoadPostings(ctx, a.store, spaceID, store.TransactionQuery{})
	if err != nil {
		return domain.AlertInputs{}, err
	}
	all := make([]store.Transaction, 0, len(postings))
	for _, posting := range postings {
		id, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return domain.AlertInputs{}, err
		}
		all = append(all, byID[id])
	}
	// Closed and deleted accounts included so every posting has a name.
	accounts, err := a.store.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return domain.AlertInputs{}, err
	}
	categories, err := a.store.ListCategories(ctx, spaceID, true)
	if err != nil {
		return domain.AlertInputs{}, err
	}

	settled, err := a.settledSlots(ctx, spaceID, today)
	if err != nil {
		return domain.AlertInputs{}, err
	}
	plan, err := a.schedule(ctx, spaceID, today, settled)
	if err != nil {
		return domain.AlertInputs{}, err
	}
	lists, err := a.watchlists(ctx, spaceID, postings, byID, categories, today)
	if err != nil {
		return domain.AlertInputs{}, err
	}

	feeCategories := feeCategoryIDs(categories)
	since := today.AddDays(-AlertWindowDays)

	inputs := domain.AlertInputs{
		Today: today, Rules: rules, Bills: plan.Bills, Watchlists: lists,
		Expected: plan.Expected, ExpectedIncome: plan.Income, Series: plan.Series,
	}
	inputs.Fulfilled = settledOccurrences(all, plan, since, today)
	inputs.OverdueRefunds = overdueRefunds(settled, plan, today)
	if inputs.Goals, err = a.goalStandings(ctx, spaceID, all, today); err != nil {
		return domain.AlertInputs{}, err
	}
	if err := a.planStandings(ctx, spaceID, today, &inputs); err != nil {
		return domain.AlertInputs{}, err
	}
	inputs.ThisMonthSpend, inputs.ThisMonthIncome = spendAndIncome(postings, domain.MonthOf(today))
	inputs.LastMonthSpend, _ = spendAndIncome(postings, domain.MonthOf(today).Shift(-1))
	byAccount := make(map[domain.ID][]domain.Posting, len(accounts))
	for _, posting := range postings {
		byAccount[posting.Txn.AccountID] = append(byAccount[posting.Txn.AccountID], posting)
	}
	for _, account := range accounts {
		if account.IsClosed || account.IsDeleted || account.IsIgnored() {
			continue
		}
		one := store.DomainAccount(account)
		inputs.Accounts = append(inputs.Accounts, domain.AlertAccount{
			ID:      one.ID,
			Name:    account.Name,
			Kind:    one.Kind,
			Balance: domain.AccountBalance(one, byAccount[one.ID]),
		})
	}

	names := make(map[domain.ID]string, len(accounts))
	for _, account := range accounts {
		names[domain.ID(account.ID.String())] = account.Name
	}
	for _, posting := range postings {
		txn := posting.Txn
		// Thresholds are in the household's currency, so compare the converted
		// posting amount, not the row's native txn.Amount.
		amount := posting.Amount()
		// A pending row earns an alert by being old, so it is gathered before the
		// window check.
		if txn.IsPending {
			inputs.Pendings = append(inputs.Pendings, domain.AlertTransaction{
				ID:          txn.ID,
				AccountID:   txn.AccountID,
				AccountName: names[txn.AccountID],
				On:          txn.Date,
				Amount:      amount,
				Payee:       txn.Payee,
			})
		}
		if txn.Date.Before(since) {
			continue
		}
		// Not `CategoryID == ""`: that is true of every split row, because saving
		// splits nulls the parent (see domain.Transaction).
		uncategorized := txn.IsUncategorized()
		if uncategorized {
			inputs.UncategorizedCount++
		}
		inputs.Transactions = append(inputs.Transactions, domain.AlertTransaction{
			ID:            txn.ID,
			AccountID:     txn.AccountID,
			AccountName:   names[txn.AccountID],
			On:            txn.Date,
			Amount:        amount,
			Payee:         txn.Payee,
			IsBankFee:     feeCategories[txn.CategoryID],
			Uncategorized: uncategorized,
			Bookkeeping:   !txn.Source.IsCashFlow(),
		})
	}
	return inputs, nil
}

// planStandings computes this month's spending plan rather than reading the
// materialized columns, which are written only when a writer opens the plan
// screen and so may be last month's. Compute persists nothing: a scheduled
// sweep must not change what a closed month says.
func (a *Alerts) planStandings(
	ctx context.Context, spaceID store.SpaceID, today domain.Date, inputs *domain.AlertInputs,
) error {
	engine := NewPlan(a.store)
	engine.Now = a.now

	month := domain.MonthOf(today)
	view, err := engine.Compute(ctx, spaceID, month)
	if err != nil {
		return err
	}
	computed, ok := view.Results[month]
	if !ok {
		// No plan rows: nothing to say, carried by HasAvailableToSpend.
		return nil
	}

	inputs.AvailableToSpend = computed.LeftThisMonth()
	inputs.HasAvailableToSpend = true

	for _, status := range computed.Envelopes {
		inputs.Envelopes = append(inputs.Envelopes, domain.AlertEnvelope{
			ID:   status.EnvelopeID,
			Name: status.Name,
			// Budget, not Target: rollover gives an envelope more than its target.
			Target: status.Budget(),
			Spent:  status.Spent,
		})
	}
	return nil
}

// goalStandings is each savings goal against the rate its target date implies,
// through domain.GoalProgressFor like the goal card, so the two cannot
// disagree.
func (a *Alerts) goalStandings(
	ctx context.Context, spaceID store.SpaceID, ledger []store.Transaction, today domain.Date,
) ([]domain.AlertGoal, error) {
	rows, err := a.conn().Query(ctx,
		`SELECT id, name, target_amount, target_on, completed_on, txn_ids,
		        withdrawal_txn_ids, spending_txn_ids, is_taken_from_plan
		   FROM goals
		  WHERE space_id = $1 AND is_deleted = false AND closed_on IS NULL
		  ORDER BY created_at, id`, spaceID.UUID())
	if err != nil {
		return nil, fmt.Errorf("service: list goals: %w", err)
	}
	defer rows.Close()

	type saving struct {
		goal        domain.Goal
		links       store.GoalLinks
		completedOn *time.Time
	}
	var goals []saving
	for rows.Next() {
		var (
			one         saving
			id          uuid.UUID
			name        string
			target      dbconv.Number
			targetOn    *time.Time
			completedOn *time.Time
		)
		// Without the direction columns every withdrawal reads as a contribution
		// and a raided goal alerts as though it were funded.
		if err := rows.Scan(&id, &name, &target, &targetOn, &completedOn,
			&one.links.TxnIDs, &one.links.WithdrawalTxnIDs, &one.links.SpendingTxnIDs,
			&one.links.IsTakenFromPlan); err != nil {
			return nil, fmt.Errorf("service: list goals: %w", err)
		}
		amount, _, err := dbconv.ReadNullMoney(target, "goals.target_amount")
		if err != nil {
			return nil, err
		}
		one.goal = domain.Goal{
			ID:              domain.ID(id.String()),
			Name:            name,
			TargetAmount:    amount,
			TargetOn:        dbconv.ReadNullDate(targetOn),
			IsTakenFromPlan: one.links.IsTakenFromPlan,
		}
		one.links.GoalID = id
		one.completedOn = completedOn
		goals = append(goals, one)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: list goals: %w", err)
	}
	if len(goals) == 0 {
		return nil, nil
	}

	byID := make(map[uuid.UUID]store.Transaction, len(ledger))
	for _, row := range ledger {
		byID[row.ID] = row
	}

	out := make([]domain.AlertGoal, 0, len(goals))
	for _, one := range goals {
		contributions := store.GoalContributions(one.links, store.LedgerLookup(byID))
		progress := domain.GoalProgressFor(one.goal, contributions, today)
		out = append(out, domain.AlertGoal{
			ID:          one.goal.ID,
			Name:        one.goal.Name,
			Needed:      progress.MonthlyNeeded,
			HasNeeded:   progress.HasMonthlyNeeded,
			Contributed: progress.ContributedThisMonth,
			// A goal marked finished is finished, whatever the arithmetic says.
			IsComplete: progress.IsComplete() || one.completedOn != nil,
		})
	}
	return out, nil
}

// RefundOverdueDays bounds the refund alert's lookback. The dedupe key is the
// series and expected day, so an unbounded lookback would announce every
// refund ever recorded on the first sweep.
const RefundOverdueDays = 30

// settledOccurrences is the series slots the ledger has settled within the
// alert window, by the Bills & Income page's rule: a row claims a slot through
// series_id and series_due_on, and settles it only once posted (dated today or
// earlier) and neither skipped nor deleted. A forecast claiming next month's
// slot is not a payment.
func settledOccurrences(
	rows []store.Transaction, plan schedule, since, today domain.Date,
) []domain.AlertFulfilment {
	var out []domain.AlertFulfilment
	for _, row := range rows {
		if row.SeriesID == uuid.Nil || row.IsDeleted ||
			row.EstimateStatus == store.SkippedEstimate {
			continue
		}
		if row.Date.After(today) || row.Date.Before(since) {
			continue
		}
		seriesID := domain.ID(row.SeriesID.String())
		kind, known := plan.Kinds[seriesID]
		if !known {
			// The series is paused or deleted, so there is no name to report.
			continue
		}
		mapped := store.DomainTransaction(row)
		out = append(out, domain.AlertFulfilment{
			TxnID:    domain.ID(row.ID.String()),
			SeriesID: seriesID,
			Name:     plan.Names[seriesID],
			Kind:     kind,
			On:       row.Date,
			Amount:   mapped.PrimaryAmount(),
		})
	}
	return out
}

// overdueRefunds is every refund whose expected day has passed with no settled
// row on its slot; a forecast row on the slot is not the refund.
func overdueRefunds(
	settled map[domain.ID]map[domain.Date]bool, plan schedule, today domain.Date,
) []domain.AlertRefund {
	var refunds []domain.Series
	for _, one := range plan.Series {
		if one.Kind == domain.SeriesRefund {
			refunds = append(refunds, one)
		}
	}
	if len(refunds) == 0 {
		return nil
	}

	// Through yesterday: a refund expected today is not yet late.
	late := domain.ExpectedOccurrences(
		refunds, today.AddDays(-RefundOverdueDays), today.AddDays(-1), settled, plan.BillConnect, nil, nil)

	out := make([]domain.AlertRefund, 0, len(late))
	for _, one := range late {
		out = append(out, domain.AlertRefund{
			SeriesID:   one.SeriesID,
			Name:       plan.Names[one.SeriesID],
			ExpectedOn: one.DueOn,
			Amount:     one.Amount,
		})
	}
	return out
}

// settledSlots is which series occurrences the ledger has closed
// (domain.SettledSlots, as the Bills screen reads it). Unbounded by date, and
// loaded apart from the sweep's ledger, because it turns on deleted rows (a
// skip is a deleted tombstone) and estimates, which that ledger excludes.
func (a *Alerts) settledSlots(
	ctx context.Context, spaceID store.SpaceID, today domain.Date,
) (map[domain.ID]map[domain.Date]bool, error) {
	rows, err := a.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
		HoldsASlot: true, IncludeDeleted: true, IncludeEstimates: true,
	})
	if err != nil {
		return nil, err
	}
	_, settled := domain.SettledSlots(store.SlotHolders(rows), today)
	return settled, nil
}

// spendAndIncome totals one month as magnitudes, by the report rule: transfers
// and excluded rows are out, or a card payment would double every purchase.
func spendAndIncome(postings []domain.Posting, month domain.Month) (spend, income domain.Money) {
	spend, income = domain.Zero, domain.Zero
	for _, posting := range postings {
		if domain.MonthOf(posting.Txn.ReportingDate(domain.DateEffective)) != month {
			continue
		}
		// The report predicate itself, not a hand-copied subset: it also drops
		// paired transfers and opening balances.
		if !domain.CountsAsIncomeOrExpense(posting, false) {
			continue
		}
		amount := posting.Amount()
		if amount.IsExpense() {
			spend = spend.Add(amount.Abs())
		} else {
			income = income.Add(amount)
		}
	}
	return spend, income
}

// schedule is one read of the series table: this week's bills, everything
// due over the projection window, this month's predicted income, and each
// series' name and kind.
type schedule struct {
	Bills    []domain.AlertBill
	Expected []domain.Occurrence
	Income   domain.Money
	Series   []domain.Series
	// BillConnect is kept so the overdue-refund sweep reads the same biller
	// figures without loading them again.
	BillConnect map[domain.ID][]domain.BillConnect
	Names       map[domain.ID]string
	Kinds       map[domain.ID]domain.SeriesKind
}

func (a *Alerts) schedule(
	ctx context.Context, spaceID store.SpaceID, today domain.Date,
	settled map[domain.ID]map[domain.Date]bool,
) (schedule, error) {
	out := schedule{
		Income: domain.Zero,
		Names:  map[domain.ID]string{},
		Kinds:  map[domain.ID]domain.SeriesKind{},
	}
	matcher := NewSeriesMatcher(a.store)
	rows, err := matcher.LoadSeriesRows(ctx,
		`SELECT `+seriesColumns+` FROM series
		  WHERE space_id = $1 AND is_active AND NOT is_deleted
		  ORDER BY id`, spaceID.UUID())
	if err != nil {
		return schedule{}, err
	}
	series := make([]domain.Series, 0, len(rows))
	for _, row := range rows {
		one := ToDomainSeries(row)
		series = append(series, one)
		out.Names[one.ID] = one.Label()
		out.Kinds[one.ID] = one.Kind
	}
	out.Series = series
	names := out.Names

	// Through the Bills screen's loader, so the alert names the same figure
	// (statement or estimate) the screen does.
	billConnect, err := NewBills(a.store).BillConnectFor(ctx, spaceID, nil)
	if err != nil {
		return schedule{}, err
	}
	out.BillConnect = billConnect
	manual, err := NewBills(a.store).PayManually(ctx, spaceID)
	if err != nil {
		return schedule{}, err
	}
	for _, one := range manual {
		names[one.BillID] = one.Connection.DisplayName()
	}

	// The projection window is the longer, so the week's bills are taken from
	// inside it. Settled slots are dropped as the Bills screen drops them, so a
	// bill paid early does not still alert as due.
	expected := domain.ExpectedOccurrences(
		series, today, today.AddDays(domain.ProjectionDays), settled, billConnect,
		ManualBills(manual), nil)

	out.Expected = expected
	month := domain.MonthOf(today)
	for _, one := range expected {
		if one.Kind == domain.SeriesIncome {
			// The income the bills-to-income share is a share of.
			if domain.MonthOf(one.DueOn) == month {
				out.Income = out.Income.Add(one.Amount)
			}
			continue
		}
		if !one.Kind.IsBill() || one.DueOn.After(today.AddDays(BillWindowDays)) {
			continue
		}
		// A pay-manually reminder is its statement.
		id := cmp.Or(one.SeriesID, one.BillID)
		out.Bills = append(out.Bills, domain.AlertBill{
			ID: id, Name: names[id], DueOn: one.DueOn, Amount: one.Amount,
		})
	}
	return out, nil
}

// watchlists is each watchlist's spend against its target this month, through
// the Watchlists page's matcher. No target is skipped, not treated as zero.
func (a *Alerts) watchlists(
	ctx context.Context, spaceID store.SpaceID, postings []domain.Posting,
	rows map[uuid.UUID]store.Transaction, categories []store.Category, today domain.Date,
) ([]domain.AlertWatchlist, error) {
	stored, err := a.store.ListWatchlists(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	var wanted []store.Watchlist
	for _, one := range stored {
		if one.HasTarget && one.TargetAmount.IsPositive() {
			wanted = append(wanted, one)
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(wanted))
	for _, one := range wanted {
		ids = append(ids, one.FilterID)
	}
	filters, facets, err := PrepareFilters(ctx, a.store, spaceID, ids, rows)
	if err != nil {
		return nil, err
	}
	byCategory := make(map[domain.ID]domain.Category, len(categories))
	for _, one := range categories {
		byCategory[domain.ID(one.ID.String())] = store.DomainCategory(one)
	}
	matcher := domain.WatchlistMatcherFor(filters, facets, byCategory)

	var out []domain.AlertWatchlist
	for _, one := range wanted {
		if _, ok := filters[domain.ID(one.FilterID.String())]; !ok {
			// A missing filter matches nothing, and zero spent would read as a fact.
			continue
		}
		watchlist := domain.Watchlist{
			ID:           domain.ID(one.ID.String()),
			Name:         one.Name,
			FilterID:     domain.ID(one.FilterID.String()),
			TargetAmount: one.TargetAmount,
			HasTarget:    true,
		}
		summary := domain.SummarizeWatchlist(
			watchlist, postings, matcher, today, domain.DateEffective)
		out = append(out, domain.AlertWatchlist{
			ID: watchlist.ID, Name: one.Name, Spent: summary.ThisMonthSpent, Target: one.TargetAmount,
		})
	}
	return out, nil
}

// AlertChannels is where one alert may go. Kept apart from
// domain.AlertRuleView so a channel toggle cannot stop an alert being decided.
type AlertChannels struct {
	Email bool
	Push  bool
}

// channels resolves each alert's delivery, falling back to the catalog.
func (a *Alerts) channels(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (map[domain.AlertType]AlertChannels, error) {
	stored, err := a.store.ListAlertRules(ctx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	byType := make(map[domain.AlertType]store.AlertRule, len(stored))
	for _, one := range stored {
		if one.AccountID == uuid.Nil {
			byType[one.AlertType] = one
		}
	}

	out := make(map[domain.AlertType]AlertChannels, len(domain.AlertCatalog))
	for _, definition := range domain.AlertCatalog {
		rule, held := byType[definition.Type]
		if !held {
			out[definition.Type] = AlertChannels{
				Email: definition.DefaultsOn(domain.ChannelEmail),
				Push:  definition.DefaultsOn(domain.ChannelPush),
			}
			continue
		}
		// A channel the catalog says the alert cannot use stays off.
		out[definition.Type] = AlertChannels{
			Email: rule.ChannelEmail && definition.Allows(domain.ChannelEmail),
			Push:  rule.ChannelPush && definition.Allows(domain.ChannelPush),
		}
	}
	return out, nil
}

// rules resolves what this person has chosen, falling back to the catalog
// defaults the settings page shows, so domain.EvaluateAlerts gets decisions.
func (a *Alerts) rules(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
) (map[domain.AlertType]domain.AlertRuleView, error) {
	stored, err := a.store.ListAlertRules(ctx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	byType := make(map[domain.AlertType]store.AlertRule, len(stored))
	for _, one := range stored {
		if one.AccountID == uuid.Nil {
			byType[one.AlertType] = one
		}
	}

	out := make(map[domain.AlertType]domain.AlertRuleView, len(domain.AlertCatalog))
	for _, definition := range domain.AlertCatalog {
		rule, held := byType[definition.Type]
		if !held {
			out[definition.Type] = defaultRuleView(definition)
			continue
		}
		out[definition.Type] = domain.AlertRuleView{
			Enabled: rule.IsEnabled, Paused: rule.IsPaused,
			Amount: rule.ThresholdAmount, HasAmount: rule.HasThresholdAmount,
			Count: rule.ThresholdCount, HasCount: rule.HasThresholdCount,
			Percent: rule.ThresholdPercent, HasPercent: rule.HasThresholdPercent,
		}
	}
	return out, nil
}

func defaultRuleView(definition domain.AlertDefinition) domain.AlertRuleView {
	view := domain.AlertRuleView{Enabled: definition.DefaultEnabled()}
	switch definition.Threshold {
	case domain.ThresholdAmount:
		view.Amount, view.HasAmount = definition.DefaultAmount, true
	case domain.ThresholdCount:
		view.Count, view.HasCount = definition.DefaultCount, true
	case domain.ThresholdPercent:
		view.Percent = decimal.NewFromInt(int64(definition.DefaultPercent))
		view.HasPercent = true
	}
	return view
}

// feeCategoryIDs is "Fees & Charges" and everything beneath it, read from the
// category tree because the household renames categories.
func feeCategoryIDs(categories []store.Category) map[domain.ID]bool {
	byID := make(map[uuid.UUID]store.Category, len(categories))
	for _, one := range categories {
		byID[one.ID] = one
	}
	out := map[domain.ID]bool{}
	for _, one := range categories {
		for cursor, depth := one, 0; depth < 8; depth++ {
			if isFeeCategory(cursor) {
				out[domain.ID(one.ID.String())] = true
				break
			}
			parent, ok := byID[cursor.ParentID]
			if !ok {
				break
			}
			cursor = parent
		}
	}
	return out
}

func isFeeCategory(one store.Category) bool {
	// Simplifi's stable marker survives a rename; the name, case-insensitive,
	// is the fallback for a hand-built tree.
	if one.KnownCategoryID == domain.KnownCategoryFeesAndCharges {
		return true
	}
	if one.KnownCategoryID != "" && strings.Contains(strings.ToUpper(one.KnownCategoryID), "FEE") {
		return true
	}
	return strings.EqualFold(one.Name, "Fees & Charges")
}
