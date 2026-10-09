package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// The bills connector's orchestration: what a pull does to the record, and the
// one loader every occurrence reader calls.
//
// Nothing here writes a `series` column: the domain merges a bill into an
// occurrence at read time, so unlinking a reminder or turning off an adjust
// switch takes effect on the next read with nothing to undo.

type Bills struct {
	base
	store *store.Store
	// Agent drives a provider's portal. Nil is a build with no engine, where every
	// connect answers ErrBillsAgentUnavailable.
	Agent BillsAgent
	// Documents is where a fetched statement is filed. Nil keeps the figures and
	// skips the files.
	Documents *Documents
	// Alerts is how a parked challenge or a stopped pull reaches a person.
	// Nil delivers nothing and changes nothing else.
	Alerts *Alerts
	// Answerers are the automatic answers tried, in order, before anybody is told
	// about a challenge. Empty, every challenge ends as a push notification.
	Answerers []BillChallengeAnswerer
	// MailedCode waits for a code a typed sign-in was sent to reach one of the
	// household's mailboxes, and HasMailbox says there is one it could reach. Nil
	// leaves the code for the person to type.
	MailedCode func(ctx context.Context, spaceID store.SpaceID, connection store.BillConnection, since time.Time) (string, bool)
	HasMailbox func(ctx context.Context, spaceID store.SpaceID) bool
	// Between is the pause between connections in one scheduled pass, so a
	// household with six does not open six browser contexts at once.
	Between time.Duration
	// PullTimeout bounds one connection's pull; zero takes pullTimeout.
	PullTimeout time.Duration
	Log         *slog.Logger
	Now         func() time.Time
}

func NewBills(st *store.Store) *Bills { return &Bills{base: newBase(st), store: st} }

func (b *Bills) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now().UTC()
}

func (b *Bills) pullBound() time.Duration {
	if b.PullTimeout > 0 {
		return b.PullTimeout
	}
	return pullTimeout
}

func (b *Bills) log() *slog.Logger {
	if b.Log == nil {
		return slog.Default()
	}
	return b.Log
}

// IngestResult is what one pull did to the record. New, Amended and Unchanged
// add up to what was handed in; a second run over the same pull reports
// everything unchanged.
type IngestResult struct {
	New        int
	Amended    int
	Unchanged  int
	Superseded int
	// Bills is the record as it stands after the pull, for the subaccount that
	// was pulled.
	Bills []store.Bill
	// Statement is the household account whose statement figures this write
	// changed, nil when it changed none.
	Statement uuid.UUID
}

// Ingest writes what a pull found for one subaccount.
//
// A bill's identity is its subaccount, due date and invoice (see billCycle), not
// the provider's statement id: not every provider issues one and an email
// carries only a message-id. The exception is a statement restated under another
// due date, recognised by id and issue date, which keeps its first due date (see
// statementOf). So the same cycle with a changed amount is an amendment, and the
// same figures twice change nothing.
//
// A newer bill supersedes an open one by domain.SupersededBills, because a
// provider that reissues a cycle just stops mentioning the old statement.
//
// A billed account linked to a household account has that account's statement
// figures written from its newest bill (domain.StatementAfterBill). This is done
// here so mailed, pulled and API-created bills all fill the card the same way.
//
// For the same reason the reminder linked to the subaccount is offered the bank
// rows around each new or changed bill (SeriesMatcher.MatchHistory): a charge
// that missed its slot against the reminder's estimate may fit the bill's
// figure. A row that takes a slot then receives the bill's statement once it
// is filed.
func (b *Bills) Ingest(
	ctx context.Context, spaceID store.SpaceID, subaccountID uuid.UUID, pulled []store.Bill,
) (IngestResult, error) {
	subaccount, err := b.store.GetBillSubaccount(ctx, spaceID, subaccountID)
	if err != nil {
		return IngestResult{}, err
	}
	existing, err := b.store.ListBills(ctx, spaceID, subaccount.ID)
	if err != nil {
		return IngestResult{}, err
	}
	known := make(map[billCycle]store.Bill, len(existing))
	statements := make(map[statementKey]store.Bill, len(existing))
	for _, one := range existing {
		known[cycleOf(one)] = one
		if key, ok := statementOf(one); ok {
			statements[key] = one
		}
	}

	var out IngestResult
	var changed []domain.Date
	for _, one := range pulled {
		one.SubaccountID = subaccount.ID
		if one.Status == "" {
			one.Status = domain.BillOpen
		}
		if one.Currency == "" {
			one.Currency = subaccountCurrency
		}
		if one.FetchedAt.IsZero() {
			one.FetchedAt = b.now()
		}
		before, seen := known[cycleOf(one)]
		if !seen {
			if key, ok := statementOf(one); ok {
				if stated, restated := statements[key]; restated {
					one.DueOn = stated.DueOn
					if one.AutopayOn.IsZero() {
						one.AutopayOn = stated.AutopayOn
					}
					before, seen = known[cycleOf(one)]
				}
			}
		}
		if seen {
			one = mergedBill(before, one)
		}
		switch {
		case !seen:
			if err := b.store.UpsertBill(ctx, spaceID, &one, false); err != nil {
				return out, err
			}
			out.New++
		case sameBill(before, one):
			out.Unchanged++
			continue
		default:
			one.ID = before.ID
			amended := !before.AmountDue.Equal(one.AmountDue)
			if err := b.store.UpsertBill(ctx, spaceID, &one, amended); err != nil {
				return out, err
			}
			if amended {
				out.Amended++
			} else {
				out.Unchanged++
			}
		}
		known[cycleOf(one)] = one
		if key, ok := statementOf(one); ok {
			statements[key] = one
		}
		changed = append(changed, one.DueOn)
	}

	open, err := b.store.ListOpenBills(ctx, spaceID, []uuid.UUID{subaccount.ID})
	if err != nil {
		return out, err
	}
	facts := make([]domain.Bill, 0, len(open))
	for _, one := range open {
		facts = append(facts, one.DomainBill())
	}
	for _, stale := range domain.SupersededBills(facts) {
		id, err := store.ParseID(stale.ID)
		if err != nil {
			return out, err
		}
		if err := b.store.SetBillStatus(ctx, spaceID, id, domain.BillSuperseded); err != nil {
			return out, err
		}
		out.Superseded++
	}

	links, err := b.store.ListSeriesBillLinks(ctx, spaceID, nil)
	if err != nil {
		return out, err
	}
	var linked []uuid.UUID
	for _, link := range links {
		if link.SubaccountID == subaccount.ID {
			linked = append(linked, link.SeriesID)
		}
	}
	if _, err := b.matchBillHistory(ctx, spaceID, linked, changed); err != nil {
		return out, err
	}

	if out.Bills, err = b.store.ListBills(ctx, spaceID, subaccount.ID); err != nil {
		return out, err
	}
	if out.Statement, err = b.fillStatement(ctx, spaceID, subaccount, out.Bills); err != nil {
		return out, err
	}
	return out, nil
}

