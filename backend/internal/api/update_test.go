package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

type fakeReleases struct {
	release provider.Release
	err     error
	calls   int
}

func (f *fakeReleases) Latest(context.Context) (provider.Release, error) {
	f.calls++
	return f.release, f.err
}

func updateClient(t *testing.T, enabled bool, releases *fakeReleases, now *time.Time) *client {
	t.Helper()
	cfg := testConfig()
	cfg.UpdateCheckEnabled = enabled
	env := NewEnv(cfg, db(t), WithReleases(releases), WithClock(func() time.Time { return *now }))
	env.Version = "0.2.0"
	c := &client{t: t, env: env, handler: RouterFor(env)}
	return c.as(makeAdmin(t))
}

func TestTheUpdateCheckNamesANewerRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	releases := &fakeReleases{release: provider.Release{
		Version: "v0.3.0", URL: "https://github.com/CornHead764/Agentifi/releases/tag/v0.3.0"}}
	c := updateClient(t, true, releases, &now)

	body := c.get("/admin/server/update").requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["enabled"])
	require.Equal(t, "0.2.0", body["current_version"])
	require.Equal(t, "v0.3.0", body["latest_version"])
	require.Equal(t, true, body["update_available"])
	require.Equal(t, releases.release.URL, body["url"])
	require.Equal(t, "", body["error"])
}

func TestTheUpdateCheckAsksAgainOnlyWhenStaleOrToldTo(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	releases := &fakeReleases{release: provider.Release{Version: "v0.2.0"}}
	c := updateClient(t, true, releases, &now)

	body := c.get("/admin/server/update").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["update_available"])
	now = now.Add(time.Hour)
	c.get("/admin/server/update").requireStatus(http.StatusOK)
	require.Equal(t, 1, releases.calls)

	c.get("/admin/server/update?refresh=1").requireStatus(http.StatusOK)
	require.Equal(t, 2, releases.calls)

	now = now.Add(updateCheckEvery)
	c.get("/admin/server/update").requireStatus(http.StatusOK)
	require.Equal(t, 3, releases.calls)
}

func TestAFailedUpdateCheckKeepsTheLastAnswerAndSaysWhy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	releases := &fakeReleases{release: provider.Release{Version: "v0.3.0"}}
	c := updateClient(t, true, releases, &now)
	c.get("/admin/server/update").requireStatus(http.StatusOK)

	releases.err = errors.New("releases: rate limit exceeded")
	body := c.get("/admin/server/update?refresh=1").requireStatus(http.StatusOK).json()
	require.Equal(t, "releases: rate limit exceeded", body["error"])
	require.Equal(t, "v0.3.0", body["latest_version"])
	require.Equal(t, true, body["update_available"])
}

func TestATurnedOffUpdateCheckAsksNobody(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	releases := &fakeReleases{release: provider.Release{Version: "v0.3.0"}}
	c := updateClient(t, false, releases, &now)

	body := c.get("/admin/server/update?refresh=1").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["enabled"])
	require.Equal(t, false, body["update_available"])
	require.Nil(t, body["checked_at"])
	require.Zero(t, releases.calls)
}
