package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type releaseRoundTripper func(*http.Request) (*http.Response, error)

func (fn releaseRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func releaseHTTPResponse(request *http.Request, status int, headers http.Header, body []byte) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        headers,
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func newGitHubReleaseTestFetcher(t *testing.T, transport http.RoundTripper) *HTTPFetcher {
	t.Helper()
	fetcher, err := NewHTTPFetcher(HTTPFetcherOptions{
		AllowedHosts: []string{"api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"},
		MaxBytes:     1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher.client.Transport = transport
	return fetcher
}

func TestDiscoverGitHubReleaseMetadataUsesBoundedPaginationAndMinimumVersion(t *testing.T) {
	metadata := []byte("{\"schema_version\":\"1.2.0\",\"component_id\":\"backend\"}")
	digest := sha256.Sum256(metadata)
	digestText := hex.EncodeToString(digest[:])
	commit := strings.Repeat("a", 40)
	assetURI := "https://github.com/example/backend/releases/download/v1.2.1/backend-1.2.1.json"

	releases := []map[string]any{
		{
			"id": 41, "url": "https://api.github.com/repos/example/backend/releases/41",
			"tag_name": "v1.0.0", "draft": false, "prerelease": false,
			"published_at": "2026-01-01T00:00:00Z", "assets": []any{},
		},
		{
			"id": 42, "url": "https://api.github.com/repos/example/backend/releases/42",
			"tag_name": "v1.2.1", "draft": false, "prerelease": false,
			"published_at": "2026-11-10T12:00:00Z",
			"assets": []map[string]any{{
				"name": "backend-1.2.1.json", "size": len(metadata),
				"digest": "sha256:" + digestText, "browser_download_url": assetURI,
			}},
		},
	}
	releaseBody, err := json.Marshal(releases)
	if err != nil {
		t.Fatal(err)
	}
	tagBody := []byte(fmt.Sprintf("{\"object\":{\"sha\":%q,\"type\":\"commit\"}}", commit))
	var requested []string
	transport := releaseRoundTripper(func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.String())
		switch {
		case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/example/backend/releases":
			if request.URL.Query().Get("per_page") != "100" || request.URL.Query().Get("page") != "1" {
				t.Fatalf("unexpected pagination query: %s", request.URL.RawQuery)
			}
			return releaseHTTPResponse(request, http.StatusOK, nil, releaseBody), nil
		case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/example/backend/git/ref/tags/v1.2.1":
			return releaseHTTPResponse(request, http.StatusOK, nil, tagBody), nil
		case request.URL.Host == "github.com" && request.URL.Path == "/example/backend/releases/download/v1.2.1/backend-1.2.1.json":
			return releaseHTTPResponse(request, http.StatusOK, nil, metadata), nil
		default:
			t.Fatalf("unexpected request: %s", request.URL.String())
			return nil, fmt.Errorf("unexpected request")
		}
	})
	fetcher := newGitHubReleaseTestFetcher(t, transport)
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
		Variant: "default", TagPrefix: "v", MinimumVersion: "1.2.1",
	}
	result, err := fetcher.DiscoverGitHubReleases(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("discovered %d Releases, want 1 (older Releases are below the migration floor)", len(result))
	}
	if result[0].Discovery.ReleaseID != 42 || result[0].Discovery.TagCommit != commit {
		t.Fatalf("unexpected immutable Release identity: %#v", result[0].Discovery)
	}
	if string(result[0].Payload) != string(metadata) || result[0].MetadataSHA != digestText {
		t.Fatal("metadata payload or digest was not preserved")
	}
	if len(requested) != 3 {
		t.Fatalf("made %d requests, want Releases list + tag ref + metadata asset: %#v", len(requested), requested)
	}
}

func TestDiscoverGitHubReleasesRetriesBoundedRateLimit(t *testing.T) {
	attempts := 0
	transport := releaseRoundTripper(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			headers := make(http.Header)
			headers.Set("X-RateLimit-Remaining", "0")
			headers.Set("Retry-After", "0")
			return releaseHTTPResponse(request, http.StatusForbidden, headers, []byte("{\"message\":\"rate limited\"}")), nil
		}
		return releaseHTTPResponse(request, http.StatusOK, nil, []byte("[]")), nil
	})
	fetcher := newGitHubReleaseTestFetcher(t, transport)
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
	}
	result, err := fetcher.DiscoverGitHubReleases(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("got %d Releases, want zero", len(result))
	}
	if attempts != 2 {
		t.Fatalf("GitHub API attempts = %d, want 2", attempts)
	}
}

func TestDiscoverGitHubReleasesFailsWhenPageBudgetIsIncomplete(t *testing.T) {
	body, err := json.Marshal(make([]githubReleaseRecord, githubReleasePageSize))
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	transport := releaseRoundTripper(func(request *http.Request) (*http.Response, error) {
		pages++
		return releaseHTTPResponse(request, http.StatusOK, nil, body), nil
	})
	fetcher := newGitHubReleaseTestFetcher(t, transport)
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
	}
	_, err = fetcher.DiscoverGitHubReleases(context.Background(), source)
	if err == nil || !strings.Contains(err.Error(), "RELEASE_DISCOVERY_LIMIT") {
		t.Fatalf("expected incomplete pagination failure, got %v", err)
	}
	if pages != maxGitHubReleasePages {
		t.Fatalf("pages fetched = %d, want exactly %d", pages, maxGitHubReleasePages)
	}
}
