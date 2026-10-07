package domain

import "slices"

// What a bill does to a series: a read-side merge of what the biller says this
// cycle costs, when it is due and when autopay takes it, into the household's
// schedule. Nothing a pull learns is written back onto a series, so unlinking
// a bill takes effect on the next read.

type BillerID string

const (
	BillerAlliant            BillerID = "alliant"
	BillerApple              BillerID = "apple"
	BillerCommunityConnect   BillerID = "community-connect"
	BillerEmailOnly          BillerID = "email-only"
	BillerErie               BillerID = "erie"
	BillerMyChart            BillerID = "mychart"
	BillerNorthwesternMutual BillerID = "northwestern-mutual"
	BillerRsyncNet           BillerID = "rsync-net"
	BillerSpectrum           BillerID = "spectrum"
	BillerTMobile            BillerID = "t-mobile"
	BillerTruGreen           BillerID = "trugreen"
	BillerWeEnergies         BillerID = "we-energies"
)

type BillerAccess string

const (
	// AccessAPI is plain HTTP against the provider's JSON endpoints with a
	// kept token.
	AccessAPI BillerAccess = "api"
	// AccessBrowser is Chrome in this process, or Camoufox for a
	// browser.FirefoxProvider.
	AccessBrowser BillerAccess = "browser"
	// AccessEmail is no live connection: the bill is read from the mailbox.
	AccessEmail BillerAccess = "email"
)

// BillChallengeKind is a second factor a provider is known to raise. A mailed
// code or a TOTP can be answered unattended; a text or a push cannot, so a
// nightly pull would park a challenge nobody is awake to answer.
type BillChallengeKind string

const (
	ChallengeTOTP    BillChallengeKind = "totp"
	ChallengeSMS     BillChallengeKind = "sms"
	ChallengeEmail   BillChallengeKind = "email"
	ChallengePush    BillChallengeKind = "push"
	ChallengeCaptcha BillChallengeKind = "captcha"
)

// Biller is one bill provider's facts, the counterpart of Merchant.
type Biller struct {
	ID BillerID
	// Name sits on the same line as the ID in Billers.
	Name   string
	Access BillerAccess
	Home   string
	// ReportsAutopay says the provider states the scheduled payment date
	// itself; otherwise the connection's own autopay rule supplies it.
	ReportsAutopay bool
	// HasDocuments says a pull can fetch the statement (a served PDF, or a
	// page printed to one). An email-only provider's attachments are linked by
	// the email phase instead.
	HasDocuments bool
	// Challenges are the second factors the provider's section of
	// docs/connectors/providers.md lists; empty means nothing known, not
	// nothing asked.
	Challenges []BillChallengeKind
	// KeepaliveDays is how often a browser provider's kept profile must be
	// touched so the site does not forget it; zero when no visit is needed.
	KeepaliveDays int
	// NeedsSite says the provider is one product deployed once per customer:
	// every address the module uses is built from the connection's configured
	// site, and a pull without one is refused before a browser opens.
	NeedsSite bool
	// SiteAddress says the deployment is a whole web address (its host and the
	// path the portal is served under) rather than a hostname label; see
	// SiteAddressOf.
	SiteAddress bool
	// Medical says the provider's statements are the receipts a health savings
	// account asks for. Its pull reads the payments too, and each statement is
	// filed on the bank row that paid it (see MatchBillPayments).
	Medical bool
	// Generic says this entry is no one company: the connection's own name is
	// the company, and bills arrive only through a household mail rule.
	Generic bool
}

func (b Biller) Raises(kind BillChallengeKind) bool {
	for _, one := range b.Challenges {
		if one == kind {
			return true
		}
	}
	return false
}

// SecondFactor is how a household chose to answer a login's second factor,
// for bill connections and merchant accounts alike. SecondFactorAny leaves the
// sign-in to rank what the provider offers; any other is the only way a
// sign-in takes. A TOTP setup key is sealed beside the password.
type SecondFactor string

