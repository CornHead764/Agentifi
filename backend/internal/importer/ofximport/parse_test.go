package ofximport_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/importer/ofximport"
)

// Reading OFX, QFX and OFX 2. Every fixture here is a shape a real bank sends.

const sgml = "OFXHEADER:100\r\n" +
	"DATA:OFXSGML\r\n" +
	"VERSION:102\r\n" +
	"CHARSET:1252\r\n" +
	"\r\n" +
	`<OFX>
<BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>USD
<BANKACCTFROM><BANKID>021000021<ACCTID>000123456789<ACCTTYPE>CHECKING</BANKACCTFROM>
<BANKTRANLIST>
<STMTTRN>
<TRNTYPE>DEBIT
<DTPOSTED>20260816120000.000[-8:PST]
<TRNAMT>-12.34
<FITID>2026081600001
<NAME>SAFEWAY #1234
<MEMO>SAFEWAY STORE 1234 SPRINGFIELD
</STMTTRN>
<STMTTRN>
<TRNTYPE>CREDIT
<DTPOSTED>20260815
<TRNAMT>2400.00
<FITID>2026081500009
<NAME>ACME PAYROLL
</STMTTRN>
</BANKTRANLIST>
<LEDGERBAL><BALAMT>5678.90<DTASOF>20260816</LEDGERBAL>
</STMTRS></STMTTRNRS></BANKMSGSRSV1>
</OFX>`

func TestAnUnclosedSgmlFileReads(t *testing.T) {
	// OFX 1.x leaves closing tags off its leaves, so an XML parser refuses the
	// whole file. This is the dialect most banks still send.
	doc, err := ofximport.Parse(strings.NewReader(sgml))
	require.NoError(t, err)
	require.Len(t, doc.Accounts, 1)

	account := doc.Accounts[0]
	require.Equal(t, "000123456789", account.ID)
	require.Equal(t, "021000021", account.RoutingN)
	require.Equal(t, "CHECKING", account.Kind)
	require.Equal(t, "USD", account.Currency)
	require.True(t, account.HasBalance)
	require.Equal(t, "5678.90", account.Balance.String())

	require.Len(t, account.Transactions, 2)
	first := account.Transactions[0]
	require.Equal(t, "2026081600001", first.FITID)
	require.Equal(t, "-12.34", first.Amount.String())
	require.Equal(t, "2026-08-16", first.Posted.String())
	require.Equal(t, "SAFEWAY #1234", first.Name)
	require.Equal(t, "SAFEWAY STORE 1234 SPRINGFIELD", first.Memo)
	require.Equal(t, "2400.00", account.Transactions[1].Amount.String())
}

func TestAnXmlFileReadsTheSameWay(t *testing.T) {
	// OFX 2.x is well-formed XML, and some exports open straight into <OFX>
	// with no declaration at all.
	doc, err := ofximport.Parse(strings.NewReader(`<?xml version="1.0"?>
<?OFX OFXHEADER="200" VERSION="211"?>
<OFX>
  <BANKMSGSRSV1><STMTTRNRS><STMTRS>
    <CURDEF>USD</CURDEF>
    <BANKACCTFROM><ACCTID>999</ACCTID><ACCTTYPE>SAVINGS</ACCTTYPE></BANKACCTFROM>
    <BANKTRANLIST>
      <STMTTRN>
        <TRNTYPE>DEBIT</TRNTYPE>
        <DTPOSTED>20260814</DTPOSTED>
        <TRNAMT>-12.00</TRNAMT>
        <FITID>abc-1</FITID>
        <NAME>STREAMSVC</NAME>
      </STMTTRN>
    </BANKTRANLIST>
  </STMTRS></STMTTRNRS></BANKMSGSRSV1>
</OFX>`))
	require.NoError(t, err)
	require.Len(t, doc.Accounts, 1)
	require.Equal(t, "999", doc.Accounts[0].ID)
	require.Len(t, doc.Accounts[0].Transactions, 1)
	require.Equal(t, "STREAMSVC", doc.Accounts[0].Transactions[0].Name)
	require.Equal(t, "-12.00", doc.Accounts[0].Transactions[0].Amount.String())
}

