package merchants

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The receipts a Costco pull lays out for printing, from the invented
// fixtures the reader's own tests use.

func TestEachDetailedReceiptInTheWindowIsLaidOutForPrinting(t *testing.T) {
	_, receipts := receiptsFixture(t)

	pages := costcoReceiptPages(harvested{receipts: receipts}, "2026-08-01", nil)

	// The fuel receipt has no barcode to file it under, and the last one is
	// older than the window.
	require.Len(t, pages, 2)
	require.Equal(t, "21100123456789012345", pages[0].OrderID)
	require.Equal(t, "costco-receipt-21100123456789012345.pdf", pages[0].Filename)
	require.Empty(t, pages[0].PDF, "the engine prints it")
	page := pages[0].HTML
	require.Contains(t, page, "Costco receipt")
	require.Contains(t, page, "Springfield")
	require.Contains(t, page, "2026-09-05")
	require.Contains(t, page, "KS ORGANIC EGGS")
	require.Contains(t, page, "KS ROTISSERIE CHICKEN")
	require.Contains(t, page, "188.50")
	require.Contains(t, page, "9.50")
	require.Contains(t, page, "VISA ••••1234")
	require.Contains(t, page, "not a copy of Costco", "it says whose layout it is")
	require.NotContains(t, page, "<script")
	require.NotContains(t, page, "http", "the page reaches for nothing")

	require.Equal(t, "21100999888777666555", pages[1].OrderID)
	require.Contains(t, pages[1].HTML, "Costco return")
	require.Contains(t, pages[1].HTML, "RETURNED THING")
}

func TestAReceiptAlreadyOnFileIsNotLaidOutAgain(t *testing.T) {
	_, receipts := receiptsFixture(t)

	pages := costcoReceiptPages(harvested{receipts: receipts}, "2026-08-01",
		map[string]bool{"21100123456789012345": true})

	require.Len(t, pages, 1)
	require.Equal(t, "21100999888777666555", pages[0].OrderID)
}

func TestAReceiptWithItemsByNumberOnlyIsNotLaidOut(t *testing.T) {
	bare := receipt{
		WarehouseName: "Springfield", TransactionDate: "2026-09-05T14:22:11.000",
		TransactionBarcode: "21100123450000000000", Total: "12.00",
		ItemArray: []receiptLine{{ItemNumber: "1234567", Amount: "12.00"}},
	}

	require.Empty(t, costcoReceiptPages(harvested{receipts: []receipt{bare}}, "2026-08-01", nil))
}

func TestWhatTheReceiptSaysIsEscapedOnThePage(t *testing.T) {
	odd := receipt{
		WarehouseName: `Lake<b>wood</b>`, TransactionDate: "2026-09-05T14:22:11.000",
		TransactionBarcode: "21100123450000000001", Total: "3.00",
		ItemArray: []receiptLine{{
			ItemNumber: "1", ItemDescription01: `<img src=x onerror="fetch('//e.test')">`, Amount: "3.00",
		}},
	}

	pages := costcoReceiptPages(harvested{receipts: []receipt{odd}}, "2026-08-01", nil)

	require.Len(t, pages, 1)
	require.NotContains(t, pages[0].HTML, "<img")
	require.NotContains(t, pages[0].HTML, "<b>")
	require.True(t, strings.Contains(pages[0].HTML, "&lt;img"), "the description is shown as text")
}