// fillStatement writes the linked account's statement figures from the newest
// of a billed account's bills, when the rule says they are newer than what the
// account holds, and answers the account when it wrote.
func (b *Bills) fillStatement(
	ctx context.Context, spaceID store.SpaceID, subaccount store.BillSubaccount, bills []store.Bill,
) (uuid.UUID, error) {
	if subaccount.AccountID == uuid.Nil {
		return uuid.Nil, nil
	}
	var newest store.Bill
	for _, one := range bills {
		if newest.ID == uuid.Nil || one.DueOn.After(newest.DueOn) {
			newest = one
		}
	}
	if newest.ID == uuid.Nil {
		return uuid.Nil, nil
	}
	account, err := b.store.GetAccount(ctx, spaceID, subaccount.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if account.IsDeleted {
		return uuid.Nil, nil
	}
	held := domain.CardStatement{
		Balance: account.StatementBalance, HasBalance: account.HasStatementBalance,
		MinimumDue: account.MinimumDue, HasMinimumDue: account.HasMinimumDue,
		DueOn: account.DueDate, FromBill: account.StatementBillID != uuid.Nil,
	}
	next, changed := domain.StatementAfterBill(held, domain.StatementBill{
		AmountDue: newest.AmountDue, MinimumDue: newest.MinimumDue,
		HasMinimumDue: newest.HasMinimumDue, DueOn: newest.DueOn,
	})
	if !changed {
		return uuid.Nil, nil
	}
	if err := b.store.SetAccountStatement(ctx, spaceID, account.ID, next, newest.ID); err != nil {
		return uuid.Nil, err
	}
	return account.ID, nil
}

// subaccountCurrency is what a bill with no stated currency is in. The engine
// reaches United States providers only.
const subaccountCurrency = "USD"

// billCycle is a bill's identity within its subaccount: the due date, and the
// invoice that tells apart bills due the same day.
type billCycle struct {
	due     domain.Date
	invoice string
}

func cycleOf(bill store.Bill) billCycle {
	return billCycle{due: bill.DueOn, invoice: bill.Invoice}
}

// statementKey is one statement as its provider names it: the provider's own
// id and the day the statement was issued.
type statementKey struct {
	external string
	issued   domain.Date
}

// statementOf is the key a restated statement is recognised by, when the bill
// carries both halves. A provider may restate a closed cycle under a different
// due date, and the due date is part of the identity, so without this the cycle
// is filed twice. Both halves are required because some providers fall back to a
// fixed id ("undated", "owed") when they have no statement number.
func statementOf(bill store.Bill) (statementKey, bool) {
	if bill.ExternalID == "" || bill.IssuedOn.IsZero() {
		return statementKey{}, false
	}
	return statementKey{external: bill.ExternalID, issued: bill.IssuedOn}, true
}

// mergedBill is what a cycle's row holds when another statement of it
// arrives. A mail onto a bill a provider or a person wrote only fills what is
// empty; anything else wins field by field, keeping what it left empty. A mail
// never sets the status of a bill on file, so it cannot reopen a paid one.
func mergedBill(before, arriving store.Bill) store.Bill {
	if arriving.Source != store.BillSourceEmail {
		return filledFrom(arriving, before)
	}
	if before.Source != store.BillSourceEmail {
		return filledFrom(before, arriving)
	}
	merged := filledFrom(arriving, before)
	merged.Status = before.Status
	return merged
}

// filledFrom is bill with each field it left empty taken from other.
func filledFrom(bill, other store.Bill) store.Bill {
	if !bill.HasMinimumDue {
		bill.MinimumDue, bill.HasMinimumDue = other.MinimumDue, other.HasMinimumDue
	}
	if bill.Currency == "" {
		bill.Currency = other.Currency
	}
	if bill.IssuedOn.IsZero() {
		bill.IssuedOn = other.IssuedOn
	}
	if bill.PeriodStart.IsZero() {
		bill.PeriodStart = other.PeriodStart
	}
	if bill.PeriodEnd.IsZero() {
		bill.PeriodEnd = other.PeriodEnd
	}
	if bill.AutopayOn.IsZero() {
		bill.AutopayOn = other.AutopayOn
	}
	if bill.ExternalID == "" {
		bill.ExternalID = other.ExternalID
	}
	if bill.StatementURL == "" {
		bill.StatementURL = other.StatementURL
	}
	return bill
}

// sameBill says the two describe one statement identically. The fetch stamps
// are left out: a pull an hour later is not a different bill.
func sameBill(before, after store.Bill) bool {
	return before.AmountDue.Equal(after.AmountDue) &&
		before.HasMinimumDue == after.HasMinimumDue && before.MinimumDue.Equal(after.MinimumDue) &&
		before.Currency == after.Currency &&
		before.IssuedOn == after.IssuedOn &&
		before.PeriodStart == after.PeriodStart &&
		before.PeriodEnd == after.PeriodEnd &&
		before.AutopayOn == after.AutopayOn &&
		before.Status == after.Status &&
		before.Source == after.Source &&
		before.ExternalID == after.ExternalID &&
		before.StatementURL == after.StatementURL
}

// LinkAccount says which household account a billed account is (the card or loan
// its statements are of), or with a nil account that it is none. A new link is
// filled at once from the bills already on file.
func (b *Bills) LinkAccount(
	ctx context.Context, spaceID store.SpaceID, subaccountID, accountID uuid.UUID,
) (store.BillSubaccount, error) {
	if err := b.store.SetBillSubaccountAccount(ctx, spaceID, subaccountID, accountID); err != nil {
		return store.BillSubaccount{}, err
	}
	subaccount, err := b.store.GetBillSubaccount(ctx, spaceID, subaccountID)
	if err != nil || accountID == uuid.Nil {
		return subaccount, err
	}
	bills, err := b.store.ListBills(ctx, spaceID, subaccount.ID)
	if err != nil {
		return subaccount, err
	}
	_, err = b.fillStatement(ctx, spaceID, subaccount, bills)
	return subaccount, err
}

// Link attaches a reminder to a subaccount. Both ends are exclusive, so
// whatever either held is released first. The bank rows across every bill on
// file are then offered to the reminder again, since a linked reminder's slots
// weigh a charge against what each bill said.
func (b *Bills) Link(
	ctx context.Context, spaceID store.SpaceID, seriesID, subaccountID uuid.UUID,
) (store.SeriesBillLink, error) {
	if _, err := b.store.GetBillSubaccount(ctx, spaceID, subaccountID); err != nil {
		return store.SeriesBillLink{}, err
	}
	link, err := b.store.LinkSeriesBill(ctx, spaceID, seriesID, subaccountID)
	if err != nil {
		return store.SeriesBillLink{}, err
	}
	standing, err := b.store.ListStandingBills(ctx, spaceID, []uuid.UUID{subaccountID})
	if err != nil {
		return link, err
	}
	dues := make([]domain.Date, 0, len(standing))
	for _, one := range standing {
		dues = append(dues, one.DueOn)
	}
	_, err = b.matchBillHistory(ctx, spaceID, []uuid.UUID{seriesID}, dues)
	return link, err
}

// matchBillHistory offers each series the bank rows that could have paid a
// slot one of the bills due on dues claims, and answers how many it linked.
func (b *Bills) matchBillHistory(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []uuid.UUID, dues []domain.Date,
) (int, error) {
	if len(seriesIDs) == 0 || len(dues) == 0 {
		return 0, nil
	}
	first, last := dues[0], dues[0]
	for _, due := range dues[1:] {
		if due.Before(first) {
			first = due
		}
		if last.Before(due) {
			last = due
		}
	}
	matcher := NewSeriesMatcher(b.store)
	linked := 0
	for _, seriesID := range seriesIDs {
		series, err := matcher.GetSeries(ctx, spaceID, seriesID)
		if err != nil {
			return linked, err
		}
		from, _ := domain.BillPaymentDates(ToRecurrence(series), first)
		_, to := domain.BillPaymentDates(ToRecurrence(series), last)
		outcomes, err := matcher.MatchHistory(ctx, spaceID, []uuid.UUID{seriesID}, from, to)
		linked += len(outcomes)
		if err != nil {
			return linked, err
		}
	}
	return linked, nil
}

// BillAccountHistory is what matching one billed account's history found.
type BillAccountHistory struct {
	Subaccount store.BillSubaccount
	// SeriesID is the linked reminder; nil leaves nothing to match.
	SeriesID uuid.UUID
	// Matched is the bank rows this run linked to the reminder's slots.
	Matched int
	domain.BillHistory
	// CadenceGapDays is the usual gap between the bills' due dates when it
	// does not fit the reminder's period (domain.BillCadenceDisagrees), and
	// zero when it fits or there are too few bills to say.
	CadenceGapDays int
}

// MatchHistory offers each billed account's linked reminder every unlinked
// bank row within reach of every open and paid bill on file, the same pass a
// new link runs, then files the statements on the rows that hold a slot and
// tallies the bills against those rows.
func (b *Bills) MatchHistory(
	ctx context.Context, spaceID store.SpaceID, subaccounts []store.BillSubaccount,
) ([]BillAccountHistory, error) {
	links, err := b.store.ListSeriesBillLinks(ctx, spaceID, nil)
	if err != nil {
		return nil, err
	}
	seriesOf := make(map[uuid.UUID]uuid.UUID, len(links))
	for _, link := range links {
		seriesOf[link.SubaccountID] = link.SeriesID
	}
	matcher := NewSeriesMatcher(b.store)
	today := domain.DateOf(b.now())
	out := make([]BillAccountHistory, 0, len(subaccounts))
	for _, subaccount := range subaccounts {
		one := BillAccountHistory{Subaccount: subaccount, SeriesID: seriesOf[subaccount.ID]}
		if one.SeriesID == uuid.Nil {
			out = append(out, one)
			continue
		}
		standing, err := b.store.ListStandingBills(ctx, spaceID, []uuid.UUID{subaccount.ID})
		if err != nil {
			return nil, err
		}
		dues := make([]domain.Date, 0, len(standing))
		onFile := make([]domain.BillOnFile, 0, len(standing))
		for _, bill := range standing {
			dues = append(dues, bill.DueOn)
			onFile = append(onFile, domain.BillOnFile{DueOn: bill.DueOn, HasStatement: bill.DocumentID != uuid.Nil})
		}
		if one.Matched, err = b.matchBillHistory(ctx, spaceID, []uuid.UUID{one.SeriesID}, dues); err != nil {
			return nil, err
		}
		if err := b.store.ReconcileSeriesReceipts(ctx, spaceID, []uuid.UUID{one.SeriesID}); err != nil {
			return nil, err
		}
		series, err := matcher.GetSeries(ctx, spaceID, one.SeriesID)
		if err != nil {
			return nil, err
		}
		held, err := b.store.SeriesHeldSlots(ctx, spaceID, one.SeriesID)
		if err != nil {
			return nil, err
		}
		recurrence := ToRecurrence(series)
		one.BillHistory = domain.TallyBillHistory(recurrence, onFile, held, today)
		if gap, known := domain.BillDueGap(dues); known && domain.BillCadenceDisagrees(recurrence, gap) {
			one.CadenceGapDays = gap
		}
		out = append(out, one)
	}
	return out, nil
}

// Unlink detaches a reminder from its bill. Nothing is undone: the series
// never carried anything the bill wrote.
func (b *Bills) Unlink(ctx context.Context, spaceID store.SpaceID, seriesID uuid.UUID) error {
	return b.store.UnlinkSeriesBill(ctx, spaceID, seriesID)
}

// BillLinkHealth is where a linked series' provider stands, for the mark an
// occurrence carries: linked and quiet, or needing somebody to act.
type BillLinkHealth string

const (
	LinkHealthy      BillLinkHealth = "ok"
	LinkNotConnected BillLinkHealth = "not_connected"
	LinkNeedsSignIn  BillLinkHealth = "needs_sign_in"
	LinkChallenge    BillLinkHealth = "challenge"
	LinkFailed       BillLinkHealth = "failed"
)

// SeriesBillLinkInfo is the provider behind a linked series, whether or not a
// bill has arrived yet, so a reminder linked to a provider that cannot sign in
// is marked before any bill is missed.
type SeriesBillLinkInfo struct {
	ConnectionID    uuid.UUID
	Biller          domain.BillerID
	ConnectionLabel string
	SubaccountLabel string
	Health          BillLinkHealth
	// Autopay says the connection has an autopay rule; without one the
	// reminder is paid by hand.
	Autopay bool
}

// LinkHealth reads a connection's standing. A provider nobody signs in to (bills
// by mail, figures on the account sync) is never unhealthy for want of a
// session; otherwise a missing or refused session outranks the last pull's word.
func LinkHealth(connection store.BillConnection) BillLinkHealth {
	biller, known := domain.BillerByID(connection.Biller)
	bridged := !known || biller.Access == domain.AccessAPI || biller.Access == domain.AccessBrowser
	if bridged {
		if connection.NeedsSignIn {
			return LinkNeedsSignIn
		}
		if !connection.HasSession {
			return LinkNotConnected
		}
	}
	switch connection.LastPullStatus {
	case store.BillPullChallenge:
		return LinkChallenge
	case store.BillPullFailed:
		return LinkFailed
	case store.BillPullNeedsSignIn:
		return LinkNeedsSignIn
	}
	return LinkHealthy
}

// SeriesLinks is the provider behind each linked series, keyed the way
// BillConnectFor is. Unlike SeriesBills it answers for a link with no bill on
// file yet.
func (b *Bills) SeriesLinks(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []domain.ID,
) (map[domain.ID]SeriesBillLinkInfo, error) {
	keys := make([]uuid.UUID, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		key, err := store.ParseID(id)
		if err != nil {
			continue
		}
		keys = append(keys, key)
	}
	if len(seriesIDs) > 0 && len(keys) == 0 {
		return nil, nil
	}
	links, err := b.store.ListSeriesBillLinks(ctx, spaceID, keys)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	subaccounts, err := b.store.ListBillSubaccounts(ctx, spaceID, uuid.Nil)
	if err != nil {
		return nil, err
	}
	connections, err := b.store.ListBillConnections(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	subaccountByID := make(map[uuid.UUID]store.BillSubaccount, len(subaccounts))
	for _, one := range subaccounts {
		subaccountByID[one.ID] = one
	}
	connectionByID := make(map[uuid.UUID]store.BillConnection, len(connections))
	for _, one := range connections {
		connectionByID[one.ID] = one
	}
	out := make(map[domain.ID]SeriesBillLinkInfo, len(links))
	for _, link := range links {
		subaccount, found := subaccountByID[link.SubaccountID]
		if !found {
			continue
		}
		connection, found := connectionByID[subaccount.ConnectionID]
		if !found {
			continue
		}
		out[domain.ID(link.SeriesID.String())] = SeriesBillLinkInfo{
			ConnectionID:    connection.ID,
			Biller:          connection.Biller,
			ConnectionLabel: connection.DisplayName(),
			SubaccountLabel: subaccount.Label,
			Health:          LinkHealth(connection),
			Autopay:         connection.AutopayRule != domain.AutopayNone,
		}
	}
	return out, nil
}

// ManualBill is one pay-manually reminder (domain.PayManually) with the
// statement, billed account and connection it names.
type ManualBill struct {
	domain.ManualBill
	Bill       store.Bill
	Subaccount store.BillSubaccount
	Connection store.BillConnection
}

// PayManually is every pay-manually reminder in the space, read from each
// shown billed account's standing statements and paired payments. A hidden
// account is neither pulled nor reminded of.
func (b *Bills) PayManually(ctx context.Context, spaceID store.SpaceID) ([]ManualBill, error) {
	connections, err := b.store.ListBillConnections(ctx, spaceID)
	if err != nil || len(connections) == 0 {
		return nil, err
	}
	connectionByID := make(map[uuid.UUID]store.BillConnection, len(connections))
	for _, one := range connections {
		connectionByID[one.ID] = one
	}
	all, err := b.store.ListBillSubaccounts(ctx, spaceID, uuid.Nil)
	if err != nil {
		return nil, err
	}
	var shown []store.BillSubaccount
	var ids []uuid.UUID
	for _, one := range all {
		if one.IsSelected {
			shown = append(shown, one)
			ids = append(ids, one.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	links, err := b.store.ListSeriesBillLinks(ctx, spaceID, nil)
	if err != nil {
		return nil, err
	}
	linked := make(map[uuid.UUID]bool, len(links))
	for _, one := range links {
		linked[one.SubaccountID] = true
	}
	standing, err := b.store.ListStandingBills(ctx, spaceID, ids)
	if err != nil {
		return nil, err
	}
	payments, err := b.store.ListPairedBillPayments(ctx, spaceID, ids)
	if err != nil {
		return nil, err
	}

	facts := make(map[uuid.UUID]*domain.BilledAccountFacts, len(shown))
	accounts := make([]domain.BilledAccountFacts, 0, len(shown))
	for _, one := range shown {
		accounts = append(accounts, domain.BilledAccountFacts{
			ID:      domain.ID(one.ID.String()),
			Autopay: connectionByID[one.ConnectionID].AutopayRuleValue(),
			Linked:  linked[one.ID],
		})
	}
	for i := range accounts {
		facts[shown[i].ID] = &accounts[i]
	}
	bills := make(map[domain.ID]store.Bill, len(standing))
	for _, one := range standing {
		account := facts[one.SubaccountID]
		account.Statements = append(account.Statements, domain.StatementFacts{
			ID: domain.ID(one.ID.String()), DueOn: one.DueOn, IssuedOn: one.IssuedOn,
			AutopayOn: one.AutopayOn, AmountDue: one.AmountDue, Status: one.Status,
			MarkedPaid: one.MarkedPaidAt != nil,
		})
		bills[domain.ID(one.ID.String())] = one
	}
	for _, one := range payments {
		account := facts[one.SubaccountID]
		account.PaidOn = append(account.PaidOn, one.PaidOn)
	}

	subaccountByID := make(map[uuid.UUID]store.BillSubaccount, len(shown))
	for _, one := range shown {
		subaccountByID[one.ID] = one
	}
	var out []ManualBill
	for _, one := range domain.PayManually(accounts) {
		bill := bills[one.BillID]
		subaccount := subaccountByID[bill.SubaccountID]
		out = append(out, ManualBill{
			ManualBill: one, Bill: bill, Subaccount: subaccount,
			Connection: connectionByID[subaccount.ConnectionID],
		})
	}
	return out, nil
}

// ManualBills is what the domain reads of the pay-manually reminders.
func ManualBills(manual []ManualBill) []domain.ManualBill {
	out := make([]domain.ManualBill, 0, len(manual))
	for _, one := range manual {
		out = append(out, one.ManualBill)
	}
	return out
}

// SeriesBill is one statement linked to a series: the statement itself, and
// what the domain reads of it for the slot it speaks about.
type SeriesBill struct {
	Bill    store.Bill
	Connect domain.BillConnect
}

// BillConnectFor is the loader every occurrence reader calls: for each linked
// series, every open or paid bill on its subaccount, oldest first, with
// AutopayOn resolved (see BillPaysOn), because ExpectedOccurrences copies that
// field straight onto the occurrence. domain.SlotBill decides which slot each
// one speaks about.
//
// The adjust switch lives on the series and must not be applied here: the due
// date returned here also decides which slot the bill claims and whether it
// has a payment date at all.
//
// An empty seriesIDs means every linked series in the space.
func (b *Bills) BillConnectFor(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []domain.ID,
) (map[domain.ID][]domain.BillConnect, error) {
	bills, err := b.SeriesBills(ctx, spaceID, seriesIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.ID][]domain.BillConnect, len(bills))
	for seriesID, linked := range bills {
		out[seriesID] = SeriesBillConnects(linked)
	}
	return out, nil
}

// SeriesBillConnects is what the domain reads of one series' bills.
func SeriesBillConnects(bills []SeriesBill) []domain.BillConnect {
	out := make([]domain.BillConnect, 0, len(bills))
	for _, one := range bills {
		out = append(out, one.Connect)
	}
	return out
}

// SeriesBills is BillConnectFor with the statement each answer came from.
func (b *Bills) SeriesBills(
	ctx context.Context, spaceID store.SpaceID, seriesIDs []domain.ID,
) (map[domain.ID][]SeriesBill, error) {
	keys := make([]uuid.UUID, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		key, err := store.ParseID(id)
		if err != nil {
			// A series id with no link is skipped rather than failing every other
			// series' bill.
			continue
		}
		keys = append(keys, key)
	}
	if len(seriesIDs) > 0 && len(keys) == 0 {
		return nil, nil
	}

	links, err := b.store.ListSeriesBillLinks(ctx, spaceID, keys)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	subaccountIDs := make([]uuid.UUID, 0, len(links))
	for _, link := range links {
		subaccountIDs = append(subaccountIDs, link.SubaccountID)
	}

	standing, err := b.store.ListStandingBills(ctx, spaceID, subaccountIDs)
	if err != nil {
		return nil, err
	}
	if len(standing) == 0 {
		return nil, nil
	}
	bySubaccount := make(map[uuid.UUID][]store.Bill, len(links))
	for _, one := range standing {
		bySubaccount[one.SubaccountID] = append(bySubaccount[one.SubaccountID], one)
	}

	rules, err := b.autopayRules(ctx, spaceID, subaccountIDs)
	if err != nil {
		return nil, err
	}
	signs, err := b.occurrenceSigns(ctx, spaceID, links)
	if err != nil {
		return nil, err
	}

	out := make(map[domain.ID][]SeriesBill, len(links))
	for _, link := range links {
		seriesID := domain.ID(link.SeriesID.String())
		for _, bill := range bySubaccount[link.SubaccountID] {
			connect := domain.BillConnect{
				ID:        domain.ID(bill.ID.String()),
				DueOn:     bill.DueOn,
				Amount:    signs[seriesID].apply(bill.AmountDue),
				AutopayOn: BillPaysOn(bill, rules[link.SubaccountID]),
				Paid:      bill.Status == domain.BillPaid,
			}
			out[seriesID] = append(out[seriesID], SeriesBill{Bill: bill, Connect: connect})
		}
	}
	return out, nil
}

// BillPaysOn is the day one stored bill's money leaves: the provider's own
// stated date, else the connection's rule applied to the due date
// (domain.AutopayOn). The occurrence loader and the bills resource both call
// this so they cannot disagree.
func BillPaysOn(bill store.Bill, rule domain.AutopayRule) domain.Date {
	return domain.AutopayOn(
		domain.BillConnect{DueOn: bill.DueOn, AutopayOn: bill.AutopayOn}, rule)
}

// occurrenceSign is which way a series' occurrences point. A provider states
// a magnitude owed; an occurrence is signed like a posting, negative for money
// out, so an auto-adjusting bill cannot turn a charge into income.
type occurrenceSign bool

const (
	signAsExpense occurrenceSign = false
	signAsIncome  occurrenceSign = true
)

func (s occurrenceSign) apply(magnitude domain.Money) domain.Money {
	if s == signAsIncome {
		return magnitude.Abs()
	}
	return magnitude.Abs().Neg()
}

// occurrenceSigns is each linked series' own direction, taken from its
// estimate and, for a series with no estimate yet, from its kind.
func (b *Bills) occurrenceSigns(
	ctx context.Context, spaceID store.SpaceID, links []store.SeriesBillLink,
) (map[domain.ID]occurrenceSign, error) {
	ids := make([]uuid.UUID, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.SeriesID)
	}
	rows, err := NewSeriesMatcher(b.store).LoadSeriesRows(ctx,
		`SELECT `+seriesColumns+` FROM series WHERE space_id = $1 AND id = ANY($2)`,
		spaceID.UUID(), ids)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.ID]occurrenceSign, len(rows))
	for _, row := range rows {
		sign := signAsExpense
		if row.Amount.IsIncome() ||
			(row.Amount.IsZero() && domain.SeriesKind(row.Kind) == domain.SeriesIncome) {
			sign = signAsIncome
		}
		out[domain.ID(row.ID.String())] = sign
	}
	return out, nil
}

// autopayRules is each subaccount's connection's rule, for the providers that
// do not state a scheduled payment date of their own.
func (b *Bills) autopayRules(
	ctx context.Context, spaceID store.SpaceID, subaccountIDs []uuid.UUID,
) (map[uuid.UUID]domain.AutopayRule, error) {
	wanted := make(map[uuid.UUID]bool, len(subaccountIDs))
	for _, id := range subaccountIDs {
		wanted[id] = true
	}
	subaccounts, err := b.store.ListBillSubaccounts(ctx, spaceID, uuid.Nil)
	if err != nil {
		return nil, err
	}
	connections, err := b.store.ListBillConnections(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	byConnection := make(map[uuid.UUID]domain.AutopayRule, len(connections))
	for _, one := range connections {
		byConnection[one.ID] = one.AutopayRuleValue()
	}
	out := make(map[uuid.UUID]domain.AutopayRule, len(wanted))
	for _, one := range subaccounts {
		if wanted[one.ID] {
			out[one.ID] = byConnection[one.ConnectionID]
		}
	}
	return out, nil
}

// --- The engine: signing in, pulling, and the challenge that stops a pull ---
//
//   - The session that comes back is the session that is kept: providers
//     rotate cookies or tokens on every call, so the snapshot a pull returns is
//     re-sealed and the one that went in is stale.
//   - A challenge outlives the request that met it: an unattended pull that
//     meets a code prompt writes a row and stops, the automatic answerers are
//     tried, and only then is a person told.
//   - A stopped pull is reported once per failing streak.

// BillChallengeTTL is how long a parked challenge can be answered for. It must
// match the engine's idle sign-in reaper (connector.SessionTTL), or a challenge
// would offer a code field for a browser context that is gone.
const BillChallengeTTL = 20 * time.Minute

// BillChallengeAnswerer is an automatic answer to a parked challenge. The first
// that answers wins and names itself, which is what `answered_by` records; a
// challenge none of them answers reaches a person.
type BillChallengeAnswerer func(
	ctx context.Context, spaceID store.SpaceID,
	connection store.BillConnection, challenge store.BillChallenge,
) (code string, answeredBy string, answered bool)

// BillPullResult is how one pull ended. Status is the vocabulary of the
// connection's last_pull_status.
type BillPullResult struct {
	Status    string
	New       int
	Amended   int
	Unchanged int
	// Documents is how many statements this pull fetched and filed.
	Documents int
	// Challenge is the parked sign-in, when the pull stopped at one.
	Challenge *store.BillChallenge
	// Error is why the pull stopped, in the provider's words where it gave
	// any. Empty on success.
	Error string
	Notes []string
	// Screenshot is whether the page the pull stopped on was kept.
	Screenshot bool
}

// ErrBillChallengeExpired is an answer that arrived after the engine had
// already thrown the sign-in away.
var ErrBillChallengeExpired = errors.New(
	"bills: that sign-in has expired; pull again to be asked afresh")

func (b *Bills) Providers(ctx context.Context) ([]provider.BillProvider, error) {
	agent, err := b.agent()
	if err != nil {
		return nil, err
	}
	return agent.Providers(ctx)
}

// StartConnect signs in with credentials a person typed.
//
// The password (and any authenticator setup key) is held in memory and sealed onto the connection only once the sign-in lands, so a password the
// provider refused is never the one a nightly pull keeps trying.
//
// A secret with no code beside it mints its own code. The key goes to the agent
// too, because a browser provider can ask for a factor again later in the
// sign-in.
func (b *Bills) StartConnect(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
	username, password, code, secret string, factor domain.SecondFactor,
) (provider.BillConnectState, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return b.startConnect(ctx, spaceID, connection, username, password, code, secret, factor)
}

// startConnect is StartConnect at the provider and site of connection as the
// caller read it, so a check made on that reading holds for the sign-in.
func (b *Bills) startConnect(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	username, password, code, secret string, factor domain.SecondFactor,
) (provider.BillConnectState, error) {
	connectionID := connection.ID
	// Kept as the sign-in starts, with the username: it is the login's own
	// choice whether or not this sign-in lands, and the nightly pull reads it.
	if connection.SecondFactor != factor {
		if err := b.store.SetBillConnectionSecondFactor(ctx, spaceID, connectionID, factor); err != nil {
			return provider.BillConnectState{}, err
		}
	}
	agent, err := b.agent()
	if err != nil {
		return provider.BillConnectState{}, err
	}
	if code == "" && secret != "" {
		minted, err := totp.Code(secret, time.Now())
		if err != nil {
			return provider.BillConnectState{}, err
		}
		code = minted
	}
	state, err := agent.StartConnect(ctx, provider.BillConnectStart{
		Provider: string(connection.Biller), Profile: billProfileID(connection),
		Site:     connection.Site,
		Username: username, Password: password, Code: code, Secret: secret,
		SecondFactor: string(factor),
	})
	if err == nil {
		rememberSignIn(state.SessionID, spaceID, connectionID)
		rememberCredential(state.SessionID, store.BillCredential{
			Username: username, Password: password, TOTPSecret: secret,
		})
	}
	return state, err
}

// lastTyped is what was typed into each connection's latest sign-in that has
// not landed, so one that stopped on something passing can be run again
// without the password being typed again. It outlives a cancelled session,
// because closing the dialog over a failure cancels it, and lapses on the
// same hour as the sign-in owners.
//
// Only the person who typed it may run it again, and only at the provider and
// site it was typed for: a connection's site is anybody's edit, and a retry
// after one would hand the password to an address nobody typed it into.
var lastTyped expiring[uuid.UUID, typedSignIn]

type typedSignIn struct {
	space      store.SpaceID
	user       uuid.UUID
	biller     domain.BillerID
	site       string
	credential store.BillCredential
}

// HoldForRetry keeps what a person typed into a sign-in that started, for
// RetryConnect.
func HoldForRetry(
	spaceID store.SpaceID, userID uuid.UUID, connection store.BillConnection,
	username, password, secret string,
) {
	lastTyped.Put(connection.ID, typedSignIn{
		space: spaceID, user: userID, biller: connection.Biller, site: connection.Site,
		credential: store.BillCredential{Username: username, Password: password, TOTPSecret: secret},
	}, time.Now().Add(signInMemory))
}

// heldFor is the hold RetryConnect may use for this person on this
// connection as it stands.
func heldFor(spaceID store.SpaceID, userID uuid.UUID, connection store.BillConnection) (typedSignIn, bool) {
	held, ok := lastTyped.Get(connection.ID)
	if !ok || held.space != spaceID || held.user != userID ||
		held.biller != connection.Biller || held.site != connection.Site {
		return typedSignIn{}, false
	}
	return held, true
}

// ErrNothingToRetry is a retry with nothing typed to run again: the last
// sign-in landed, its hour lapsed, or the server restarted since.
var ErrNothingToRetry = errors.New("bills: nothing typed into the last sign-in is held any more")

// BillSignInRetryable says RetryConnect has a sign-in this person may run
// again on the connection.
func BillSignInRetryable(spaceID store.SpaceID, userID uuid.UUID, connection store.BillConnection) bool {
	_, ok := heldFor(spaceID, userID, connection)
	return ok
}

// RetryConnect starts the connection's last sign-in that did not land again,
// with what the same person typed into it.
func (b *Bills) RetryConnect(
	ctx context.Context, spaceID store.SpaceID, userID uuid.UUID, connectionID uuid.UUID,
) (provider.BillConnectState, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	held, ok := heldFor(spaceID, userID, connection)
	if !ok {
		return provider.BillConnectState{}, ErrNothingToRetry
	}
	return b.startConnect(ctx, spaceID, connection, held.credential.Username,
		held.credential.Password, "", held.credential.TOTPSecret, connection.SecondFactor)
}

// keptCredentials is the typed password, between the start of the sign-in and
// its landing, keyed by the engine's session id. It lapses on the same hour as
// the sign-in owners, so an abandoned dialog leaves nothing.
var keptCredentials expiring[string, store.BillCredential]

func rememberCredential(sessionID string, credential store.BillCredential) {
	if sessionID != "" {
		keptCredentials.Put(sessionID, credential, time.Now().Add(signInMemory))
	}
}

func takeCredential(sessionID string) (store.BillCredential, bool) {
	return keptCredentials.Take(sessionID)
}

// StartLiveConnect opens a browser the person drives themself: the first
// connect at every browser provider, and the fallback wherever a module meets
// something it does not recognise.
func (b *Bills) StartLiveConnect(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, width, height int,
) (provider.BillConnectState, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	agent, err := b.agent()
	if err != nil {
		return provider.BillConnectState{}, err
	}
	state, err := agent.StartLiveConnect(ctx, string(connection.Biller),
		billProfileID(connection), connection.Site, width, height)
	if err == nil {
		rememberSignIn(state.SessionID, spaceID, connectionID)
	}
	return state, err
}

// signInAgent is the agent for a sign-in this connection started, and a
// sign-in started anywhere else is not found.
func (b *Bills) signInAgent(spaceID store.SpaceID, connectionID uuid.UUID, sessionID string) (BillsAgent, error) {
	if err := ownSignIn(sessionID, spaceID, connectionID); err != nil {
		return nil, err
	}
	return b.agent()
}

// ConnectStatus re-reads where a connect stands, for the approval state where
// the page moves on by itself.
func (b *Bills) ConnectStatus(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
) (provider.BillConnectState, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return agent.ConnectStatus(ctx, sessionID)
}

// ConnectTrail is what the provider showed at each round of this sign-in,
// readable only from the connection it was started on. It carries no credential
// and no picture, so a household can paste it to somebody.
func (b *Bills) ConnectTrail(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
) ([]provider.BillTrailEntry, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return nil, err
	}
	return agent.ConnectTrail(ctx, sessionID)
}

// The four developer steers, each over a sign-in this connection started. The
// engine decides what they do (never off the provider's own site, never a
// field's value); the routes are owner-only and refused in-process, because a
// steer drives a signed-in browser.
func (b *Bills) SteerTo(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID, address string,
) (provider.BillSignInSteer, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillSignInSteer{}, err
	}
	return agent.SteerTo(ctx, sessionID, address)
}

