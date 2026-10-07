package domain

import (
	"fmt"
	"strings"
)

// Guidance notes: the household's own rules for rows, in their words. A note
// is never parsed or executed; its words go in front of the model beside the
// facts. When a note applies is a Filter (ground rule 3), evaluated by the
// same Matches as rules, and only notes about the row in hand are shown, since
// a prompt with every note buries the one that matters.
//
// An empty condition set matches nothing, as for a rule (service.MatchesRule).
// The API refuses to save one; this is the second door, and a note whose
// conditions went missing must go quiet rather than speak on every row.

// Guidance is one standing instruction, with the rows it is about.
type Guidance struct {
	ID   ID
	Name string
	// Instruction is the household's own words, verbatim.
	Instruction string
	// Filter selects the rows this note is about. An empty one selects none.
	Filter Filter
}

// Applies reports whether a note is about this row. No conditions means no
// match (unlike the register, where an empty filter shows everything). A date
// condition reads the effective date (trap 4), as rules and automations do.
func (g Guidance) Applies(posting Posting, facets Facets) bool {
	if len(g.Filter.Items) == 0 {
		return false
	}
	return Matches(g.Filter, posting, facets, DateEffective)
}

// SelectGuidance is the notes that are about one row, in the household's
// order.
func SelectGuidance(notes []Guidance, posting Posting, facets Facets) []Guidance {
	out := make([]Guidance, 0, len(notes))
	for _, note := range notes {
		if note.Applies(posting, facets) {
			out = append(out, note)
		}
	}
	return out
}

// GuidancePreamble introduces the section in the run's opening message. It
// lives here rather than in the automation's prompt because an automation
// stores a copy of its prompt at creation, so template changes never reach it;
// this is built fresh every run. A note outranks the payee's history, but the
// wording does not tell the model to overrule the facts of an order.
const GuidancePreamble = `These are the household's own standing instructions, written by them and
switched on. They outrank Agentifi's assessment, the payee's history and the
corrections: this is what the household wrote down about rows like this one,
on purpose. Follow a note where it covers this row, and fall back to the
history where it is silent. Follow only the notes below — they were selected
because their conditions matched this row.`

// GuidanceNoSubjectPreamble introduces the section on a run with no one row
// (a daily sweep): every note is listed with its conditions.
const GuidanceNoSubjectPreamble = `These are the household's own standing instructions, written by them and
switched on. They outrank the payee's history for the rows they cover.
This run is about more than one transaction, so every note is listed, each
with the rows it applies to — follow a note only on a row that matches what
its "applies to" line says.`

// DescribeFilter is a filter in words a model and a person both read. Fields
// the builder does not offer are named by field rather than dropped, since an
// unmentioned condition would make a note seem to apply more widely. "nothing"
// for an empty filter matches Applies. `names` resolves id-valued fields; a
// missing id is printed as itself.
func DescribeFilter(filter Filter, names map[ID]string) string {
	if len(filter.Items) == 0 {
		return "nothing"
	}
	groups := map[int][]string{}
	order := []int{}
	for _, item := range filter.Items {
		if _, seen := groups[item.GroupIndex]; !seen {
			order = append(order, item.GroupIndex)
		}
		groups[item.GroupIndex] = append(groups[item.GroupIndex], describeItem(item, names))
	}
	sortInts(order)
	clauses := make([]string, 0, len(order))
	for _, index := range order {
		clauses = append(clauses, strings.Join(groups[index], " and "))
	}
	return strings.Join(clauses, ", or ")
}

