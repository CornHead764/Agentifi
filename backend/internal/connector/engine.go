// Package connector is the engine that signs in to a bill provider or a
// merchant and pulls from it, in this process: it drives internal/billers for
// service.Bills (through Bills) and internal/merchants for service.Merchants
// (through Merchants). The connectors are described in docs/connectors/.
//
//   - Both run one sign-in loop (advance): the password is typed once, a
//     factor page is pressed through at most twice, and a round that changes
//     nothing is given up on.
//   - A module that cannot read an amount leaves the bill out; it never sends 0.
//   - Documents come by reference: FetchDocument serves each once, within
//     DocumentTTL, and forgets it.
//   - A bill pull that hits a code is parked with the pull in flight; the
//     answer comes through AnswerConnect and ResumePull finishes it.
//   - A merchant keeps no profile directory: the session of record is the
//     sealed storage state, because Amazon rolls its cookies on every pull.
//   - A password is held only for the minutes a sign-in takes, and is never
//     noted, logged or answered. Keeping it is the backend's business.
package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

const SessionTTL = browser.SessionTTL

// DocumentTTL is how long a minted statement is held for the one read that
// takes it.
const DocumentTTL = 10 * time.Minute

// RoundsBound is how long the sign-in loop may go without finishing a round
// before it is taken for wedged. It bounds the dialog's spinner and the
// profile claim, which would otherwise never end by themselves.
const RoundsBound = 5 * time.Minute

type Engine struct {
	Billers   *billers.Registry
	Merchants *merchants.Registry
	// Open and OpenFirefox are the browsers a connector runs in (agent.For).
	// OpenFirefox is nil when no Camoufox server is configured.
	Open        agent.Opener
	OpenFirefox agent.Opener
	// Fetcher is where a module's calls go: a plain client, or a page at the
	// provider's own origin for a module that declares one.
	Fetcher      agent.Fetcher
	ProfilesRoot string
	Log          *slog.Logger
	Now          func() time.Time
	// Sleep is injectable because an authenticator code minted at the tail of
	// its step waits for the next one.
	Sleep func(time.Duration)
	// Print turns a module's HTML invoice into a PDF, offline and with
	// scripts off. Nil files no HTML invoice.
	Print func(page string) ([]byte, error)
	// SignInEnded is told, once, how each sign-in a person started ended
	// when it did not land, for the connection to keep. Nil keeps nothing.
	SignInEnded func(provider.BillSignInEnded)

	sessions browser.Sessions[*session]
	// mu guards documents and every session's trail.
	mu        sync.Mutex
	documents map[string]*heldDocument
}

// Bills is the engine as service.Bills drives it.
type Bills struct{ *Engine }

// Merchants is the engine as service.Merchants drives it.
type Merchants struct{ *Engine }

// OpenBrowser is a connection's browser: its own profile where it has one.
type OpenBrowser = agent.Browser

func New(engine *browser.Engine, bills *billers.Registry, shops *merchants.Registry) *Engine {
	out := &Engine{Billers: bills, Merchants: shops, Fetcher: agent.Fetchers(engine)}
	if engine != nil {
		out.Open = agent.Chrome(engine)
		out.ProfilesRoot = engine.ProfilesRoot
		out.Print = engine.PrintPDF
	}
	if engine != nil && engine.HasFirefox() {
		out.OpenFirefox = agent.Firefox(engine)
	}
	return out
}

// opener opens the browser this module runs in, at the default viewport.
func (e *Engine) opener(module any, open agent.Open) (*OpenBrowser, error) {
	open.Viewport = browser.DefaultViewport
	return agent.For(module, e.Open, e.OpenFirefox)(open)
}

// caller is the HTTP caller a module's calls go through and its release: a
// plain client, or a page at the origin a module that runs in Camoufox names.
func (e *Engine) caller(module any, origin, document string) (browser.Fetcher, func(), error) {
	fetchers := e.Fetcher
	if fetchers == nil {
		fetchers = agent.Fetchers(nil)
	}
	return fetchers(browser.RunsInFirefox(module), origin, document)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) sleep(d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (e *Engine) log() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}

