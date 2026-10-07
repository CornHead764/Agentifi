package importer

import (
	"fmt"
	"sort"
	"strings"
)

// The structured result of an import run. Every problem names the store, the
// record id and the field.
//
// An error is something the export holds and the schema cannot represent, and
// the run must not be committed: a partially-written ledger reconciles against
// nothing. A warning is a fact carried across with a loss the operator has to
// be told about. A note is something the file holds that the importer does not
// read at all: a field or store a later exporter adds changes nothing that is
// written, so it is listed and left out.
//
// Problems are deduplicated by (kind, store, field, message) into one line
// with a count; the first record id is kept as the example. Warnings and notes
// carry a Kind, so a reader sees one line per kind with the lines beneath it.

// Kind is what a warning or note is about.
type Kind string

const (
	KindNotRead          Kind = "not_read"
	KindUnlinked         Kind = "unlinked_accounts"
	KindRuleOff          Kind = "rule_switched_off"
	KindRuleRebuilt      Kind = "rule_rebuilt"
	KindRuleKeywordOrder Kind = "rule_keyword_order"
	KindNoColumn         Kind = "no_column"
	KindNoFile           Kind = "file_not_in_export"
	KindUnpaired         Kind = "transfer_unpaired"
	KindMissingReference Kind = "missing_reference"
	KindSpendingPlan     Kind = "spending_plan_gap"
	KindUnconfirmed      Kind = "unconfirmed_series"
	KindAlert            Kind = "alert_not_in_catalog"
	KindGoalAccount      Kind = "goal_account"
	KindDefaults         Kind = "defaults"
	KindAccountGuessed   Kind = "account_kind_guessed"
	KindUncategorized    Kind = "uncategorized"
	KindCategoryCheck    Kind = "category_check"
	KindSplitDisagrees   Kind = "split_disagrees"
	KindNoStatementName  Kind = "no_statement_name"
	KindExclusion        Kind = "exclusion_both_flags"
)

// Actions a reader can take in the app about a kind of warning once the
// import is written. The client maps each to a page.
const (
	ActionLinkAccounts     = "link_accounts"
	ActionReviewAccounts   = "review_accounts"
	ActionReviewRules      = "review_rules"
	ActionReviewCategories = "review_categories"
	ActionReviewTransfers  = "review_transfers"
	ActionReviewRecurring  = "review_recurring"
	ActionReviewGoals      = "review_goals"
)

type kindInfo struct {
	summary string
	action  string
}

var kinds = map[Kind]kindInfo{
	KindNotRead: {"Fields and stores this importer does not read, left out", ""},
	KindUnlinked: {"Accounts arrive unlinked; match each to its SimpleFIN account so it syncs",
		ActionLinkAccounts},
	KindRuleOff:     {"Rules that arrive switched off, each with the reason", ActionReviewRules},
	KindRuleRebuilt: {"Rules rebuilt from the rows they changed; compare each with Simplifi's", ActionReviewRules},
	KindRuleKeywordOrder: {"Contains rules match their keywords in any order; Simplifi also wants that order",
		ActionReviewRules},
	KindNoColumn:         {"Simplifi facts with no place here, left out or shortened", ""},
	KindNoFile:           {"Files the export names but does not carry", ""},
	KindUnpaired:         {"Transfers imported unpaired, because the other leg is missing", ActionReviewTransfers},
	KindMissingReference: {"References to records the export does not hold, dropped", ""},
	KindSpendingPlan:     {"Spending plan figures Simplifi does not store, left at their defaults", ""},
	KindUnconfirmed:      {"Recurring suggestions never confirmed, imported inactive", ActionReviewRecurring},
	KindAlert:            {"Notification rules not in this app's catalog", ""},
	KindGoalAccount:      {"Savings goal accounts, and how each goal was kept", ActionReviewGoals},
	KindDefaults:         {"Settings the export does not hold, set to defaults", ""},
	KindAccountGuessed:   {"Accounts whose kind was guessed; set each by hand", ActionReviewAccounts},
	KindUncategorized:    {"Transactions with no category, left uncategorized", ""},
	KindCategoryCheck:    {"Categories whose kind may be wrong", ActionReviewCategories},
	KindSplitDisagrees:   {"Split transactions whose rows disagree or stand alone", ""},
	KindNoStatementName:  {"Transactions with no statement name for a rule or series to match", ""},
	KindExclusion:        {"Exclusions set both flags, because the file has one column for two", ""},
}

// Problem is one thing the import could not do, located precisely enough to
// fix.
type Problem struct {
	Kind     Kind
	Store    string
	RecordID string
	Field    string
	Message  string
	// Occurrences counts the records that hit the same problem. Only the
	// first is named.
	Occurrences int
}

func (p Problem) Render() string {
	where := make([]string, 0, 3)
	for _, part := range []string{p.Store, p.RecordID, p.Field} {
		if part != "" {
			where = append(where, part)
		}
	}
	repeats := ""
	if p.Occurrences > 1 {
		repeats = fmt.Sprintf(" (x%d)", p.Occurrences)
	}
	return fmt.Sprintf("%s: %s%s", strings.Join(where, " "), p.Message, repeats)
}

type problemKey struct {
	kind                  Kind
	store, field, message string
}

