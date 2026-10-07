package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// The tests that need a real Chromium are guarded by an environment variable
// rather than skipped on a missing browser: the image carries no browser, so
// a test that skips itself when one is absent would pass without exercising
// anything.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/
//
// It needs the Playwright driver (scripts/playwright-driver.sh) and Google
// Chrome at AGENTIFI_CHROME_PATH, as scripts/install-chrome.sh installs it.
func TestEngineLaunches(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })

	page, err := OpenPage(context)
	require.NoError(t, err)
	_, err = page.Goto("about:blank", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	require.NoError(t, err)

	shot, err := Screenshot(page)
	require.NoError(t, err)
	require.NotEmpty(t, shot)
	require.Equal(t, []byte("\x89PNG"), shot[:4])
}

// The persistent profile, which is the thing the bills bridge actually runs on:
// it opens on a directory of its own, records its device there, and the
// directory holds a Chromium profile afterwards.
func TestEngineOpensAProfileAndRecordsItsDevice(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	root := t.TempDir()
	dir, err := ProfileDir(root, "connection-1")
	require.NoError(t, err)
	device, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.NoError(t, engine.Claim(dir))

	context, err := engine.OpenProfile(dir, device)
	require.NoError(t, err)
	page, err := OpenPage(context)
	require.NoError(t, err)
	_, err = page.Goto("about:blank", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	require.NoError(t, err)
	require.NoError(t, context.Close())
	engine.Release(dir)

	require.True(t, ProfileExists(dir))
	require.FileExists(t, filepath.Join(dir, DeviceFile))
}

// What Chromium actually leaves in a user data directory: the singleton while
// it runs, nothing once it has exited cleanly. This keeps singletonFiles
// honest: a name Chromium started writing that it does not list would be left
// behind on the next deploy and refuse every sign-in afterwards.
func TestAProfileCarriesChromiumsSingletonWhileItRuns(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	dir, err := ProfileDir(t.TempDir(), "connection-1")
	require.NoError(t, err)
	device, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)
	require.NoError(t, engine.Claim(dir))

	context, err := engine.OpenProfile(dir, device)
	require.NoError(t, err)
	for _, name := range singletonFiles {
		target, err := os.Readlink(filepath.Join(dir, name))
		require.NoError(t, err, "%s is not the symlink the singleton is", name)
		require.NotEmpty(t, target)
	}

	require.NoError(t, context.Close())
	engine.Release(dir)

	for _, name := range singletonFiles {
		_, err := os.Lstat(filepath.Join(dir, name))
		require.ErrorIs(t, err, os.ErrNotExist, "%s outlived a clean exit", name)
	}
}

// A profile directory carrying a singleton from a container that died, opened
// by a Chromium on a host of another name. Without the clearing Chromium
// refuses the user data directory outright and Playwright fails the launch.
func TestAProfileOrphanedByADeployStillOpens(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	dir, err := ProfileDir(t.TempDir(), "connection-1")
	require.NoError(t, err)
	device, err := RecordedDevice(dir, DefaultDevice(DefaultViewport, testUserAgent))
	require.NoError(t, err)

	// The container that died, and the lock it left behind with it.
	require.NoError(t, os.Symlink("abcdef012345-4242", filepath.Join(dir, "SingletonLock")))
	require.NoError(t, os.Symlink("1234567890123456789", filepath.Join(dir, "SingletonCookie")))
	require.NoError(t, os.Symlink("/tmp/org.chromium.Chromium.invented/SingletonSocket",
		filepath.Join(dir, "SingletonSocket")))
	orphaned, err := TakeProfileLock(dir, LockHolder, time.Now().Add(-LockStale-time.Minute))
	require.NoError(t, err)
	require.NotEmpty(t, orphaned)

	require.NoError(t, engine.Claim(dir))
	context, err := engine.OpenProfile(dir, device)
	require.NoError(t, err)
	page, err := OpenPage(context)
	require.NoError(t, err)
	_, err = page.Goto("about:blank", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	require.NoError(t, err)
	require.NoError(t, context.Close())
	engine.Release(dir)
}
