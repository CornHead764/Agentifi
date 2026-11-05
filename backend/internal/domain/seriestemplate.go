package domain

import (
	"slices"
	"strings"
)

// SeriesTemplate is what a series files the rows that pay it under: its label
// as the payee, its category, its tags and a standing split set against its
// own amount. calculations.md §9, "A linked row takes the series' template".
type SeriesTemplate struct {
	Payee      string
	CategoryID ID
	TagIDs     []ID
	Splits     []Split
	// Amount is the series amount the splits add up to.
	Amount Money
}

// TemplateFill is what a row takes from its series' template. A zero field
// is nothing to fill.
type TemplateFill struct {
	Payee      string
	CategoryID ID
	TagIDs     []ID
	Splits     []Split
}

func (f TemplateFill) IsEmpty() bool {
	return f.Payee == "" && f.CategoryID == "" && len(f.TagIDs) == 0 && len(f.Splits) == 0
}

// FillFromTemplate is what a row linked to a series takes from the template:
// only what the row lacks, so a person's edit or a rule's category stands.
func FillFromTemplate(row Transaction, template SeriesTemplate) TemplateFill {
	var fill TemplateFill
	if strings.TrimSpace(row.Payee) == "" {
		fill.Payee = template.Payee
	}
	if len(row.TagIDs) == 0 {
		fill.TagIDs = slices.Clone(template.TagIDs)
	}
	// A split decides where the money is filed, so it lands only where nothing
	// has decided that yet: no category and no split of the row's own.
	if row.CategoryID == "" && len(row.Splits) == 0 {
		fill.CategoryID = template.CategoryID
		fill.Splits = ScaleSplits(template.Splits, template.Amount, row.Amount)
	}
	return fill
}

// ScaleSplits carves an amount up in the proportions splits set against from.
// Each line is rounded to the cent and the remainder lands on the largest
// line, the first of equals, so the lines add up to the amount. Nil when there
// is no proportion to take: no splits, or from is zero.
func ScaleSplits(splits []Split, from, to Money) []Split {
	if len(splits) == 0 || from.IsZero() {
		return nil
	}
	ratio, _ := Ratio(to, from)
	out := make([]Split, len(splits))
	running := Zero
	largest := 0
	for i, one := range splits {
		out[i] = Split{
			Amount:     one.Amount.Scale(ratio).Round(),
			CategoryID: one.CategoryID,
			Memo:       one.Memo,
			TagIDs:     slices.Clone(one.TagIDs),
		}
		running = running.Add(out[i].Amount)
		if out[i].Amount.Abs().GreaterThan(out[largest].Amount.Abs()) {
			largest = i
		}
	}
	if remainder := to.Round().Sub(running); !remainder.IsZero() {
		out[largest].Amount = out[largest].Amount.Add(remainder)
	}
	return out
}
