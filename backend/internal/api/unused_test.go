package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Purging the categories and tags nothing uses.
//
// The tests that matter here are the ones about what counts as a use, because
// that list is the only thing between a purge and a rule, a reminder or a saved
// report that quietly loses what it pointed at. Most of the references carry
// no foreign key, so nothing but the queries in store/unused.go would notice.

// --- The tree rule, without a database ---------------------------------------

func categoryFor(name string, parent uuid.UUID, editable bool) store.Category {
	return store.Category{
		ID: uuid.New(), ParentID: parent, Name: name,
		Kind: domain.CategoryExpense, IsUserAssignable: true, IsEditable: editable,
	}
}

func everything(categories []store.Category) map[uuid.UUID]bool {
	want := map[uuid.UUID]bool{}
	for _, one := range categories {
		want[one.ID] = true
	}
	return want
}

func TestAGroupGoesOnlyOnceItsWholeBranchCanGo(t *testing.T) {
	group := categoryFor("Housing", uuid.Nil, true)
	rent := categoryFor("Rent", group.ID, true)
	mortgage := categoryFor("Mortgage", group.ID, true)
	categories := []store.Category{group, rent, mortgage}

	ok, _ := prunableCategories(categories, map[uuid.UUID][]string{}, everything(categories))
	require.True(t, ok[group.ID])
	require.True(t, ok[rent.ID])
	require.True(t, ok[mortgage.ID])

	uses := map[uuid.UUID][]string{rent.ID: {"transactions"}}
	ok, why := prunableCategories(categories, uses, everything(categories))
	require.False(t, ok[rent.ID])
	require.False(t, ok[group.ID])
	require.True(t, ok[mortgage.ID])
	require.Equal(t, "it is used by transactions", why[rent.ID])
	require.Contains(t, why[group.ID], "Rent")
}

func TestAGroupHeldBackByAChildHoldsBackItsOwnParent(t *testing.T) {
	group := categoryFor("Housing", uuid.Nil, true)
	middle := categoryFor("Utilities", group.ID, true)
	leaf := categoryFor("Water", middle.ID, true)
	categories := []store.Category{group, middle, leaf}

	ok, _ := prunableCategories(
		categories, map[uuid.UUID][]string{leaf.ID: {"splits"}}, everything(categories))
	require.False(t, ok[leaf.ID])
	require.False(t, ok[middle.ID])
	require.False(t, ok[group.ID])
}

func TestASubcategoryLeftOutOfTheBatchKeepsItsParent(t *testing.T) {
	group := categoryFor("Housing", uuid.Nil, true)
	rent := categoryFor("Rent", group.ID, true)
	categories := []store.Category{group, rent}

	ok, why := prunableCategories(categories, map[uuid.UUID][]string{},
		map[uuid.UUID]bool{group.ID: true})
	require.False(t, ok[group.ID])
	require.Contains(t, why[group.ID], "Rent")
}

func TestACategoryTheAppMaintainsNeverGoes(t *testing.T) {
	system := categoryFor("Opening Balance", uuid.Nil, false)
	ok, why := prunableCategories(
		[]store.Category{system}, map[uuid.UUID][]string{}, everything([]store.Category{system}))
	require.False(t, ok[system.ID])
	require.Equal(t, "it is maintained by the app", why[system.ID])
}

func TestRefusalsNameThreeAndCountTheRest(t *testing.T) {
	require.Equal(t, `"a", "b" and "c", and 2 more`,
		refusalMessage([]string{`"e"`, `"d"`, `"c"`, `"b"`, `"a"`}))
	require.Equal(t, `"a"`, refusalMessage([]string{`"a"`}))
}

// --- Fixtures -----------------------------------------------------------------

// spare adds a category nothing refers to, and returns it.
func spare(t *testing.T, l *ledger, name string) store.Category {
	t.Helper()
	category := &store.Category{
		Name: name, Kind: domain.CategoryExpense, IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, l.env.DB.CreateCategory(t.Context(), store.SpaceIDOf(l.id("space")), category))
	return *category
}

// spareTag adds a tag nothing refers to, and returns it.
func spareTag(t *testing.T, l *ledger, name string) store.Tag {
	t.Helper()
	tag := &store.Tag{Name: name}
	require.NoError(t, l.env.DB.CreateTag(t.Context(), store.SpaceIDOf(l.id("space")), tag))
	return *tag
}

