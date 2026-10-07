package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// ErrInvalidFilter is a stored filter the evaluator cannot run as written (a
// 409 in internal/api).
var ErrInvalidFilter = errors.New("service: a filter cannot be evaluated")

// ValidFilter is a stored filter as the evaluator reads it. One that names a
// field the evaluator does not handle is refused, because that item would
// quietly match less and the figure would still look like a fact.
func ValidFilter(stored store.Filter) (domain.Filter, error) {
	converted := store.DomainFilter(stored)
	if err := converted.Validate(); err != nil {
		return domain.Filter{}, fmt.Errorf("%w: %w", ErrInvalidFilter, err)
	}
	return converted, nil
}

// PrepareFilters loads each filter once, keyed as the domain matchers read
// them, and the facets they read of rows. A filter that is no longer stored is
// left out of the map, and the caller decides what a missing one means; one
// that cannot be evaluated is ErrInvalidFilter, and any other failure is
// returned rather than read as a filter that matches nothing.
func PrepareFilters(
	ctx context.Context, db *store.Store, spaceID store.SpaceID,
	ids []uuid.UUID, rows map[uuid.UUID]store.Transaction,
) (map[domain.ID]domain.Filter, map[domain.ID]domain.Facets, error) {
	filters := make(map[domain.ID]domain.Filter, len(ids))
	for _, id := range ids {
		key := domain.ID(id.String())
		if _, seen := filters[key]; seen {
			continue
		}
		stored, err := db.GetFilter(ctx, spaceID, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		filter, err := ValidFilter(stored)
		if err != nil {
			return nil, nil, err
		}
		filters[key] = filter
	}
	asked := make([]domain.Filter, 0, len(filters))
	for _, filter := range filters {
		asked = append(asked, filter)
	}
	facets, err := FilterFacets(ctx, db, spaceID, rows, asked...)
	if err != nil {
		return nil, nil, err
	}
	return filters, facets, nil
}

// FilterFacets is what these filters read of each row, keyed by transaction
// id: the row's own flags, and the facets that are not on the row (a pending
// category suggestion, a document on it) when any filter asks. Every reader of
// a user's filter goes through here so they all answer alike.
func FilterFacets(
	ctx context.Context, db *store.Store, spaceID store.SpaceID,
	rows map[uuid.UUID]store.Transaction, filters ...domain.Filter,
) (map[domain.ID]domain.Facets, error) {
	facets := make(map[domain.ID]domain.Facets, len(rows))
	for id, row := range rows {
		facets[domain.ID(id.String())] = domain.Facets(store.Facets(row))
	}
	suggestion, attachment, receipt := false, false, false
	for _, filter := range filters {
		suggestion = suggestion || filter.Tests(domain.FieldHasCategorySuggestion)
		attachment = attachment || filter.Tests(domain.FieldHasAttachment)
		receipt = receipt || filter.Tests(domain.FieldIsMissingReceipt)
	}
	if suggestion {
		waiting, err := db.TransactionsWithSuggestion(ctx, spaceID)
		if err != nil {
			return nil, err
		}
		markFacets(facets, waiting, func(one *domain.Facets) { one.HasCategorySuggestion = true })
	}
	if !attachment && !receipt {
		return facets, nil
	}
	documented, err := db.TransactionsWithDocuments(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if attachment {
		markFacets(facets, documented, func(one *domain.Facets) { one.HasAttachment = true })
	}
	if receipt {
		statuses, err := ReceiptStatuses(ctx, db, spaceID, rows, documented)
		if err != nil {
			return nil, err
		}
		missing := map[uuid.UUID]bool{}
		for id, status := range statuses {
			if status == domain.ReceiptMissing {
				missing[id] = true
			}
		}
		markFacets(facets, missing, func(one *domain.Facets) { one.MissingReceipt = true })
	}
	return facets, nil
}

// markFacets sets a facet on the rows already in the map; a row the caller
// did not load stays out of it.
func markFacets(facets map[domain.ID]domain.Facets, ids map[uuid.UUID]bool, set func(*domain.Facets)) {
	for id := range ids {
		key := domain.ID(id.String())
		if one, ok := facets[key]; ok {
			set(&one)
			facets[key] = one
		}
	}
}
