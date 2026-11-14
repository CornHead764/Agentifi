package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The review queue, gathered on read from the sources that already hold each
// finding (calculations.md §15). Nothing here is stored, so a decision made
// on any screen removes the item from this one too.

// statementDueWithin is how far ahead a pay-by-hand statement becomes an item.
const statementDueWithin = 7

// reviewAction is a decision an item offers, as the wire names it.
type reviewAction struct {
	Action  string
	Label   string
	Confirm bool
}

// reviewItem is an item with the words and links a card draws.
type reviewItem struct {
	domain.ReviewItem
	Title, Detail, Link string
	Subjects            []string
	Actions             []reviewAction
	// Target is what Act writes to: a challenge, a series slot, a bill, a pair.
	Target reviewTarget
}

// reviewTarget is the source row an item's decisions write to.
type reviewTarget struct {
	ID       uuid.UUID
	SeriesID uuid.UUID
	DueOn    domain.Date
	KeepID   uuid.UUID
	OtherID  uuid.UUID
}

// reviewRow is one new transaction with what the screen shows about it.
type reviewRow struct {
	Txn          store.Transaction
	Reason       domain.ReviewReason
	Suggestion   *TransactionSuggestion
	AccountName  string
	AccountMask  string
	DuplicateKey string
}

type reviewDay struct {
	Date       domain.Date
	Out        domain.Money
	NeedsALook int
	Rows       []reviewRow
}

type reviewSnapshot struct {
	Items      []reviewItem
	Days       []reviewDay
	Summary    domain.ReviewSummary
	Kinds      map[domain.ReviewKind]int
	Categories map[uuid.UUID]store.Category
}

// gatherReview reads every source once and assembles both lanes.
func gatherReview(ctx context.Context, env *Env, sp auth.SpaceContext) (reviewSnapshot, error) {
	now := env.now()
	out := reviewSnapshot{Kinds: map[domain.ReviewKind]int{}}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return out, err
	}
	accountByID := make(map[uuid.UUID]store.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	categories, err := env.DB.ListCategories(ctx, sp.ID(), true)
	if err != nil {
		return out, err
	}
	out.Categories = make(map[uuid.UUID]store.Category, len(categories))
	// Deleted categories included: one still excludes the spending filed under it.
	ledgerCategories := make(map[domain.ID]domain.Category, len(categories))
	for _, category := range categories {
		out.Categories[category.ID] = category
		ledgerCategories[domain.ID(category.ID.String())] = store.DomainCategory(category)
	}

	duplicates, err := env.DB.ListOpenDuplicates(ctx, sp.ID())
	if err != nil {
		return out, err
	}

	items, err := gatherReviewItems(ctx, env, sp, now, accounts, duplicates)
	if err != nil {
		return out, err
	}
	days, rowReasons, err := gatherReviewRows(ctx, env, sp, accountByID, ledgerCategories, duplicates)
	if err != nil {
		return out, err
	}

	queued := make([]domain.ReviewItem, len(items))
	byKey := make(map[string]reviewItem, len(items))
	for i, item := range items {
		queued[i] = item.ReviewItem
		byKey[item.Key] = item
	}
	ordered := domain.BuildReviewQueue(queued, nil, now)
	out.Items = make([]reviewItem, len(ordered))
	for i, item := range ordered {
		out.Items[i] = byKey[item.Key]
		out.Kinds[item.Kind]++
	}
	out.Days = days
	out.Summary = domain.ReviewCounts(ordered, rowReasons)
	return out, nil
}

