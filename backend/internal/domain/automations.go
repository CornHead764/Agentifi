package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// An automation is the assistant's conversation loop with a trigger and nobody
// on the other end. The safety story is the conversation's, unchanged: a
// "propose" tool makes a card, an "apply" tool goes through the Apply button's
// own path, and neither is reachable unless the connection has changes on.

// What fires an automation.
const (
	// AutomationTriggerTransaction fires once per row that arrives from a
	// bank sync or a file import. Hand-entered rows never fire it: the person
	// who typed the row has already decided what it is.
	AutomationTriggerTransaction = "transaction_arrived"
	// AutomationTriggerDaily fires once a day at a time of the space's own.
	AutomationTriggerDaily = "daily"
	// AutomationTriggerManual fires only from the button.
	AutomationTriggerManual = "manual"
)

// How far a run may go.
const (
	// AutomationModeObserve offers the read catalogue only. The output is the
	// run's report, and nothing is proposed.
	AutomationModeObserve = "observe"
	// AutomationModePropose offers change tools that make cards.
	AutomationModePropose = "propose"
	// AutomationModeApply offers change tools that take effect immediately,
	// through the same path the Apply button uses.
	AutomationModeApply = "apply"
)

// The states a run moves through. Skipped is a run that found nothing to do
// before reaching the model: the history already agreed with the row, or the
// row is a transfer leg.
const (
	AutomationRunQueued    = "queued"
	AutomationRunRunning   = "running"
	AutomationRunSucceeded = "succeeded"
	AutomationRunFailed    = "failed"
	AutomationRunSkipped   = "skipped"
)

// Who decided a run's outcome.
const (
	AutomationDecidedByAgentifi = "agentifi"
	AutomationDecidedByModel    = "model"
)

// What fired a run.
const (
	AutomationFiredByTransaction = "transaction"
	AutomationFiredBySchedule    = "schedule"
	AutomationFiredByManual      = "manual"
	// AutomationFiredByMatch is a run queued because something outside the
	// ledger learned more about a row after it arrived — an Amazon order
	// matched to it — so the automations that read the match look again.
	AutomationFiredByMatch = "match"
)

// AutomationTriggerConfig narrows a trigger. Which rows a transaction trigger
// fires on is not here: that is the automation's Filter (ground rule 3).
type AutomationTriggerConfig struct {
	// At is the time of day for a daily trigger, "HH:MM" in the space's zone.
	At string `json:"at,omitempty"`
	// SkipTransfers is a switch rather than a filter item because it asks
	// about the pairing, and no filter field reads the pairing.
	SkipTransfers bool `json:"skip_transfers,omitempty"`
}

// AutomationContext says which facts are handed to the model before it starts.
// Anything here it could also look up with a tool; up front is more reliable
// on a small model, which may not think to ask for the payee's history.
type AutomationContext struct {
	Transaction bool `json:"transaction"`
	// SimilarTransactions is how many past rows from the same payee to
	// include, with their categories. Zero for none.
	SimilarTransactions int  `json:"similar_transactions"`
	Categories          bool `json:"categories"`
	Rules               bool `json:"rules"`
	Accounts            bool `json:"accounts"`
	// Corrections are what was proposed for a payee and what the household
	// chose instead: this row's payee first, then the most recent others.
	Corrections bool `json:"corrections"`
	// Guidance is the standing instructions whose conditions select this row,
	// or all of them on a run about no single row; see guidance.go.
	Guidance bool `json:"guidance"`
	// CashFlowHistory is twelve months of dates and amounts only — no payee,
	// account or category, on purpose: the ledger is not narrated to a model
	// to answer a question about monthly totals. The reminders through the
	// forecast's last month come with it, as dates and amounts.
	CashFlowHistory bool `json:"cash_flow_history"`
}

