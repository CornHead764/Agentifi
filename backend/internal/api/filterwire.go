package api

import (
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The one filter entity (ground rule 3) on the procedure wire, and the id and
// date conversions the services that embed it share.

func filterProto(f store.Filter) *agentifiv1.Filter {
	items := make([]*agentifiv1.FilterItem, 0, len(f.Items))
	for _, item := range f.Items {
		items = append(items, &agentifiv1.FilterItem{
			Id:         item.ID.String(),
			Field:      item.Field,
			Operator:   item.Operator,
			GroupIndex: int32(item.GroupIndex),
			Position:   int32(item.Position),
			Negated:    item.Negated,
			ValueIds:   uuidStrings(item.ValueIDs),
			ValueTexts: store.NonNil(item.ValueTexts),
			Text:       dbconv.NullText(item.Text),
			AmountMin:  nullableMoneyProto(item.AmountMin, item.HasAmountMin),
			AmountMax:  nullableMoneyProto(item.AmountMax, item.HasAmountMax),
			DateFrom:   dateProto(item.DateFrom),
			DateTo:     dateProto(item.DateTo),
			DatePreset: dbconv.NullText(item.DatePreset),
			State:      item.State,
		})
	}
	return &agentifiv1.Filter{
		Id:        f.ID.String(),
		Name:      dbconv.NullText(f.Name),
		Scope:     f.Scope,
		QueryText: dbconv.NullText(f.QueryText),
		Position:  int32(f.Position),
		Items:     items,
	}
}

// filterItemWrites reads the items a request sent into the shape
// buildFilterItems checks, refusing a malformed id, amount or date at `at`.
func filterItemWrites(sent []*agentifiv1.FilterItemWrite, at string) ([]FilterItemWrite, error) {
	out := make([]FilterItemWrite, 0, len(sent))
	for _, one := range sent {
		write := FilterItemWrite{
			Field:      domain.FilterField(one.GetField()),
			Operator:   domain.FilterOperator(one.GetOperator()),
			GroupIndex: int(one.GetGroupIndex()),
			Position:   int(one.GetPosition()),
			Negated:    one.GetNegated(),
			ValueTexts: one.GetValueTexts(),
			Text:       one.Text,
			DatePreset: one.DatePreset,
			State:      one.State,
		}
		if one.GetValueIds() != nil {
			ids, err := uuidsFrom(one.GetValueIds(), "body", at, "value_ids")
			if err != nil {
				return nil, err
			}
			write.ValueIDs = ids
		}
		for _, bound := range []struct {
			sent *agentifiv1.NullableMoney
			into **domain.Money
			name string
		}{
			{one.GetAmountMin(), &write.AmountMin, "amount_min"},
			{one.GetAmountMax(), &write.AmountMax, "amount_max"},
		} {
			if bound.sent == nil {
				continue
			}
			amount, err := moneyFrom(bound.sent, "body", at, bound.name)
			if err != nil {
				return nil, err
			}
			*bound.into = &amount
		}
		for _, day := range []struct {
			sent *string
			into **Date
			name string
		}{
			{one.DateFrom, &write.DateFrom, "date_from"},
			{one.DateTo, &write.DateTo, "date_to"},
		} {
			if day.sent == nil {
				continue
			}
			parsed, err := dateFrom(*day.sent, "body", at, day.name)
			if err != nil {
				return nil, err
			}
			wire := Date(parsed)
			*day.into = &wire
		}
		out = append(out, write)
	}
	return out, nil
}

// uuidFrom reads an id a request names in its body. Empty is uuid.Nil, which
// the caller's own check refuses as it refused an omitted id.
func uuidFrom(raw string, loc ...string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalid("uuid_parsing", loc, "%q is not an id", raw)
	}
	return id, nil
}

func uuidsFrom(raws []string, loc ...string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raws))
	for _, raw := range raws {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, errInvalid("uuid_parsing", loc, "%q is not an id", raw)
		}
		out = append(out, id)
	}
	return out, nil
}

// dateFrom reads a "YYYY-MM-DD" field.
func dateFrom(raw string, loc ...string) (domain.Date, error) {
	parsed, err := parseDate(raw)
	if err != nil {
		return domain.Date{}, errInvalid("date_parsing", loc, "%s", err)
	}
	return parsed, nil
}