const (
	SecondFactorAny   SecondFactor = ""
	SecondFactorEmail SecondFactor = "email"
	SecondFactorSMS   SecondFactor = "sms"
	SecondFactorTOTP  SecondFactor = "totp"
)

func (f SecondFactor) Valid() bool {
	switch f {
	case SecondFactorAny, SecondFactorEmail, SecondFactorSMS, SecondFactorTOTP:
		return true
	}
	return false
}

// Unattended says the choice answers its second factor with nobody awake; an
// authenticator with no key kept answers nothing.
func (f SecondFactor) Unattended(keyKept bool) bool {
	return f == SecondFactorEmail || (f == SecondFactorTOTP && keyKept)
}

// NeedsAPersonForACode says a challenge here can only be answered from a phone
// (SMS or push), so a nightly pull is not run unattended.
func (b Biller) NeedsAPersonForACode() bool {
	return b.Raises(ChallengeSMS) || b.Raises(ChallengePush)
}

// Billers is every provider the engine knows, in display order. Each
// provider's section of docs/connectors/providers.md records what is verified.
var Billers = []Biller{
	{
		// bill/Current states the scheduled autopay date when one exists.
		ID: BillerAlliant, Name: "Alliant Energy", Access: AccessAPI,
		Home: "https://myaccount.alliantenergy.com", HasDocuments: true, ReportsAutopay: true,
	},
	{
		// The portal asks for a second factor at every sign-in (passkey,
		// authenticator or text); the authenticator is the one a pull can
		// answer. No email option is offered.
		ID: BillerErie, Name: "Erie Insurance", Access: AccessBrowser,
		Home: "https://www.erieinsurance.com", HasDocuments: true,
		Challenges: []BillChallengeKind{ChallengeTOTP, ChallengeSMS}, KeepaliveDays: 7,
	},
	{
		ID: BillerSpectrum, Name: "Spectrum", Access: AccessBrowser,
		Home: "https://www.spectrum.net", HasDocuments: true,
		Challenges: []BillChallengeKind{ChallengeSMS, ChallengeEmail}, KeepaliveDays: 7,
	},
	{
		// The portal asks for no second factor. Its Azure AD B2C sign-in
		// behind an F5 gateway offers no "remember this device", so the
		// session is kept alive daily, the shortest-lived here.
		ID: BillerWeEnergies, Name: "We Energies", Access: AccessBrowser,
		Home: "https://www.we-energies.com", HasDocuments: true, ReportsAutopay: true,
		KeepaliveDays: 1,
	},
	{
		// The authenticator factor is answered by the sealed setup key. The
		// autopay date is stated by the portal's billing data.
		ID: BillerTMobile, Name: "T-Mobile", Access: AccessBrowser,
		Home: "https://www.t-mobile.com", ReportsAutopay: true, HasDocuments: true,
		Challenges: []BillChallengeKind{ChallengeTOTP}, KeepaliveDays: 7,
	},
	{
		// The receipt mail is both the bill and the payment: the charge is
		// fixed by the subscription and taken on the renewal date.
		ID: BillerApple, Name: "Apple", Access: AccessEmail,
		Home: "https://reportaproblem.apple.com", ReportsAutopay: true,
	},
	{
		// The Account Manager at rsync.net/am: server-rendered HTML, no API.
		// Two-factor is offered but not built, so Challenges is empty for
		// what is built — see docs/connectors/providers.md. The receipt link
		// answers application/pdf with the cookie alone.
		ID: BillerRsyncNet, Name: "rsync.net", Access: AccessBrowser,
		Home: "https://www.rsync.net", HasDocuments: true, KeepaliveDays: 7,
	},
	{
		// One product per municipality (NeedsSite): the town's subdomain is
		// also a path segment in its API, and each connection names its own
		// town. See docs/connectors/providers.md.
		//
		// It runs in Firefox via Camoufox. Its sign-in shows a page check, which is
		// waited for, never answered; Challenges names the captcha because one
		// that does not clear stops the sign-in for a person. The session lives in the tab's sessionStorage
		// (Firebase browserSessionPersistence), so every pull signs in and
		// KeepaliveDays is zero. FetchDocument refuses anything that is not a
		// PDF, which is what a lapsed session answers under a 200.
		ID: BillerCommunityConnect, Name: "Our Community Connect", Access: AccessBrowser,
		Home: "https://www.ourcommunityconnect.com", NeedsSite: true, HasDocuments: true,
		Challenges: []BillChallengeKind{ChallengeCaptcha},
	},
	{
		// The factor page offers an authenticator app, answered by the kept
		// setup key. The billing page states the day autopay drafts the next
		// premium ("Scheduled For"). No statement document is offered — see
		// docs/connectors/providers.md.
		ID: BillerNorthwesternMutual, Name: "Northwestern Mutual", Access: AccessBrowser,
		Home: "https://www.northwesternmutual.com", ReportsAutopay: true,
		Challenges: []BillChallengeKind{ChallengeTOTP}, KeepaliveDays: 7,
	},
	{
		// The portal asks for no second factor. It states no due date and
		// no payment day, so ReportsAutopay is false and only settled
		// invoices are filed — see docs/connectors/providers.md. Each work
		// order's invoice answers application/pdf with the page's cookies.
		ID: BillerTruGreen, Name: "TruGreen", Access: AccessBrowser,
		Home: "https://www.trugreen.com", HasDocuments: true, KeepaliveDays: 7,
	},
	{
		// Every health system runs its own MyChart (NeedsSite, SiteAddress):
		// the site is the address the household signs in at. The sign-in asks
		// for a code by e-mail or text unless the device is trusted, and the
		// session lapses within the hour, so every pull signs in. Statements
		// and payments are read from the billing pages — see
		// docs/connectors/providers.md.
		ID: BillerMyChart, Name: "MyChart", Access: AccessBrowser,
		Home: "https://www.mychart.org", NeedsSite: true, SiteAddress: true, Medical: true, HasDocuments: true,
		Challenges: []BillChallengeKind{ChallengeEmail, ChallengeSMS},
	},
	{
		// Any company that mails its bills and has no module. It has no
		// sign-in, no pull and no module: the mail is read by a rule the
		// household writes.
		ID: BillerEmailOnly, Name: "Emailed bills", Access: AccessEmail, Home: "", Generic: true,
	},
}