// notes is a fresh pull's or sign-in's notes, whose traces are logged under
// the connector's name.
func (e *Engine) notes(name string) *agent.Notes {
	return &agent.Notes{Log: e.log().With("connector", name)}
}

// session is a sign-in, or a pull that stopped to ask something, at a bill
// provider (module) or a merchant (shop).
//
// The dialog polls a session while its sign-in loop runs in another
// goroutine, and a cancel can arrive while a request is still reading it, so
// the page reading, the loop's status and the credentials are behind mu, the
// browser is behind its Hold, and both go through accessors, never directly.
// The rest belongs to one request at a time.
type session struct {
	id     string
	module billers.Module
	shop   merchants.Module
	// signIn is the module's sign-in pages, nil for a provider signed in to
	// over its API.
	signIn agent.SignInModule
	// name is the catalogue's name for the provider or the merchant.
	name string
	// profile is the connection's profile id, and dir the directory it claimed.
	profile string
	// site is the deployment this connection is at. The module is already
	// aimed at it; this carries it to a reader through billers.Call.
	site string

	// Hold is the browser. The dialog's poll reads it while the loop drives
	// it, which is why it is behind a lock of its own.
	agent.Hold

	mu sync.Mutex
	// fetcher is the module's HTTP caller, kept while a challenge is parked so
	// the rest of the sign-in continues on the same client.
	fetcher      browser.Fetcher
	closeFetcher func()

	notes  *billers.Notes
	kept   billers.Session
	creds  billers.Credentials
	answer func(ctx context.Context, code string, notes *billers.Notes) billers.SignIn
	subs   []billers.Subaccount
	where  billers.State
	pull   *provider.BillPullRequest
	// passwordTried is a pull parked at a code its own kept-password sign-in
	// reached, so the resumed pull does not sign in with it a second time.
	passwordTried bool
	// typed is the password typed into this sign-in. A form that asks again
	// has turned it down, and typing it twice is how a site comes to lock an
	// account; only a person's answer starts a fresh try.
	typed bool
	// browserNoted is the notes having said which browser the sign-in ran in.
	browserNoted bool
	live         bool
	// attended is a person watching this sign-in; view the picture and mouse
	// they act through, made when a page check needs them, and atCheck the
	// sign-in parked on that check.
	attended bool
	view     *browser.LiveView
	atCheck  bool
	// classified is when a live sign-in last classified the page; the status
	// poll throttles classifies against it.
	classified time.Time
	trail      []provider.BillTrailEntry
	// driving is the sign-in loop running in its own goroutine, stepped when
	// it last finished a round, and step the sentence it left for the dialog.
	driving bool
	stepped time.Time
	step    string
	// unfinished reports a sign-in a person started that is shut without
	// landing; nil for every other session. reported says its end has been
	// reported, completed that it landed, and cancelled that the person
	// closed it.
	unfinished                     func()
	reported, completed, cancelled bool
}

func (s *session) reading() (billers.State, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.where, s.classified
}

// mark records what the page last said without touching when it was read: a
// typed connect classifies outside the live view's throttle, and resetting the
// clock there would make the next frame poll re-read a page nothing touched.
func (s *session) mark(where billers.State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.where = where
}

func (s *session) record(where billers.State, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.where, s.classified = where, at
}

func (s *session) drives(at time.Time, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.driving, s.stepped, s.step = true, at, line
}

// says is what the loop is doing now, by the trail's rule. A session nothing
// is driving keeps no sentence.
func (s *session) says(at time.Time, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.driving {
		s.stepped, s.step = at, line
	}
}

func (s *session) landed(where billers.State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.driving, s.step, s.where = false, "", where
}