func TestABalanceRowIsNotATransaction(t *testing.T) {
	// Several banks emit the running balance as a <STMTTRN>. Importing one
	// doubles the month, because it carries the same money as the rows that
	// produced it.
	doc, err := ofximport.Parse(strings.NewReader(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260801<TRNAMT>1000.00<FITID>a<NAME>Saldo Anterior</STMTTRN>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-25.00<FITID>b<NAME>PADARIA</STMTTRN>
<STMTTRN><DTPOSTED>20260831<TRNAMT>975.00<FITID>c<MEMO>SALDO FINAL</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`))
	require.NoError(t, err)
	require.Len(t, doc.Accounts[0].Transactions, 1)
	require.Equal(t, "PADARIA", doc.Accounts[0].Transactions[0].Name)
}

func TestAnEmptyFitidIsAbsentRatherThanFatal(t *testing.T) {
	// Banco do Brasil sends <FITID> with nothing in it; the row is kept with
	// no bank id to dedupe on.
	doc, err := ofximport.Parse(strings.NewReader(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-25.00<FITID><NAME>PADARIA</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`))
	require.NoError(t, err)
	require.Len(t, doc.Accounts[0].Transactions, 1)
	require.Empty(t, doc.Accounts[0].Transactions[0].FITID)
	require.Equal(t, "PADARIA", doc.Accounts[0].Transactions[0].Name)
}

func TestACommaDecimalIsNotThreeOrdersOfMagnitude(t *testing.T) {
	// European exports write -1.234,56, which as a US number is -1.234.
	doc, err := ofximport.Parse(strings.NewReader(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-1.234,56<FITID>a<NAME>MIETE</STMTTRN>
<STMTTRN><DTPOSTED>20260803<TRNAMT>1,234.56<FITID>b<NAME>SALARY</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`))
	require.NoError(t, err)
	require.Equal(t, "-1234.56", doc.Accounts[0].Transactions[0].Amount.String())
	require.Equal(t, "1234.56", doc.Accounts[0].Transactions[1].Amount.String())
}

func TestATimestampKeepsTheDayTheStatementStates(t *testing.T) {
	// Converting the zone moves rows across month boundaries: a payment stated
	// as 1 August at 00:30 in a +2 zone becomes 31 July in UTC, and lands in
	// the wrong month of every report.
	doc, err := ofximport.Parse(strings.NewReader(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260801003000.000[+2:CEST]<TRNAMT>-25.00<FITID>a<NAME>X</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`))
	require.NoError(t, err)
	require.Equal(t, "2026-08-01", doc.Accounts[0].Transactions[0].Posted.String())
}

func TestSeveralStatementsInOneFileStayApart(t *testing.T) {
	// A QFX download can carry every account at the institution. Folding them
	// together would file one account's rows against another.
	doc, err := ofximport.Parse(strings.NewReader(`<OFX>
<BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>111</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260801<TRNAMT>-1.00<FITID>a<NAME>ONE</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1>
<CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS>
<CCACCTFROM><ACCTID>222</ACCTID></CCACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-2.00<FITID>b<NAME>TWO</STMTTRN>
</CCSTMTRS></CCSTMTTRNRS></CREDITCARDMSGSRSV1>
</OFX>`))
	require.NoError(t, err)
	require.Len(t, doc.Accounts, 2)
	require.Equal(t, "111", doc.Accounts[0].ID)
	require.Equal(t, "222", doc.Accounts[1].ID)
	require.Equal(t, "ONE", doc.Accounts[0].Transactions[0].Name)
	require.Equal(t, "TWO", doc.Accounts[1].Transactions[0].Name)
	require.Equal(t, 2, doc.Count())
}

func TestLatin1TextSurvives(t *testing.T) {
	// A mis-decoded payee is a payee that never matches a rule again.
	raw := []byte(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<STMTTRN><DTPOSTED>20260802<TRNAMT>-25.00<FITID>a<NAME>CAF` + "\xc9" + ` PARIS</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`)
	doc, err := ofximport.Parse(strings.NewReader(string(raw)))
	require.NoError(t, err)
	require.Equal(t, "CAFÉ PARIS", doc.Accounts[0].Transactions[0].Name)
}

func TestSomethingThatIsNotOfxIsRefused(t *testing.T) {
	// Refused by name rather than parsed into an empty statement, because a
	// silent zero-transaction import reads as "the file had nothing in it".
	for _, body := range []string{"", "Date,Amount,Payee\n2026-08-01,-3.00,Shop\n", "<html><body>hi"} {
		_, err := ofximport.Parse(strings.NewReader(body))
		require.Error(t, err, "accepted %q", body)
	}
}

func TestAStatementWithNoTransactionsIsStillAnAccount(t *testing.T) {
	doc, err := ofximport.Parse(strings.NewReader(`<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>USD<BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM>
<LEDGERBAL><BALAMT>10.00<DTASOF>20260816</LEDGERBAL>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`))
	require.NoError(t, err)
	require.Len(t, doc.Accounts, 1)
	require.Empty(t, doc.Accounts[0].Transactions)
	require.Equal(t, "10.00", doc.Accounts[0].Balance.String())
	require.Equal(t, 0, doc.Count())
}