// BillerByID finds a provider, or reports that the id names none.
func BillerByID(id BillerID) (Biller, bool) {
	for _, one := range Billers {
		if one.ID == id {
			return one, true
		}
	}
	return Biller{}, false
}

// BillConnect is one statement of the billed account linked to a series, as
// its occurrences read it. AutopayOn is already resolved (stated or from the
// connection's rule), so every reader gets one answer to "when does the money
// move".
type BillConnect struct {
	// ID is the stored statement's, so a reader can name which bill an
	// occurrence came from.
	ID    ID
	DueOn Date
	// Amount is negative for money going out. A provider states a magnitude;
	// whoever loads one signs it against its series, or an unsigned bill
	// raises the projected balance by what it is about to take.
	Amount    Money
	AutopayOn Date // zero when the account does not autopay
	// Paid is the provider's word that the cycle is settled. Its amount still
	// says what the cycle cost, but a paid bill's due date can be one the
	// reader worked out, so it moves no slot and dates no payment. Whether
	// the slot is paid is the ledger's question, never the provider's.
	Paid bool
}

// BillClaimsSlot reports whether a bill due on billDueOn speaks about the
// scheduled slot. It reuses the matcher's own MatchWindow so "this slot's
// bill" and "this slot's charge" never drift apart, and since each window
// stays inside one period no bill speaks about two slots.
func BillClaimsSlot(recurrence Recurrence, slot, billDueOn Date) bool {
	if billDueOn.IsZero() || slot.IsZero() {
		return false
	}
	before, after := MatchWindow(recurrence)
	return billDueOn.NotBefore(slot.AddDays(-before)) && billDueOn.NotAfter(slot.AddDays(after))
}