func (b *Bills) SteerClick(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID, text, selector string,
) (provider.BillSignInSteer, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillSignInSteer{}, err
	}
	return agent.SteerClick(ctx, sessionID, text, selector)
}

func (b *Bills) SteerDOM(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID, selector string, limit int,
) (provider.BillSignInDOM, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillSignInDOM{}, err
	}
	return agent.SteerDOM(ctx, sessionID, selector, limit)
}

func (b *Bills) SteerFetch(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
	request provider.BillSignInFetchRequest,
) (provider.BillSignInFetch, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillSignInFetch{}, err
	}
	return agent.SteerFetch(ctx, sessionID, request)
}

// AnswerConnect gives the provider the code or CAPTCHA answer it asked for,
// inside a sign-in somebody is sitting at.
func (b *Bills) AnswerConnect(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID, code string,
) (provider.BillConnectState, error) {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return agent.AnswerConnect(ctx, sessionID, code)
}

// SignInInput plays what the person did in the live view of a sign-in parked
// on a page check.
func (b *Bills) SignInInput(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
	events []provider.BillLiveInput,
) error {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return err
	}
	return agent.SignInInput(ctx, sessionID, events)
}

// mailedCode is AnswerFromMail's wait for a connection's code.
func (b *Bills) mailedCode(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (func(since time.Time) (string, bool), error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}
	if _, err := b.agent(); err != nil {
		return nil, err
	}
	if b.MailedCode == nil || b.HasMailbox == nil || !b.HasMailbox(ctx, spaceID) {
		return nil, nil
	}
	return func(since time.Time) (string, bool) {
		return b.MailedCode(ctx, spaceID, connection, since)
	}, nil
}