func exec(t *testing.T, l *ledger, sql string, args ...any) {
	t.Helper()
	_, err := l.env.DB.Pool().Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

// filterNaming adds a filter with one membership item over field, and returns
// its id. Nothing points at it until the caller makes something.
func filterNaming(
	t *testing.T, l *ledger, scope string, field domain.FilterField, ids ...uuid.UUID,
) uuid.UUID {
	t.Helper()
	space := store.SpaceIDOf(l.id("space"))
	filter := &store.Filter{Name: "Named", Scope: scope}
	require.NoError(t, l.env.DB.CreateFilter(t.Context(), space, filter))
	filter.Items = []store.FilterItem{{
		FilterID: filter.ID, Field: string(field), Operator: string(domain.OpIn), ValueIDs: ids,
	}}
	require.NoError(t, l.env.DB.ReplaceFilterItems(t.Context(), space, filter))
	return filter.ID
}

type unusedList struct {
	categories map[string]map[string]any
	tags       map[string]map[string]any
	body       map[string]any
}

func unused(l *ledger) unusedList {
	l.t.Helper()
	body := l.alex.get("/unused").requireStatus(http.StatusOK).json()
	out := unusedList{
		categories: map[string]map[string]any{}, tags: map[string]map[string]any{}, body: body,
	}
	for _, row := range body["categories"].([]any) {
		one := row.(map[string]any)
		out.categories[one["name"].(string)] = one
	}
	for _, row := range body["tags"].([]any) {
		one := row.(map[string]any)
		out.tags[one["name"].(string)] = one
	}
	return out
}

func purge(l *ledger, categories []uuid.UUID, tags []uuid.UUID) *response {
	l.t.Helper()
	body := map[string]any{"category_ids": categories, "tag_ids": tags}
	if categories == nil {
		body["category_ids"] = []uuid.UUID{}
	}
	if tags == nil {
		body["tag_ids"] = []uuid.UUID{}
	}
	return l.alex.post("/unused/purge", body)
}

// --- What counts as a use of a category ---------------------------------------

func TestTheUnusedListLeavesOutEverythingTheHistoryNames(t *testing.T) {
	l := buildLedger(t)
	alimony := spare(t, l, "Alimony")
	exec(t, l, `UPDATE transactions SET is_reviewed = true WHERE id = $1`, l.id("august_groceries"))

	listed := unused(l)
	require.Contains(t, listed.categories, "Alimony")
	// Groceries carries a reviewed transaction; Food & Dining holds Groceries;
	// Opening Balance is the app's own.
	require.NotContains(t, listed.categories, "Groceries")
	require.NotContains(t, listed.categories, "Food & Dining")
	require.NotContains(t, listed.categories, "Opening Balance")
	require.NotContains(t, listed.categories, "Their Groceries")
	require.NotContains(t, listed.tags, "theirs")

	require.Equal(t, alimony.ID.String(), listed.categories["Alimony"]["id"])
	require.Equal(t, "Alimony", listed.categories["Alimony"]["path"])
	require.Contains(t, listed.body["categories_checked"], "rule conditions")
	require.Contains(t, listed.body["categories_checked"], "subcategories")
	require.Contains(t, listed.body["tags_checked"], "goals")
}

func TestASubcategoryIsListedWithItsPath(t *testing.T) {
	l := buildLedger(t)
	group := spare(t, l, "Kids")
	child := &store.Category{
		ParentID: group.ID, Name: "Allowance", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, l.env.DB.CreateCategory(t.Context(), store.SpaceIDOf(l.id("space")), child))

	listed := unused(l)
	require.Equal(t, "Kids · Allowance", listed.categories["Allowance"]["path"])
	require.Contains(t, listed.categories, "Kids")

	// A used child keeps its parent off the list.
	exec(t, l, `UPDATE transactions SET category_id = $2 WHERE id = $1`,
		l.id("august_corner"), child.ID)
	listed = unused(l)
	require.NotContains(t, listed.categories, "Allowance")
	require.NotContains(t, listed.categories, "Kids")
}

// Each case plants one reference to the category and nothing else, and the
// category must drop off the list and be refused by the purge.
func TestEveryKindOfCategoryReferenceKeepsTheCategory(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, l *ledger, id uuid.UUID)
	}{
		{"a deleted reviewed transaction", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `UPDATE transactions SET category_id = $2, is_deleted = true WHERE id = $1`,
				l.id("august_corner"), id)
		}},
		{"a split", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO transaction_splits (id, transaction_id, position, amount, space_id, category_id)
				VALUES ($1, $2, 0, -25.00, $3, $4)`,
				uuid.New(), l.id("august_corner"), l.id("space"), id)
		}},
		{"a rule's action", func(t *testing.T, l *ledger, id uuid.UUID) {
			space := store.SpaceIDOf(l.id("space"))
			filter := filterNaming(t, l, "rule", domain.FieldPayee)
			require.NoError(t, l.env.DB.CreateRule(t.Context(), space, &store.Rule{
				Name: "File it", FilterID: filter, IsActive: true, SetCategoryID: id,
			}))
		}},
		{"a rule's conditions", func(t *testing.T, l *ledger, id uuid.UUID) {
			space := store.SpaceIDOf(l.id("space"))
			filter := filterNaming(t, l, "rule", domain.FieldCategory, id)
			require.NoError(t, l.env.DB.CreateRule(t.Context(), space, &store.Rule{
				Name: "Review it", FilterID: filter, IsActive: true,
			}))
		}},
		{"a reminder", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO series (id, space_id, account_id, kind, description, currency,
					alias, "interval", start_on, reminder_days, match_criteria, category_id)
				VALUES ($1, $2, $3, 'bill', 'Daycare', 'USD', 'monthly', 1, '2026-01-01', 3, 'amount', $4)`,
				uuid.New(), l.id("space"), l.id("checking"), id)
		}},
		{"a reminder's split template", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO series (id, space_id, account_id, kind, description, currency,
					alias, "interval", start_on, reminder_days, match_criteria, template_splits)
				VALUES ($1, $2, $3, 'bill', 'Daycare', 'USD', 'monthly', 1, '2026-01-01', 3, 'amount', $4::jsonb)`,
				uuid.New(), l.id("space"), l.id("checking"),
				`[{"amount":"-100.00","category_id":"`+id.String()+`","memo":"","tag_ids":[]}]`)
		}},
		{"a mail rule", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO mail_rules (id, space_id, name, income_category_id)
				VALUES ($1, $2, 'Payslips', $3)`, uuid.New(), l.id("space"), id)
		}},
		{"a watchlist", func(t *testing.T, l *ledger, id uuid.UUID) {
			filter := filterNaming(t, l, "watchlist", domain.FieldCategory, id)
			exec(t, l, `INSERT INTO watchlists (id, filter_id, name, period, space_id)
				VALUES ($1, $2, 'Watch', 'monthly', $3)`, uuid.New(), filter, l.id("space"))
		}},
		{"a planned-spend envelope", func(t *testing.T, l *ledger, id uuid.UUID) {
			l.alex.post("/spending-plan/2026-08/envelopes", map[string]any{
				"name": "Vacation", "target_amount": "100.00", "category_ids": []string{id.String()},
			}).requireStatus(http.StatusOK)
		}},
		{"a saved report", func(t *testing.T, l *ledger, id uuid.UUID) {
			filterNaming(t, l, "report", domain.FieldCategory, id)
		}},
		{"a guidance note", func(t *testing.T, l *ledger, id uuid.UUID) {
			filter := filterNaming(t, l, "guidance", domain.FieldCategory, id)
			exec(t, l, `INSERT INTO assistant_guidance (id, space_id, name, filter_id)
				VALUES ($1, $2, 'Note', $3)`, uuid.New(), l.id("space"), filter)
		}},
		{"a pending assistant proposal", func(t *testing.T, l *ledger, id uuid.UUID) {
			conversation := uuid.New()
			exec(t, l, `INSERT INTO assistant_conversations (id, space_id, user_id)
				VALUES ($1, $2, $3)`, conversation, l.id("space"), l.users["alex"].ID)
			exec(t, l, `INSERT INTO assistant_actions (id, space_id, conversation_id, tool_name,
					method, path, body)
				VALUES ($1, $2, $3, 'update_transaction', 'PATCH', $4, $5::jsonb)`,
				uuid.New(), l.id("space"), conversation,
				"/transactions/"+l.id("august_corner").String(),
				`{"category_id":"`+id.String()+`"}`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := buildLedger(t)
			kept := spare(t, l, "Kept")
			spare(t, l, "Control")
			require.Contains(t, unused(l).categories, "Kept")

			c.plant(t, l, kept.ID)

			listed := unused(l)
			require.NotContains(t, listed.categories, "Kept")
			require.Contains(t, listed.categories, "Control")
			purge(l, []uuid.UUID{kept.ID}, nil).requireStatus(http.StatusConflict)
		})
	}
}

