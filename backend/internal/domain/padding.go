package domain

// FoldPadding separates the padding income rows (Transaction.IsPadding) from
// a list, keeping each side in its order. A register that hides them lists
// shown and reports padding as a count and a total beside it; nothing that
// computes a figure calls this.
func FoldPadding(postings []Posting) (shown, padding []Posting) {
	shown = make([]Posting, 0, len(postings))
	for _, posting := range postings {
		if posting.Txn.IsPadding() {
			padding = append(padding, posting)
			continue
		}
		shown = append(shown, posting)
	}
	return shown, padding
}
