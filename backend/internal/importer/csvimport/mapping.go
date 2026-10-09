package csvimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Rows in, rows or named errors out; writing.go is the only part that needs a
// database. Ids are allocated here, and the writer replaces an allocated id
// wherever the space already holds a row with the same natural key.

// DefaultCurrency is used when the operator names none; the file does not say.
const DefaultCurrency = "USD"

// Options are the choices the file cannot make.
type Options struct {
	// Source names the file in the report's problems; empty means the CSV
	// export.
	Source string

	// Currency stamps every transaction; the file has no currency column.
	Currency string
}

// Account is one account the file mentions, with the classification the name
// rule produced.
type Account struct {
	Row   store.Account
	Guess AccountGuess
}

// Category is one node of the category tree, keyed by its full path
// ("Auto & Transport:Registration"): a leaf name is not unique.
type Category struct {
	Row   store.Category
	Path  string
	Guess CategoryGuess
	Depth int
}

// Mapped is one run's output: every row the import would write, plus the
// report that says whether it may.
type Mapped struct {
	Report *importer.Report

	Accounts     []*Account
	Categories   []*Category
	Tags         []*store.Tag
	Transactions []*store.Transaction
}

// Map turns read rows into the rows Agentifi stores. A row it cannot represent
// is named on the caller's report, which then refuses the write.
func Map(report *importer.Report, rows []Row, options Options) *Mapped {
	if options.Currency == "" {
		options.Currency = DefaultCurrency
	}
	source := options.Source
	if source == "" {
		source = storeCSV
	}
	m := &mapper{
		source:      source,
		report:      report,
		options:     options,
		accountIDs:  map[string]uuid.UUID{},
		categoryIDs: map[string]uuid.UUID{},
		tagIDs:      map[string]uuid.UUID{},
		occurrences: map[string]int{},
		signs:       map[string]*signTally{},
	}
	m.out = &Mapped{Report: m.report}

	m.mapAccounts(rows)
	m.mapCategories(rows)
	m.mapTags(rows)
	m.mapTransactions(rows)
	m.checkCategorySigns()
	m.countWritten()
	return m.out
}

type signTally struct{ positive, negative int }

type mapper struct {
	source  string
	report  *importer.Report
	options Options
	out     *Mapped

	accountIDs  map[string]uuid.UUID
	categoryIDs map[string]uuid.UUID
	tagIDs      map[string]uuid.UUID
	// accountNames is the lowercased set the category rule matches against, to
	// recognise a transfer leg by the counterparty account in its category.
	accountNames map[string]bool
	categories   map[string]*Category

	// occurrences counts rows already seen with one natural key, so two
	// genuinely identical purchases on one day keep two rows instead of
	// colliding on the dedupe key.
	occurrences map[string]int
	signs       map[string]*signTally
}

// -- accounts ---------------------------------------------------------

func (m *mapper) mapAccounts(rows []Row) {
	m.accountNames = map[string]bool{}
	for _, row := range rows {
		key := strings.ToLower(row.Account)
		if _, seen := m.accountIDs[key]; seen {
			continue
		}
		guess := GuessAccount(row.Account)
		id := uuid.New()
		m.accountIDs[key] = id
		m.accountNames[key] = true
		if !guess.Matched() {
			m.report.Warnf(importer.KindAccountGuessed, m.source, lineID(row.Line), colAccount,
				"no naming rule matched %q, so it was classified %s/%s; set its kind by hand",
				row.Account, guess.Kind, guess.Type)
		}
		m.out.Accounts = append(m.out.Accounts, &Account{
			Guess: guess,
			Row: store.Account{
				ID:        id,
				Name:      row.Account,
				Kind:      guess.Kind,
				Type:      guess.Type,
				Currency:  m.options.Currency,
				SortOrder: len(m.out.Accounts),
				// A CSV states no balance; the transactions are the balance
				// until the account is linked to SimpleFIN.
				IncludeInNetWorth: true,
			},
		})
	}
	m.report.Read["accounts named"] = len(m.out.Accounts)
}

// -- categories -------------------------------------------------------

