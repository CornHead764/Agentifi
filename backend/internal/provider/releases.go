package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// A release is a signed vX.Y.Z tag; not every tag has a GitHub Release, so
// the newest release is read from the tags, not from /releases/latest.

const (
	releasesRepository = "CornHead764/Agentifi"
	githubAPIBaseURL   = "https://api.github.com"
	releasesTimeout    = 15 * time.Second
	// releasesPageSize is the most tags one page answers with; the newest
	// release is among them for as long as fewer tags than this follow it.
	releasesPageSize = 100
)

// Release is the newest published version of Agentifi.
type Release struct {
	// Version is the tag, "v0.2.0".
	Version string
	// URL is the page that describes it.
	URL string
}

type ReleaseSource interface {
	Latest(ctx context.Context) (Release, error)
}

// GitHubReleases reads the repository's tags anonymously.
type GitHubReleases struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (g *GitHubReleases) Latest(ctx context.Context) (Release, error) {
	base := strings.TrimRight(g.BaseURL, "/")
	if base == "" {
		base = githubAPIBaseURL
	}
	endpoint := fmt.Sprintf("%s/repos/%s/tags?per_page=%d", base, releasesRepository, releasesPageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("releases: cannot build the request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentifi-update-check")

	client := g.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: releasesTimeout}
	}
	resp, err := httpx.Read(client, req, 0)
	if err != nil {
		return Release{}, fmt.Errorf("releases: request failed: %w", err)
	}
	if !resp.OK() {
		return Release{}, fmt.Errorf("releases: %w", resp.Err())
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(resp.Body, &tags); err != nil {
		return Release{}, fmt.Errorf("releases: the tag list is not JSON: %w", err)
	}

	var (
		newest domain.Version
		tag    string
	)
	for _, one := range tags {
		if !strings.HasPrefix(one.Name, "v") {
			continue
		}
		version, ok := domain.ParseVersion(one.Name)
		if !ok || version.PreRelease != "" {
			continue
		}
		if tag == "" || version.Compare(newest) > 0 {
			newest, tag = version, one.Name
		}
	}
	if tag == "" {
		return Release{}, errors.New("releases: the repository has no release tag")
	}
	return Release{
		Version: tag,
		URL: fmt.Sprintf("https://github.com/%s/releases/tag/%s",
			releasesRepository, url.PathEscape(tag)),
	}, nil
}
