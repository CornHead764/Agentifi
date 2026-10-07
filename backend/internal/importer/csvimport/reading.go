package csvimport

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer"
)

// The columns Simplifi's "Export transactions" writes, in its order.
//
// The header is read by name rather than positionally. A column not on this
// list is noted and skipped: a later export adding one changes no row.
const (
	colDate        = "Date"
	colAccount     = "Account"
	colFlag        = "Flag"
	colReviewed    = "Reviewed"
	colStatus      = "Status"
	colPayee       = "Payee"
	colStatement   = "Statement name"
	colCategory    = "Category"
	colSplit       = "Split"
	colTags        = "Tags"
	colNotes       = "Notes"
	colAttachments = "Attachments"
	colExclusion   = "Exclusion"
	colRecurring   = "Recurring"
	colAmount      = "Amount"
	colCheck       = "Check #"
)

var knownColumns = []string{
	colDate, colAccount, colFlag, colReviewed, colStatus, colPayee, colStatement,
	colCategory, colSplit, colTags, colNotes, colAttachments, colExclusion,
	colRecurring, colAmount, colCheck,
}

// requiredColumns are the ones with no defensible default. Everything else may
// be absent from an older export.
var requiredColumns = []string{colDate, colAccount, colPayee, colCategory, colAmount}

// storeCSV is the Problem.Store value for everything this package reports, so
// a CSV problem never looks like one from the IndexedDB importer.
const storeCSV = "transactions.csv"

// Row is one CSV record with every cell already normalized and typed.
type Row struct {
	// Line is the file line the record starts on, which is what the operator
	// sees in an editor. It is not the record index: a statement name can
	// contain a newline, so the two drift apart as the file goes on.
	Line int

	// ExternalID is the source's own id for the row, when the file carries
	// one. The CSV has none; the OFX reader sets it.
	ExternalID string

	Date          domain.Date
	Account       string
	Flag          string
	Reviewed      bool
	Pending       bool
	Payee         string
	StatementName string
	// Category is the full path as the file writes it, "Parent:Child", with
	// the separator kept. Mapping splits it.
	Category    string
	IsSplit     bool
	Tags        []string
	Notes       string
	Excluded    bool
	Recurring   bool
	Amount      domain.Money
	CheckNumber string
}

// ReadRows parses the whole file, reporting every bad record rather than
// stopping at the first.
//
// A record that cannot be read is named on the report with its line and
// column, never skipped, and the run will refuse to write.
//
// The second result is false only when the header is unreadable or
// unrecognised, meaning this is not a transaction export at all.
func ReadRows(r io.Reader, report *importer.Report) ([]Row, bool) {
	reader := csv.NewReader(r)
	// Set from the header below. -1 while reading the header so a header of
	// any width is read rather than rejected by the count check.
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		report.Errorf(storeCSV, "", "", "cannot read the header row: %v", err)
		return nil, false
	}
	index, err := indexColumns(header, report)
	if err != nil {
		report.Errorf(storeCSV, "", "", "%v", err)
		return nil, false
	}
	reader.FieldsPerRecord = len(header)

	rows := make([]Row, 0, 8192)
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var parse *csv.ParseError
			if errors.As(err, &parse) {
				report.Errorf(storeCSV, lineID(parse.StartLine), "", "%v", parse.Err)
				continue
			}
			report.Errorf(storeCSV, "", "", "cannot read: %v", err)
			return rows, true
		}
		line, _ := reader.FieldPos(0)
		row, ok := parseRow(record, index, line, report)
		if ok {
			rows = append(rows, row)
		}
	}
	report.Read["csv rows"] = len(rows)
	return rows, true
}

func indexColumns(header []string, report *importer.Report) (map[string]int, error) {
	index := make(map[string]int, len(header))
	known := make(map[string]bool, len(knownColumns))
	for _, name := range knownColumns {
		known[name] = true
	}
	for at, raw := range header {
		name := clean(strings.TrimPrefix(raw, "\ufeff"))
		if !known[name] {
			report.NotRead(storeCSV, "", name)
			continue
		}
		if _, repeated := index[name]; repeated {
			return nil, fmt.Errorf("column %q appears twice", name)
		}
		index[name] = at
	}
	for _, name := range requiredColumns {
		if _, found := index[name]; !found {
			return nil, fmt.Errorf("the header has no %q column", name)
		}
	}
	return index, nil
}

