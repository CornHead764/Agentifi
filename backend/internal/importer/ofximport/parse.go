// Package ofximport reads OFX and QFX statement files.
//
// OFX 1.x is SGML: a colon-delimited header, then tags whose closing halves
// are optional, so `<NAME>SAFEWAY` is a complete element and an XML parser
// refuses the whole file. OFX 2.x is ordinary XML. QFX is Intuit's OFX with
// extra tags this ignores. One tolerant scanner reads all three.
package ofximport

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Transaction is one <STMTTRN>.
type Transaction struct {
	// FITID is the bank's own id for the row. Empty when the bank sent none,
	// which some do.
	FITID string
	Type  string
	// Posted is the date the row settled; only the day of OFX's timestamp
	// survives.
	Posted domain.Date
	Amount domain.Money
	// Name is the bank's short label, Memo its longer one.
	Name      string
	Memo      string
	CheckNum  string
	AccountID string

	// amountBad records that a <TRNAMT> was present but did not parse, which
	// a zero Amount alone cannot tell from a genuine $0.00 charge.
	amountBad bool
}

// Account is one statement's account, as the file describes it.
type Account struct {
	// ID is <ACCTID> — the account number, usually complete rather than masked.
	ID       string
	RoutingN string
	Kind     string
	Currency string

	Balance    domain.Money
	HasBalance bool
	BalanceOn  domain.Date

	Transactions []Transaction
}

// Document is one file.
type Document struct {
	Accounts []Account
}

// Count is every transaction in the file, across its statements.
func (d Document) Count() int {
	total := 0
	for _, account := range d.Accounts {
		total += len(account.Transactions)
	}
	return total
}

// Parse reads a whole OFX, QFX or OFX-2 file.
func Parse(r io.Reader) (Document, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Document{}, fmt.Errorf("ofx: reading: %w", err)
	}
	if len(raw) == 0 {
		return Document{}, fmt.Errorf("ofx: the file is empty")
	}

	text := decode(raw)
	if !strings.Contains(strings.ToUpper(text), "<OFX") {
		return Document{}, fmt.Errorf("ofx: this is not an OFX or QFX file")
	}
	return build(tokenize(text))
}

// decode turns the bytes into text.
//
// UTF-8 first, then Latin-1, which is what OFX 1.x files from European banks
// are; the header declares a charset nobody honours.
func decode(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	runes := make([]rune, 0, len(raw))
	for _, b := range raw {
		runes = append(runes, rune(b))
	}
	return string(runes)
}

// token is one tag, with its value when the tag is a leaf.
type token struct {
	tag   string
	value string
	// closing is `</AGGREGATE>`. Leaves in OFX 1.x have no closing tag at all.
	closing bool
}

// tokenize walks the markup without deciding which dialect it is.
//
// A tag followed by text is a leaf carrying that text. A tag followed by
// another tag is an aggregate. That rule reads unclosed SGML elements and
// well-formed XML ones identically.
func tokenize(text string) []token {
	var out []token
	for i := 0; i < len(text); {
		open := strings.IndexByte(text[i:], '<')
		if open < 0 {
			break
		}
		i += open
		close := strings.IndexByte(text[i:], '>')
		if close < 0 {
			break
		}
		tag := strings.TrimSpace(text[i+1 : i+close])
		i += close + 1

		// Processing instructions and declarations: `<?xml ...?>`, `<?OFX ...?>`,
		// `<!-- -->`. OFX 2.x opens with one and it names no element.
		if tag == "" || strings.HasPrefix(tag, "?") || strings.HasPrefix(tag, "!") {
			continue
		}
		if strings.HasPrefix(tag, "/") {
			out = append(out, token{tag: strings.ToUpper(strings.TrimPrefix(tag, "/")), closing: true})
			continue
		}
		// Attributes are not used anywhere in OFX; dropping them keeps the tag
		// comparable.
		if space := strings.IndexAny(tag, " \t\r\n"); space >= 0 {
			tag = tag[:space]
		}

		next := strings.IndexByte(text[i:], '<')
		value := text[i:]
		if next >= 0 {
			value = text[i : i+next]
		}
		out = append(out, token{tag: strings.ToUpper(tag), value: strings.TrimSpace(value)})
	}
	return out
}