func (b *Bills) signInStatus(ctx context.Context, sessionID string) (provider.BillConnectState, error) {
	agent, err := b.agent()
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return agent.ConnectStatus(ctx, sessionID)
}

func (b *Bills) answerSignIn(ctx context.Context, sessionID, code string) (provider.BillConnectState, error) {
	agent, err := b.agent()
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return agent.AnswerConnect(ctx, sessionID, code)
}

// CancelConnect gives up an unfinished sign-in. The browser is closed and the
// profile freed, so the next attempt is not refused until the reaper runs, and
// the password held for the keep is dropped; lastTyped keeps its copy for a
// retry.
func (b *Bills) CancelConnect(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
) error {
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return err
	}
	err = agent.CancelSignIn(ctx, sessionID)
	signIns.Delete(sessionID)
	takeCredential(sessionID)
	return err
}

// KeepSignInEnd keeps, on the connection that started it, how a sign-in a
// person started ended without landing: the sentence, the page and the trail,
// in the last pull's place, so they can be read once the session is gone. The
// engine reports it whether the sign-in failed, was closed, or was left for
// the reaper.
func (b *Bills) KeepSignInEnd(ctx context.Context, end provider.BillSignInEnded) {
	owner, ok := signIns.Get(end.SessionID)
	if !ok {
		return
	}
	if err := b.store.MarkBillSignInEnded(ctx, owner.space, owner.connection,
		end.Detail, failureShot(nil, end.Image), trailJSON(end.Trail)); err != nil {
		b.log().Error("bills: keeping an unfinished sign-in", "error", err)
	}
}