func describeItem(item FilterItem, names map[ID]string) string {
	negation := ""
	if item.Negated {
		negation = "not "
	}
	switch item.Field {
	case FieldStatementName, FieldPayee:
		field := "the statement name"
		if item.Field == FieldPayee {
			field = "the payee"
		}
		return field + " " + negation + describeTextOperator(item)
	case FieldAmount:
		return negation + describeAmount(item)
	case FieldAccount, FieldCategory, FieldTag:
		field := map[FilterField]string{
			FieldAccount: "the account", FieldCategory: "the category", FieldTag: "the tag",
		}[item.Field]
		return field + " is " + negation + "one of " +
			strings.Join(quoteAll(resolveNames(item.Values, names)), ", ")
	case FieldText:
		return negation + "the row mentions " + quote(item.Text)
	case FieldFlag:
		return "the flag is " + negation + "one of " + strings.Join(quoteAll(item.Values), ", ")
	case FieldDate:
		return negation + describeDates(item)
	}
	// The yes/no fields: State and Negated are read together so the wording
	// never says "not is reviewed".
	if question, ok := stateQuestions[item.Field]; ok {
		yes := item.State != item.Negated
		if !item.HasState {
			yes = !item.Negated
		}
		if yes {
			return question.yes
		}
		return question.no
	}
	return negation + string(item.Field) + " matches"
}

// stateQuestions is the wording for the yes/no fields, both ways round,
// written out because composing "not" reads badly.
var stateQuestions = map[FilterField]struct{ yes, no string }{
	FieldIsReviewed: {yes: "the row has been reviewed", no: "the row has not been reviewed"},
	FieldIsPending:  {yes: "the row is still pending", no: "the row has settled"},
	FieldIsUncategorized: {
		yes: "the row has no category and is not a paired transfer",
		no:  "the row already has a category or is a paired transfer",
	},
	FieldIsExcludedFromReports: {
		yes: "the row is excluded from reports", no: "the row counts in reports",
	},
	FieldIsExcludedFromSpendingPlan: {
		yes: "the row is excluded from the spending plan",
		no:  "the row counts in the spending plan",
	},
	FieldIsBillOrSubscription: {
		yes: "the row is a bill or subscription", no: "the row is not a bill or subscription",
	},
	FieldHasTag: {yes: "the row has a tag", no: "the row has no tags"},
	FieldIsCategoryUndetermined: {
		yes: "the category check has already looked at the row and could not place it",
		no:  "the category check has not given up on the row",
	},
	FieldHasCategorySuggestion: {
		yes: "the assistant has suggested a category for the row",
		no:  "no category suggestion is waiting on the row",
	},
	FieldHasAttachment: {
		yes: "the row has a file attached or a receipt filed on it",
		no:  "the row has no files attached",
	},
	FieldIsMissingReceipt: {
		yes: "the row's account requires a receipt and the row has none",
		no:  "the row has its receipt or does not need one",
	},
}

func describeDates(item FilterItem) string {
	switch {
	case !item.Start.IsZero() && !item.End.IsZero():
		return "the date is between " + item.Start.String() + " and " + item.End.String()
	case !item.Start.IsZero():
		return "the date is on or after " + item.Start.String()
	case !item.End.IsZero():
		return "the date is on or before " + item.End.String()
	}
	return "the date is anything"
}

func describeTextOperator(item FilterItem) string {
	values := quoteAll(item.Values)
	switch item.Operator {
	case OpContains:
		return "contains " + strings.Join(values, " and ")
	case OpMatches:
		return "matches the pattern " + strings.Join(values, " or ")
	}
	return "is exactly " + strings.Join(values, " or ")
}

func describeAmount(item FilterItem) string {
	direction := "the amount"
	if item.HasState {
		if item.State {
			direction = "money in"
		} else {
			direction = "money out"
		}
	}
	switch item.Operator {
	case OpEquals:
		return direction + " is " + item.Minimum.Abs().String()
	case OpGreaterThan:
		return direction + " is more than " + item.Minimum.Abs().String()
	case OpLessThan:
		return direction + " is less than " + item.Maximum.Abs().String()
	}
	switch {
	case item.HasMinimum && item.HasMaximum:
		return direction + " is between " + item.Minimum.Abs().String() +
			" and " + item.Maximum.Abs().String()
	case item.HasMinimum:
		return direction + " is at least " + item.Minimum.Abs().String()
	case item.HasMaximum:
		return direction + " is at most " + item.Maximum.Abs().String()
	}
	return direction + " is anything"
}

func resolveNames(values []string, names map[ID]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if name, ok := names[ID(value)]; ok && name != "" {
			out = append(out, name)
			continue
		}
		out = append(out, value)
	}
	return out
}

func quote(value string) string { return fmt.Sprintf("%q", value) }

func quoteAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, quote(value))
	}
	return out
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
