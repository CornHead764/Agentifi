package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/config"
)

// The `probe-sign-in` subcommand: what a provider's sign-in page looks like to
// the reader that will drive it. It reports where the page landed and what it
// found, then both again a few seconds later, because an app-drawn page reads
// as nothing at first. The census is billers.Census, the same rule a sign-in
// acts on.
//
// Nothing here may print a secret (see internal/browser/devtools.go): names,
// types, counts and addresses only, never a value, cookie, storage state or
// request body.
//
// Reached before config.Load: probing a public page needs no database.
func probeSignIn(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("agentifi probe-sign-in", flag.ContinueOnError)
	fs.SetOutput(out)
	wait := fs.Duration("wait", 6*time.Second,
		"how long to let an application-rendered page paint before reading it again")
	fs.Usage = func() {
		fmt.Fprint(out, `agentifi probe-sign-in <url> [--wait 6s]

Drives the browser this build carries to a sign-in page and reports what the
shared classifier makes of it. Set AGENTIFI_BROWSER_HEADFUL=true to watch.
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("agentifi: probe-sign-in takes one address")
	}
	address := fs.Arg(0)

	settings, err := config.LoadBrowser()
	if err != nil {
		return err
	}
	engine := browser.NewEngine(settings)
	defer func() {
		if err := engine.Close(); err != nil {
			fmt.Fprintf(out, "closing the browser: %v\n", err)
		}
	}()
	context, err := engine.NewContext("", browser.DefaultViewport)
	if err != nil {
		return err
	}
	defer context.Close()
	opened, err := browser.OpenPage(context)
	if err != nil {
		return err
	}
	page := browser.Wrap(opened)

	fmt.Fprintf(out, "asked for  %s\n", address)
	fmt.Fprintf(out, "headless   %v\n", engine.Headless)

	// The navigation's own error is not fatal: a portal that redirects
	// mid-load still leaves the browser somewhere worth reporting.
	response, err := opened.Goto(address, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		fmt.Fprintf(out, "goto       %v\n", err)
	}
	for _, hop := range redirectChain(response) {
		fmt.Fprintf(out, "redirect   %s\n", hop)
	}
	page.Settle()

	report(out, "at once", page)
	page.Sleep(*wait)
	report(out, fmt.Sprintf("after %s", wait.Round(time.Millisecond)), page)
	return nil
}

// report is one census of the page, under a heading saying when it was taken.
func report(out io.Writer, when string, page browser.Page) {
	fmt.Fprintf(out, "\n--- %s ---\n", when)
	billers.TakeCensus(page).Print(out)
}

// redirectChain is the addresses the server sent the browser through, oldest
// first. A JavaScript redirect leaves none, which is why `landed` is always
// printed.
func redirectChain(response playwright.Response) []string {
	if response == nil {
		return nil
	}
	var hops []string
	for request := response.Request(); request != nil; request = request.RedirectedFrom() {
		hops = append(hops, request.URL())
	}
	// Oldest first: what was asked for, then each hop, then where it landed.
	for left, right := 0, len(hops)-1; left < right; left, right = left+1, right-1 {
		hops[left], hops[right] = hops[right], hops[left]
	}
	if len(hops) < 2 {
		return nil
	}
	return hops
}