// BillPaymentDates is every day a charge for the slot this bill claims could
// post: the slot lies within the match window of the bill's due date
// (BillClaimsSlot), and the charge within the match window of the slot, so
// the two windows add.
func BillPaymentDates(recurrence Recurrence, billDueOn Date) (from, to Date) {
	before, after := MatchWindow(recurrence)
	return billDueOn.AddDays(-(before + after)), billDueOn.AddDays(before + after)
}

// SlotBill is the linked bill that speaks about one scheduled slot, past or
// still to come: the one whose due date falls in the slot's match window, the
// nearer when two do and the later of two as near.
func SlotBill(series Series, slot Date, bills []BillConnect) (BillConnect, bool) {
	var best BillConnect
	bestOff := 0
	found := false
	for _, bill := range bills {
		if !BillClaimsSlot(series.Recurrence, slot, bill.DueOn) {
			continue
		}
		off := DaysBetween(slot, bill.DueOn)
		if off < 0 {
			off = -off
		}
		if !found || off < bestOff || (off == bestOff && bill.DueOn.After(best.DueOn)) {
			best, bestOff, found = bill, off, true
		}
	}
	return best, found
}

// BillOnFile is a standing bill (open or paid) as the history tally reads it.
type BillOnFile struct {
	DueOn        Date
	HasStatement bool
}

// BillHistory is what the reminder's paid slots say about a billed account's
// bills. A bill is settled when a bank row holds a slot the bill claims
// (BillClaimsSlot, the rule that files its statement on that row), and
// WithStatement counts the settled bills whose statement is on file. A bill
// due by today that no row settles is Unsettled; one due later is still to be
// paid and counts in neither.
type BillHistory struct {
	Settled       int
	WithStatement int
	Unsettled     int
}

// TallyBillHistory counts the bills against the slots bank rows hold.
func TallyBillHistory(recurrence Recurrence, bills []BillOnFile, heldSlots []Date, today Date) BillHistory {
	var out BillHistory
	for _, bill := range bills {
		settled := false
		for _, slot := range heldSlots {
			if BillClaimsSlot(recurrence, slot, bill.DueOn) {
				settled = true
				break
			}
		}
		switch {
		case settled:
			out.Settled++
			if bill.HasStatement {
				out.WithStatement++
			}
		case bill.DueOn.NotAfter(today):
			out.Unsettled++
		}
	}
	return out
}

// SlotBills is every linked bill that speaks about one scheduled slot: the
// bills due on SlotBill's day, in the order given. A provider that bills
// twice on one day for one billed account sends two invoices for one cycle,
// and the cycle costs both.
func SlotBills(series Series, slot Date, bills []BillConnect) []BillConnect {
	nearest, found := SlotBill(series, slot, bills)
	if !found {
		return nil
	}
	var out []BillConnect
	for _, bill := range bills {
		if bill.DueOn == nearest.DueOn {
			out = append(out, bill)
		}
	}
	return out
}

// BillDueGap is the median number of days between consecutive distinct due
// dates, or false with fewer than two.
func BillDueGap(dues []Date) (int, bool) {
	sorted := make([]Date, 0, len(dues))
	for _, due := range dues {
		if !due.IsZero() && !slices.Contains(sorted, due) {
			sorted = append(sorted, due)
		}
	}
	if len(sorted) < 2 {
		return 0, false
	}
	slices.SortFunc(sorted, func(a, b Date) int { return DaysBetween(b, a) })
	gaps := make([]int, 0, len(sorted)-1)
	for i := 1; i < len(sorted); i++ {
		gaps = append(gaps, DaysBetween(sorted[i-1], sorted[i]))
	}
	slices.Sort(gaps)
	return gaps[len(gaps)/2], true
}

