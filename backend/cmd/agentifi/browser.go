package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// browserSelftest proves this install can drive a browser: it launches it,
// loads a page, takes a picture, and says where each piece came from. Reached
// before config.Load, so it runs in a container with no DATABASE_URL.
func browserSelftest(out io.Writer) error {
	settings, err := config.LoadBrowser()
	if err != nil {
		return err
	}
	chrome, err := browser.FindChrome(settings.ChromePath)
	if err != nil {
		return err
	}
	engine := browser.NewEngine(settings)
	defer func() {
		if err := engine.Close(); err != nil {
			fmt.Fprintf(out, "closing the browser: %v\n", err)
		}
	}()

	fmt.Fprintf(out, "driver   %s\n", textutil.FirstNonBlank(engine.DriverDir, "(the user cache directory)"))
	describeChrome(out, chrome)
	fmt.Fprintf(out, "headless %v\n", engine.Headless)

	started := time.Now()
	context, err := engine.NewContext("", browser.DefaultViewport)
	if err != nil {
		return err
	}
	defer context.Close()

	page, err := browser.OpenPage(context)
	if err != nil {
		return err
	}
	if _, err := page.Goto("about:blank", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return fmt.Errorf("agentifi: the browser would not load a page: %w", err)
	}
	shot, err := browser.Screenshot(page)
	if err != nil {
		return fmt.Errorf("agentifi: the browser would not take a screenshot: %w", err)
	}
	if len(shot) < 4 || string(shot[:4]) != "\x89PNG" {
		return fmt.Errorf("agentifi: the screenshot is %d bytes and is not a PNG", len(shot))
	}
	launched, err := engine.Browser()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "ok       Chrome %s launched, loaded a page and answered a %d-byte PNG in %s\n",
		launched.Version(), len(shot), time.Since(started).Round(time.Millisecond))

	// A mailed bill with no attachment is filed as a print of the mail, the
	// one thing that needs Chrome's print path.
	started = time.Now()
	printed, err := engine.PrintPDF(`<!doctype html><p>Amount due: $1.00</p>`)
	if err != nil {
		return fmt.Errorf("agentifi: the browser would not print a page: %w", err)
	}
	if len(printed) < 5 || string(printed[:5]) != "%PDF-" {
		return fmt.Errorf("agentifi: the printed page is %d bytes and is not a PDF", len(printed))
	}
	fmt.Fprintf(out, "ok       printed a page, offline, to a %d-byte PDF in %s\n",
		len(printed), time.Since(started).Round(time.Millisecond))
	return nil
}

// describeChrome says which Chrome the engine launches and, for one the
// chrome service installed, the package, URL and checksum it came from.
func describeChrome(out io.Writer, chrome browser.Chrome) {
	fmt.Fprintf(out, "chrome   %s\n", chrome.Resolved)
	if chrome.Resolved != chrome.Path {
		fmt.Fprintf(out, "         through %s\n", chrome.Path)
	}
	if chrome.Origin == "" {
		fmt.Fprintf(out, "         not installed by the chrome service; no record of where it came from\n")
		return
	}
	for _, line := range strings.Split(chrome.Origin, "\n") {
		fmt.Fprintf(out, "         %s\n", line)
	}
}