// trailJSON is a trail as the connection keeps it, or nil for none.
func trailJSON(trail []provider.BillTrailEntry) []byte {
	if len(trail) == 0 {
		return nil
	}
	encoded, err := json.Marshal(trail)
	if err != nil {
		return nil
	}
	return encoded
}

// PullTrail is the trail the connection's last pull or unfinished sign-in
// kept; store.ErrNotFound when it kept none.
func (b *Bills) PullTrail(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) ([]provider.BillTrailEntry, error) {
	raw, err := b.store.BillPullTrail(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}
	var trail []provider.BillTrailEntry
	if err := json.Unmarshal(raw, &trail); err != nil {
		return nil, fmt.Errorf("bills: the kept trail: %w", err)
	}
	return trail, nil
}

// CompleteConnect seals the finished session onto the connection and records
// what the login bills. Subaccounts are upserted on the provider's own key, so a
// renamed or linked premise keeps its row.
func (b *Bills) CompleteConnect(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID, sessionID string,
) ([]store.BillSubaccount, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return nil, err
	}
	agent, err := b.signInAgent(spaceID, connectionID, sessionID)
	if err != nil {
		return nil, err
	}
	done, err := agent.CompleteConnect(ctx, sessionID)
	signIns.Delete(sessionID)
	credential, held := takeCredential(sessionID)
	if err != nil {
		return nil, err
	}
	lastTyped.Delete(connectionID)
	if len(done.SessionState) == 0 {
		return nil, fmt.Errorf("the agent handed back no session")
	}
	if err := b.store.SaveBillConnectionSession(ctx, spaceID, connectionID,
		string(done.SessionState), billProfileID(connection), true); err != nil {
		return nil, err
	}
	if held {
		if err := b.store.SaveBillConnectionCredential(ctx, spaceID, connectionID, credential); err != nil {
			return nil, err
		}
	}
	for _, one := range done.Subaccounts {
		subaccount := store.BillSubaccount{
			ConnectionID: connectionID, ExternalID: one.ExternalID,
			Label: one.Label, MaskedNumber: one.MaskedNumber, IsSelected: true,
		}
		if subaccount.Label == "" {
			subaccount.Label = one.ExternalID
		}
		if err := b.store.UpsertBillSubaccount(ctx, spaceID, &subaccount); err != nil {
			return nil, err
		}
	}
	return b.store.ListBillSubaccounts(ctx, spaceID, connectionID)
}

// Forget drops the kept session and tells the engine to delete the browser
// profile behind it. Both halves: a profile left on the volume is a signed-in
// browser nobody can see.
func (b *Bills) Forget(ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID) error {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return err
	}
	if err := b.store.ClearBillConnectionSession(ctx, spaceID, connectionID); err != nil {
		return err
	}
	b.forgetBrowser(ctx, connection)
	return nil
}

// forgetBrowser deletes the browser profile a connection signs in with. An
// agent that is down or refuses does not fail the caller: the session is
// already gone from the database, which is the half that matters.
func (b *Bills) forgetBrowser(ctx context.Context, connection store.BillConnection) {
	agent, err := b.agent()
	if err != nil {
		return
	}
	if err := agent.ForgetProfile(ctx, billProfileID(connection)); err != nil {
		b.log().Warn("bills: the agent kept a browser profile",
			"connection", connection.DisplayName(), "error", err)
	}
}

// ReleaseBrowser lets go of the browser this connection is holding, and nothing
// else: the kept session, password and profile stay. It clears the lock a
// restart mid-sign-in leaves on the profile volume, which otherwise refuses every
// later sign-in.
func (b *Bills) ReleaseBrowser(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (provider.BillProfileRelease, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return provider.BillProfileRelease{}, err
	}
	agent, err := b.agent()
	if err != nil {
		return provider.BillProfileRelease{}, err
	}
	released, err := agent.ReleaseProfile(ctx, billProfileID(connection))
	// The sessions the engine closed drop their kept passwords now, as on a
	// cancel, rather than waiting for the sweep.
	for _, sessionID := range released.Sessions {
		signIns.Delete(sessionID)
		takeCredential(sessionID)
	}
	return released, err
}

// ForgetCredential drops the kept password and the session with it, so nothing
// stays signed in on a token the password minted.
func (b *Bills) ForgetCredential(ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID) error {
	lastTyped.Delete(connectionID)
	if err := b.store.ClearBillConnectionCredential(ctx, spaceID, connectionID); err != nil {
		return err
	}
	return b.Forget(ctx, spaceID, connectionID)
}

// Pull reads one connection's bills and files what it finds. A kept password is
// sent along, and the engine signs in with it only when the kept session does
// not serve. The pull is bounded by pullBound, so a provider's portal that
// never answers stops the connection's own pull rather than the scheduler's
// whole pass, and outlives an on-demand caller whose own request context
// drops, since a pull cut off part way has neither its bills nor its "last
// pulled" stamp written.
//
// ErrPullRunning, and nothing recorded, when a pull of the connection is
// already running here.
func (b *Bills) Pull(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (BillPullResult, error) {
	if !billPulls.claim(connectionID) {
		return BillPullResult{}, ErrPullRunning
	}
	defer billPulls.release(connectionID)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.pullBound())
	defer cancel()
	return b.pull(ctx, spaceID, connectionID)
}

// PullAfterSignIn starts a background pull of a connection somebody has just
// signed in to, through the same path as Update now, and answers whether it
// started one (false when a pull is already running).
func (b *Bills) PullAfterSignIn(spaceID store.SpaceID, connectionID uuid.UUID) bool {
	if !b.HasAgent() || !billPulls.claim(connectionID) {
		return false
	}
	billPulls.inBackground(connectionID, func(ctx context.Context) {
		result, err := b.pull(ctx, spaceID, connectionID)
		switch {
		case err != nil:
			b.log().Error("bills: the pull after a sign-in failed", "connection", connectionID, "error", err)
		default:
			b.log().Info("bills: pulled after a sign-in", "connection", connectionID,
				"status", result.Status, "new", result.New, "amended", result.Amended,
				"documents", result.Documents, "notes", result.Notes)
		}
	})
	return true
}

