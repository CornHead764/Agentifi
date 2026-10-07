package browser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testOrigin = "package google-chrome-stable 120.0.1.0-1\n" +
	"from    https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb\n" +
	"sha256  0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"

// installedChrome lays a directory out as scripts/install-chrome.sh does:
// <root>/<version>/chrome beside its ORIGIN, and <root>/current pointing at
// the version.
func installedChrome(t *testing.T, origin string) string {
	t.Helper()
	root := t.TempDir()
	version := filepath.Join(root, "120.0.1.0-1")
	require.NoError(t, os.Mkdir(version, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(version, "chrome"), []byte("#!/bin/sh\n"), 0o755))
	if origin != "" {
		require.NoError(t, os.WriteFile(filepath.Join(version, "ORIGIN"), []byte(origin), 0o644))
	}
	require.NoError(t, os.Symlink("120.0.1.0-1", filepath.Join(root, "current")))
	return root
}

func TestFindChromeFollowsTheCurrentLinkAndReadsWhereItCameFrom(t *testing.T) {
	root := installedChrome(t, testOrigin)
	path := filepath.Join(root, "current", "chrome")

	chrome, err := FindChrome(path)

	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(filepath.Join(root, "120.0.1.0-1", "chrome"))
	require.NoError(t, err)
	require.Equal(t, path, chrome.Path)
	require.Equal(t, want, chrome.Resolved)
	require.Equal(t, "package google-chrome-stable 120.0.1.0-1\n"+
		"from    https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_120.0.1.0-1_amd64.deb\n"+
		"sha256  0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", chrome.Origin)
}

func TestFindChromeAcceptsAChromeInstalledSomeOtherWay(t *testing.T) {
	root := installedChrome(t, "")

	chrome, err := FindChrome(filepath.Join(root, "current", "chrome"))

	require.NoError(t, err)
	require.Empty(t, chrome.Origin)
}

// A Chrome that is not there is an error that says how to get one, never a
// quiet launch of Playwright's Chromium.
func TestFindChromeRefusesWhatIsNotAnInstalledChrome(t *testing.T) {
	root := t.TempDir()
	notExecutable := filepath.Join(root, "not-executable")
	require.NoError(t, os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o644))
	dangling := filepath.Join(root, "current")
	require.NoError(t, os.Symlink("119.0.1.0-1", dangling))

	for name, path := range map[string]string{
		"missing":        filepath.Join(root, "chrome"),
		"dangling link":  filepath.Join(dangling, "chrome"),
		"not executable": notExecutable,
		"a directory":    root,
		"no path":        "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FindChrome(path)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrNoChrome), "got %v", err)
			require.Contains(t, err.Error(), "docker compose logs chrome")
			require.Contains(t, err.Error(), "AGENTIFI_CHROME_PATH")
			if path != "" {
				require.Contains(t, err.Error(), path)
			}
		})
	}
}

// Nothing is launched for a missing Chrome, so the error arrives before the
// Playwright driver is ever started.
func TestTheEngineRefusesToLaunchWithoutChrome(t *testing.T) {
	engine := &Engine{ChromePath: filepath.Join(t.TempDir(), "chrome"), Headless: true}

	_, err := engine.Browser()
	require.ErrorIs(t, err, ErrNoChrome)
	_, err = engine.OpenProfile(t.TempDir(), DefaultDevice(DefaultViewport, testUserAgent))
	require.ErrorIs(t, err, ErrNoChrome)
	require.Nil(t, engine.pw)
}
