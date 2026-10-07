package browser

import (
	"errors"
	"fmt"

	"github.com/playwright-community/playwright-go"
)

const printTimeoutMS = 30_000

// PrintPDF prints a page of HTML to a PDF with nothing on it allowed to reach
// out: scripts off, the context offline, and every request refused. The page
// is untrusted (a mail), so each of the three holds on its own, and it gets a
// fresh context rather than any connection's profile so no provider session is
// in reach.
func (e *Engine) PrintPDF(page string) ([]byte, error) {
	b, err := e.Browser()
	if err != nil {
		return nil, err
	}
	context, err := b.NewContext(playwright.BrowserNewContextOptions{
		JavaScriptEnabled: playwright.Bool(false),
		Offline:           playwright.Bool(true),
		ServiceWorkers:    playwright.ServiceWorkerPolicyBlock,
		AcceptDownloads:   playwright.Bool(false),
		Locale:            playwright.String(defaultLocale),
		TimezoneId:        playwright.String(defaultTimezone),
	})
	if err != nil {
		return nil, fmt.Errorf("browser: no context to print in: %w", err)
	}
	defer func() { _ = context.Close() }()
	context.SetDefaultTimeout(printTimeoutMS)
	context.SetDefaultNavigationTimeout(printTimeoutMS)
	if err := context.Route("**/*", func(route playwright.Route) {
		_ = route.Abort("blockedbyclient")
	}); err != nil {
		return nil, fmt.Errorf("browser: the print context could not be closed off: %w", err)
	}

	tab, err := context.NewPage()
	if err != nil {
		return nil, fmt.Errorf("browser: no page to print in: %w", err)
	}
	if err := tab.SetContent(page, playwright.PageSetContentOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
	}); err != nil {
		return nil, fmt.Errorf("browser: the page to print would not load: %w", err)
	}
	printed, err := tab.PDF(letterPDF())
	if err != nil {
		return nil, fmt.Errorf("browser: the page would not print: %w", err)
	}
	if len(printed) == 0 {
		return nil, errors.New("browser: the page printed to nothing")
	}
	return printed, nil
}

func letterPDF() playwright.PagePdfOptions {
	return playwright.PagePdfOptions{
		Format:          playwright.String("Letter"),
		PrintBackground: playwright.Bool(true),
		Margin: &playwright.Margin{
			Top: playwright.String("0.5in"), Bottom: playwright.String("0.5in"),
			Left: playwright.String("0.5in"), Right: playwright.String("0.5in"),
		},
	}
}