// BillCadenceDisagrees reports that bills arriving gapDays apart do not fit
// the reminder's period: under half of it or over one and a half times it. A
// monthly reminder linked to a yearly premium shows the premium in every
// month, and only the slot near each due date can take its payment.
func BillCadenceDisagrees(recurrence Recurrence, gapDays int) bool {
	period := PeriodDays(recurrence)
	gap := float64(gapDays)
	return gap < period/2 || gap > period*3/2
}

// AutopayRuleKind is how an autopay date is worked out when the provider does
// not state one.
type AutopayRuleKind string

const (
	AutopayNone          AutopayRuleKind = "none"
	AutopayDaysBeforeDue AutopayRuleKind = "days_before_due"
	AutopayOnDueDate     AutopayRuleKind = "on_due_date"
	AutopayDayOfMonth    AutopayRuleKind = "day_of_month"
)

type AutopayRule struct {
	Kind       AutopayRuleKind
	Days       int
	DayOfMonth int
}

// AutopayOn is the day the money leaves for this bill, or zero when nothing is
// known. The provider's stated date wins over the connection's rule. The
// answer is never after the due date, or the projection would draw a balance
// later than the reminder says the bill was owed.
func AutopayOn(bill BillConnect, rule AutopayRule) Date {
	if bill.DueOn.IsZero() {
		return Date{}
	}
	pays := bill.AutopayOn
	if pays.IsZero() {
		switch rule.Kind {
		case AutopayDaysBeforeDue:
			if rule.Days < 0 {
				return Date{}
			}
			pays = bill.DueOn.AddDays(-rule.Days)
		case AutopayOnDueDate:
			pays = bill.DueOn
		case AutopayDayOfMonth:
			pays = lastDayOfMonthNotAfter(bill.DueOn, rule.DayOfMonth)
		default:
			return Date{}
		}
	}
	if pays.IsZero() {
		return Date{}
	}
	if pays.After(bill.DueOn) {
		return bill.DueOn
	}
	return pays
}

// lastDayOfMonthNotAfter is the given day of the month at or before the date.
func lastDayOfMonthNotAfter(on Date, dayOfMonth int) Date {
	if dayOfMonth < 1 {
		return Date{}
	}
	candidate := MonthOf(on).Day(dayOfMonth)
	if candidate.After(on) {
		candidate = MonthOf(on).Prev().Day(dayOfMonth)
	}
	return candidate
}

// BillStatus is what the provider says about a bill, not whether the ledger
// settled it (that is SettledSlots).
type BillStatus string

const (
	BillOpen       BillStatus = "open"
	BillPaid       BillStatus = "paid"
	BillSuperseded BillStatus = "superseded"
)

// Bill is one statement. Its identity is its subaccount, due date and invoice
// (empty unless the provider bills separately on one day), so two bills can
// share a due date.
type Bill struct {
	ID           ID
	SubaccountID ID
	DueOn        Date
	AmountDue    Money
	Status       BillStatus
}

// SupersededBills is every open bill a newer one stands for: within each
// subaccount, every open bill due before the latest open due date. A provider
// that reissues a cycle just stops mentioning the old statement; two bills due
// the same day are two invoices, and neither supersedes the other.
func SupersededBills(bills []Bill) []Bill {
	latest := map[ID]Date{}
	for _, one := range bills {
		if one.Status != BillOpen {
			continue
		}
		if due, seen := latest[one.SubaccountID]; !seen || one.DueOn.After(due) {
			latest[one.SubaccountID] = one.DueOn
		}
	}
	var out []Bill
	for _, one := range bills {
		if one.Status != BillOpen {
			continue
		}
		if one.DueOn.Before(latest[one.SubaccountID]) {
			out = append(out, one)
		}
	}
	return out
}