func TestAnAppliedProposalAndARetiredRuleAreNotUses(t *testing.T) {
	l := buildLedger(t)
	gone := spare(t, l, "Gone")
	space := store.SpaceIDOf(l.id("space"))

	conversation := uuid.New()
	exec(t, l, `INSERT INTO assistant_conversations (id, space_id, user_id) VALUES ($1, $2, $3)`,
		conversation, l.id("space"), l.users["alex"].ID)
	exec(t, l, `INSERT INTO assistant_actions (id, space_id, conversation_id, tool_name,
			method, path, body, status)
		VALUES ($1, $2, $3, 'update_transaction', 'PATCH', '/transactions', $4::jsonb, 'discarded')`,
		uuid.New(), l.id("space"), conversation, `{"category_id":"`+gone.ID.String()+`"}`)

	filter := filterNaming(t, l, "rule", domain.FieldPayee)
	rule := &store.Rule{Name: "Old", FilterID: filter, IsActive: true, SetCategoryID: gone.ID}
	require.NoError(t, l.env.DB.CreateRule(t.Context(), space, rule))
	exec(t, l, `UPDATE rules SET is_deleted = true WHERE id = $1`, rule.ID)

	require.Contains(t, unused(l).categories, "Gone")
	purge(l, []uuid.UUID{gone.ID}, nil).requireStatus(http.StatusOK)
}

