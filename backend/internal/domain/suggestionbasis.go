package domain

import "slices"

// How a category suggestion was reached, as the register names it in a word.
// The model chooses among ModelSuggestionBases when it proposes; the rest are
// Agentifi's own, written when the history decides without the model.
const (
	SuggestionBasisOrderItems  = "order_items"
	SuggestionBasisGuidance    = "guidance"
	SuggestionBasisCorrection  = "correction"
	SuggestionBasisHistory     = "history"
	SuggestionBasisPayeeName   = "payee_name"
	SuggestionBasisGuess       = "guess"
	SuggestionBasisTransfer    = "transfer"
	SuggestionBasisCardPayment = "card_payment"
	SuggestionBasisRefund      = "refund"
)

// ModelSuggestionBases are the bases a model may give, strongest first.
var ModelSuggestionBases = []string{
	SuggestionBasisOrderItems, SuggestionBasisGuidance, SuggestionBasisCorrection,
	SuggestionBasisHistory, SuggestionBasisPayeeName, SuggestionBasisGuess,
}

var agentifiSuggestionBases = []string{
	SuggestionBasisTransfer, SuggestionBasisCardPayment, SuggestionBasisRefund,
}

// SuggestionBasis is raw when it names a basis, and "" otherwise, so a
// model's free text never reaches the register as one.
func SuggestionBasis(raw string) string {
	if slices.Contains(ModelSuggestionBases, raw) || slices.Contains(agentifiSuggestionBases, raw) {
		return raw
	}
	return ""
}
