package merchants

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// ordersPage is an Amazon whose order history has two full pages, then ends.
func ordersPage() *printingPage {
	page := &printingPage{StubPage: showing(
		"https://www.amazon.com/your-orders/orders", []string{"orders"}, nil, "")}
	classify := page.OnEvaluate
	card := func(id string) map[string]any {
		return map[string]any{
			"order_id": id, "date_text": "September 12, 2026", "total": "10.00",
			"currency": "USD", "status": "Delivered",
			"items": []any{map[string]any{"title": "Desk lamp", "quantity": 1}},
		}
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case amazonReadOrders:
			switch {
			case strings.Contains(page.Location, "startIndex=10"):
				return []any{card("111-0000003-0000003")}, nil
			case strings.Contains(page.Location, "startIndex="):
				return []any{}, nil
			}
			return []any{card("111-0000001-0000001"), card("111-0000002-0000002")}, nil
		case amazonReadInvoice:
			return map[string]any{
				"items":    []any{map[string]any{"quantity": 1, "title": "Desk lamp", "price": "9.00"}},
				"subtotal": "9.00", "tax": "1.00", "grand_total": "10.00", "gift_card": "0.00",
				"charges": []any{},
			}, nil
		case amazonReadCharges:
			return map[string]any{"charges": []any{}, "hasNext": false}, nil
		}
		return classify(script, arg)
	}
	return page
}

func TestThePullReportsEachPhaseItIsIn(t *testing.T) {
	var lines []string
	ctx := provider.WithPullProgress(context.Background(), func(line string) { lines = append(lines, line) })
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	call := Call{
		Ctx: ctx, Page: ordersPage(), Notes: &Notes{}, SinceDays: 30,
		Now: func() time.Time { return now },
	}

	_, err := amazonModule{}.Fetch(call)
	require.NoError(t, err)

	require.Equal(t, []string{
		"Opening Amazon's order history",
		"Reading order history: the last 30 days, page 1 (0 orders found so far)",
		"Reading order history: the last 30 days, page 2 (2 orders found so far)",
		"Reading order history: the last 30 days, page 3 (3 orders found so far)",
		"Reading invoices: 1 of 3",
		"Reading invoices: 2 of 3",
		"Reading invoices: 3 of 3",
		"Reading card charges",
		"Reading card charges: page 1",
		"Reading the gift card balance",
	}, lines)
}

func TestAYearlyHistoryNamesTheYearItIsReading(t *testing.T) {
	var lines []string
	ctx := provider.WithPullProgress(context.Background(), func(line string) { lines = append(lines, line) })
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	call := Call{
		Ctx: ctx, Page: ordersPage(), Notes: &Notes{}, SinceDays: 400,
		Now: func() time.Time { return now },
	}

	_, err := amazonModule{}.readOrderPages(call, filtersFor(400, domain.DateOf(now)), "2025-08-16")
	require.NoError(t, err)

	require.Contains(t, lines, "Reading order history: 2026, page 1 (0 orders found so far)")
	require.Contains(t, lines, "Reading order history: 2025, page 1 (3 orders found so far)")
}

func TestAPullWithNobodyListeningStillRuns(t *testing.T) {
	call := Call{Page: ordersPage(), Notes: &Notes{}, SinceDays: 30,
		Now: func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) }}

	result, err := amazonModule{}.Fetch(call)

	require.NoError(t, err)
	require.Equal(t, 3, result.Orders)
}
