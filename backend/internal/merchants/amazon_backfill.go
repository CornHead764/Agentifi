package merchants

import (
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// amazonBackfillPause is the wait between two invoice pages of a backfill,
// which a pull does not have: a backfill is never quicker on Amazon than a
// pull.
const amazonBackfillPause = 3 * time.Second

// BackfillInvoices opens each order's printable invoice page, in the order
// given, and prints it. It is the pull's own page, read the pull's own way,
// with nothing else asked of Amazon; it stops at the first page that wants a
// sign-in or a check page.
func (m amazonModule) BackfillInvoices(call Call, orders []string, each func(orderID string, printed *Invoice)) (string, bool) {
	visit := &invoiceVisit{module: m, call: call}
	visit.printer, visit.prints = call.Page.(browser.PDFPrinter)
	if !visit.prints {
		return "this browser cannot print, so no invoice was fetched", false
	}
	for n, orderID := range orders {
		if err := call.context().Err(); err != nil {
			return fmt.Sprintf("it stopped before the last order (%v); run it again to go on", err), false
		}
		if n > 0 {
			call.Page.Sleep(amazonBackfillPause)
		}
		if _, ok := visit.open(orderID); !ok {
			if visit.stopped != "" {
				return visit.stopped, true
			}
			each(orderID, nil)
			continue
		}
		printed, ok := visit.print(orderID)
		if !ok {
			return "the invoices could not be printed", false
		}
		each(orderID, &printed)
	}
	return "", false
}