// AutomationTransactionFacts is what the trigger predicate needs to know about
// a row.
type AutomationTransactionFacts struct {
	Source Source
	// IsTransfer is the whole question, not the pair id alone: SkipTransfers
	// means a row filed under a transfer category as much as a matched leg.
	IsTransfer bool
	IsDeleted  bool
	// Posting and Facets are read off the row the way the rules engine reads
	// them.
	Posting Posting
	Facets  Facets
}

// AutomationTriggers reports whether a row should fire an automation with this
// trigger configuration, asked for this reason.
//
// The source rule applies to arrival only. Only rows the bank or a file
// brought fire on the way in: a hand-entered row was categorized by the person
// who typed it, and the synthetic sources are bookkeeping. A row being looked
// at again (picked out, or explained by an order) is not asked where it came
// from — an imported ledger is entirely rows of another source, and gating on
// it would refuse every one. The filter and SkipTransfers apply whatever the
// reason; a person naming specific rows goes through the forced path instead.
//
// A nil scope is every row. A scope with no items fires on nothing, unlike a
// register filter with none: the one way a trigger's scope loses its items is
// its filter going missing, and a scope must never widen on its own.
func AutomationTriggers(
	config AutomationTriggerConfig, scope *Filter, facts AutomationTransactionFacts, firedBy string,
) bool {
	if facts.IsDeleted {
		return false
	}
	if firedBy == AutomationFiredByTransaction && !ArrivingSourceFires(facts.Source) {
		return false
	}
	if config.SkipTransfers && facts.IsTransfer {
		return false
	}
	if scope != nil && (len(scope.Items) == 0 ||
		!Matches(*scope, facts.Posting, facts.Facets, DateEffective)) {
		return false
	}
	return true
}

// AutomationToolsFor is the catalogue a run is offered: observe gets the read
// half, the other modes both halves, and none gets an Attended tool. An unknown
// name in `chosen` is dropped rather than refused, so an automation saved
// before a tool was renamed keeps running with the tools that still exist.
func AutomationToolsFor(mode string, chosen []string) []AssistantTool {
	var all []AssistantTool
	for _, one := range AssistantToolsFor(mode != AutomationModeObserve) {
		if !one.Attended {
			all = append(all, one)
		}
	}
	if len(chosen) == 0 {
		return all
	}
	wanted := make(map[string]bool, len(chosen))
	for _, name := range chosen {
		wanted[name] = true
	}
	out := make([]AssistantTool, 0, len(chosen))
	for _, one := range all {
		if wanted[one.Name] {
			out = append(out, one)
		}
	}
	return out
}

// ArrivingSourceFires reports whether a row of this source fires an automation
// as it arrives; see AutomationTriggers for why it asks only of an arrival.
func ArrivingSourceFires(source Source) bool {
	switch source {
	case SourceSync, SourceFileImport:
		return true
	}
	return false
}

func AutomationModeIsValid(mode string) bool {
	switch mode {
	case AutomationModeObserve, AutomationModePropose, AutomationModeApply:
		return true
	}
	return false
}

func AutomationTriggerIsValid(trigger string) bool {
	switch trigger {
	case AutomationTriggerTransaction, AutomationTriggerDaily, AutomationTriggerManual:
		return true
	}
	return false
}

// AutomationFramingPrompt is what a run is told that a conversation is not.
// The first sentence matters most: a model that believes it is in a
// conversation ends with a question, and in a run nobody answers it.
const AutomationFramingPrompt = `

## How you are running

You are running as an automation, not in a conversation. Nobody will reply to
you, so never end with a question. Use the facts under "Context" below and the
tools, do the task, and then finish with a terse plain-text report: a few
short "- " lines, key figures first — what you concluded, and what you
proposed or changed. No preamble and no narration of your steps. If nothing
needs to change, say so in one line and stop. Reference a category, tag or
transaction by the id given in the context or read from a tool; never guess an
id.

If you decide something should change, call the change tool for it. A report
that describes a change you did not call a tool for changes nothing, and is
wrong. Call each change tool once per change: a tool that has answered has
recorded the change, and calling it again records the same change twice.`

