package billers

import (
	"fmt"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The bill providers this engine knows, in the catalogue's display order.

type Registry struct {
	byID map[domain.BillerID]Module
	ids  []domain.BillerID
}

// New is the registry as the application runs with it. A browser module with
// no reader yet is a Draft (see draft.go).
func New() *Registry {
	return NewWith(
		NewAlliant(),
		NewSpectrum(),
		NewWeEnergies(),
		NewErie(),
		NewTMobile(),
		NewRsyncNet(),
		// Carried unaimed; the engine aims a copy at the connection's
		// deployment. See SiteModule.
		NewCommunityConnect(),
		NewNorthwesternMutual(),
		NewTruGreen(),
		NewMyChart(),
	)
}

// NewWith is a registry of exactly these modules, for a test.
func NewWith(modules ...Module) *Registry {
	registry := &Registry{byID: map[domain.BillerID]Module{}}
	for _, module := range modules {
		registry.byID[module.ID()] = module
	}
	// Display order is the catalogue's, so this list and the frontend's read
	// the same. A module the catalogue does not carry (a test's) goes last
	// rather than missing.
	listed := map[domain.BillerID]bool{}
	for _, biller := range domain.Billers {
		if _, held := registry.byID[biller.ID]; held {
			registry.ids = append(registry.ids, biller.ID)
			listed[biller.ID] = true
		}
	}
	for _, module := range modules {
		if !listed[module.ID()] {
			registry.ids = append(registry.ids, module.ID())
			listed[module.ID()] = true
		}
	}
	return registry
}

// ErrUnknownProvider is a request that named a provider this build has no
// module for.
type ErrUnknownProvider struct {
	ID    string
	Known []domain.BillerID
}

func (e *ErrUnknownProvider) Error() string {
	known := make([]string, 0, len(e.Known))
	for _, one := range e.Known {
		known = append(known, string(one))
	}
	if e.ID == "" {
		return "a provider is required; known: " + strings.Join(known, ", ")
	}
	return fmt.Sprintf("unknown provider %q; known: %s", e.ID, strings.Join(known, ", "))
}

// Pick is the provider a request named. There is no default, unlike the
// merchants': a pull that guessed would sign in to the wrong provider.
func (r *Registry) Pick(id string) (Module, error) {
	wanted := domain.BillerID(strings.ToLower(strings.TrimSpace(id)))
	if module, held := r.byID[wanted]; held && wanted != "" {
		return module, nil
	}
	return nil, &ErrUnknownProvider{ID: string(wanted), Known: r.ids}
}

// IDs are the providers this build carries, in display order.
func (r *Registry) IDs() []domain.BillerID { return append([]domain.BillerID(nil), r.ids...) }

// Providers is the catalogue answer the connect dialog reads: what this build
// can reach today, beside what the application knows about each provider.
func (r *Registry) Providers() []provider.BillProvider {
	out := make([]provider.BillProvider, 0, len(r.ids))
	for _, id := range r.ids {
		biller, known := domain.BillerByID(id)
		if !known {
			continue
		}
		module := r.byID[id]
		entry := provider.BillProvider{
			ID: string(biller.ID), Name: biller.Name, Home: biller.Home,
			Access:          string(biller.Access),
			SignIn:          provider.BillProviderSign{Kinds: signInKinds(module)},
			SessionPersists: sessionPersists(module),
			KeepaliveDays:   biller.KeepaliveDays,
			ReportsAutopay:  biller.ReportsAutopay,
			HasDocuments:    biller.HasDocuments,
			Challenges:      make([]string, 0, len(biller.Challenges)),
		}
		for _, challenge := range biller.Challenges {
			entry.Challenges = append(entry.Challenges, string(challenge))
		}
		if browserModule, ok := module.(BrowserModule); ok {
			entry.SignIn.Prompt = browserModule.SignInPrompt()
		} else {
			entry.SignIn.Prompt = apiSignInPrompt(biller)
		}
		out = append(out, entry)
	}
	return out
}

// signInKinds: every provider signs in through the typed form. A Chrome
// provider also lists "live", the live view the development routes open for
// writing a module; the settings page never offers it. API and Camoufox
// providers have no live view.
func signInKinds(module Module) []string {
	if _, ok := module.(BrowserModule); ok && !browser.RunsInFirefox(module) {
		return []string{"live", "typed"}
	}
	return []string{"typed"}
}

// apiSignInPrompt derives from the catalogue's home, so a provider whose
// address changes says the new one.
func apiSignInPrompt(biller domain.Biller) string {
	host := strings.TrimPrefix(strings.TrimPrefix(biller.Home, "https://"), "http://")
	return "Sign in with the username and password you use at " + host + "."
}

// sessionPersists is true for every provider: none forgets its session
// between pulls, which the connect dialog shows.
func sessionPersists(Module) bool { return true }