// mapCategories builds the tree, creating every ancestor a path implies even
// when no row is filed against it.
func (m *mapper) mapCategories(rows []Row) {
	m.categories = map[string]*Category{}
	paths := make([]string, 0, 128)
	for _, row := range rows {
		if domain.NamesUncategorized(row.Category) {
			// Uncategorized is the absence of a category, not one to create.
			continue
		}
		if row.Category == "" {
			// Simplifi writes "Uncategorized" for an unfiled row, so an empty
			// cell is a row we cannot place.
			m.report.Warnf(importer.KindUncategorized, m.source, lineID(row.Line), colCategory,
				"is empty; the transaction was left uncategorized")
			continue
		}
		levels := splitCategoryPath(row.Category)
		for depth := range levels {
			path := strings.Join(levels[:depth+1], categorySeparator)
			if _, seen := m.categories[path]; seen {
				continue
			}
			m.categories[path] = &Category{Path: path, Depth: depth}
			paths = append(paths, path)
		}
	}

	// Shallowest first: the writer relies on this order, because
	// categories.parent_id is checked on insert, not at commit.
	sort.SliceStable(paths, func(i, j int) bool {
		return m.categories[paths[i]].Depth < m.categories[paths[j]].Depth
	})

	for _, path := range paths {
		node := m.categories[path]
		levels := splitCategoryPath(path)
		guess := GuessCategory(path, m.accountNames)
		id := uuid.New()
		m.categoryIDs[path] = id

		parentID := uuid.Nil
		if node.Depth > 0 {
			parentID = m.categoryIDs[strings.Join(levels[:node.Depth], categorySeparator)]
		}
		// A top-level category the account rule would recognise is almost
		// certainly a transfer leg to an account absent from this file. It is
		// named rather than reclassified.
		if node.Depth == 0 && guess.Kind == domain.CategoryExpense && GuessAccount(levels[0]).Matched() {
			m.report.Warnf(importer.KindCategoryCheck, m.source, path, colCategory,
				"%q reads as an account name but no account in the file is called that; "+
					"if it is one, its rows are transfers rather than spending", path)
		}

		node.Guess = guess
		node.Row = store.Category{
			ID:       id,
			ParentID: parentID,
			Name:     levels[node.Depth],
			Kind:     guess.Kind,
			// Rows already filed against a system category keep it; only the
			// picker is closed.
			IsUserAssignable: !guess.System,
			IsEditable:       !guess.System,
			SortOrder:        len(m.out.Categories),
		}
		m.out.Categories = append(m.out.Categories, node)
	}
	m.report.Read["category paths"] = len(m.out.Categories)
}

// checkCategorySigns reports a category whose every row contradicts the kind
// the name rule gave it. A warning, not an error: a tax refund under Taxes is
// legitimate.
func (m *mapper) checkCategorySigns() {
	for _, category := range m.out.Categories {
		tally := m.signs[category.Path]
		if tally == nil {
			continue
		}
		switch {
		case category.Row.Kind == domain.CategoryExpense && tally.negative == 0 && tally.positive > 0:
			// The path goes in the message: problems are deduplicated by
			// message.
			m.report.Warnf(importer.KindCategoryCheck, m.source, category.Path, colCategory,
				"%q is classified as spending, but every row filed against it is positive",
				category.Path)
		case category.Row.Kind == domain.CategoryIncome && tally.positive == 0 && tally.negative > 0:
			m.report.Warnf(importer.KindCategoryCheck, m.source, category.Path, colCategory,
				"%q is classified as income, but every row filed against it is negative",
				category.Path)
		}
	}
}

// -- tags -------------------------------------------------------------

func (m *mapper) mapTags(rows []Row) {
	for _, row := range rows {
		for _, name := range row.Tags {
			key := strings.ToLower(name)
			if _, seen := m.tagIDs[key]; seen {
				continue
			}
			id := uuid.New()
			m.tagIDs[key] = id
			m.out.Tags = append(m.out.Tags, &store.Tag{ID: id, Name: name})
		}
	}
	m.report.Read["tags named"] = len(m.out.Tags)
}

// -- transactions -----------------------------------------------------

