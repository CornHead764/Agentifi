package store

import (
	"regexp"
	"strconv"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// DomainFilter converts a stored filter into the one internal/domain evaluates.
//
// ValueIDs and ValueTexts are unioned into one Values list; a category item
// never carries texts and a payee item never carries ids, so they cannot mix.
//
// A date preset is resolved here, at evaluation time, so "last 3 months" keeps
// rolling and server sums match the client's window. An unrecognized preset
// falls back to the dates captured at save time rather than an open range.
func DomainFilter(stored Filter) domain.Filter {
	return DomainFilterOn(stored, domain.DateOf(time.Now()))
}

// DomainFilterOn is DomainFilter with the clock supplied, for tests.
func DomainFilterOn(stored Filter, today domain.Date) domain.Filter {
	items := make([]domain.FilterItem, 0, len(stored.Items))
	for _, item := range stored.Items {
		values := make([]string, 0, len(item.ValueIDs)+len(item.ValueTexts))
		for _, id := range item.ValueIDs {
			values = append(values, id.String())
		}
		values = append(values, item.ValueTexts...)

		start, end := item.DateFrom, item.DateTo
		if item.DatePreset != "" {
			if from, to, ok := resolveDatePreset(item.DatePreset, today); ok {
				start, end = from, to
			}
		}

		converted := domain.FilterItem{
			Field:      domain.FilterField(item.Field),
			Operator:   domain.FilterOperator(item.Operator),
			Values:     values,
			Minimum:    item.AmountMin,
			HasMinimum: item.HasAmountMin,
			Maximum:    item.AmountMax,
			HasMaximum: item.HasAmountMax,
			Start:      start,
			End:        end,
			Text:       item.Text,
			Negated:    item.Negated,
			GroupIndex: item.GroupIndex,
		}
		if item.State != nil {
			converted.State, converted.HasState = *item.State, true
		}
		items = append(items, converted)
	}
	return domain.Filter{ID: domainID(stored.ID), Items: items, Name: stored.Name}
}

// relativePreset matches "-90d" and its siblings — the client date picker's
// grammar.
var relativePreset = regexp.MustCompile(`^-(\d+)([dwmy])$`)

func resolveDatePreset(preset string, today domain.Date) (domain.Date, domain.Date, bool) {
	month := domain.MonthOf(today)
	quarter := domain.NewMonth(today.Year, (today.Month-1)/3*3+1)
	switch preset {
	case "all-time":
		return domain.Date{}, domain.Date{}, true
	case "this-month":
		return month.FirstDay(), today, true
	case "this-quarter":
		return quarter.FirstDay(), today, true
	case "this-year":
		return domain.NewDate(today.Year, time.January, 1), today, true
	case "last-month", "last-quarter", "last-year":
		// The previous complete calendar unit.
		var from, to domain.Date
		switch preset {
		case "last-month":
			from, to = month.Prev().FirstDay(), month.Prev().LastDay()
		case "last-quarter":
			from, to = quarter.Shift(-3).FirstDay(), quarter.Prev().LastDay()
		default: // last-year
			from = domain.NewDate(today.Year-1, time.January, 1)
			to = domain.NewDate(today.Year-1, time.December, 31)
		}
		return from, to, true
	}

	if m := relativePreset.FindStringSubmatch(preset); m != nil {
		count, _ := strconv.Atoi(m[1])
		var from domain.Date
		switch m[2] {
		case "d":
			from = today.AddDays(-count)
		case "w":
			from = today.AddDays(-7 * count)
		case "m":
			// Clamped, not AddDate: Go normalizes February 31 into March, so
			// "-1m" on March 31 would open on March 3 and skip February.
			target := domain.MonthOf(today).Shift(-count)
			from = target.Day(today.Day)
		case "y":
			from = domain.ClampToMonth(today.Year-count, today.Month, today.Day)
		}
		return from, today, true
	}
	return domain.Date{}, domain.Date{}, false
}