// --- Unreviewed transactions ---------------------------------------------------

func rowCategory(t *testing.T, l *ledger, id uuid.UUID) uuid.UUID {
	t.Helper()
	var category *uuid.UUID
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT category_id FROM transactions WHERE id = $1`, id).Scan(&category))
	if category == nil {
		return uuid.Nil
	}
	return *category
}

// appliedAction records an applied update_transaction on the row, in a thread
// an automation run wrote when run is not Nil and a person's otherwise.
func appliedAction(t *testing.T, l *ledger, run, txn uuid.UUID, body, proposed string) {
	t.Helper()
	conversation := uuid.New()
	var automationRun *uuid.UUID
	if run != uuid.Nil {
		automationRun = &run
	}
	exec(t, l, `INSERT INTO assistant_conversations (id, space_id, user_id, automation_run_id)
		VALUES ($1, $2, $3, $4)`, conversation, l.id("space"), l.users["alex"].ID, automationRun)
	exec(t, l, `INSERT INTO assistant_actions (id, space_id, conversation_id, tool_name,
			method, path, body, proposed_body, status)
		VALUES ($1, $2, $3, 'update_transaction', 'PATCH', $4, $5::jsonb, $6::jsonb, 'applied')`,
		uuid.New(), l.id("space"), conversation, "/transactions/"+txn.String(), body, proposed)
}

func TestWhatOnlyUnreviewedRowsCarryIsUnusedAndAPersonsChoiceStays(t *testing.T) {
	// Three unreviewed rows, none filed by an automation's own decision: one
	// by hand, one from a card the person changed before applying, one from a
	// card in a conversation. Each keeps the retired category, as deleting a
	// category by hand leaves it; none is handed back for a suggestion.
	l := buildLedger(t)
	gone := spare(t, l, "Gone")
	other := spare(t, l, "Other")
	tag := spareTag(t, l, "maybe")
	hand, edited, chat := l.id("card_charge"), l.id("august_groceries"), l.id("july")
	for _, id := range []uuid.UUID{hand, edited, chat} {
		exec(t, l, `UPDATE transactions SET category_id = $2 WHERE id = $1`, id, gone.ID)
	}
	exec(t, l, `INSERT INTO transaction_tags (transaction_id, tag_id) VALUES ($1, $2)`, hand, tag.ID)
	appliedAction(t, l, uuid.New(), edited,
		`{"category_id":"`+gone.ID.String()+`"}`, `{"category_id":"`+other.ID.String()+`"}`)
	appliedAction(t, l, uuid.Nil, chat, `{"category_id":"`+gone.ID.String()+`"}`, `null`)

	listed := unused(l)
	require.Contains(t, listed.categories, "Gone")
	require.Contains(t, listed.tags, "maybe")
	require.Contains(t, listed.body["categories_checked"], "reviewed transactions")
	require.Contains(t, listed.body["tags_checked"], "reviewed transactions")

	body := purge(l, []uuid.UUID{gone.ID}, []uuid.UUID{tag.ID}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), body["categories_deleted"])
	require.Equal(t, float64(1), body["tags_deleted"])
	require.Equal(t, float64(0), body["resuggested"])
	for _, id := range []uuid.UUID{hand, edited, chat} {
		require.Equal(t, gone.ID, rowCategory(t, l, id))
	}
	var links int
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM transaction_tags WHERE tag_id = $1`, tag.ID).Scan(&links))
	require.Zero(t, links)
}