func (b *Bills) pull(
	ctx context.Context, spaceID store.SpaceID, connectionID uuid.UUID,
) (BillPullResult, error) {
	connection, err := b.store.GetBillConnection(ctx, spaceID, connectionID)
	if err != nil {
		return BillPullResult{}, err
	}
	agent, err := b.agent()
	if err != nil {
		return BillPullResult{}, err
	}
	session, err := b.store.BillConnectionSession(ctx, spaceID, connectionID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return BillPullResult{}, err
	}
	var credential map[string]string
	if keepsPassword(connection) {
		kept, err := b.store.BillConnectionCredential(ctx, spaceID, connectionID)
		if err != nil {
			return BillPullResult{}, err
		}
		credential = map[string]string{"username": kept.Username, "password": kept.Password}
		// The setup key rather than a code: the engine mints one when it signs in,
		// which may be minutes from now, and a code lasts thirty seconds.
		if kept.TOTPSecret != "" {
			credential["totp_secret"] = kept.TOTPSecret
		}
	}
	if session == "" && credential == nil {
		return b.stopped(ctx, spaceID, connection, store.BillPullNeedsSignIn,
			fmt.Sprintf("%s has no session on file: sign in first", connection.DisplayName()), nil, nil), nil
	}

	subaccounts, err := b.store.ListBillSubaccounts(ctx, spaceID, connectionID)
	if err != nil {
		return BillPullResult{}, err
	}
	request := provider.BillPullRequest{
		Provider: string(connection.Biller), Profile: billProfileID(connection),
		Site:         connection.Site,
		SessionState: session, Credential: credential,
		SecondFactor: string(connection.SecondFactor),
	}
	for _, one := range subaccounts {
		if one.IsSelected {
			request.Subaccounts = append(request.Subaccounts, one.ExternalID)
		}
	}
	if request.KnownDocuments, err = b.storedStatements(ctx, spaceID, subaccounts); err != nil {
		return BillPullResult{}, err
	}

	pulled, err := agent.Pull(ctx, request)
	if err != nil {
		detail := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			detail = fmt.Sprintf("%s did not answer within %s; the pull was given up on",
				connection.DisplayName(), b.pullBound())
		}
		return b.stopped(ctx, spaceID, connection, store.BillPullFailed, detail, failureShot(err, ""), nil), nil
	}
	return b.apply(ctx, spaceID, connection, agent, pulled, store.BillChallengeFromPull)
}

// AnswerChallenge gives the provider the code somebody typed and resumes the
// stopped pull in the browser context the engine still holds, rather than
// starting a second sign-in.
func (b *Bills) AnswerChallenge(
	ctx context.Context, spaceID store.SpaceID, challengeID uuid.UUID, code string,
) (BillPullResult, error) {
	return b.answer(ctx, spaceID, challengeID, code, store.BillChallengeByPerson)
}

func (b *Bills) answer(
	ctx context.Context, spaceID store.SpaceID, challengeID uuid.UUID, code, answeredBy string,
) (BillPullResult, error) {
	challenge, err := b.store.GetBillChallenge(ctx, spaceID, challengeID)
	if err != nil {
		return BillPullResult{}, err
	}
	if !challenge.IsWaiting() || !challenge.ExpiresAt.After(b.now()) {
		if challenge.IsWaiting() {
			_ = b.store.MarkBillChallengeState(ctx, spaceID, challenge.ID, store.BillChallengeExpired)
		}
		return BillPullResult{}, ErrBillChallengeExpired
	}
	connection, err := b.store.GetBillConnection(ctx, spaceID, challenge.ConnectionID)
	if err != nil {
		return BillPullResult{}, err
	}
	agent, err := b.agent()
	if err != nil {
		return BillPullResult{}, err
	}

	// A push approval has no code, so an empty answer means "try the pull again".
	// Only for push: elsewhere an empty code is a person who pressed too early.
	approved := strings.TrimSpace(code) == "" &&
		challenge.Method == string(domain.ChallengePush)
	if !approved {
		state, err := agent.AnswerConnect(ctx, challenge.AgentSession, code)
		if errors.Is(err, provider.ErrAgentNotFound) {
			// The engine has already reaped this sign-in; close the row rather than
			// leave it offering a code field to nobody.
			_ = b.store.MarkBillChallengeState(ctx, spaceID, challenge.ID, store.BillChallengeExpired)
			return BillPullResult{}, ErrBillChallengeExpired
		}
		if err != nil {
			return BillPullResult{}, err
		}
		if state.State == provider.BillConnectFailed || state.Error != "" {
			// The provider said no. The row stays waiting so a mistyped code is asked
			// again.
			return BillPullResult{
				Status: store.BillPullChallenge, Challenge: &challenge,
				Error: textutil.FirstNonBlank(state.Error, state.Prompt),
			}, nil
		}
	}

	resumed, err := agent.ResumePull(ctx, challenge.AgentSession)
	switch {
	case errors.Is(err, provider.ErrAgentConflict):
		// The sign-in has not moved on, which for a push approval means the tap has
		// not landed yet. The row stays waiting so the dialog can ask again.
		return BillPullResult{
			Status: store.BillPullChallenge, Challenge: &challenge,
			Error: connection.ProviderName() + " has not seen the approval yet",
		}, nil
	case errors.Is(err, provider.ErrAgentNotFound):
		_ = b.store.MarkBillChallengeState(ctx, spaceID, challenge.ID, store.BillChallengeExpired)
		return BillPullResult{}, ErrBillChallengeExpired
	}
	if err != nil {
		return b.stopped(ctx, spaceID, connection, store.BillPullFailed, err.Error(), failureShot(err, ""), nil), nil
	}
	// Marked once the pull is actually moving again: a code the provider took
	// and a sign-in that then went nowhere is not an answered challenge.
	if err := b.store.MarkBillChallengeAnswered(ctx, spaceID, challenge.ID, answeredBy); err != nil {
		return BillPullResult{}, err
	}
	return b.apply(ctx, spaceID, connection, agent, resumed, store.BillChallengeFromPull)
}

// apply is the one reading of a pull's answer, whether it came from the pull
// itself or from the resume after a challenge.
func (b *Bills) apply(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	agent BillsAgent, pulled provider.BillPull, raisedBy string,
) (BillPullResult, error) {
	switch {
	case pulled.Challenge != nil:
		return b.park(ctx, spaceID, connection, *pulled.Challenge, raisedBy, pulled.Notes)
	case pulled.NeedsSignIn:
		reason := pulled.Reason
		if reason == "" {
			reason = connection.ProviderName() + " asked to sign in again"
		}
		// A kept password is never forgotten here: the engine has already tried it.
		// When that try ended where only a person can carry on (password refused, or a
		// code nobody answered) the sign-in is paused, which stops the scheduler locking
		// the account or texting a code every night.
		paused := ""
		switch {
		case pulled.PasswordRefused:
			paused = provider.SignInPausedPasswordRefused
			reason = strings.TrimRight(reason, ". ") + ". The password is still kept, but it will " +
				"not be tried on its own again until you sign in, change it, or press Update now."
		case pulled.CodeNeeded:
			paused = provider.SignInPausedCodeNeeded
			reason = strings.TrimRight(reason, ". ") + ". Automatic updates wait until you sign in."
		case pulled.PageCheck:
			paused = provider.SignInPausedPageCheck
			reason = strings.TrimRight(reason, ". ") + ". Automatic updates wait until you sign in and tick it."
		}
		out := b.stopped(ctx, spaceID, connection, store.BillPullNeedsSignIn, reason,
			failureShot(nil, pulled.Image), trailJSON(pulled.Trail))
		if paused != "" {
			if err := b.store.PauseBillSignIn(ctx, spaceID, connection.ID, paused); err != nil {
				return BillPullResult{}, err
			}
		}
		out.Notes = append(out.Notes, pulled.Notes...)
		return out, nil
	}

	// The rolled snapshot first: the provider may have invalidated the one that
	// went in, and a failure below must not leave the row holding a dead session.
	if len(pulled.SessionState) > 0 {
		if err := b.store.SaveBillConnectionSession(ctx, spaceID, connection.ID,
			string(pulled.SessionState), "", false); err != nil {
			return BillPullResult{}, err
		}
	}

	out := BillPullResult{Status: store.BillPullOK, Notes: pulled.Notes}
	subaccounts, err := b.store.ListBillSubaccounts(ctx, spaceID, connection.ID)
	if err != nil {
		return BillPullResult{}, err
	}
	known := make(map[string]store.BillSubaccount, len(subaccounts))
	for _, one := range subaccounts {
		known[one.ExternalID] = one
	}
	if err := b.refreshLabels(ctx, spaceID, known, pulled.Subaccounts); err != nil {
		return BillPullResult{}, err
	}

	subaccountOf := func(external string) (store.BillSubaccount, error) {
		subaccount, held := known[external]
		if held {
			return subaccount, nil
		}
		// The pull is the authority on what a login bills: a premise that appeared
		// since the connect is a row to add.
		subaccount = store.BillSubaccount{
			ConnectionID: connection.ID, ExternalID: external,
			Label: external, IsSelected: true,
		}
		if err := b.store.UpsertBillSubaccount(ctx, spaceID, &subaccount); err != nil {
			return store.BillSubaccount{}, err
		}
		known[external] = subaccount
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%s billed %q, which was not on file; it was added", connection.DisplayName(), external))
		return subaccount, nil
	}

	for _, external := range pulledOrder(pulled.Bills) {
		statements := billsFor(pulled.Bills, external)
		subaccount, err := subaccountOf(external)
		if err != nil {
			return BillPullResult{}, err
		}

		rows := make([]store.Bill, 0, len(statements))
		for _, one := range statements {
			rows = append(rows, storedBill(one, b.now()))
		}
		ingested, err := b.Ingest(ctx, spaceID, subaccount.ID, rows)
		if err != nil {
			return BillPullResult{}, err
		}
		out.New += ingested.New
		out.Amended += ingested.Amended
		out.Unchanged += ingested.Unchanged

		filed, notes := b.fileStatements(ctx, spaceID, agent, statements, ingested.Bills)
		out.Documents += filed
		out.Notes = append(out.Notes, notes...)
	}

	if len(pulled.Payments) > 0 {
		byAccount := map[string][]store.BillPayment{}
		var order []string
		for _, one := range pulled.Payments {
			if _, seen := byAccount[one.Subaccount]; !seen {
				order = append(order, one.Subaccount)
			}
			byAccount[one.Subaccount] = append(byAccount[one.Subaccount], store.BillPayment{
				ExternalID: one.ExternalID, PaidOn: one.PaidOn, Amount: one.Amount,
				Method: one.Method, FetchedAt: b.now(),
			})
		}
		for _, external := range order {
			subaccount, err := subaccountOf(external)
			if err != nil {
				return BillPullResult{}, err
			}
			if _, err := b.store.UpsertBillPayments(ctx, spaceID, subaccount.ID, byAccount[external]); err != nil {
				return BillPullResult{}, err
			}
		}
		paired, err := b.store.MatchBillPayments(ctx, spaceID)
		if err != nil {
			return BillPullResult{}, err
		}
		out.Notes = append(out.Notes, fmt.Sprintf("Payments read from %s: %d. Newly paired with a bank row: %d.",
			connection.DisplayName(), len(pulled.Payments), paired))
	}

	detail := ""
	if len(out.Notes) > 0 {
		detail = out.Notes[0]
	}
	if err := b.store.MarkBillPull(ctx, spaceID, connection.ID, store.BillPullOK, detail, nil,
		trailJSON(pulled.Trail)); err != nil {
		return BillPullResult{}, err
	}
	if b.Alerts != nil {
		if err := b.Alerts.ResolveCondition(ctx, spaceID, billPullCondition(connection.ID)); err != nil {
			b.log().Error("bills: withdrawing a stopped-pull alert", "error", err)
		}
	}
	return out, nil
}

