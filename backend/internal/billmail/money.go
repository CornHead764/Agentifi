package billmail

import (
	"regexp"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The grouped form comes first so that "1,318" is not read as "1".
var amountShape = regexp.MustCompile(`\(?-?\s?\$?\s?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d{1,2})?\)?(?:\s?CR\b)?`)

// ParseAmount reads the first amount in a fragment; a parenthesised figure, a
// leading sign or a trailing "CR" is a credit.
func ParseAmount(s string) (domain.Money, bool) {
	found := amountShape.FindString(s)
	if found == "" {
		return domain.Zero, false
	}
	// The pattern may take one parenthesis of a pair, a sign inside a pair, or
	// a trailing "CR"; any of those makes the figure a credit.
	credit := strings.HasPrefix(found, "(") || strings.Contains(found, "-") || strings.HasSuffix(found, "CR")
	digits := strings.NewReplacer("(", "", ")", "", "-", "", "CR", "").Replace(found)
	amount, ok := domain.ParseMoneyText(digits)
	if !ok {
		return domain.Zero, false
	}
	if credit {
		amount = amount.Neg()
	}
	return amount, true
}