// build walks the tokens into statements.
func build(tokens []token) (Document, error) {
	var (
		doc     Document
		account *Account
		txn     *Transaction
		inBal   bool
	)

	flushAccount := func() {
		if account != nil {
			doc.Accounts = append(doc.Accounts, *account)
			account = nil
		}
	}

	for _, one := range tokens {
		if one.closing {
			switch one.tag {
			case "STMTTRN":
				if txn != nil && account != nil && !isBalanceSummary(txn.Name, txn.Memo) {
					account.Transactions = append(account.Transactions, *txn)
				}
				txn = nil
			case "STMTRS", "CCSTMTRS":
				flushAccount()
			case "LEDGERBAL":
				inBal = false
			}
			continue
		}

		switch one.tag {
		case "STMTRS", "CCSTMTRS":
			// A file can hold several statements; each opens its own account.
			flushAccount()
			account = &Account{}
			continue
		case "STMTTRN":
			if account == nil {
				account = &Account{}
			}
			txn = &Transaction{}
			continue
		case "LEDGERBAL":
			inBal = true
			continue
		}
		if one.value == "" {
			continue
		}

		if txn != nil {
			applyTransactionField(txn, one)
			continue
		}
		if account != nil {
			applyAccountField(account, one, inBal)
		}
	}

	if txn != nil && account != nil && !isBalanceSummary(txn.Name, txn.Memo) {
		account.Transactions = append(account.Transactions, *txn)
	}
	flushAccount()

	if len(doc.Accounts) == 0 {
		return Document{}, fmt.Errorf("ofx: the file holds no statement")
	}
	return doc, nil
}

func applyTransactionField(txn *Transaction, one token) {
	switch one.tag {
	case "TRNTYPE":
		txn.Type = one.value
	case "DTPOSTED", "DTUSER":
		if txn.Posted.IsZero() {
			if on, ok := parseDate(one.value); ok {
				txn.Posted = on
			}
		}
	case "TRNAMT":
		if amount, ok := parseAmount(one.value); ok {
			txn.Amount = amount
		} else {
			txn.amountBad = true
		}
	case "FITID":
		txn.FITID = one.value
	case "NAME":
		txn.Name = one.value
	case "MEMO":
		txn.Memo = one.value
	case "CHECKNUM":
		txn.CheckNum = one.value
	case "ACCTID":
		txn.AccountID = one.value
	}
}

func applyAccountField(account *Account, one token, inBalance bool) {
	switch one.tag {
	case "ACCTID":
		account.ID = one.value
	case "BANKID":
		account.RoutingN = one.value
	case "ACCTTYPE":
		account.Kind = one.value
	case "CURDEF":
		account.Currency = one.value
	case "BALAMT":
		if !inBalance {
			return
		}
		if amount, ok := parseAmount(one.value); ok {
			account.Balance, account.HasBalance = amount, true
		}
	case "DTASOF":
		if !inBalance {
			return
		}
		if on, ok := parseDate(one.value); ok {
			account.BalanceOn = on
		}
	}
}

// balanceSummaries are rows that are not transactions at all.
//
// Banco do Brasil and several other banks emit the running balance as a
// <STMTTRN>. Importing them doubles a month: the balance row carries the same
// money as the transactions that produced it.
var balanceSummaries = []string{"saldo anterior", "saldo do dia", "saldo final", "s a l d o"}

func isBalanceSummary(name, memo string) bool {
	for _, text := range []string{name, memo} {
		lowered := strings.ToLower(strings.TrimSpace(text))
		for _, prefix := range balanceSummaries {
			if lowered != "" && strings.HasPrefix(lowered, prefix) {
				return true
			}
		}
	}
	return false
}

// parseDate reads an OFX timestamp.
//
// The format is YYYYMMDD with an optional time, an optional fraction and an
// optional bracketed zone: `20260816120000.000[-5:EST]`. Only the day is kept,
// as written rather than converted, because converting moves rows across
// month boundaries.
func parseDate(value string) (domain.Date, bool) {
	digits := value
	if cut := strings.IndexAny(digits, "["); cut >= 0 {
		digits = digits[:cut]
	}
	digits = strings.TrimSpace(digits)
	if len(digits) < 8 {
		return domain.Date{}, false
	}
	on, err := time.Parse("20060102", digits[:8])
	if err != nil {
		return domain.Date{}, false
	}
	return domain.DateOf(on), true
}

// parseAmount reads a signed decimal, in either separator.
//
// European exports write `-1.234,56`, which read as a US number is -1.234.
func parseAmount(value string) (domain.Money, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return domain.Zero, false
	}
	lastComma, lastDot := strings.LastIndex(text, ","), strings.LastIndex(text, ".")
	switch {
	case lastComma > lastDot:
		// Comma is the decimal separator, so dots are grouping.
		text = strings.ReplaceAll(text, ".", "")
		text = strings.Replace(text, ",", ".", 1)
	default:
		text = strings.ReplaceAll(text, ",", "")
	}
	return domain.ParseMoneyText(text)
}