// AutomationDryRunPrompt is added to a dry run's prompt. Untold, the model
// reports a change it believes is waiting for somebody.
const AutomationDryRunPrompt = `

## This is a dry run

Nothing you do here changes anything and nothing is put in front of the person
to approve. A change tool records what you would have done and stops. Use the
tools exactly as you would in a real run, then report what you would have
changed, and why, in short lines in the past conditional: "Would have
recategorized…".`

// AutomationPrompt is the whole system prompt for one run. The instruction
// goes last because it is the most specific and a model weights what it read
// most recently.
func AutomationPrompt(mode, instruction string, today Date, currencyCode string) string {
	prompt := AssistantPromptFor(
		mode != AutomationModeObserve, mode == AutomationModeApply) + AutomationFramingPrompt
	prompt += "\n\n## Your task\n\n" + strings.TrimSpace(instruction)
	prompt += AssistantFacts(today, currencyCode)
	return prompt
}

// AutomationSubject names what a run was about, for its row in a list.
func AutomationSubject(payee, amount, date string) string {
	parts := []string{}
	for _, part := range []string{strings.TrimSpace(payee), amount, date} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// AutomationTemplate is a starting point for the editor. The prompts are
// written for a small local model: short, one job, an explicit finish.
type AutomationTemplate struct {
	Key           string                  `json:"key"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	Trigger       string                  `json:"trigger"`
	TriggerConfig AutomationTriggerConfig `json:"trigger_config"`
	Prompt        string                  `json:"prompt"`
	Context       AutomationContext       `json:"context"`
	Mode          string                  `json:"mode"`
	Tools         []string                `json:"tools"`
	// ConfidenceThreshold is the vote score at or above which the history is
	// acted on without the model. Zero always asks.
	ConfidenceThreshold float64 `json:"confidence_threshold"`
}

// AutomationTemplateCheckCategory is the one built-in the application creates
// on a household's behalf. Callers ask for it by key, not by the name a
// household may have rewritten.
const AutomationTemplateCheckCategory = "check_category"

// AutomationTemplateCashFlowForecast's report is data rather than prose; the
// layer that records a finished run asks by key whether to parse it.
const AutomationTemplateCashFlowForecast = "cash_flow_forecast"

// AutomationAnswersWithForecast reports whether a run's report is to be read as
// a cash-flow forecast: a copy of the built-in however edited, or any
// automation handed the cash-flow history, since nothing else reads that series.
func AutomationAnswersWithForecast(templateKey string, context AutomationContext) bool {
	return templateKey == AutomationTemplateCashFlowForecast || context.CashFlowHistory
}

// AutomationTemplates is read on every load of an automation started from one:
// an untouched copy shows and runs its template's prompt and description as
// they stand here, so a key that copies may name stays in this list.
var AutomationTemplates = []AutomationTemplate{
	{
		Key:  AutomationTemplateCheckCategory,
		Name: "Suggest categories",
		Description: "When a transaction arrives from the bank, or when you ask with Suggest " +
			"categories in Transactions, work out its category from everything known about " +
			"it (the account, the bank's wording, the amount, any order behind it and the " +
			"payee's history) and suggest one with a confidence, or say why it could not. " +
			"Each suggestion waits on the row for you to accept; nothing changes until you do.",
		Trigger:       AutomationTriggerTransaction,
		TriggerConfig: AutomationTriggerConfig{SkipTransfers: true},
		Prompt: `A transaction has arrived from the bank and Agentifi could not settle its
category from the history alone. Make the best guess the evidence allows and say
how sure you are, or say in one line why no guess is possible.

The evidence, all in the context:
- the row: statement_name (the bank's words), payee, memo, bank_data, and the
  amount, whose sign says which way the money went and whose size says what
  kind of thing it can be;
- "The account": its name, kind and type, and what the household files its rows
  under;
- the order or receipt behind the charge, when there is one;
- "Agentifi's assessment" and the similar past transactions;
- the corrections and the guidance, when there are any;
- the categories: choose only from this list, by id.

History makes a guess better; it is not needed for one. The account and the
wording often settle a row alone: a target-date retirement fund bought on an
investment account is a retirement contribution or an investment purchase, a
dividend paid in is dividend income or a reinvestment, interest paid into a bank
account is interest earned. Use whichever of the household's categories fits;
when none does, propose nothing.

Rules, in order. A later rule outranks an earlier one.
1. Direction. Money out belongs in an expense category; never propose an income
   category for money out. Money in is income only when it is earned: pay,
   interest, dividends, rewards or cashback. A dividend is Dividend Income and
   interest is Interest Earned, or whichever of the household's categories says
   so. A refund, return or credit from a merchant is not income: it goes in the
   expense category of what was bought from that merchant, and that is a
   proposal like any other. On a credit card or a gift card, money in that is
   not a payment on the card is usually such a credit. On a loan, a payment
   arrives as money in and is spending all the same ("The account" says so).
   When the assessment says the history's leading category runs the wrong way,
   decide from what the row is, as it describes.
2. The row itself. Decide what it is from the wording, the memo, the account and
   the amount. A well-known chain or brand, a fund or security on an investment
   account, a dividend, interest, a fee, or a payment on a loan places a row on
   its own. A bare code, a reference number or an unfamiliar name on an everyday
   account, with nothing else behind it, does not: propose nothing and say
   "Could not guess:" and why.
3. History. Where the payee's past rows carry a category, that is the household's
   habit: it outranks a guess from the wording, and the assessment's leading
   category is your answer unless the row plainly is something else. History
   that agrees with the row raises your confidence; history that disagrees with
   the wording lowers it.
   Uncategorized past rows are weak evidence and never on their own a reason to
   propose nothing. Rows nobody has reviewed, and rows that arrived marked
   reviewed (from an import, or on an account that marks every row reviewed), say
   nothing about the category. Rows the household reviewed and left
   uncategorized lower your confidence; they do not forbid a guess. Past rows
   that are mostly transfer legs mean the row is a transfer: propose the
   transfer category, or its credit-card payment category for a payment on a
   credit card.
   When the assessment says it read the account's own rows because the wording
   names no payee, the account's leading category is the history.
4. If the row already has a category, it is evidence, not the answer, and its
   category_standing says how much it weighs. A reviewed category stands: it
   outranks the history and the wording, and only something that plainly shows
   it is wrong (money the wrong way for it, an order's items, a correction or
   the guidance) is reason to propose another. An unreviewed one is the best
   guess so far: keep it unless the evidence points elsewhere. When your answer
   is the category the row already has, propose nothing; say in one line that
   the category stands. Proposing it again changes nothing and nobody sees it.
5. If the context carries the order or receipt behind this charge (an Amazon
   order, a Costco receipt), the items decide: choose the category that fits
   what was bought. The payee's history for that merchant is what the household
   chose when it could not see the items, so it does not outrank them. Name the
   account the order was placed on in the summary.
   When the order section lists a share of the charge for each item, GROUP the
   items by the category each one belongs in and propose split_transaction with
   one split PER CATEGORY GROUP, not per item. Each split's amount is the sum
   of its items' shares (the share_of_charge values). Its memo lists what is in
   it — "Eggs, Chicken, Bread" for three grocery items in one split. When
   every item belongs in the same category, use update_transaction. When there
   is one item, or no shares, use update_transaction: the whole charge is that
   item. Tax and shipping are part of what each item cost; never propose a
   split with a line for shipping, tax or fees. Do not set is_reviewed: marking
   a row reviewed is the person's own act.
6. If "Corrections the household has made" shows they changed one of your earlier
   proposals, that is their answer and it outranks everything above, including the
   history. Follow it for the same payee, and for the same kind of item where
   the correction names one. Never propose the category they took off.
7. If "The household's guidance" is in the context, it outranks everything
   above, the corrections included: it is what they wrote down about rows like
   this one, on purpose. Follow it where it covers this row and fall back to the
   rules above where it is silent. It is words, not arithmetic — read it and
   decide.

Confidence is a number from 0 to 1: how likely your category is right.
- 0.9 or more: the household's history agrees with what the row plainly is, or a
  correction or the guidance names this case.
- 0.7 to 0.9: the history mostly agrees, or the wording and the account together
  allow one reading, such as a dividend on a brokerage account.
- 0.4 to 0.7: one good signal and nothing against it, such as a well-known chain
  with no history, or a fund's name on an investment account.
- Under 0.4: a plausible reading with little behind it, or one the history
  argues against, such as rows the household reviewed and left uncategorized.
Propose your best guess at whatever confidence it has: the household sees every
proposal and decides. Do not round up.

Propose at most one change, for this transaction only. Its summary names the
evidence and ends with the confidence, as "(confidence 0.62)". Finish with one
line saying what you decided, and when you proposed a change, a last line
"Confidence: 0.62" with your figure.`,
		Context: AutomationContext{
			Transaction: true, SimilarTransactions: 12, Categories: true, Rules: true,
			Corrections: true, Guidance: true,
		},
		Mode: AutomationModePropose,
		Tools: []string{"search_transactions", "list_categories", "update_transaction",
			"split_transaction"},
		ConfidenceThreshold: 0.85,
	},
	{
		Key:  "daily_uncategorized",
		Name: "Categorize what the rules missed, daily",
		Description: "Every morning, find the transactions from the last week that still " +
			"have no category and propose one for each, from the payee's history.",
		Trigger:       AutomationTriggerDaily,
		TriggerConfig: AutomationTriggerConfig{At: "06:30"},
		Prompt: `Find the transactions from the last 7 days that have no category, using
search_transactions with uncategorized set. For each one, search the last two
years for the same payee to see how it was categorized before, and propose a
category with update_transaction — one card per transaction, with a summary that
names the payee and the reason. Skip any row you are not confident about, and
skip transfers. Finish with a short list of what you proposed and what you skipped.`,
		Context: AutomationContext{
			Categories: true, Rules: true, Corrections: true, Guidance: true,
		},
		Mode:  AutomationModePropose,
		Tools: []string{"search_transactions", "list_categories", "update_transaction"},
	},
	{
		Key:  "flag_unusual",
		Name: "Flag an unusual transaction",
		Description: "When a transaction arrives, say whether it looks out of the ordinary " +
			"for this payee and account — a much larger amount than usual, a first-time " +
			"payee, a duplicate. Observes only; nothing is proposed.",
		Trigger:       AutomationTriggerTransaction,
		TriggerConfig: AutomationTriggerConfig{SkipTransfers: true},
		Prompt: `A transaction has just arrived. Compare it with the similar past transactions
in the context. Report in one or two short lines whether anything about it is
unusual: an amount well outside this payee's usual range, a payee never seen
before, or a possible duplicate of a recent row. If it looks ordinary, say
"Nothing unusual" and stop.`,
		Context: AutomationContext{Transaction: true, SimilarTransactions: 20},
		Mode:    AutomationModeObserve,
		Tools:   []string{"search_transactions"},
	},
	{
		Key:  AutomationTemplateCashFlowForecast,
		Name: "Estimate the next six months of cash flow",
		Description: "Every morning, read the last twelve months of money in and out — dates " +
			"and amounts only, no payees — with the reminders already scheduled, and " +
			"estimate the next six months. The projection " +
			"card shows the estimate beside the arithmetic it already draws.",
		Trigger:       AutomationTriggerDaily,
		TriggerConfig: AutomationTriggerConfig{At: "05:45"},
		Prompt: `The context holds this household's last twelve months of money in and out: one
row per month, and one row per day. Dates and amounts, and deliberately nothing
else — no payees, no categories, no accounts.

Estimate the next six calendar months, starting with the month today falls in.
What you are estimating is everything the household actually does, not only the
bills: the projection this sits beside already adds up the known bills and
paychecks, and what it cannot see is the spending that happens every month
without ever being set up as a series. Read the rhythm — the pay cycle, the
months that run heavier, a level of everyday spending — and say what it implies.

The context also lists the household's reminders: the bills and income already
scheduled from today to the end of the sixth month, and any bill from the last
two months still unpaid (past_due). Dates and amounts too. Every one of them is
money that will move inside your six months — a past_due bill in the month
today falls in — so your figures include them, and what you add on top is the
spending no reminder covers.

You may call cash_flow to see the projected balance. Do not try to look up
individual transactions: the series you were given is what this task reads.

Your entire final report must be one JSON object in exactly this shape, and
nothing else — no explanation before or after it:

{"months": [{"month": "2026-10", "money_in": "8450.00", "money_out": "7310.00"},
            {"month": "2026-11", "money_in": "8450.00", "money_out": "9100.50"}],
 "narrative": "One short sentence: what drives the estimate."}

Rules for the figures:
1. Six months, in order, with no month missing. "month" is YYYY-MM.
2. money_in and money_out are both positive amounts: what comes in, and what
   goes out. Never a negative number, and never a single net figure.
3. Amounts are plain decimal strings — "8450.00". No currency symbols, no
   thousands separators, no words.
4. Do not include a "net" field unless it is exactly money_in minus money_out.
5. Round to whole cents. Estimates to the nearest ten dollars are fine; write
   them as "8450.00".
6. The narrative is required and is what a person reads to decide whether to
   believe the figures. Name what drives the estimate — "rent, two paychecks a
   month, about 2,400 of everyday spending" — not how you calculated it. Keep
   it to one line, no filler.`,
		Context: AutomationContext{CashFlowHistory: true},
		Mode:    AutomationModeObserve,
		Tools:   []string{"cash_flow"},
	},
}

func AutomationTemplateByKey(key string) (AutomationTemplate, bool) {
	for _, one := range AutomationTemplates {
		if one.Key == key {
			return one, true
		}
	}
	return AutomationTemplate{}, false
}

// SameAutomationText reports whether two prompts or descriptions are the same
// words, however their lines are wrapped or indented.
func SameAutomationText(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}

var statedID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// StatedCategory reads the category a model's answer names by id: the one id
// in it that is among known (lowercase category ids). The answer also carries
// row and account ids, so only known ones count. An answer that names two
// categories, or says it could not guess, names none.
func StatedCategory(answer string, known map[string]bool) (string, bool) {
	if strings.Contains(strings.ToLower(answer), "could not guess") {
		return "", false
	}
	found := ""
	for _, id := range statedID.FindAllString(answer, -1) {
		id = strings.ToLower(id)
		if !known[id] || id == found {
			continue
		}
		if found != "" {
			return "", false
		}
		found = id
	}
	return found, found != ""
}

// StatedConfidence reads the confidence a model's answer ends on: the last line
// that starts "Confidence:", as a fraction from 0 to 1 ("0.62") or a percentage
// ("62%"). Emphasis around the line is ignored. An answer with no such line, or
// a figure out of range, states none.
func StatedConfidence(answer string) (float64, bool) {
	lines := strings.Split(answer, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.Trim(strings.TrimSpace(lines[i]), "*_` ")
		label, figure, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.Trim(label, "*_` "), "confidence") {
			continue
		}
		figure = strings.TrimRight(strings.Trim(strings.TrimSpace(figure), "*_` "), ".")
		scale := 1.0
		if trimmed, percent := strings.CutSuffix(figure, "%"); percent {
			figure, scale = strings.TrimSpace(trimmed), 100
		}
		value, err := strconv.ParseFloat(figure, 64)
		if err != nil {
			return 0, false
		}
		value /= scale
		if !(value >= 0 && value <= 1) {
			return 0, false
		}
		return value, true
	}
	return 0, false
}
