package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// fakeSite stands in for Camoufox on a site: every lookup opens a surface
// whose page answers the in-page fetch from handler, following redirects the
// way a browser's fetch does, and answers document.cookie from cookie.
type fakeSite struct {
	handler http.Handler
	cookie  string

	opened, closed int
	origins        []string
}

func (s *fakeSite) open(origin, document string) (browser.FetchSurface, error) {
	s.opened++
	s.origins = append(s.origins, origin+document)
	page := &browser.StubPage{Location: origin + document}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if strings.Contains(script, "document.cookie") {
			return s.cookie, nil
		}
		shaped := arg.(map[string]any)
		return s.serve(shaped)
	}
	return browser.FetchSurface{Page: page, Close: func() error { s.closed++; return nil }}, nil
}

func (s *fakeSite) serve(shaped map[string]any) (any, error) {
	address, _ := shaped["url"].(string)
	method, _ := shaped["method"].(string)
	body, _ := shaped["body"].(string)
	for range 5 {
		req := httptest.NewRequest(method, address, strings.NewReader(body))
		if headers, ok := shaped["headers"].(map[string]any); ok {
			for name, value := range headers {
				req.Header.Set(name, value.(string))
			}
		}
		recorder := httptest.NewRecorder()
		s.handler.ServeHTTP(recorder, req)
		if location := recorder.Header().Get("Location"); recorder.Code/100 == 3 && location != "" {
			next, err := req.URL.Parse(location)
			if err != nil {
				return nil, err
			}
			address, method, body = next.String(), http.MethodGet, ""
			continue
		}
		return map[string]any{
			"status":  float64(recorder.Code),
			"url":     address,
			"headers": map[string]any{"content-type": recorder.Header().Get("Content-Type")},
			"base64":  base64.StdEncoding.EncodeToString(recorder.Body.Bytes()),
		}, nil
	}
	return nil, errors.New("fake site: too many redirects")
}

func TestValuationAmountReadsTheLiteralAndRefusesNothing(t *testing.T) {
	value, ok := valuationAmount(json.Number("412345.67"))
	require.True(t, ok)
	require.Equal(t, "412345.67", value.String())

	for _, hollow := range []any{nil, json.Number(""), json.Number("0"), json.Number("-5"), "abc", 12.5} {
		_, ok := valuationAmount(hollow)
		require.False(t, ok, "%#v", hollow)
	}
}

func TestWithoutCamoufoxThereAreNoValuers(t *testing.T) {
	require.Empty(t, Valuers(nil))

	site := &fakeSite{}
	valuers := Valuers(site.open)
	require.Equal(t, "zillow", valuers[AssetTypeRealEstate].Name())
	require.Equal(t, "kbb", valuers[AssetTypeVehicle].Name())
	require.Zero(t, site.opened, "indexing the providers opens no browser")
}
