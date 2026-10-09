package csvimport

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Every fixture in this file is invented, carrying only the shapes the real
// export contains.

// nbsp is the rune that makes two identical-looking account names two
// accounts.
const nbsp = "\u00a0"

// cells names the columns a fixture sets, so a row that leaves eleven of them
// alone does not have to count commas to say so. The zero value is a posted,
// reviewed, unexcluded, non-recurring row.
type cells struct {
	date        string
	account     string
	flag        string
	reviewed    string
	status      string
	payee       string
	statement   string
	category    string
	split       string
	tags        string
	notes       string
	attachments string
	exclusion   string
	recurring   string
	amount      string
	check       string
}

func (c cells) record() []string {
	or := func(value, fallback string) string {
		if value == "" {
			return fallback
		}
		return value
	}
	return []string{
		c.date, c.account, c.flag, or(c.reviewed, "yes"), c.status, c.payee, c.statement,
		c.category, or(c.split, "no"), c.tags, c.notes, or(c.attachments, "no"),
		or(c.exclusion, "no"), or(c.recurring, "no"), c.amount, c.check,
	}
}

// file writes a fixture through encoding/csv, so a fixture with a comma in a
// payee is quoted the way the real export quotes it.
func file(rows ...cells) string {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{
		"Date", "Account", "Flag", "Reviewed", "Status", "Payee", "Statement name",
		"Category", "Split", "Tags", "Notes", "Attachments", "Exclusion", "Recurring",
		"Amount", "Check #",
	})
	for _, row := range rows {
		_ = w.Write(row.record())
	}
	w.Flush()
	return b.String()
}

func mapped(t *testing.T, rows ...cells) *Mapped {
	t.Helper()
	report := importer.NewReport()
	read, _ := ReadRows(strings.NewReader(file(rows...)), report)
	return Map(report, read, Options{})
}

// spend is the row every test starts from: one posted, reviewed expense.
func spend(account, payee, category, amount string) cells {
	return cells{date: "Apr 2, 2026", account: account, payee: payee,
		statement: strings.ToUpper(payee), category: category, amount: amount}
}