// busy is whether the loop still holds the session, and what it last said,
// bounded by RoundsBound.
func (s *session) busy(now time.Time) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.driving || now.Sub(s.stepped) > RoundsBound {
		return false, ""
	}
	return true, s.step
}

// login is the credentials the loop types, read under the lock a cancel or a
// finished sign-in clears them under.
func (s *session) login() billers.Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creds
}

func (s *session) setLogin(credentials billers.Credentials) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds = credentials
}

// forgetSecrets drops the credentials once signed in: the profile, or the
// sealed session, is the way back in from now on.
func (s *session) forgetSecrets() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds.Password = ""
	s.creds.Secret = ""
	s.creds.Code = ""
}

func (s *session) passwordTyped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typed
}

// freshTry is a person's answer: the password may be typed once more.
func (s *session) freshTry() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.typed = false
}

func (s *session) markTyped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.typed = true
}

// billSession is a session at a bill provider, not yet registered.
func billSession(module billers.Module, notes *billers.Notes) *session {
	s := &session{id: uuid.NewString(), module: module, name: billerName(module), notes: notes}
	if pages, ok := module.(billers.BrowserModule); ok {
		s.signIn = pages
	}
	return s
}

// shopSession is a session at a merchant, not yet registered: a pull's
// re-sign-in drives the loop on one nobody else can reach.
func shopSession(module merchants.Module, notes *billers.Notes) *session {
	if notes == nil {
		notes = &billers.Notes{}
	}
	return &session{
		id: uuid.NewString(), shop: module, signIn: module, name: merchants.Name(module), notes: notes,
	}
}

func (e *Engine) add(s *session) *session {
	e.sessions.Add(s.id, s, e.now())
	return s
}

// holding is every session driving one connection's profile.
func (e *Engine) holding(profile string) []*session {
	return e.sessions.Where(func(s *session) bool { return s.module != nil && s.profile == profile })
}

// find is a session by id, touched. A session the reaper has taken, or one at
// the other kind of connector, is provider.ErrAgentNotFound, which service
// acts on.
func (e *Engine) find(id string, at func(*session) bool, what string) (*session, error) {
	e.Reap()
	s, held := e.sessions.Find(id, e.now())
	if !held || !at(s) {
		return nil, agentError(provider.ErrAgentNotFound,
			"no such %s; it may have expired — start again", what)
	}
	return s, nil
}

func (e Bills) find(id string) (*session, error) {
	return e.Engine.find(id, func(s *session) bool { return s.module != nil }, "connect")
}

func (e Merchants) find(id string) (*session, error) {
	return e.Engine.find(id, func(s *session) bool { return s.shop != nil }, "sign-in")
}

func (e *Engine) close(s *session) {
	if s == nil {
		return
	}
	e.sessions.Close(s.id, s)
}

// Shut lets go of the browser and the transport, reporting first a sign-in
// that never landed while its page is still there to be seen.
func (s *session) Shut() {
	s.mu.Lock()
	unfinished := s.unfinished
	s.mu.Unlock()
	if unfinished != nil {
		unfinished()
	}
	s.release()
	if s.closeFetcher != nil {
		s.closeFetcher()
	}
}

// release lets a session's browser go without forgetting the session, whose
// failure and trail the dialog is still waiting to read. The transport is left
// alone: a session that has one is answering a challenge.
func (s *session) release() {
	s.closeView()
	var module any = s.module
	if s.shop != nil {
		module = s.shop
	}
	s.Release(func(page browser.Page) { forgetPage(module, page) })
}

// forget is a variable so a test can see which pages were forgotten.
var forget = billers.Forget

// forgetPage drops what this process remembers about a page that is about to
// stop existing: the sign-in helpers' (billers.Forget), and a module's own —
// Costco keeps the refusal it heard on the wire.
func forgetPage(module any, page browser.Page) {
	if page == nil {
		return
	}
	forget(page)
	if forgetful, ok := module.(interface{ Forget(browser.Page) }); ok {
		forgetful.Forget(page)
	}
}