// Report holds the counts, errors, warnings and notes for one run of the
// importer.
type Report struct {
	// Read counts records seen per Simplifi store, before any mapping.
	Read map[string]int
	// Written counts rows produced per Agentifi table.
	Written  map[string]int
	Errors   []Problem
	Warnings []Problem
	Notes    []Problem
	// IgnoredStores lists stores present in the export that we deliberately do
	// not import, with the reason.
	IgnoredStores map[string]string

	errorIndex   map[problemKey]int
	warningIndex map[problemKey]int
	noteIndex    map[problemKey]int
}

func NewReport() *Report {
	return &Report{
		Read:          map[string]int{},
		Written:       map[string]int{},
		IgnoredStores: map[string]string{},
		errorIndex:    map[problemKey]int{},
		warningIndex:  map[problemKey]int{},
		noteIndex:     map[problemKey]int{},
	}
}

func (r *Report) OK() bool { return len(r.Errors) == 0 }

func (r *Report) Errorf(store, recordID, field, format string, args ...any) {
	r.Errors = add(r.Errors, r.errorIndex, "", store, recordID, field, fmt.Sprintf(format, args...))
}

func (r *Report) Warnf(kind Kind, store, recordID, field, format string, args ...any) {
	r.Warnings = add(r.Warnings, r.warningIndex, kind, store, recordID, field, fmt.Sprintf(format, args...))
}

// NotRead notes a field, store or column the importer leaves out.
func (r *Report) NotRead(store, recordID, field string) {
	r.Notes = add(r.Notes, r.noteIndex, KindNotRead, store, recordID, field, "not read")
}

func (r *Report) count(table string, n int) { r.Written[table] += n }

func add(bucket []Problem, index map[problemKey]int, kind Kind, store, recordID, field, message string) []Problem {
	key := problemKey{kind, store, field, message}
	if at, seen := index[key]; seen {
		bucket[at].Occurrences++
		return bucket
	}
	index[key] = len(bucket)
	return append(bucket, Problem{
		Kind:        kind,
		Store:       store,
		RecordID:    recordID,
		Field:       field,
		Message:     message,
		Occurrences: 1,
	})
}

// Group is every warning or note of one kind: a one-line summary, how many
// records it covers, and each deduplicated line beneath it.
type Group struct {
	Kind    Kind     `json:"kind"`
	Note    bool     `json:"note"`
	Summary string   `json:"summary"`
	Action  string   `json:"action"`
	Count   int      `json:"count"`
	Items   []string `json:"items"`
}

// Groups is the warnings by kind, in the order each kind first appeared, then
// the notes.
func (r *Report) Groups() []Group {
	out := []Group{}
	at := map[Kind]int{}
	collect := func(problems []Problem, note bool) {
		for _, problem := range problems {
			i, seen := at[problem.Kind]
			if !seen {
				info := kinds[problem.Kind]
				i = len(out)
				at[problem.Kind] = i
				out = append(out, Group{
					Kind: problem.Kind, Note: note, Summary: info.summary, Action: info.action,
					Items: []string{},
				})
			}
			out[i].Count += problem.Occurrences
			out[i].Items = append(out[i].Items, problem.Render())
		}
	}
	collect(r.Warnings, false)
	collect(r.Notes, true)
	return out
}

// RenderGroups writes the warnings and notes, one summary line per kind with
// its lines indented beneath.
func (r *Report) RenderGroups(b *strings.Builder) {
	groups := r.Groups()
	if len(groups) == 0 {
		return
	}
	b.WriteString("\nWarnings\n")
	for _, group := range groups {
		fmt.Fprintf(b, "  %s (%d)\n", group.Summary, group.Count)
		for _, item := range group.Items {
			fmt.Fprintf(b, "    %s\n", item)
		}
	}
}

func (r *Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Simplifi import — %d errors, %d warnings\n\nRead\n", len(r.Errors), len(r.Warnings))
	writeCounts(&b, r.Read)
	b.WriteString("\nWritten\n")
	writeCounts(&b, r.Written)
	if len(r.IgnoredStores) > 0 {
		b.WriteString("\nIgnored\n")
		for _, store := range sortedKeys(r.IgnoredStores) {
			fmt.Fprintf(&b, "  %-32s %s\n", store, r.IgnoredStores[store])
		}
	}
	r.RenderGroups(&b)
	if len(r.Errors) > 0 {
		b.WriteString("\nErrors\n")
		for _, problem := range r.Errors {
			fmt.Fprintf(&b, "  %s\n", problem.Render())
		}
	}
	if r.OK() {
		b.WriteString("\nOK — nothing unrepresentable\n")
	} else {
		b.WriteString("\nFAILED — nothing was written\n")
	}
	return b.String()
}

func writeCounts(b *strings.Builder, counts map[string]int) {
	for _, name := range sortedKeys(counts) {
		fmt.Fprintf(b, "  %-32s %7d\n", name, counts[name])
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// FieldError is a problem one record cannot survive: mapping a record is
// all-or-nothing, so an unparseable amount is never written as zero. The
// walker records it and carries on with the next record.
type FieldError struct {
	Field   string
	Message string
}

func fieldErrorf(field, format string, args ...any) *FieldError {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}