// refreshLabels gives each billed account on file the label the pull found
// for it while the label on file is still the provider's own (see
// providersLabel), so a name the provider's page was misread for is put right
// by the next pull. A label somebody wrote is kept.
func (b *Bills) refreshLabels(
	ctx context.Context, spaceID store.SpaceID, known map[string]store.BillSubaccount,
	found []provider.BillSubaccountRef,
) error {
	for _, one := range found {
		held, onFile := known[one.ExternalID]
		if !onFile || one.Label == "" || held.Label == one.Label || !providersLabel(held.Label, one) {
			continue
		}
		held.Label = one.Label
		held.MaskedNumber = cmp.Or(one.MaskedNumber, held.MaskedNumber)
		if err := b.store.UpsertBillSubaccount(ctx, spaceID, &held); err != nil {
			return err
		}
		known[one.ExternalID] = held
	}
	return nil
}

// providersLabel says a label on file is one a pull or a connect wrote: the
// provider's own key, or the fresh label's part before " · " (the masked
// number a label leads with) followed by anything or nothing.
func providersLabel(label string, fresh provider.BillSubaccountRef) bool {
	stem, _, _ := strings.Cut(fresh.Label, " · ")
	return label == fresh.ExternalID || label == stem || strings.HasPrefix(label, stem+" · ")
}

// billPullCondition is the state "this login's pull is stopped", as an alert
// names it.
func billPullCondition(connectionID uuid.UUID) string {
	return "bill-pull-stopped:" + connectionID.String()
}

// park writes the challenge row, tries the automatic answerers, and tells a
// person only when none of them answered.
func (b *Bills) park(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	state provider.BillChallengeState, raisedBy string, notes []string,
) (BillPullResult, error) {
	challenge := &store.BillChallenge{
		ConnectionID: connection.ID, AgentSession: state.SessionID,
		Method: textutil.FirstNonBlank(state.Method, state.State), Prompt: state.Prompt,
		Image: state.Image, State: store.BillChallengeWaiting, RaisedBy: raisedBy,
		ExpiresAt: b.now().Add(BillChallengeTTL),
	}
	if err := b.store.CreateBillChallenge(ctx, spaceID, challenge); err != nil {
		return BillPullResult{}, err
	}
	if err := b.store.MarkBillPull(ctx, spaceID, connection.ID, store.BillPullChallenge,
		textutil.FirstNonBlank(state.Prompt, connection.ProviderName()+" asked for a code"), nil, nil); err != nil {
		return BillPullResult{}, err
	}

	for _, answerer := range b.Answerers {
		code, answeredBy, answered := answerer(ctx, spaceID, connection, *challenge)
		if !answered {
			continue
		}
		return b.answer(ctx, spaceID, challenge.ID, code, answeredBy)
	}

	// Nothing answered, so until a person does the scheduler leaves the connection
	// alone rather than raise a code every night.
	if err := b.store.PauseBillSignIn(ctx, spaceID, connection.ID, provider.SignInPausedCodeNeeded); err != nil {
		return BillPullResult{}, err
	}
	b.notifyChallenge(ctx, spaceID, connection, *challenge)
	return BillPullResult{
		Status: store.BillPullChallenge, Challenge: challenge,
		Error: state.Prompt, Notes: notes,
	}, nil
}

// stopped records a pull that ended badly, with the page it stopped on or nil
// and its trail or nil, and tells the household once.
func (b *Bills) stopped(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	status, detail string, shot, trail []byte,
) BillPullResult {
	// The pull's own bound can be why it stopped, so writing the stop must not
	// run under that same, already-spent deadline.
	ctx = context.WithoutCancel(ctx)
	// Read before the mark: "is this new" compares with what the row said before
	// this pull.
	repeated := connection.LastPullStatus == status
	if err := b.store.MarkBillPull(ctx, spaceID, connection.ID, status, detail, shot, trail); err != nil {
		b.log().Error("bills: recording a stopped pull", "connection", connection.DisplayName(), "error", err)
	}
	if !repeated {
		if b.Alerts != nil {
			if err := b.Alerts.ResolveCondition(ctx, spaceID, billPullCondition(connection.ID)); err != nil {
				b.log().Error("bills: withdrawing a stopped-pull alert", "error", err)
			}
		}
		b.notifyStopped(ctx, spaceID, connection, status, detail)
	}
	return BillPullResult{Status: status, Error: detail, Screenshot: len(shot) > 0}
}

// storedStatements is the statements already on file, by the provider's own
// key, so the engine fetches a document once rather than every night.
func (b *Bills) storedStatements(
	ctx context.Context, spaceID store.SpaceID, subaccounts []store.BillSubaccount,
) ([]string, error) {
	var out []string
	for _, subaccount := range subaccounts {
		bills, err := b.store.ListBills(ctx, spaceID, subaccount.ID)
		if err != nil {
			return nil, err
		}
		for _, one := range bills {
			if one.DocumentID != uuid.Nil && one.ExternalID != "" {
				out = append(out, one.ExternalID)
			}
		}
	}
	return out, nil
}

// fileStatements fetches each document the pull offered and files it against
// its bill, reporting how many and what went wrong. A document that fails is a
// note, never a failed pull: the figures are what matter.
func (b *Bills) fileStatements(
	ctx context.Context, spaceID store.SpaceID, agent BillsAgent,
	statements []provider.BillStatement, stored []store.Bill,
) (int, []string) {
	// The agent that minted the reference is the one holding the bytes.
	if b.Documents == nil || agent == nil {
		return 0, nil
	}
	filed := 0
	var notes []string
	for _, statement := range statements {
		if statement.Document == nil || statement.Document.Ref == "" {
			continue
		}
		bill, held := BillOfCycle(stored, statement.DueOn, statement.Invoice)
		if !held {
			continue
		}
		raw, _, filename, err := agent.FetchDocument(ctx, statement.Document.Ref)
		if err != nil {
			notes = append(notes, fmt.Sprintf("the statement due %s was not fetched: %v",
				statement.DueOn, err))
			continue
		}
		if filename == "" {
			filename = textutil.FirstNonBlank(statement.Document.Filename,
				"statement-"+statement.DueOn.String()+".pdf")
		}
		_, err = b.Documents.AttachStatement(ctx, spaceID, bill.ID, DocumentUpload{
			Bytes: raw, Filename: filename,
			Source: store.DocumentSourceBillPull, SourceRef: statement.StatementURL,
		})
		if err != nil {
			notes = append(notes, fmt.Sprintf("the statement due %s was refused: %v",
				statement.DueOn, err))
			continue
		}
		filed++
	}
	return filed, notes
}

// PullDue pulls every connection whose window has opened, across every space,
// and reports how many it pulled.
//
// Unlike the bank pass, a connection with its own hour is due at that hour's
// most recent occurrence; and a provider that texts a code, at a household with
// no hour set and nothing that can read a text, is not pulled at all, since the
// pull would only park a challenge in the night.
func (b *Bills) PullDue(ctx context.Context, since time.Time) int {
	if !b.HasAgent() {
		return 0
	}
	return runDue(ctx, b.store, b.log(), dueRun[store.BillConnection]{
		kind: "bills",
		list: func(ctx context.Context) ([]store.BillConnection, error) {
			due, err := b.store.ListBillConnectionsDue(ctx, since)
			// API providers first: a browser context costs a tab, a gigabyte of
			// shared memory and a minute, and the cheap ones should not queue
			// behind it.
			sort.SliceStable(due, func(i, j int) bool {
				return billerAccessRank(due[i]) < billerAccessRank(due[j])
			})
			return due, err
		},
		run: func(ctx context.Context, connection store.BillConnection) bool {
			return b.pullDue(ctx, connection, since)
		},
		between: b.Between,
	})
}

func (b *Bills) pullDue(ctx context.Context, connection store.BillConnection, since time.Time) bool {
	biller, known := domain.BillerByID(connection.Biller)
	if !known || !reachableByBridge(biller) || !b.isDue(connection, since) {
		return false
	}
	// A login whose own second factor is a mailed code or a kept authenticator
	// is answered with nobody awake, whatever else the provider may raise.
	if connection.PullAt == "" && biller.NeedsAPersonForACode() &&
		!connection.SecondFactor.Unattended(connection.HasTOTP) {
		if err := b.store.NoteBillPullSkipped(ctx, connection.SpaceID, connection.ID,
			biller.Name+" asks for a code somebody has to read, so it is pulled by "+
				"Update now, or at an hour you set"); err != nil {
			b.log().Error("bills: noting a skipped pull",
				"connection", connection.DisplayName(), "error", err)
		}
		return false
	}

	result, err := b.Pull(ctx, connection.SpaceID, connection.ID)
	switch {
	case errors.Is(err, ErrPullRunning):
		b.log().Info("bills: already pulling", "connection", connection.DisplayName())
		return false
	case err != nil:
		b.log().Error("bills: pull failed",
			"biller", connection.Biller, "connection", connection.DisplayName(), "error", err)
	case result.Status != store.BillPullOK:
		b.log().Warn("bills: pull stopped", "biller", connection.Biller,
			"connection", connection.DisplayName(), "status", result.Status, "detail", result.Error)
	default:
		b.log().Info("bills: pulled", "biller", connection.Biller,
			"connection", connection.DisplayName(), "new", result.New, "amended", result.Amended,
			"documents", result.Documents, "notes", result.Notes)
	}
	return true
}

