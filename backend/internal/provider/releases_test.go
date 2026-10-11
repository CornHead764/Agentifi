package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheNewestReleaseIsTheHighestVersionTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/CornHead764/Agentifi/tags", r.URL.Path)
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		// GitHub lists tags by name, so 0.10.0 sorts before 0.9.0.
		fmt.Fprint(w, `[{"name":"v0.10.0"},{"name":"v0.9.0"},{"name":"v1.0.0-rc.1"},`+
			`{"name":"nightly"},{"name":"0.11.0"},{"name":"v0.2.0"}]`)
	}))
	defer server.Close()

	release, err := (&GitHubReleases{BaseURL: server.URL, HTTPClient: server.Client()}).
		Latest(context.Background())
	require.NoError(t, err)
	require.Equal(t, Release{
		Version: "v0.10.0",
		URL:     "https://github.com/CornHead764/Agentifi/releases/tag/v0.10.0",
	}, release)
}

func TestARepositoryWithoutReleaseTagsHasNoLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"nightly"}]`)
	}))
	defer server.Close()

	_, err := (&GitHubReleases{BaseURL: server.URL, HTTPClient: server.Client()}).
		Latest(context.Background())
	require.ErrorContains(t, err, "no release tag")
}

func TestAThrottledTagListIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer server.Close()

	_, err := (&GitHubReleases{BaseURL: server.URL, HTTPClient: server.Client()}).
		Latest(context.Background())
	require.ErrorContains(t, err, "rate limit")
}
