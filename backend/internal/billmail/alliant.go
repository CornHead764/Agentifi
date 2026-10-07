package billmail

import (
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Alliant Energy's bill-ready mail: one table row per account, so one
// Bill per row. The marketing list sends from a different host.
const (
	alliantSender  = "noreply@myaccount.alliantenergy.com"
	alliantSubject = "Your Alliant Energy bill is ready to view"
)

type alliantParser struct{}

func init() { Register(alliantParser{}) }

func (alliantParser) Biller() domain.BillerID { return domain.BillerAlliant }

func (p alliantParser) Match(m Message) (Claim, bool) {
	if !senderIs(m.Sender, alliantSender) || !textutil.ContainsFold(m.Subject, alliantSubject) {
		return Claim{}, false
	}

	var bills []Bill
	account, amount, due := -1, -1, -1
	for _, row := range TableCells(m.HTML) {
		if a, t, d, ok := alliantColumns(row); ok {
			account, amount, due = a, t, d
			continue
		}
		bill, ok := alliantRow(row, account, amount, due)
		if ok {
			bills = append(bills, bill)
		}
	}
	if len(bills) == 0 {
		return Claim{}, false
	}
	return Claim{Biller: domain.BillerAlliant, Bills: bills}, true
}

func alliantColumns(row []string) (account, amount, due int, ok bool) {
	account, amount, due = -1, -1, -1
	for i, cell := range row {
		cell = strings.ToLower(cell)
		switch {
		case strings.Contains(cell, "account no"):
			account = i
		case strings.Contains(cell, "total amount due"):
			amount = i
		case strings.Contains(cell, "due date"):
			due = i
		}
	}
	return account, amount, due, account >= 0 && amount >= 0 && due >= 0
}

var alliantAccount = regexp.MustCompile(`^\d{4,}$`)

func alliantRow(row []string, account, amount, due int) (Bill, bool) {
	if account < 0 || account >= len(row) || amount >= len(row) || due >= len(row) {
		return Bill{}, false
	}
	number := strings.TrimSpace(row[account])
	if !alliantAccount.MatchString(number) {
		return Bill{}, false
	}
	total, ok := ParseAmount(row[amount])
	if !ok {
		return Bill{}, false
	}
	day, ok := ParseDate(row[due])
	if !ok {
		return Bill{}, false
	}
	return Bill{
		ExternalID:   number,
		MaskedNumber: domain.MaskAccount(number),
		AmountDue:    total,
		DueOn:        day,
	}, true
}