// mapTransactions walks the rows once, reassembling split groups as it goes.
func (m *mapper) mapTransactions(rows []Row) {
	splitRows, splitGroups := 0, 0
	for at := 0; at < len(rows); {
		group := splitGroupAt(rows, at)
		at += len(group)
		if len(group) > 1 || group[0].IsSplit {
			splitRows += len(group)
			splitGroups++
		}
		m.mapGroup(group)
	}
	m.report.Read["split rows"] = splitRows
	m.report.Read["split groups"] = splitGroups
	m.report.Read["transactions"] = len(m.out.Transactions)
}

// splitGroupAt returns the rows of one transaction starting at `at`.
//
// A split arrives flattened: adjacent Split=yes rows sharing the date,
// account, payee and statement name. The statement name is needed to tell
// apart two splits of one payee on one day; adjacent groups agreeing on all
// four are indistinguishable, so group sizes go on the report.
func splitGroupAt(rows []Row, at int) []Row {
	head := rows[at]
	if !head.IsSplit {
		return rows[at : at+1]
	}
	end := at + 1
	for end < len(rows) && rows[end].IsSplit && sameSplitGroup(head, rows[end]) {
		end++
	}
	return rows[at:end]
}

func sameSplitGroup(a, b Row) bool {
	return a.Date == b.Date &&
		strings.EqualFold(a.Account, b.Account) &&
		a.Payee == b.Payee &&
		a.StatementName == b.StatementName
}

func (m *mapper) mapGroup(group []Row) {
	head := group[0]
	id := lineID(head.Line)

	// A row with no date would vanish from every register window. The CSV
	// reader rejects one earlier; this is the backstop for readers such as
	// OFX that build a Row directly.
	if head.Date.IsZero() {
		m.report.Errorf(m.source, id, colDate,
			"has no valid date, so it cannot be filed under any month")
	}

	if head.IsSplit && len(group) == 1 {
		// A split of one lost its siblings in the export; it imports plain.
		m.report.Warnf(importer.KindSplitDisagrees, m.source, id, colSplit,
			"is marked as a split but has no sibling rows; imported as a whole transaction")
	}

	amounts := make([]domain.Money, len(group))
	for i, row := range group {
		amounts[i] = row.Amount
		m.tallySign(row)
	}
	amount := domain.Total(amounts...)

	txn := &store.Transaction{
		ID:            uuid.New(),
		AccountID:     m.accountIDs[strings.ToLower(head.Account)],
		ExternalID:    m.externalID(head, amount),
		Date:          head.Date,
		Amount:        amount,
		Currency:      m.options.Currency,
		StatementName: head.StatementName,
		Payee:         head.Payee,
		Source:        domain.SourceFileImport,
		UserFlag:      head.Flag,
	}

	if txn.StatementName == "" {
		// Left empty rather than filled from the payee: matching reads the
		// bank's wording, which the payee is not.
		m.report.Warnf(importer.KindNoStatementName, m.source, id, colStatement,
			"is empty, so this row has no bank wording for a rule or a series to match against")
	}

	m.applyFlags(txn, group)
	if len(group) == 1 {
		m.applySingle(txn, head)
	} else {
		m.applySplits(txn, group)
	}
	m.out.Transactions = append(m.out.Transactions, txn)
}

// applyFlags folds the per-row flags of a split group onto its parent.
// Reviewed needs every row; pending and excluded need only one, the safe
// direction for each.
func (m *mapper) applyFlags(txn *store.Transaction, group []Row) {
	txn.IsReviewed = true
	reviewedDiffers, pendingDiffers, excludedDiffers := false, false, false
	for _, row := range group {
		txn.IsReviewed = txn.IsReviewed && row.Reviewed
		txn.IsPending = txn.IsPending || row.Pending
		txn.ExcludedFromReports = txn.ExcludedFromReports || row.Excluded

		reviewedDiffers = reviewedDiffers || row.Reviewed != group[0].Reviewed
		pendingDiffers = pendingDiffers || row.Pending != group[0].Pending
		excludedDiffers = excludedDiffers || row.Excluded != group[0].Excluded

		if row.Recurring {
			// The CSV names no series, only that one existed; none is invented.
			m.report.Warnf(importer.KindMissingReference, m.source, lineID(row.Line), colRecurring,
				"is part of a recurring series the export does not name; imported with no series link")
		}
	}

	// The file has one exclusion column for our two independent flags; setting
	// both is the only choice that cannot understate a total.
	txn.ExcludedFromSpendingPlan = txn.ExcludedFromReports
	if txn.ExcludedFromReports {
		m.report.Warnf(importer.KindExclusion, m.source, lineID(group[0].Line), colExclusion,
			"is one column and the schema has two independent flags; "+
				"excluded_from_reports and excluded_from_spending_plan were both set")
	}

	id := lineID(group[0].Line)
	if reviewedDiffers {
		m.report.Warnf(importer.KindSplitDisagrees, m.source, id, colReviewed,
			"the rows of this split disagree; the transaction is reviewed only if every row was")
	}
	if pendingDiffers {
		m.report.Warnf(importer.KindSplitDisagrees, m.source, id, colStatus,
			"the rows of this split disagree; the transaction is pending because one row was")
	}
	if excludedDiffers {
		m.report.Warnf(importer.KindSplitDisagrees, m.source, id, colExclusion,
			"the rows of this split disagree; the transaction is excluded because one row was")
	}
}

