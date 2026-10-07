package billers

import (
	"slices"
	"strings"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

// A module carries a domain.BillerID and reads every fact from
// domain.BillerByID, so what is left to check is coverage: which provider this
// build can actually reach.

// providersWithNoModuleYet is the deliberate list: a catalogued provider with a
// live access path that this build does not carry.
var providersWithNoModuleYet = map[domain.BillerID]string{}

func (r *Registry) Modules() []Module {
	out := make([]Module, 0, len(r.ids))
	for _, id := range r.ids {
		out = append(out, r.byID[id])
	}
	return out
}

func TestEveryModuleNamesACataloguedProviderWithALiveAccessPath(t *testing.T) {
	for _, module := range New().Modules() {
		biller, known := domain.BillerByID(module.ID())
		require.True(t, known, "%s is not in domain.Billers", module.ID())
		require.Contains(t, []domain.BillerAccess{domain.AccessAPI, domain.AccessBrowser},
			biller.Access, "%s has no live access path but has a module", biller.ID)
	}
}

// A steer runs inside the host of the catalogue's Home, so a sign-in on
// another host is one nobody may steer through. A provider deployed per
// customer builds its sign-in from the connection's site instead.
func TestEveryBrowserSignInSitsInsideTheCataloguesHome(t *testing.T) {
	for _, module := range New().Modules() {
		signing, ok := module.(BrowserModule)
		biller, _ := domain.BillerByID(module.ID())
		if !ok || biller.NeedsSite {
			continue
		}
		require.NotEmpty(t, browser.WithinSite(signing.SignInURL(), biller.Home, ""),
			"%s: %s is outside %s", biller.ID, signing.SignInURL(), biller.Home)
	}
}

func TestEveryProviderWithALiveAccessPathHasAModuleOrIsNamedAsWaiting(t *testing.T) {
	registry := New()
	carried := map[domain.BillerID]bool{}
	for _, id := range registry.IDs() {
		carried[id] = true
	}
	for _, biller := range domain.Billers {
		if biller.Access != domain.AccessAPI && biller.Access != domain.AccessBrowser {
			continue
		}
		if carried[biller.ID] {
			continue
		}
		_, waiting := providersWithNoModuleYet[biller.ID]
		require.True(t, waiting,
			"%s is reachable by the catalogue and has no module; add one or name it as waiting", biller.ID)
	}
	// And nothing is named as waiting that is already here.
	for id := range providersWithNoModuleYet {
		require.False(t, carried[id], "%s has a module and is still named as waiting", id)
	}
}

// A `builtin` connection at either of the two browser providers resolves to a
// module with a reader, not to a draft that answers no bills.
func TestSpectrumAndWeEnergiesResolveToModulesThatReadTheirPortals(t *testing.T) {
	registry := New()
	for _, id := range []domain.BillerID{domain.BillerSpectrum, domain.BillerWeEnergies} {
		module, err := registry.Pick(string(id))
		require.NoError(t, err)
		require.Equal(t, id, module.ID())
		_, isBrowser := module.(BrowserModule)
		require.True(t, isBrowser, "%s is signed in to in a page", id)
		// A draft answers a pull with no bills and a note saying so; these two
		// have their own FetchBills, and a page is what they want.
		result, err := module.FetchBills(Call{Notes: &Notes{}})
		require.NoError(t, err)
		require.True(t, result.NeedsSignIn)
		require.Contains(t, result.Reason, "kept browser profile")
	}
}

// modulesWithNoStatement is the deliberate list: a module past its draft whose
// provider has no statement to fetch, with the reason.
var modulesWithNoStatement = map[domain.BillerID]string{
	domain.BillerNorthwesternMutual: "the portal offers no bill or statement document, only payment activity",
}

// HasDocuments is the only thing that makes a pull call FetchDocument, so a
// written reader behind a false flag is never asked and the flag can never be
// earned by a pull. A draft, known by the note its pull answers, has no reader
// and claims neither a statement nor an autopay date; every other module says
// it has a statement, or is named above.
func TestAModuleWithAStatementReaderSaysSo(t *testing.T) {
	for _, module := range New().Modules() {
		biller, _ := domain.BillerByID(module.ID())
		notes := &Notes{}
		if _, err := module.FetchBills(Call{Notes: notes}); err == nil &&
			slices.ContainsFunc(notes.List(), func(note string) bool { return strings.Contains(note, "is a draft") }) {
			require.False(t, biller.HasDocuments, "%s is a draft", biller.ID)
			require.False(t, biller.ReportsAutopay, "%s is a draft", biller.ID)
			continue
		}
		if _, none := modulesWithNoStatement[biller.ID]; none {
			require.False(t, biller.HasDocuments, "%s is named as having no statement", biller.ID)
			continue
		}
		require.True(t, biller.HasDocuments,
			"%s has a reader and HasDocuments is false, so no pull will ever ask it for a statement", biller.ID)
	}
}

func TestEveryModuleIsOneOfTheTwoKinds(t *testing.T) {
	for _, module := range New().Modules() {
		biller, _ := domain.BillerByID(module.ID())
		switch biller.Access {
		case domain.AccessAPI:
			api, ok := module.(APIModule)
			require.True(t, ok, "%s is an api provider and is not an APIModule", biller.ID)
			require.NotEmpty(t, api.SessionKinds(),
				"%s keeps a token and names no session kind, so a token from elsewhere would be taken", biller.ID)
		case domain.AccessBrowser:
			_, ok := module.(BrowserModule)
			require.True(t, ok, "%s is a browser provider and is not a BrowserModule", biller.ID)
		}
	}
}

func TestTheCatalogueAnswerIsTheProvidersOwnFacts(t *testing.T) {
	providers := New().Providers()
	require.NotEmpty(t, providers)

	// Alliant first, as the catalogue orders it.
	require.Equal(t, "alliant", providers[0].ID)
	require.Equal(t, "Alliant Energy", providers[0].Name)
	require.Equal(t, "api", providers[0].Access)
	require.Equal(t, []string{"typed"}, providers[0].SignIn.Kinds,
		"an api provider has no page to show, so typed is all there is")
	require.Equal(t, "Sign in with the username and password you use at myaccount.alliantenergy.com.",
		providers[0].SignIn.Prompt)
	require.Empty(t, providers[0].Challenges)
	require.True(t, providers[0].HasDocuments)
	require.True(t, providers[0].ReportsAutopay)
	require.Equal(t, 0, providers[0].KeepaliveDays)
	require.True(t, providers[0].SessionPersists)

	// The Chrome providers list the live view beside the typed form, for the
	// development routes that write a module. A Camoufox provider — Community
	// Connect — has no live view, so it offers the typed form alone.
	for _, one := range providers {
		if one.Access != "browser" {
			continue
		}
		if one.ID == string(domain.BillerCommunityConnect) {
			require.Equal(t, []string{"typed"}, one.SignIn.Kinds)
		} else {
			require.Equal(t, []string{"live", "typed"}, one.SignIn.Kinds)
		}
		require.NotEmpty(t, one.SignIn.Prompt)
	}

	// Erie's second factors come from the catalogue, not from the module.
	found := false
	for _, one := range providers {
		if one.ID != "erie" {
			continue
		}
		found = true
		require.Equal(t, []string{"totp", "sms"}, one.Challenges)
		require.Equal(t, 7, one.KeepaliveDays)
	}
	require.True(t, found)
}

func TestAProviderIsNamedOrTheRequestIsRefusedAndThereIsNoDefault(t *testing.T) {
	registry := New()
	module, err := registry.Pick("alliant")
	require.NoError(t, err)
	require.Equal(t, domain.BillerAlliant, module.ID())

	// Case and space are forgiven; a guess is not.
	module, err = registry.Pick("  ALLIANT ")
	require.NoError(t, err)
	require.Equal(t, domain.BillerAlliant, module.ID())

	_, err = registry.Pick("")
	require.ErrorContains(t, err, "a provider is required")
	_, err = registry.Pick("nowhere")
	require.ErrorContains(t, err, `unknown provider "nowhere"`)
	// A provider taken out of the catalogue is refused by name like any other
	// unknown one: a connection still naming it fails its pull in words.
	_, err = registry.Pick("citi")
	require.ErrorContains(t, err, `unknown provider "citi"`)
	require.ErrorContains(t, err, "known: alliant")
}