func gatherReviewItems(
	ctx context.Context, env *Env, sp auth.SpaceContext, now time.Time,
	accounts []store.Account, duplicates []store.DuplicateCandidate,
) ([]reviewItem, error) {
	var items []reviewItem
	today := domain.DateOf(now)

	connections, err := env.DB.ListBillConnections(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	providerName := map[uuid.UUID]string{}
	for _, conn := range connections {
		providerName[conn.ID] = conn.DisplayName()
	}
	challenges, err := env.DB.ListBillChallenges(ctx, sp.ID(), "waiting")
	if err != nil {
		return nil, err
	}
	asking := map[uuid.UUID]bool{}
	for _, challenge := range challenges {
		if !challenge.ExpiresAt.After(now) {
			continue
		}
		asking[challenge.ConnectionID] = true
		items = append(items, reviewItem{
			ReviewItem: domain.ReviewItem{
				Key: "challenge:" + challenge.ID.String(), Kind: domain.ReviewSignInCode,
				DueAt: challenge.ExpiresAt, Since: challenge.CreatedAt,
			},
			Title:   providerName[challenge.ConnectionID] + " wants a code",
			Detail:  challenge.Prompt,
			Link:    "/settings/bills",
			Actions: []reviewAction{{Action: "answer_code", Label: "Send"}},
			Target:  reviewTarget{ID: challenge.ID},
		})
	}
	for _, conn := range connections {
		if !conn.NeedsSignIn || asking[conn.ID] {
			continue
		}
		items = append(items, reviewItem{
			ReviewItem: domain.ReviewItem{Key: "bill-sign-in:" + conn.ID.String(), Kind: domain.ReviewSignIn},
			Title:      "Sign in to " + conn.DisplayName(),
			Detail:     signInWhy(conn.SignInPausedFor),
			Link:       "/settings/bills",
			Subjects:   []string{conn.ID.String()},
		})
	}
	for _, merchant := range domain.Merchants {
		merchantAccounts, err := env.DB.ListMerchantAccounts(ctx, sp.ID(), merchant.ID)
		if err != nil {
			return nil, err
		}
		for _, account := range merchantAccounts {
			if !account.NeedsSignIn {
				continue
			}
			items = append(items, reviewItem{
				ReviewItem: domain.ReviewItem{Key: "merchant-sign-in:" + account.ID.String(), Kind: domain.ReviewSignIn},
				Title:      "Sign in to " + account.Name(),
				Detail:     signInWhy(account.SignInPausedFor),
				Link:       merchant.SettingsPath,
				Subjects:   []string{account.ID.String()},
			})
		}
		_, unmatched, err := env.DB.ListMerchantOrders(ctx, sp.ID(),
			store.MerchantOrderQuery{Merchant: merchant.ID, Unmatched: true, Limit: 1})
		if err != nil {
			return nil, err
		}
		if unmatched > 0 {
			items = append(items, reviewItem{
				ReviewItem: domain.ReviewItem{
					Key: "unmatched-orders:" + string(merchant.ID), Kind: domain.ReviewUnmatchedOrder, Count: unmatched,
				},
				Title:  fmt.Sprintf("%d %s with no charge matched", unmatched, pluralNoun(unmatched, merchant.Name+" "+merchant.Noun, merchant.Name+" "+merchant.Plural)),
				Detail: "Match them to card charges, or ignore the ones that never were.",
				Link:   merchant.SettingsPath,
			})
		}
	}

	banks, err := env.DB.ListConnections(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	for _, bank := range banks {
		if bank.Status != store.ConnectionCredentialsExpired {
			continue
		}
		item := reviewItem{
			ReviewItem: domain.ReviewItem{Key: "bank:" + bank.ID.String(), Kind: domain.ReviewBankReconnect},
			Title:      "Reconnect " + bank.Name,
			Detail:     "The bank connection needs a new setup token.",
			Link:       "/settings/accounts",
		}
		if bank.LastSuccessfulSyncAt != nil {
			item.Since = *bank.LastSuccessfulSyncAt
			item.Detail = "Sync stopped " + daysAgo(domain.DateOf(*bank.LastSuccessfulSyncAt), today)
		}
		items = append(items, item)
	}

	reminders, err := loadReminders(ctx, env, sp, today.AddDays(-reminderDaysBack), today.AddDays(statementDueWithin))
	if err != nil {
		return nil, err
	}
	for _, one := range reminders {
		if one.MarkedPaid {
			continue
		}
		manual := one.Series.ID == ""
		switch {
		case manual:
			bill, err := uuid.Parse(string(one.BillID))
			if err != nil {
				continue
			}
			items = append(items, reviewItem{
				ReviewItem: domain.ReviewItem{
					Key: "statement:" + bill.String(), Kind: domain.ReviewStatementDue,
					DueAt: one.DueOn.Time(), Amount: one.Amount,
				},
				Title:   "Pay " + one.Label + " by hand",
				Detail:  "Statement due " + dueWords(one.DueOn, today),
				Link:    "/upcoming",
				Actions: []reviewAction{{Action: "mark_paid", Label: "Mark paid"}},
				Target:  reviewTarget{ID: bill},
			})
		case one.Status == string(domain.OccurrencePastDue):
			series, err := uuid.Parse(string(one.SeriesID))
			if err != nil {
				continue
			}
			items = append(items, reviewItem{
				ReviewItem: domain.ReviewItem{
					Key: "bill:" + series.String() + ":" + one.DueOn.String(), Kind: domain.ReviewBillPastDue,
					DueAt: one.DueOn.Time(), Amount: one.Amount,
				},
				Title:    one.Label + " is past due",
				Detail:   "Expected " + dueWords(one.DueOn, today),
				Link:     "/upcoming",
				Subjects: []string{series.String()},
				Actions: []reviewAction{
					{Action: "mark_paid", Label: "Mark paid"},
					{Action: "skip_occurrence", Label: "Skip"},
				},
				Target: reviewTarget{SeriesID: series, DueOn: one.DueOn},
			})
		}
	}

	if len(duplicates) > 0 {
		ids := make([]uuid.UUID, 0, 2*len(duplicates))
		for _, pair := range duplicates {
			ids = append(ids, pair.FirstID, pair.SecondID)
		}
		legs, err := env.DB.ListTransactions(ctx, sp.ID(), store.TransactionQuery{IDs: ids})
		if err != nil {
			return nil, err
		}
		byID := make(map[uuid.UUID]store.Transaction, len(legs))
		for _, leg := range legs {
			byID[leg.ID] = leg
		}
		accountName := map[uuid.UUID]string{}
		for _, account := range accounts {
			accountName[account.ID] = account.Name
		}
		for _, pair := range duplicates {
			first, okFirst := byID[pair.FirstID]
			second, okSecond := byID[pair.SecondID]
			if !okFirst || !okSecond {
				continue
			}
			keep := uuid.MustParse(string(domain.SuggestedDuplicateKeep(store.DomainTransaction(first), store.DomainTransaction(second))))
			other := second.ID
			if keep == second.ID {
				other = first.ID
			}
			items = append(items, reviewItem{
				ReviewItem: domain.ReviewItem{
					Key: duplicateItemKey(pair.ID), Kind: domain.ReviewDuplicate,
					Amount: first.Amount, Since: pair.CreatedAt,
				},
				Title: "Possible duplicate: " + displayPayee(first),
				Detail: fmt.Sprintf("%s and %s on %s, the same amount from two sources",
					first.Date.String(), second.Date.String(), accountName[first.AccountID]),
				Link:     "/settings/duplicates",
				Subjects: []string{first.ID.String(), second.ID.String()},
				Actions: []reviewAction{
					{Action: "keep_suggested", Label: "Merge into one", Confirm: true},
					{Action: "both_real", Label: "Both are real", Confirm: true},
				},
				Target: reviewTarget{ID: pair.ID, KeepID: keep, OtherID: other},
			})
		}
	}

	orphans, err := service.NewTransfers(env.DB).FindOrphanPairs(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	if len(orphans) > 0 {
		items = append(items, reviewItem{
			ReviewItem: domain.ReviewItem{Key: "orphan-transfers", Kind: domain.ReviewOrphanTransfers, Count: len(orphans)},
			Title:      fmt.Sprintf("%d transfer %s lost %s partner", len(orphans), pluralNoun(len(orphans), "leg", "legs"), pluralNoun(len(orphans), "its", "their")),
			Detail:     "Until they are released they count toward nothing, so their money is missing from reports.",
			Link:       "/settings/transfers",
			Actions:    []reviewAction{{Action: "repair_transfers", Label: "Release them"}},
		})
	}

	for _, account := range accounts {
		if !account.HasWithheldBalance || account.WithheldBalanceAt == nil || account.IsDeleted {
			continue
		}
		items = append(items, reviewItem{
			ReviewItem: domain.ReviewItem{
				Key: "held:" + account.ID.String(), Kind: domain.ReviewHeldBalance,
				Amount: account.WithheldBalance, Since: *account.WithheldBalanceAt,
			},
			Title:    account.Name + ": a new balance is on hold",
			Detail:   "The bank reported " + account.WithheldBalance.String() + ", which looks like a reset.",
			Link:     "/settings/accounts",
			Subjects: []string{account.ID.String()},
			Actions:  []reviewAction{{Action: "accept_balance", Label: "Accept it", Confirm: true}},
			Target:   reviewTarget{ID: account.ID},
		})
	}

	pending, err := env.DB.ListPendingAutomationActions(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	for _, one := range pending {
		if isRowSuggestion(one.Action) {
			continue
		}
		items = append(items, reviewItem{
			ReviewItem: domain.ReviewItem{
				Key: "proposal:" + one.Action.ID.String(), Kind: domain.ReviewProposal, Since: one.Action.CreatedAt,
			},
			Title:  one.Action.Summary,
			Detail: one.Subject,
			Link:   "/assistant",
			Actions: []reviewAction{
				{Action: "accept_proposal", Label: "Accept"},
				{Action: "decline_proposal", Label: "Decline"},
			},
			Target: reviewTarget{ID: one.Action.ID},
		})
	}
	return items, nil
}

// gatherReviewRows is the row lane: every unreviewed row on an account that
// is not ignored, with its reason, grouped by effective date, newest first.
func gatherReviewRows(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accounts map[uuid.UUID]store.Account, categories map[domain.ID]domain.Category,
	duplicates []store.DuplicateCandidate,
) ([]reviewDay, []domain.ReviewReason, error) {
	entries, err := env.DB.RegisterEntries(ctx, sp.ID(), store.RegisterQuery{HasReviewed: true, IsReviewed: false})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		// A padding row folds away wherever rows are listed.
		if !entry.IsPadding {
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}

	postings, byID, err := service.LoadPostings(ctx, env.DB, sp.ID(), store.TransactionQuery{IDs: ids})
	if err != nil {
		return nil, nil, err
	}
	suggestions, err := env.DB.PendingActionsForTransactions(ctx, sp.ID(), ids)
	if err != nil {
		return nil, nil, err
	}
	pairOf := map[uuid.UUID]uuid.UUID{}
	for _, pair := range duplicates {
		pairOf[pair.FirstID], pairOf[pair.SecondID] = pair.ID, pair.ID
	}
	names := make([]string, 0, len(byID))
	for _, txn := range byID {
		names = append(names, txn.StatementName)
	}
	history, err := env.DB.RecentReviewed(ctx, sp.ID(), names)
	if err != nil {
		return nil, nil, err
	}

	postingByID := make(map[domain.ID]domain.Posting, len(postings))
	for _, posting := range postings {
		postingByID[posting.Txn.ID] = posting
	}

	dayOf := map[domain.Date]*reviewDay{}
	reasons := make([]domain.ReviewReason, 0, len(byID))
	for _, txn := range byID {
		dtxn := store.DomainTransaction(txn)
		row := reviewRow{Txn: txn, AccountName: accounts[txn.AccountID].Name, AccountMask: domain.LastFour(accounts[txn.AccountID].MaskedNumber)}
		facts := domain.ReviewRow{
			Txn:          dtxn,
			Undetermined: txn.CategoryCheckNote != "" && dtxn.IsUncategorized(),
			Amount:       domain.AmountUsualness(dtxn, history, domain.SeriesRange{}),
		}
		if pair, ok := pairOf[txn.ID]; ok {
			facts.DuplicateLeg = true
			row.DuplicateKey = duplicateItemKey(pair)
		}
		if pending, ok := suggestions[txn.ID]; ok {
			if suggestion := transactionSuggestion(pending); suggestion != nil {
				row.Suggestion = suggestion
				facts.HasSuggestion = true
				if suggestion.Confidence != nil {
					facts.HasConfidence, facts.Confidence = true, *suggestion.Confidence
				}
			}
		}
		row.Reason = domain.ReviewReasonOf(facts)
		reasons = append(reasons, row.Reason)

		on := dtxn.ReportingDate(domain.DateEffective)
		day := dayOf[on]
		if day == nil {
			day = &reviewDay{Date: on}
			dayOf[on] = day
		}
		day.Rows = append(day.Rows, row)
		if !row.Reason.LooksRight() {
			day.NeedsALook++
		}
	}

	days := make([]reviewDay, 0, len(dayOf))
	for _, day := range dayOf {
		sort.Slice(day.Rows, func(i, j int) bool {
			a, b := day.Rows[i].Txn, day.Rows[j].Txn
			if a.Date != b.Date {
				return a.Date.After(b.Date)
			}
			return a.ID.String() < b.ID.String()
		})
		dayPostings := make([]domain.Posting, 0, len(day.Rows))
		for _, row := range day.Rows {
			if posting, ok := postingByID[domain.ID(row.Txn.ID.String())]; ok {
				dayPostings = append(dayPostings, posting)
			}
		}
		day.Out = domain.ReviewDayOut(dayPostings, categories)
		days = append(days, *day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date.After(days[j].Date) })
	return days, reasons, nil
}

func duplicateItemKey(pair uuid.UUID) string { return "duplicate:" + pair.String() }

func displayPayee(txn store.Transaction) string {
	if strings.TrimSpace(txn.Payee) != "" {
		return txn.Payee
	}
	return txn.StatementName
}

// signInWhy says why a connection stopped, from its paused reason.
func signInWhy(reason string) string {
	switch reason {
	case "password_refused":
		return "The provider refused the saved password."
	case "code_needed":
		return "The provider wants a code to sign in."
	case "page_check":
		return "The provider asked for a check a person has to pass."
	default:
		return "The saved session ended."
	}
}

func dueWords(due, today domain.Date) string {
	days := int(today.Time().Sub(due.Time()).Hours() / 24)
	switch {
	case days == 0:
		return due.Time().Format("Jan 2") + ", today"
	case days > 0:
		return fmt.Sprintf("%s · %d %s late", due.Time().Format("Jan 2"), days, pluralNoun(days, "day", "days"))
	default:
		return due.Time().Format("Mon, Jan 2")
	}
}

func daysAgo(on, today domain.Date) string {
	days := int(today.Time().Sub(on.Time()).Hours() / 24)
	switch days {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

func pluralNoun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
