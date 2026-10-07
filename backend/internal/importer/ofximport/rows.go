package ofximport

import (
	"fmt"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/importer/csvimport"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// storeOFX names this reader in the report, so a problem from here never reads
// as one from the CSV or the Simplifi importer.
const storeOFX = "ofx"

// Turning a statement into the CSV importer's rows, so OFX shares its
// idempotent writer.

// AccountName is the fallback name to file a statement's rows under when the
// operator names none: the account kind and the last four digits, enough to
// tell two statements in one file apart.
func AccountName(account Account) string {
	kind := strings.ToUpper(strings.TrimSpace(account.Kind))
	switch kind {
	case "":
		kind = "Account"
	case "CHECKING", "SAVINGS", "MONEYMRKT", "CREDITLINE":
		kind = strings.ToUpper(kind[:1]) + strings.ToLower(kind[1:])
	}
	id := strings.TrimSpace(account.ID)
	if len(id) > 4 {
		id = id[len(id)-4:]
	}
	if id == "" {
		return kind
	}
	return fmt.Sprintf("%s ····%s", kind, id)
}

// Rows converts a parsed document into importer rows, recording on the report
// any row the file described but this cannot represent.
//
// `named` overrides the account name for every statement in the file.
//
// A row whose amount or date did not parse is reported as an error rather than
// imported at a silent zero. The row is still returned so the report counts
// stay honest; the errors block the write.
func Rows(doc Document, named string, report *importer.Report) []csvimport.Row {
	var out []csvimport.Row
	for _, account := range doc.Accounts {
		name := named
		if name == "" {
			name = AccountName(account)
		}
		for _, txn := range account.Transactions {
			id := txn.FITID
			if id == "" {
				id = "a transaction with no FITID"
			}
			if txn.amountBad {
				report.Errorf(storeOFX, id, "TRNAMT",
					"has an amount that could not be read; importing it as $0.00 would be a wrong number")
			}
			if txn.Posted.IsZero() {
				report.Errorf(storeOFX, id, "DTPOSTED",
					"has no readable posted date, so it cannot be filed under any month")
			}
			out = append(out, row(txn, name))
		}
	}
	return out
}

func row(txn Transaction, account string) csvimport.Row {
	// NAME and MEMO are both the bank's, so the raw one seeds the statement
	// name and the payee, and the longer one becomes a note when it says
	// something more.
	statement := textutil.FirstNonBlank(txn.Name, txn.Memo)
	notes := ""
	if txn.Memo != "" && !strings.EqualFold(txn.Memo, txn.Name) {
		notes = txn.Memo
	}

	return csvimport.Row{
		// Only a bank id, never a synthesised one, which looks stable and is
		// not; with none set the importer derives its own key.
		ExternalID:    externalID(txn.FITID),
		Date:          txn.Posted,
		Account:       account,
		Payee:         statement,
		StatementName: statement,
		Notes:         notes,
		CheckNumber:   txn.CheckNum,
		Amount:        txn.Amount,
	}
}

func externalID(fitid string) string {
	if strings.TrimSpace(fitid) == "" {
		return ""
	}
	return "ofx:" + strings.TrimSpace(fitid)
}