// isDue answers the connection's own window. With no hour of its own the claim
// query has already decided; with one, it is measured against that hour's most
// recent occurrence, the same arithmetic the banks use.
func (b *Bills) isDue(connection store.BillConnection, since time.Time) bool {
	if connection.PullAt == "" || connection.LastPulledAt == nil {
		return true
	}
	window, ok := parseBillPullAt(connection.PullAt)
	if !ok {
		return true
	}
	return connection.LastPulledAt.Before(window.MostRecent(b.now()))
}

// KeepaliveDue touches the kept profile of every browser connection whose
// provider forgets one left alone. Loading a landing page says whether the
// session is still good, so a lost one is found before the bill is due. A pull is
// also a touch, so the last touch is the latest of all three stamps.
func (b *Bills) KeepaliveDue(ctx context.Context, now time.Time) int {
	if !b.HasAgent() {
		return 0
	}
	connections, err := b.store.ListBillConnectionsWithSession(ctx)
	if err != nil {
		b.log().Error("bills: listing connections to keep alive", "error", err)
		return 0
	}
	touched := 0
	for _, connection := range connections {
		if ctx.Err() != nil {
			return touched
		}
		biller, known := domain.BillerByID(connection.Biller)
		if !known || !reachableByBridge(biller) || biller.KeepaliveDays <= 0 {
			continue
		}
		last := lastTouch(connection)
		if !last.IsZero() && now.Sub(last) < time.Duration(biller.KeepaliveDays)*24*time.Hour {
			continue
		}
		// At an API provider this call is the token refresh, and the answer carries
		// the session to keep.
		session, err := b.store.BillConnectionSession(ctx, connection.SpaceID, connection.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			b.log().Error("bills: opening a session to keep alive",
				"connection", connection.DisplayName(), "error", err)
			continue
		}

		agent, err := b.agent()
		if err != nil {
			continue
		}
		answer, err := agent.Keepalive(ctx, string(connection.Biller),
			billProfileID(connection), connection.Site, session)
		if err != nil {
			b.log().Warn("bills: keepalive failed",
				"biller", connection.Biller, "connection", connection.DisplayName(), "error", err)
			continue
		}
		if len(answer.SessionState) > 0 {
			if err := b.store.SaveBillConnectionSession(ctx, connection.SpaceID, connection.ID,
				string(answer.SessionState), "", false); err != nil {
				b.log().Error("bills: keeping a refreshed session",
					"connection", connection.DisplayName(), "error", err)
			}
		}
		if err := b.store.MarkBillKeepalive(ctx, connection.SpaceID, connection.ID, now); err != nil {
			b.log().Error("bills: recording a keepalive", "connection", connection.DisplayName(), "error", err)
		}
		touched++
		if answer.SignedIn {
			continue
		}
		reason := textutil.FirstNonBlank(answer.Reason, connection.ProviderName()+" has forgotten the kept session")
		b.stopped(ctx, connection.SpaceID, connection, store.BillPullNeedsSignIn, reason, nil, nil)
	}
	return touched
}

// ExpireChallenges retires the rows whose sign-in the engine has already
// thrown away, across every space.
func (b *Bills) ExpireChallenges(ctx context.Context) (int, error) {
	return b.store.ExpireBillChallenges(ctx, b.now())
}

// notifyChallenge is the push that fetches somebody. It fires only where no
// automatic answerer could.
func (b *Bills) notifyChallenge(
	ctx context.Context, spaceID store.SpaceID,
	connection store.BillConnection, challenge store.BillChallenge,
) {
	if b.Alerts == nil {
		return
	}
	prompt := strings.TrimSpace(challenge.Prompt)
	if prompt == "" {
		prompt = "It asked for a code to finish signing in."
	}
	hit := domain.AlertHit{
		Type:  domain.AlertBillChallenge,
		Title: fmt.Sprintf("%s is waiting for a code", connection.Title()),
		Body: fmt.Sprintf("%s Bill pull paused until answered; expires in about %d minutes.",
			prompt, int(BillChallengeTTL/time.Minute)),
		URL:       "/settings/bills?challenge=" + challenge.ID.String(),
		DedupeKey: "bill-challenge:" + challenge.ID.String(),
	}
	if _, err := b.Alerts.DeliverToSpace(ctx, spaceID, hit); err != nil {
		b.log().Error("bills: delivering a challenge alert", "error", err)
	}
}

// billSignInURL opens one login's sign-in: the Bills screen reads `sign-in`
// and starts it.
func billSignInURL(connectionID uuid.UUID) string {
	return "/settings/bills?sign-in=" + connectionID.String()
}

// notifyStopped tells the household a pull is not running, once per failing
// streak; the next pull that gets in withdraws it.
func (b *Bills) notifyStopped(
	ctx context.Context, spaceID store.SpaceID, connection store.BillConnection,
	status, detail string,
) {
	if b.Alerts == nil {
		return
	}
	name := connection.ProviderName()
	hit := domain.AlertHit{
		Type:  domain.AlertBillPullFailed,
		Title: fmt.Sprintf("%s stopped pulling bills", connection.Title()),
		Body: fmt.Sprintf("Bill pull failed: %s. Reminders show your estimate until it "+
			"succeeds.", detail),
		URL: "/settings/bills",
		// The status is in the key, so a change from failing to needing a sign-in is
		// news rather than deduped against the failure.
		DedupeKey: billPullCondition(connection.ID) + ":" + status, Ongoing: true,
	}
	if status == store.BillPullNeedsSignIn {
		hit.Title = fmt.Sprintf("%s needs you to sign in again", connection.Title())
		hit.Body = fmt.Sprintf("%s asked to sign in again: %s. Open this to sign in and "+
			"resume the pull.", name, detail)
		hit.URL = billSignInURL(connection.ID)
	}
	if _, err := b.Alerts.DeliverToSpace(ctx, spaceID, hit); err != nil {
		b.log().Error("bills: delivering a stopped-pull alert", "error", err)
	}
}

// storedBill is one pulled statement as the record holds it. The status is the
// provider's own word, so it is folded: anything but the provider saying paid is
// still owed. "superseded" is decided by the ingest, not the provider.
func storedBill(statement provider.BillStatement, fetchedAt time.Time) store.Bill {
	status := domain.BillOpen
	if strings.EqualFold(statement.Status, string(domain.BillPaid)) {
		status = domain.BillPaid
	}
	return store.Bill{
		DueOn: statement.DueOn, AmountDue: statement.AmountDue,
		MinimumDue: statement.MinimumDue, HasMinimumDue: statement.HasMinimumDue,
		Currency: statement.Currency, IssuedOn: statement.IssuedOn, PeriodStart: statement.PeriodStart,
		PeriodEnd: statement.PeriodEnd, AutopayOn: statement.AutopayOn,
		Status: status, Source: store.BillSourceProvider, ExternalID: statement.ExternalID,
		Invoice:      statement.Invoice,
		StatementURL: statement.StatementURL, FetchedAt: fetchedAt,
	}
}

// pulledOrder is the subaccounts a pull spoke about, in the order it named
// them, so a pull of several premises writes them in a stable order.
func pulledOrder(statements []provider.BillStatement) []string {
	var out []string
	seen := map[string]bool{}
	for _, one := range statements {
		if seen[one.Subaccount] {
			continue
		}
		seen[one.Subaccount] = true
		out = append(out, one.Subaccount)
	}
	return out
}

func billsFor(statements []provider.BillStatement, external string) []provider.BillStatement {
	var out []provider.BillStatement
	for _, one := range statements {
		if one.Subaccount == external {
			out = append(out, one)
		}
	}
	return out
}

func keepsPassword(connection store.BillConnection) bool {
	return connection.CredentialSource == store.BillCredentialStored && connection.HasCredential
}

// billProfileID is the persistent browser profile for a connection: the
// connection's own id unless one was chosen, because a profile must survive a
// restart and outlive a session. An api provider is handed one and ignores it.
func billProfileID(connection store.BillConnection) string {
	if connection.ProfileID != "" {
		return connection.ProfileID
	}
	return connection.ID.String()
}

// reachableByBridge says the engine has anything to do for this provider. A
// card whose terms arrive on the bank sync, and a provider whose bill is an
// email, are both connections with no pull.
func reachableByBridge(biller domain.Biller) bool {
	return biller.Access == domain.AccessAPI || biller.Access == domain.AccessBrowser
}

func billerAccessRank(connection store.BillConnection) int {
	if biller, known := domain.BillerByID(connection.Biller); known && biller.Access == domain.AccessAPI {
		return 0
	}
	return 1
}

// parseBillPullAt reads the connection's own hour, HH:MM.
func parseBillPullAt(value string) (SyncWindow, bool) {
	hour, minute, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found {
		return SyncWindow{}, false
	}
	h, err := strconv.Atoi(hour)
	if err != nil || h < 0 || h > 23 {
		return SyncWindow{}, false
	}
	m, err := strconv.Atoi(minute)
	if err != nil || m < 0 || m > 59 {
		return SyncWindow{}, false
	}
	return SyncWindow{Hour: h, Minute: m}, true
}

// lastTouch is the most recent time anything reached the provider with this
// connection's session.
func lastTouch(connection store.BillConnection) time.Time {
	var last time.Time
	for _, stamp := range []*time.Time{
		connection.LastPulledAt, connection.SignedInAt, connection.LastKeepaliveAt,
	} {
		if stamp != nil && stamp.After(last) {
			last = *stamp
		}
	}
	return last
}