func TestTheTagComesOffUnreviewedRowsOnly(t *testing.T) {
	// A tag on a reviewed row is a use and blocks the purge; the purge itself
	// never reaches a reviewed row's tags.
	l := buildLedger(t)
	going, staying := spareTag(t, l, "going"), spareTag(t, l, "staying")
	exec(t, l, `INSERT INTO transaction_tags (transaction_id, tag_id) VALUES ($1, $2), ($1, $3), ($4, $3)`,
		l.id("card_charge"), going.ID, staying.ID, l.id("august_corner"))

	listed := unused(l)
	require.Contains(t, listed.tags, "going")
	require.NotContains(t, listed.tags, "staying")
	purge(l, nil, []uuid.UUID{staying.ID}).requireStatus(http.StatusConflict)
	purge(l, nil, []uuid.UUID{going.ID}).requireStatus(http.StatusOK)

	var tags []uuid.UUID
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT array_agg(tag_id) FROM transaction_tags WHERE transaction_id = $1`,
		l.id("card_charge")).Scan(&tags))
	require.Equal(t, []uuid.UUID{staying.ID}, tags)
}

func TestARowAnAutomationFiledIsAskedAgainWhenItsCategoryGoes(t *testing.T) {
	// Snack Shack's eight earlier rows were filed by hand and never reviewed,
	// and a category check that applies what it decides filed the ninth from
	// them. Purging Snacks sends the ninth back to uncategorized and queues the
	// automations for it; the eight keep the retired category, and no longer
	// vote for it. (A card a person applies ticks its row reviewed, so an
	// unreviewed row an automation filed is one it filed without asking.)
	l := buildLedger(t)
	snacks := spare(t, l, "Snacks")
	seedHistory(l, "Snack Shack", "SNACK SHACK #12", snacks.ID.String(), 8)
	arrived := uuid.MustParse(l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-09-12", "amount": "-12.50",
		"payee": "Snack Shack", "statement_name": "SNACK SHACK #12",
	}).requireStatus(http.StatusCreated).json()["id"].(string))

	model := newFakeModel(t, answerReply("Nothing to go on."))
	allowWrites(l, model)
	automation := l.alex.post("/assistant-automations", suggestCategoriesBody(map[string]any{
		"confidence_threshold": 0.85, "mode": domain.AutomationModeApply,
	})).requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+automation+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, "agentifi", run["decided_by"], run["error"])
	action := run["conversation"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	require.Equal(t, snacks.ID.String(), action["body"].(map[string]any)["category_id"])
	require.Equal(t, "applied", action["status"])
	require.Equal(t, snacks.ID, rowCategory(t, l, arrived))

	require.Contains(t, unused(l).categories, "Snacks")
	body := purge(l, []uuid.UUID{snacks.ID}, nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), body["resuggested"])
	require.Equal(t, uuid.Nil, rowCategory(t, l, arrived))

	var kept, queued int
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM transactions WHERE category_id = $1`, snacks.ID).Scan(&kept))
	require.Equal(t, 8, kept)
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM assistant_automation_runs
		  WHERE transaction_id = $1 AND fired_by = $2 AND status = $3`,
		arrived, domain.AutomationFiredByManual, domain.AutomationRunQueued).Scan(&queued))
	require.Equal(t, 1, queued)

	again := l.alex.post("/assistant-automations/"+automation+"/run",
		map[string]any{"transaction_id": arrived}).requireStatus(http.StatusOK).json()
	require.Equal(t, "model", again["decided_by"], "a retired category settled the vote")
	require.Len(t, model.seen, 1)
	require.Empty(t, again["conversation"].(map[string]any)["actions"])
}

// --- Filters ------------------------------------------------------------------

func TestABroadFilterItemIsNotAUseAndIsRepaired(t *testing.T) {
	// "Uncategorized" is stored as "not any of every category", and Simplifi's
	// "everything except X" as a list of everything else. Counted as uses they
	// would answer "nothing is unused" forever.
	l := buildLedger(t)
	going := spare(t, l, "Alimony")
	space := store.SpaceIDOf(l.id("space"))
	var all []uuid.UUID
	for _, row := range l.alex.get("/categories").requireStatus(http.StatusOK).list() {
		all = append(all, uuid.MustParse(row["id"].(string)))
	}
	filter := filterNaming(t, l, "report", domain.FieldCategory, all...)

	require.Contains(t, unused(l).categories, "Alimony")

	body := purge(l, []uuid.UUID{going.ID}, nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), body["categories_deleted"])
	require.Equal(t, float64(1), body["filters_repaired"])

	reloaded, err := l.env.DB.GetFilter(t.Context(), space, filter)
	require.NoError(t, err)
	require.Len(t, reloaded.Items, 1)
	require.NotContains(t, reloaded.Items[0].ValueIDs, going.ID)
	require.Len(t, reloaded.Items[0].ValueIDs, len(all)-1)
}

func TestARegisterSearchNamingOnlyThePurgedIdIsRetired(t *testing.T) {
	l := buildLedger(t)
	going := spare(t, l, "Alimony")
	search := filterNaming(t, l, "ad_hoc", domain.FieldCategory, going.ID)

	require.Contains(t, unused(l).categories, "Alimony")
	body := purge(l, []uuid.UUID{going.ID}, nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), body["filters_retired"])

	var retired bool
	require.NoError(t, l.env.DB.Pool().QueryRow(t.Context(),
		`SELECT is_deleted FROM filters WHERE id = $1`, search).Scan(&retired))
	require.True(t, retired)
}

// --- What counts as a use of a tag --------------------------------------------

func TestEveryKindOfTagReferenceKeepsTheTag(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, l *ledger, id uuid.UUID)
	}{
		{"a reviewed transaction", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO transaction_tags (transaction_id, tag_id) VALUES ($1, $2)`,
				l.id("august_corner"), id)
		}},
		{"a split", func(t *testing.T, l *ledger, id uuid.UUID) {
			split := uuid.New()
			exec(t, l, `INSERT INTO transaction_splits (id, transaction_id, position, amount, space_id)
				VALUES ($1, $2, 0, -25.00, $3)`, split, l.id("august_corner"), l.id("space"))
			exec(t, l, `INSERT INTO split_tags (split_id, tag_id) VALUES ($1, $2)`, split, id)
		}},
		{"a rule's action", func(t *testing.T, l *ledger, id uuid.UUID) {
			space := store.SpaceIDOf(l.id("space"))
			filter := filterNaming(t, l, "rule", domain.FieldPayee)
			rule := &store.Rule{Name: "Tag it", FilterID: filter, IsActive: true}
			require.NoError(t, l.env.DB.CreateRule(t.Context(), space, rule))
			exec(t, l, `UPDATE rules SET add_tag_ids = ARRAY[$2::uuid] WHERE id = $1`, rule.ID, id)
		}},
		{"a rule's conditions", func(t *testing.T, l *ledger, id uuid.UUID) {
			space := store.SpaceIDOf(l.id("space"))
			filter := filterNaming(t, l, "rule", domain.FieldTag, id)
			require.NoError(t, l.env.DB.CreateRule(t.Context(), space, &store.Rule{
				Name: "Review it", FilterID: filter, IsActive: true,
			}))
		}},
		{"a saved report", func(t *testing.T, l *ledger, id uuid.UUID) {
			filterNaming(t, l, "report", domain.FieldTag, id)
		}},
		{"a reminder", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO series (id, space_id, account_id, kind, description, currency,
					alias, "interval", start_on, reminder_days, match_criteria, template_tag_ids)
				VALUES ($1, $2, $3, 'bill', 'Daycare', 'USD', 'monthly', 1, '2026-01-01', 3, 'amount',
					ARRAY[$4::uuid])`,
				uuid.New(), l.id("space"), l.id("checking"), id)
		}},
		{"a reminder's split template", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO series (id, space_id, account_id, kind, description, currency,
					alias, "interval", start_on, reminder_days, match_criteria, template_splits)
				VALUES ($1, $2, $3, 'bill', 'Daycare', 'USD', 'monthly', 1, '2026-01-01', 3, 'amount', $4::jsonb)`,
				uuid.New(), l.id("space"), l.id("checking"),
				`[{"amount":"-100.00","memo":"","tag_ids":["`+id.String()+`"]}]`)
		}},
		{"a goal", func(t *testing.T, l *ledger, id uuid.UUID) {
			exec(t, l, `INSERT INTO goals (id, account_id, name, space_id, tag_id)
				VALUES ($1, $2, 'Trip', $3, $4)`, uuid.New(), l.id("checking"), l.id("space"), id)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := buildLedger(t)
			kept := spareTag(t, l, "kept")
			spareTag(t, l, "control")
			require.Contains(t, unused(l).tags, "kept")

			c.plant(t, l, kept.ID)

			listed := unused(l)
			require.NotContains(t, listed.tags, "kept")
			require.Contains(t, listed.tags, "control")
			purge(l, nil, []uuid.UUID{kept.ID}).requireStatus(http.StatusConflict)
		})
	}
}