func (m *mapper) applySingle(txn *store.Transaction, row Row) {
	txn.Notes = row.Notes
	txn.CheckNumber = row.CheckNumber
	txn.CategoryID = m.categoryIDs[canonicalPath(row.Category)]
	txn.TagIDs = m.tagsFor(row)
	if category, found := m.categories[canonicalPath(row.Category)]; found && category.Guess.Source != "" {
		txn.Source = category.Guess.Source
	}
}

// applySplits hangs the group's rows off the parent as splits. The parent's
// category stays null, or reports would count it twice.
func (m *mapper) applySplits(txn *store.Transaction, group []Row) {
	for position, row := range group {
		if row.CheckNumber != "" && txn.CheckNumber == "" {
			txn.CheckNumber = row.CheckNumber
		}
		txn.Splits = append(txn.Splits, store.Split{
			ID:         uuid.New(),
			Position:   position,
			Amount:     row.Amount,
			CategoryID: m.categoryIDs[canonicalPath(row.Category)],
			// The split's own Notes cell, never folded onto the parent.
			Memo:   row.Notes,
			TagIDs: m.tagsFor(row),
		})
	}
}

func (m *mapper) tagsFor(row Row) []uuid.UUID {
	if len(row.Tags) == 0 {
		return nil
	}
	out := make([]uuid.UUID, 0, len(row.Tags))
	for _, name := range row.Tags {
		out = append(out, m.tagIDs[strings.ToLower(name)])
	}
	return out
}

func (m *mapper) tallySign(row Row) {
	if row.Category == "" {
		return
	}
	path := canonicalPath(row.Category)
	tally := m.signs[path]
	if tally == nil {
		tally = &signTally{}
		m.signs[path] = tally
	}
	switch {
	case row.Amount.IsPositive():
		tally.positive++
	case row.Amount.IsNegative():
		tally.negative++
	}
}

// externalID derives the stable dedupe key these rows do not carry, from the
// account, date, amount and the two names, plus an ordinal in file order for
// identical rows. It makes a re-import idempotent; a row edited in Simplifi
// re-exports as a new transaction.
func (m *mapper) externalID(head Row, amount domain.Money) string {
	if head.ExternalID != "" {
		return head.ExternalID
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.ToLower(head.Account),
		head.Date.String(),
		amount.String(),
		strings.ToLower(head.StatementName),
		strings.ToLower(head.Payee),
	}, "\x00")))
	key := hex.EncodeToString(sum[:12])
	occurrence := m.occurrences[key]
	m.occurrences[key] = occurrence + 1
	return fmt.Sprintf("csv:%s:%d", key, occurrence)
}

func (m *mapper) countWritten() {
	splits, txnTags, splitTags := 0, 0, 0
	for _, txn := range m.out.Transactions {
		splits += len(txn.Splits)
		txnTags += len(txn.TagIDs)
		for _, split := range txn.Splits {
			splitTags += len(split.TagIDs)
		}
	}
	m.report.Written["accounts"] = len(m.out.Accounts)
	m.report.Written["categories"] = len(m.out.Categories)
	m.report.Written["tags"] = len(m.out.Tags)
	m.report.Written["transactions"] = len(m.out.Transactions)
	m.report.Written["transaction_splits"] = splits
	m.report.Written["transaction_tags"] = txnTags
	m.report.Written["split_tags"] = splitTags
}