// closeOpened lets go of a browser no session holds.
func closeOpened(module any, opened *OpenBrowser) {
	forgetPage(module, opened.Page)
	if opened.Close != nil {
		opened.Close()
	}
}

// CancelSignIn gives up a sign-in nobody finished. A bill connection's profile
// is claimed for as long as the session lives (two Chromiums on one user data
// directory corrupt it), so without this an abandoned sign-in blocks the next
// until the reaper runs. A challenge a pull parked is refused: closing it
// would throw away a half-done pull.
func (e *Engine) CancelSignIn(ctx context.Context, sessionID string) error {
	s, err := e.find(sessionID, func(*session) bool { return true }, "sign-in")
	if err != nil {
		return err
	}
	if s.pull != nil {
		return agentError(provider.ErrAgentConflict,
			"that sign-in belongs to a pull waiting for a code; answer it instead")
	}
	return e.guard(s.name, "the cancelled sign-in", func() error {
		s.mu.Lock()
		s.cancelled = true
		s.mu.Unlock()
		e.close(s)
		return nil
	})
}

// Reap closes what nobody came back for. Called before every session lookup
// and every minute by serve.
func (e *Engine) Reap() {
	now := e.now()
	e.sessions.Reap(now, func(s *session, now time.Time) bool {
		busy, _ := s.busy(now)
		return busy
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	for ref, held := range e.documents {
		if now.Sub(held.minted) > DocumentTTL {
			delete(e.documents, ref)
		}
	}
}

func (e *Engine) CloseAll() { e.sessions.CloseAll() }

func (e *Engine) Sessions() int { return e.sessions.Len() }

// agentError is a refusal of one of provider's kinds, in words a person reads.
func agentError(kind error, format string, args ...any) error {
	return &provider.AgentError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// --- bills ---------------------------------------------------------------------

func (e Bills) Available() bool { return e.Engine != nil && e.Billers != nil }

func (e Bills) Health(ctx context.Context) error {
	if !e.Available() {
		return provider.ErrBillsAgentUnavailable
	}
	return nil
}

func (e Bills) Providers(ctx context.Context) ([]provider.BillProvider, error) {
	if !e.Available() {
		return nil, provider.ErrBillsAgentUnavailable
	}
	return e.Billers.Providers(), nil
}

// pick refuses an unknown provider as a 400 rather than as a failure.
func (e Bills) pick(id string) (billers.Module, error) {
	module, err := e.Billers.Pick(id)
	if err != nil {
		return nil, agentError(provider.ErrAgentBadRequest, "%v", err)
	}
	return module, nil
}

// open is a session at a bill provider, registered.
func (e Bills) open(module billers.Module) *session {
	return e.add(billSession(module, e.notes(billerName(module))))
}

// fetcher is the HTTP caller a bill module's calls go through, kept on the
// session with its release.
func (e Bills) fetcher(module billers.Module, s *session) error {
	origin, document := "", ""
	if api, ok := module.(billers.APIModule); ok {
		origin, document = api.BrowserOrigin()
	}
	fetcher, release, err := e.caller(module, origin, document)
	if err != nil {
		return err
	}
	s.fetcher, s.closeFetcher = fetcher, release
	return nil
}

// ForgetProfile removes a connection's kept browser. A name that is not a
// profile is not a failure: a disconnect must not be held up by a connection
// that never had a browser.
func (e Bills) ForgetProfile(ctx context.Context, profile string) error {
	err := browser.ForgetProfile(e.ProfilesRoot, profile)
	if err != nil && !errors.Is(err, browser.ErrNotAProfile) {
		return err
	}
	return nil
}

// ReleaseProfile gives up a connection's browser without giving up anything
// else: the sessions this process holds on it (a parked challenge included),
// and the lock and Chromium singleton a restarted process left on the
// profiles volume, which otherwise refuse every later sign-in.
//
// A lock somebody is still refreshing belongs to a running Chromium (the old
// container on a deploy) and is refused: two on one user data directory
// corrupt it.
func (e Bills) ReleaseProfile(
	ctx context.Context, profile string,
) (provider.BillProfileRelease, error) {
	var out provider.BillProfileRelease
	dir, err := browser.ProfileDir(e.ProfilesRoot, profile)
	if err != nil {
		if errors.Is(err, browser.ErrNotAProfile) {
			return out, nil
		}
		return out, err
	}
	for _, s := range e.holding(profile) {
		e.close(s)
		out.Sessions = append(out.Sessions, s.id)
	}
	gave, err := browser.ReleaseAbandonedProfile(dir, e.now())
	if err != nil {
		return out, agentError(provider.ErrAgentConflict,
			"something is still driving this connection's browser — on a deploy, the "+
				"container being replaced. A browser nobody is driving lets go by itself "+
				"a few minutes after it stops; try again then")
	}
	out.Lock, out.Singleton = gave.Lock, gave.Singleton
	return out, nil
}

type heldDocument struct {
	bytes       []byte
	contentType string
	filename    string
	minted      time.Time
}

func (e *Engine) mint(document *billers.Document) *provider.BillDocumentRef {
	ref := uuid.NewString()
	contentType := document.ContentType
	if contentType == "" {
		contentType = "application/pdf"
	}
	filename := document.Filename
	if filename == "" {
		filename = "statement-" + ref
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.documents == nil {
		e.documents = map[string]*heldDocument{}
	}
	e.documents[ref] = &heldDocument{
		bytes: document.Bytes, contentType: contentType, filename: filename, minted: e.now(),
	}
	return &provider.BillDocumentRef{
		Ref: ref, ContentType: contentType, Size: len(document.Bytes), Filename: filename,
	}
}

// FetchDocument trades a pull's reference for the bytes, once.
func (e Bills) FetchDocument(ctx context.Context, ref string) ([]byte, string, string, error) {
	e.Reap()
	e.mu.Lock()
	defer e.mu.Unlock()
	held, ok := e.documents[ref]
	if !ok {
		return nil, "", "", agentError(provider.ErrAgentNotFound,
			"no such document; a ref is good for one read within ten minutes of the pull")
	}
	delete(e.documents, ref)
	return held.bytes, held.contentType, held.filename, nil
}

// --- merchants -----------------------------------------------------------------

func (e Merchants) Available() bool { return e.Engine != nil && e.Merchants != nil }

// Health is per merchant: a Camoufox merchant has no browser without a
// Camoufox server (browser.ErrNoFirefox), whatever Chrome can do.
func (e Merchants) Health(ctx context.Context, merchant domain.MerchantID) error {
	if !e.Available() {
		return provider.ErrMerchantAgentUnavailable
	}
	module, err := e.Merchants.Pick(merchant)
	if err != nil {
		return err
	}
	if browser.RunsInFirefox(module) && e.OpenFirefox == nil {
		return browser.ErrNoFirefox
	}
	return nil
}

// pick refuses an unknown merchant as a 400 rather than as a failure.
func (e Merchants) pick(merchant domain.MerchantID) (merchants.Module, error) {
	if !e.Available() {
		return nil, provider.ErrMerchantAgentUnavailable
	}
	module, err := e.Merchants.Pick(merchant)
	if err != nil {
		return nil, agentError(provider.ErrAgentBadRequest, "%v", err)
	}
	return module, nil
}

// httpFor is the caller a handed-over session's calls go through.
func (e Merchants) httpFor(module merchants.Module) (browser.Fetcher, func(), error) {
	if !browser.RunsInFirefox(module) {
		return e.caller(module, "", "")
	}
	firefox, ok := module.(browser.FirefoxOriginProvider)
	if !ok {
		return nil, nil, browser.ErrNoFirefox
	}
	origin, document := firefox.FirefoxOrigin()
	return e.caller(module, origin, document)
}