// --- The purge ----------------------------------------------------------------

func TestThePurgeTakesCategoriesAndTagsTogether(t *testing.T) {
	l := buildLedger(t)
	group := spare(t, l, "Kids")
	child := &store.Category{
		ParentID: group.ID, Name: "Allowance", Kind: domain.CategoryExpense,
		IsUserAssignable: true, IsEditable: true,
	}
	require.NoError(t, l.env.DB.CreateCategory(t.Context(), store.SpaceIDOf(l.id("space")), child))
	tag := spareTag(t, l, "someday")

	// The same id twice counts once.
	body := purge(l, []uuid.UUID{group.ID, child.ID, child.ID}, []uuid.UUID{tag.ID}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), body["categories_deleted"])
	require.Equal(t, float64(1), body["tags_deleted"])

	for _, row := range l.alex.get("/categories").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, group.ID.String(), row["id"])
		require.NotEqual(t, child.ID.String(), row["id"])
	}
	listed := unused(l)
	require.NotContains(t, listed.categories, "Kids")
	require.NotContains(t, listed.tags, "someday")
}

func TestThePurgeDoesNotTrustTheListItHandedOut(t *testing.T) {
	l := buildLedger(t)
	used := spare(t, l, "Rent")
	tag := spareTag(t, l, "someday")
	require.Contains(t, unused(l).categories, "Rent")

	// Somebody files a transaction under it while the screen is open.
	exec(t, l, `UPDATE transactions SET category_id = $2 WHERE id = $1`,
		l.id("august_corner"), used.ID)

	body := purge(l, []uuid.UUID{used.ID}, []uuid.UUID{tag.ID}).
		requireStatus(http.StatusConflict).json()
	require.Contains(t, body["detail"], "Rent")
	require.Contains(t, body["detail"], "transactions")
	// The whole request is refused, the tag with it.
	require.Contains(t, unused(l).tags, "someday")
}