func TestADateAndAnAmountBecomeTypedValues(t *testing.T) {
	out := mapped(t, spend("Everyday Checking", "Coffee", "Food & Dining:Coffee Shops", " -4.50"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Len(t, out.Transactions, 1)

	txn := out.Transactions[0]
	require.Equal(t, domain.NewDate(2026, time.April, 2), txn.Date)
	require.Equal(t, "-4.50", txn.Amount.String())
	require.Equal(t, "COFFEE", txn.StatementName)
	require.Equal(t, "Coffee", txn.Payee)
	require.Equal(t, domain.SourceFileImport, txn.Source)
	require.True(t, txn.IsReviewed)
	require.False(t, txn.IsPending)
}

// The amount arrives with a leading space and must never travel through a
// float on its way to Money.
func TestAmountsKeepEveryCent(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "A", "Shopping", " -0.10"),
		spend("Everyday Checking", "B", "Shopping", " -0.20"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, "-0.30",
		domain.Total(out.Transactions[0].Amount, out.Transactions[1].Amount).String())
}

// Two account names that differ only by a non-breaking space are one account.
func TestANonBreakingSpaceDoesNotCreateASecondAccount(t *testing.T) {
	out := mapped(t,
		spend("Apple"+nbsp+"Card", "A", "Shopping", " -1.00"),
		spend("Apple Card", "B", "Shopping", " -2.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Len(t, out.Accounts, 1)
	require.Equal(t, "Apple Card", out.Accounts[0].Row.Name)
	require.Equal(t, out.Transactions[0].AccountID, out.Transactions[1].AccountID)
}

func TestANonBreakingSpaceDoesNotCreateASecondCategory(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "A", "Food"+nbsp+"& Dining:Groceries", " -1.00"),
		spend("Everyday Checking", "B", "Food & Dining:Groceries", " -2.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, []string{"Food & Dining", "Food & Dining:Groceries"}, paths(out))
}

func TestUncategorizedIsNoCategoryToCreate(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "A", "Uncategorized", " -1.00"),
		spend("Everyday Checking", "B", "Groceries", " -2.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, []string{"Groceries"}, paths(out))
	require.Equal(t, uuid.Nil, out.Transactions[0].CategoryID)
	require.NotEqual(t, uuid.Nil, out.Transactions[1].CategoryID)
}

func TestAPayeeWithACommaSurvives(t *testing.T) {
	out := mapped(t, cells{date: "Apr 2, 2026", account: "Everyday Checking",
		payee: `Smith, Jones & Co`, statement: `SMITH, JONES "THE" CO`,
		category: "Shopping", amount: " -9.99"})
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, `Smith, Jones & Co`, out.Transactions[0].Payee)
	require.Equal(t, `SMITH, JONES "THE" CO`, out.Transactions[0].StatementName)
}

func TestACategoryPathBecomesAHierarchy(t *testing.T) {
	out := mapped(t, spend("Everyday Checking", "A",
		"Auto & Transport:Registration:Registration Fees", " -75.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, []string{
		"Auto & Transport",
		"Auto & Transport:Registration",
		"Auto & Transport:Registration:Registration Fees",
	}, paths(out))

	byPath := map[string]*Category{}
	for _, category := range out.Categories {
		byPath[category.Path] = category
	}
	require.Equal(t, byPath["Auto & Transport"].Row.ID,
		byPath["Auto & Transport:Registration"].Row.ParentID)
	require.Equal(t, byPath["Auto & Transport:Registration"].Row.ID,
		byPath["Auto & Transport:Registration:Registration Fees"].Row.ParentID)
	require.Equal(t, out.Transactions[0].CategoryID,
		byPath["Auto & Transport:Registration:Registration Fees"].Row.ID)
}

func TestASplitBecomesOneTransactionWithChildren(t *testing.T) {
	out := mapped(t,
		split("Amazon Card", "Store", "STORE 123", "Personal Care:Laundry", " -11.00"),
		split("Amazon Card", "Store", "STORE 123", "Food & Dining:Groceries", " -1.50"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Len(t, out.Transactions, 1)

	txn := out.Transactions[0]
	require.Equal(t, "-12.50", txn.Amount.String())
	require.Len(t, txn.Splits, 2)
	// Reports read the splits when there are any and the parent otherwise, so
	// a parent that also carried a category would be counted twice.
	require.Equal(t, uuid.Nil, txn.CategoryID)
	require.Equal(t, "-11.00", txn.Splits[0].Amount.String())
	require.Equal(t, "-1.50", txn.Splits[1].Amount.String())
	require.Equal(t, txn.Amount,
		domain.Sum(txn.Splits, func(s store.Split) domain.Money { return s.Amount }))
}

// Two adjacent two-way splits of one payee on one day are told apart only by
// the statement name.
func TestTwoSplitsOfOnePayeeOnOneDayStayTwoTransactions(t *testing.T) {
	out := mapped(t,
		split("Amazon Card", "Store", "ORDER 111", "Personal Care:Laundry", " -11.00"),
		split("Amazon Card", "Store", "ORDER 111", "Food & Dining:Groceries", " -1.50"),
		split("Amazon Card", "Store", "ORDER 222", "Personal Care:Laundry", " -12.00"),
		split("Amazon Card", "Store", "ORDER 222", "Personal Care:Hair", " -16.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Len(t, out.Transactions, 2)
	require.Equal(t, "-12.50", out.Transactions[0].Amount.String())
	require.Equal(t, "-28.00", out.Transactions[1].Amount.String())
}

func TestASplitsRowsCarryTheirOwnCategoryTagAndMemo(t *testing.T) {
	first := split("Amazon Card", "Store", "STORE", "Shopping", " -10.00")
	first.tags = "Reimbursable"
	first.notes = "work half"
	out := mapped(t, first, split("Amazon Card", "Store", "STORE", "Home:Tools", " -5.00"))

	require.True(t, out.Report.OK(), out.Report.Errors)
	txn := out.Transactions[0]
	require.Equal(t, "work half", txn.Splits[0].Memo)
	require.Len(t, txn.Splits[0].TagIDs, 1)
	require.Empty(t, txn.Splits[1].TagIDs)
	require.Len(t, out.Tags, 1)
	require.Equal(t, "Reimbursable", out.Tags[0].Name)
}

// Ground rule 4. The CSV has one exclusion column and the schema has two
// independent flags, so both are set and the operator is told.
func TestOneExclusionColumnSetsBothFlagsAndSaysSo(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.exclusion = "yes"
	out := mapped(t, row)

	require.True(t, out.Report.OK(), out.Report.Errors)
	require.True(t, out.Transactions[0].ExcludedFromReports)
	require.True(t, out.Transactions[0].ExcludedFromSpendingPlan)
	requireWarning(t, out, "two independent flags")
}

func TestARecurringRowIsWarnedAboutRatherThanGivenAnInventedSeries(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.recurring = "yes"
	out := mapped(t, row)

	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, uuid.Nil, out.Transactions[0].SeriesID)
	requireWarning(t, out, "recurring series the export does not name")
}

func TestPendingComesFromTheStatusColumn(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.status = "Pending"
	row.reviewed = "no"
	out := mapped(t, row)

	require.True(t, out.Report.OK(), out.Report.Errors)
	require.True(t, out.Transactions[0].IsPending)
	require.False(t, out.Transactions[0].IsReviewed)
}

func TestACheckNumberAndNotesAreCarried(t *testing.T) {
	row := spend("Everyday Checking", "Plumber", "Home:Home Services", " -350.00")
	row.check = "1042"
	row.notes = "kitchen tap"
	out := mapped(t, row)

	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, "1042", out.Transactions[0].CheckNumber)
	require.Equal(t, "kitchen tap", out.Transactions[0].Notes)
}

// Account kinds are inferred from finance vocabulary, never from a list of
// one household's account names.
func TestAccountKindsAreInferredFromTheName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    domain.AccountKind
		matched bool
	}{
		{"Cashback Mastercard", domain.KindCreditCard, true},
		{"Amazon Card", domain.KindCreditCard, true},
		{"Alex's Discover", domain.KindCreditCard, true},
		{"Everyday Checking", domain.KindCash, true},
		{"Apple Cash", domain.KindCash, true},
		{"High-Yield Savings", domain.KindCash, true},
		{"Rainy Day Fund", domain.KindCash, true},
		{"Health Savings", domain.KindCash, true},
		{"Car Loan", domain.KindLoan, true},
		{"Student Loan B", domain.KindLoan, true},
		{"14 Sample Street", domain.KindAsset, true},
		{"Example Roth IRA", domain.KindInvestment, true},
		{"Coinbase Wallet", domain.KindInvestment, true},
		{"Universal Life Policy", domain.KindInvestment, true},
		{"Wallet", domain.KindCash, true},
		// Nothing in "Second Car" says what it is, so the fallback classifies it
		// and the report says a rule did not.
		{"Second Car", domain.KindAsset, false},
	} {
		guess := GuessAccount(tc.name)
		require.Equal(t, tc.kind, guess.Kind, tc.name)
		require.Equal(t, tc.matched, guess.Matched(), tc.name)
	}
}

// An HSA is the banking side, the debit card's account, unless its name says
// it is the brokerage the cash is swept into.
func TestAnHSAIsInferredAsBankingUnlessItNamesItsInvestments(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        domain.AccountKind
		accountType string
	}{
		{"Health Savings", domain.KindCash, "hsa"},
		{"Flex Spending", domain.KindCash, "hsa"},
		{"Health Savings Investments", domain.KindInvestment, "hsa_investment"},
		{"Family HSA Brokerage", domain.KindInvestment, "hsa_investment"},
	} {
		guess := GuessAccount(tc.name)
		require.Equal(t, tc.kind, guess.Kind, tc.name)
		require.Equal(t, tc.accountType, guess.Type, tc.name)
	}
}

// Crypto and a life policy's cash value are investments with picker labels of
// their own; "cash value" is a phrase, and is not the cash rule's "cash".
func TestCryptoAndLifeInsuranceAreInferredWithTheirOwnTypes(t *testing.T) {
	for _, tc := range []struct{ name, accountType string }{
		{"Coinbase", "crypto"},
		{"ETH Staked", "crypto"},
		{"Cold Storage Bitcoin Wallet", "crypto"},
		{"Universal Life Policy", "life_insurance"},
		{"Policy Cash Value", "life_insurance"},
		{"Northwind Life Insurance", "life_insurance"},
		{"Petty Cash", "cash"},
		{"Lifetime Savings", "savings"},
	} {
		guess := GuessAccount(tc.name)
		require.Equal(t, tc.accountType, guess.Type, tc.name)
		require.True(t, guess.Matched(), tc.name)
	}
}

// A substring match on "card" would make Cardinal Credit Union a credit card.
func TestAccountRulesMatchWordsNotSubstrings(t *testing.T) {
	require.Equal(t, domain.KindCash, GuessAccount("Cardinal Bank Checking").Kind)
	require.False(t, GuessAccount("Cardamom").Matched())
}

func TestAnUnmatchedAccountNameIsWarnedAbout(t *testing.T) {
	out := mapped(t, spend("Second Car", "Value", "Balance Adjustment", " 1500.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	requireWarning(t, out, "no naming rule matched")
}

// Simplifi writes a transfer's category as the other account's name. Read as a
// spending category it turns every credit-card payment into an expense.
func TestACategoryNamingAnAccountIsATransfer(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "Payment", "Amazon Card", " -50.00"),
		spend("Amazon Card", "Payment", "Everyday Checking", " 50.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Len(t, out.Categories, 2)
	for _, category := range out.Categories {
		require.Equal(t, domain.CategoryTransfer, category.Row.Kind, category.Path)
	}
}

func TestIncomeAndTransferCategoriesAreRecognised(t *testing.T) {
	none := map[string]bool{}
	require.Equal(t, domain.CategoryIncome, GuessCategory("Personal Income:Paycheck", none).Kind)
	require.Equal(t, domain.CategoryTransfer, GuessCategory("Transfer", none).Kind)
	require.Equal(t, domain.CategoryTransfer, GuessCategory("Credit Card Payment", none).Kind)
	require.Equal(t, domain.CategoryExpense, GuessCategory("Food & Dining:Groceries", none).Kind)
}

// Opening Balance and Balance Adjustment move a balance without anybody having
// spent or earned.
func TestTheSystemCategoriesGetABookkeepingSource(t *testing.T) {
	out := mapped(t,
		cells{date: "Jan 1, 2020", account: "Everyday Checking", payee: "Opening",
			statement: "OPENING", category: "Opening Balance", amount: " 1000.00"},
		cells{date: "Feb 1, 2020", account: "14 Sample Street", payee: "Value",
			statement: "VALUE", category: "Balance Adjustment", amount: " 250000.00"})

	require.True(t, out.Report.OK(), out.Report.Errors)
	require.Equal(t, domain.SourceOpeningBalance, out.Transactions[0].Source)
	require.Equal(t, domain.SourceBalanceAdjustment, out.Transactions[1].Source)
	require.False(t, out.Transactions[0].Source.IsCashFlow())
	for _, category := range out.Categories {
		require.False(t, category.Row.IsUserAssignable, category.Path)
	}
}

func TestACategoryWhoseEveryRowContradictsItsKindIsFlagged(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "Refund", "Taxes:Federal Tax", " 1200.00"),
		spend("Everyday Checking", "Refund", "Taxes:Federal Tax", " 300.00"))
	require.True(t, out.Report.OK(), out.Report.Errors)
	requireWarning(t, out, `"Taxes:Federal Tax" is classified as spending`)
}

// -- refusals ---------------------------------------------------------

func TestAnUnparseableAmountIsAnErrorNamingTheLineAndColumn(t *testing.T) {
	out := mapped(t, spend("Everyday Checking", "A", "Shopping", " twelve"))
	require.False(t, out.Report.OK())
	requireProblem(t, out.Report.Errors, "line 2", colAmount)
}

// 03/04/2024 is two different days depending on who exported it.
func TestAnAmbiguousNumericDateIsRefused(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.date = "03/04/2024"
	out := mapped(t, row)
	require.False(t, out.Report.OK())
	requireProblem(t, out.Report.Errors, "line 2", colDate)
}

func TestAnUnknownYesNoValueIsRefused(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.reviewed = "maybe"
	out := mapped(t, row)
	require.False(t, out.Report.OK())
	requireProblem(t, out.Report.Errors, "line 2", colReviewed)
}

func TestAnUnknownStatusIsRefused(t *testing.T) {
	row := spend("Everyday Checking", "A", "Shopping", " -1.00")
	row.status = "Settled"
	out := mapped(t, row)
	require.False(t, out.Report.OK())
	requireProblem(t, out.Report.Errors, "line 2", colStatus)
}

func TestAnUnknownColumnIsNotedAndSkipped(t *testing.T) {
	report := importer.NewReport()
	rows, readable := ReadRows(strings.NewReader(
		"Date,Account,Payee,Category,Amount,Cryptocurrency\n\"Apr 2, 2026\",Everyday Checking,A,Shopping,-1.00,yes\n"), report)
	require.True(t, readable)
	require.True(t, report.OK())
	require.Len(t, rows, 1)
	require.Equal(t, "A", rows[0].Payee)
	require.Len(t, report.Notes, 1)
	require.Equal(t, "Cryptocurrency", report.Notes[0].Field)
}

func TestAMissingRequiredColumnIsRefused(t *testing.T) {
	report := importer.NewReport()
	_, readable := ReadRows(strings.NewReader("Date,Account,Payee,Category\n"), report)
	require.False(t, readable)
	require.False(t, report.OK())
	require.Contains(t, report.Errors[0].Message, `no "Amount" column`)
}

// A record with the wrong number of fields is a file that has drifted, and
// reading it positionally would load a payee into the notes.
func TestARecordOfTheWrongWidthIsRefused(t *testing.T) {
	report := importer.NewReport()
	body := file(spend("Everyday Checking", "A", "Shopping", " -1.00")) + "Apr 2 2026,Everyday Checking\n"
	ReadRows(strings.NewReader(body), report)
	require.False(t, report.OK())
}

// One bad value on many rows has to read as one line with a count.
func TestRepeatedProblemsAreOneLineWithACount(t *testing.T) {
	rows := make([]cells, 0, 3)
	for range 3 {
		row := spend("Everyday Checking", "A", "Shopping", " -1.00")
		row.reviewed = "maybe"
		rows = append(rows, row)
	}
	out := mapped(t, rows...)
	require.Len(t, out.Report.Errors, 1)
	require.Equal(t, 3, out.Report.Errors[0].Occurrences)
	require.Contains(t, out.Report.Errors[0].Render(), "(x3)")
}

// -- the dedupe key ---------------------------------------------------

func TestTheDerivedKeyIsStableAcrossRuns(t *testing.T) {
	row := spend("Everyday Checking", "Coffee", "Food & Dining:Coffee Shops", " -4.50")
	first := mapped(t, row)
	second := mapped(t, row)
	require.Equal(t, first.Transactions[0].ExternalID, second.Transactions[0].ExternalID)
	require.True(t, strings.HasPrefix(first.Transactions[0].ExternalID, "csv:"))
}

// Two identical coffees on one day are two transactions, and the key has to
// keep them apart or the second import loses one.
func TestIdenticalRowsGetDistinctKeys(t *testing.T) {
	row := spend("Everyday Checking", "Coffee", "Food & Dining:Coffee Shops", " -4.50")
	out := mapped(t, row, row)
	require.Len(t, out.Transactions, 2)
	require.NotEqual(t, out.Transactions[0].ExternalID, out.Transactions[1].ExternalID)
}

func TestADifferentAmountIsADifferentKey(t *testing.T) {
	out := mapped(t,
		spend("Everyday Checking", "Coffee", "Shopping", " -4.50"),
		spend("Everyday Checking", "Coffee", "Shopping", " -4.51"))
	require.NotEqual(t, out.Transactions[0].ExternalID, out.Transactions[1].ExternalID)
}

// -- helpers ----------------------------------------------------------

func split(account, payee, statement, category, amount string) cells {
	return cells{date: "Apr 2, 2026", account: account, payee: payee, statement: statement,
		category: category, split: "yes", amount: amount}
}

func paths(m *Mapped) []string {
	out := make([]string, len(m.Categories))
	for i, category := range m.Categories {
		out[i] = category.Path
	}
	return out
}

func requireWarning(t *testing.T, m *Mapped, substring string) {
	t.Helper()
	for _, problem := range m.Report.Warnings {
		if strings.Contains(problem.Message, substring) {
			return
		}
	}
	t.Fatalf("no warning containing %q; warnings were %v", substring, m.Report.Warnings)
}

func requireProblem(t *testing.T, problems []importer.Problem, recordID, field string) {
	t.Helper()
	for _, problem := range problems {
		if problem.RecordID == recordID && problem.Field == field {
			return
		}
	}
	t.Fatalf("no problem for %s %s; problems were %v", recordID, field, problems)
}

func TestMapRefusesARowWithNoDate(t *testing.T) {
	// Defense in depth for the readers that build a Row directly (the OFX
	// reader): a zero date cannot be filed under any month, so Map refuses it
	// rather than writing a row no register window would ever show.
	report := importer.NewReport()
	rows := []Row{{
		Line:          1,
		Account:       "Checking",
		Payee:         "Broken",
		StatementName: "BROKEN",
		Amount:        domain.MustFromString("-10.00"),
		// Date left at its zero value.
	}}
	Map(report, rows, Options{})
	require.False(t, report.OK(), "a row with no date must make the report not OK")
}
