package ofximport_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/importer/ofximport"
)

func parsed(t *testing.T, body string) ofximport.Document {
	t.Helper()
	doc, err := ofximport.Parse(strings.NewReader(body))
	require.NoError(t, err)
	return doc
}

func TestABankIdBecomesTheRowsIdentity(t *testing.T) {
	// The bank's own id is stable across a payee being renamed at the bank,
	// which a key derived from the payee is not.
	rows := ofximport.Rows(parsed(t, sgml), "Everyday Checking", importer.NewReport())
	require.Len(t, rows, 2)
	require.Equal(t, "ofx:2026081600001", rows[0].ExternalID)
	require.Equal(t, "Everyday Checking", rows[0].Account)
	require.Equal(t, "SAFEWAY #1234", rows[0].Payee)
	require.Equal(t, "SAFEWAY #1234", rows[0].StatementName)
	require.Equal(t, "SAFEWAY STORE 1234 SPRINGFIELD", rows[0].Notes)
	require.Equal(t, "-12.34", rows[0].Amount.String())
}

func TestARowWithNoBankIdGetsNoInventedOne(t *testing.T) {
	// A synthesised id looks stable and is not, so a second import would write
	// the row again. Left empty, the importer derives a key that says what it
	// is.
	doc := parsed(t, `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-25.00<FITID><NAME>PADARIA</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`)
	rows := ofximport.Rows(doc, "Everyday Checking", importer.NewReport())
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].ExternalID)
}

func TestAnUnnamedStatementIsFiledUnderSomethingLegible(t *testing.T) {
	// An ACCTID means nothing to a person. The fallback has to be enough to
	// tell two statements in one file apart.
	doc := parsed(t, `<OFX>
<BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>000123456789</ACCTID><ACCTTYPE>CHECKING</ACCTTYPE></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260801<TRNAMT>-1.00<FITID>a<NAME>ONE</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1>
<CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS>
<CCACCTFROM><ACCTID>444455556666</ACCTID></CCACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-2.00<FITID>b<NAME>TWO</STMTTRN>
</CCSTMTRS></CCSTMTTRNRS></CREDITCARDMSGSRSV1>
</OFX>`)
	rows := ofximport.Rows(doc, "", importer.NewReport())
	require.Len(t, rows, 2)
	require.Equal(t, "Checking ····6789", rows[0].Account)
	require.Equal(t, "Account ····6666", rows[1].Account)
}

func TestANamedAccountCoversEveryStatementInTheFile(t *testing.T) {
	doc := parsed(t, sgml)
	rows := ofximport.Rows(doc, "Everyday Checking", importer.NewReport())
	for _, row := range rows {
		require.Equal(t, "Everyday Checking", row.Account)
	}
}

func TestAMemoThatOnlyRepeatsTheNameIsNotANote(t *testing.T) {
	doc := parsed(t, `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-25.00<FITID>a<NAME>STREAMSVC<MEMO>streamsvc</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`)
	rows := ofximport.Rows(doc, "Card", importer.NewReport())
	require.Empty(t, rows[0].Notes)
	require.Equal(t, "STREAMSVC", rows[0].StatementName)
}

func TestAGarbledAmountIsRefusedRatherThanImportedAsZero(t *testing.T) {
	// A transaction whose TRNAMT does not parse must not slip in as $0.00: the
	// report goes not-OK so the write is refused, the way the CSV reader refuses
	// a bad amount cell.
	doc := parsed(t, `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>--12.3.4<FITID>a<NAME>BROKEN</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`)
	report := importer.NewReport()
	rows := ofximport.Rows(doc, "Checking", report)
	require.Len(t, rows, 1)
	require.False(t, report.OK(), "a garbled amount must make the report not OK")
}

func TestAMissingDateIsRefusedRatherThanFiledUnderYearZero(t *testing.T) {
	// With no readable DTPOSTED the row would land on year zero and never show
	// in any register window. Refused instead.
	doc := parsed(t, `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><TRNAMT>-25.00<FITID>a<NAME>NODATE</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`)
	report := importer.NewReport()
	rows := ofximport.Rows(doc, "Checking", report)
	require.Len(t, rows, 1)
	require.False(t, report.OK(), "a missing date must make the report not OK")
}