func TestASavedFilterNamingOnlyThePurgedIdsRefusesThePurge(t *testing.T) {
	// A broad item — over half the space — that would be left empty. An empty
	// membership item selects nothing, so emptying it rewrites the report.
	l := buildLedger(t)
	a, b := spareTag(t, l, "a"), spareTag(t, l, "b")
	filterNaming(t, l, "report", domain.FieldTag, a.ID, b.ID)
	listed := unused(l)
	require.Contains(t, listed.tags, "a")
	require.Contains(t, listed.tags, "b")

	purge(l, nil, []uuid.UUID{a.ID, b.ID}).requireStatus(http.StatusConflict)
	require.Contains(t, unused(l).tags, "a")
}

func TestThePurgeRefusesTheAppsOwnAndAnotherSpacesRows(t *testing.T) {
	l := buildLedger(t)
	purge(l, []uuid.UUID{l.id("system_category")}, nil).requireStatus(http.StatusConflict)
	purge(l, []uuid.UUID{l.id("stranger_category")}, nil).requireStatus(http.StatusNotFound)
	purge(l, nil, []uuid.UUID{l.id("stranger_tag")}).requireStatus(http.StatusNotFound)
	purge(l, nil, nil).requireStatus(http.StatusUnprocessableEntity)
}

func TestAViewerCannotPurge(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").post("/unused/purge", map[string]any{
		"tag_ids": []string{l.id("tag").String()},
	}).requireStatus(http.StatusForbidden)
}