func parseRow(record []string, index map[string]int, line int, report *importer.Report) (Row, bool) {
	row := Row{Line: line}
	id := lineID(line)
	ok := true

	// Mapping one record is all-or-nothing, but every field is still checked
	// so one run names every problem in the row.
	fail := func(field, format string, args ...any) {
		report.Errorf(storeCSV, id, field, format, args...)
		ok = false
	}
	cell := func(name string) string {
		at, found := index[name]
		if !found || at >= len(record) {
			return ""
		}
		return clean(record[at])
	}
	yesNo := func(name string) bool {
		raw := cell(name)
		switch strings.ToLower(raw) {
		case "yes", "true":
			return true
		case "no", "false", "":
			return false
		}
		fail(name, "expected yes or no, got %q", raw)
		return false
	}

	date, err := parseDate(cell(colDate))
	if err != nil {
		fail(colDate, "%v", err)
	}
	row.Date = date

	amount, err := parseAmount(record, index, colAmount)
	if err != nil {
		fail(colAmount, "%v", err)
	}
	row.Amount = amount

	row.Account = cell(colAccount)
	if row.Account == "" {
		fail(colAccount, "is empty; a transaction with no account cannot be placed")
	}
	row.Payee = cell(colPayee)
	row.StatementName = cell(colStatement)
	row.Category = cell(colCategory)
	row.Notes = cell(colNotes)
	row.CheckNumber = cell(colCheck)
	row.Flag = cell(colFlag)
	row.Reviewed = yesNo(colReviewed)
	row.IsSplit = yesNo(colSplit)
	row.Excluded = yesNo(colExclusion)
	row.Recurring = yesNo(colRecurring)
	row.Tags = splitTags(cell(colTags))

	switch strings.ToLower(cell(colStatus)) {
	case "":
		// Simplifi leaves the cell empty for a posted row.
	case "pending":
		row.Pending = true
	default:
		fail(colStatus, "expected Pending or an empty cell, got %q", cell(colStatus))
	}

	// Attachments is a yes/no flag saying an attachment exists, never the blob
	// itself, so there is nothing here to import even when it says yes.
	if strings.EqualFold(cell(colAttachments), "yes") {
		report.Warnf(importer.KindNoFile, storeCSV, id, colAttachments,
			"the row has an attachment; a CSV export carries no attachment content, so none was imported")
	}

	return row, ok
}

func lineID(line int) string { return "line " + strconv.Itoa(line) }

// parseDate refuses every all-numeric format on purpose: 03/04/2024 is two
// different days depending on who exported it, and nothing in the file says
// which.
func parseDate(raw string) (domain.Date, error) {
	if raw == "" {
		return domain.Date{}, errors.New("is empty")
	}
	for _, layout := range []string{"Jan 2, 2006", "January 2, 2006", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return domain.DateOf(t), nil
		}
	}
	return domain.Date{}, fmt.Errorf("cannot parse %q; expected a date such as \"Apr 2, 2026\"", raw)
}

// parseAmount reads the raw cell, never one normalized by clean, so money
// never passes through the free-text normalizer. Every amount in the export is
// written with a leading space.
func parseAmount(record []string, index map[string]int, name string) (domain.Money, error) {
	at, found := index[name]
	if !found || at >= len(record) {
		return domain.Zero, errors.New("is missing")
	}
	raw := strings.TrimSpace(record[at])
	if raw == "" {
		return domain.Zero, errors.New("is empty")
	}
	amount, ok := domain.ParseMoneyText(raw)
	if !ok {
		return domain.Zero, fmt.Errorf("cannot parse %q as an amount", raw)
	}
	return amount, nil
}

// splitTags reads the tags cell, which joins several tags with a comma.
//
// A tag whose own name contains a comma is indistinguishable from two tags.
func splitTags(cell string) []string {
	if cell == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, part := range strings.Split(cell, ",") {
		if name := clean(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// clean normalizes one cell.
//
// U+00A0 turns up inside account and category names in an export, and left
// alone yields two accounts whose names look identical. Statement names also
// arrive with embedded newlines, so every kind of space is collapsed.
func clean(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == '\ufeff' || r == '\u200b':
			// Zero-width: neither a space nor a character, and invisible in
			// every name comparison it survives.
		case unicode.IsSpace(r):
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}
