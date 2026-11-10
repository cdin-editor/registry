package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type githubRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn githubRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func githubResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        headers,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func githubTestFetcher(t *testing.T, options HTTPFetcherOptions, transport http.RoundTripper) *HTTPFetcher {
	t.Helper()
	fetcher, err := NewHTTPFetcher(options)
	if err != nil {
		t.Fatal(err)
	}
	if transport != nil {
		fetcher.client.Transport = transport
	}
	return fetcher
}

func TestNewHTTPFetcherRequiresExplicitGitHubAllowList(t *testing.T) {
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"example.invalid"}}, nil)
	if err := fetcher.validateURL(mustURL(t, "https://api.github.com/repos/acme/engine/releases")); err == nil {
		t.Fatal("GitHub API was implicitly allow-listed without --allow-host github.com")
	}
	if _, err := NewHTTPFetcher(HTTPFetcherOptions{}); err == nil {
		t.Fatal("empty explicit host allow-list was accepted")
	}
}

func TestHTTPFetcherListsGitHubReleasePages(t *testing.T) {
	releasesJSON := `[{"id":17,"tag_name":"v1.2.3","draft":false,"prerelease":false,"published_at":"2026-11-10T12:00:00Z","url":"https://api.github.com/repos/acme/engine/releases/17","html_url":"https://github.com/acme/engine/releases/tag/v1.2.3","assets":[]}]
`
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.github.com" || request.URL.Path != "/repos/acme/engine/releases" {
			return nil, fmt.Errorf("unexpected GitHub API request %s", request.URL)
		}
		if request.URL.Query().Get("page") != "2" || request.URL.Query().Get("per_page") != "100" {
			return nil, fmt.Errorf("unexpected pagination query: %s", request.URL.RawQuery)
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" {
			return nil, fmt.Errorf("missing GitHub API media type")
		}
		return githubResponse(http.StatusOK, nil, releasesJSON), nil
	}))
	releases, err := fetcher.ListGitHubReleases(context.Background(), ReleaseSource{
		SourceID: "engine", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default", TagPrefix: "v",
	}, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].ID != 17 || releases[0].TagName != "v1.2.3" {
		t.Fatalf("unexpected GitHub releases: %#v", releases)
	}
	if _, err := fetcher.ListGitHubReleases(context.Background(), ReleaseSource{RepositoryURI: "https://github.com/acme/engine"}, maxGitHubReleasePages+1, 100); err == nil {
		t.Fatal("out-of-range page was accepted")
	}
}

func TestHTTPFetcherResolvesAnnotatedGitHubTagToCommit(t *testing.T) {
	tagObject := strings.Repeat("a", 40)
	commit := strings.Repeat("b", 40)
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/acme/engine/git/ref/tags/v1.2.3":
			return githubResponse(http.StatusOK, nil, fmt.Sprintf(`{"object":{"type":"tag","sha":%q}}`, tagObject)), nil
		case "/repos/acme/engine/git/tags/" + tagObject:
			return githubResponse(http.StatusOK, nil, fmt.Sprintf(`{"object":{"type":"commit","sha":%q}}`, commit)), nil
		default:
			return nil, fmt.Errorf("unexpected GitHub API request %s", request.URL.Path)
		}
	}))
	got, err := fetcher.ResolveGitHubTagCommit(context.Background(), ReleaseSource{
		SourceID: "engine", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/engine",
	}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got != commit {
		t.Fatalf("resolved commit = %q, want %q", got, commit)
	}
}

func TestHTTPFetcherFailsClosedOnGitHubRateLimit(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-RateLimit-Remaining", "0")
	requests := 0
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return githubResponse(http.StatusForbidden, headers, `{"message":"API rate limit exceeded"}`), nil
	}))
	_, err := fetcher.ListGitHubReleases(context.Background(), ReleaseSource{
		SourceID: "engine", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/engine",
	}, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_RATE_LIMIT_EXHAUSTED") {
		t.Fatalf("error = %v, want explicit GitHub rate-limit exhaustion", err)
	}
	if requests != 3 {
		t.Fatalf("rate-limited GitHub API attempts = %d, want bounded retry count 3", requests)
	}
}

func TestHTTPFetcherRetriesTransientGitHubAPIError(t *testing.T) {
	requests := 0
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return githubResponse(http.StatusServiceUnavailable, nil, `{"message":"temporarily unavailable"}`), nil
		}
		return githubResponse(http.StatusOK, nil, "[]"), nil
	}))
	releases, err := fetcher.ListGitHubReleases(context.Background(), ReleaseSource{
		SourceID: "engine", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/engine",
	}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 || requests != 2 {
		t.Fatalf("releases=%v, API attempts=%d; want empty inventory after one retry", releases, requests)
	}
}

func TestGitHubAPISnapshotSupportsExactOfflineReplay(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default", TagPrefix: "v",
	}
	payload := `[]
`
	onlineCalls := 0
	online := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		onlineCalls++
		return githubResponse(http.StatusOK, nil, payload), nil
	}))
	if _, err := online.ListGitHubReleases(context.Background(), source, 1, 100); err != nil {
		t.Fatal(err)
	}
	if onlineCalls != 1 {
		t.Fatalf("online request count = %d, want 1", onlineCalls)
	}

	offline := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir, Offline: true}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("offline mode attempted a network request")
		return nil, nil
	}))
	releases, err := offline.ListGitHubReleases(context.Background(), source, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("offline release snapshot = %#v, want empty array", releases)
	}
}

func TestGitHubAPISnapshotRejectsChangedSourceIdentity(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	first := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}
	payload := `[]
`
	online := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return githubResponse(http.StatusOK, nil, payload), nil
	}))
	if _, err := online.ListGitHubReleases(context.Background(), first, 1, 100); err != nil {
		t.Fatal(err)
	}
	differentSource := first
	differentSource.SourceID = "other-source"
	offline := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir, Offline: true}, nil)
	_, err := offline.ListGitHubReleases(context.Background(), differentSource, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_RELEASE_OFFLINE_MISS") {
		t.Fatalf("error = %v, want exact-source offline cache miss", err)
	}
}

func TestGitHubReleaseAssetCacheKeyIncludesReleaseIdentity(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	payload := []byte(`{"component_id":"engine"}`)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	assetURI := "https://github.com/acme/engine/releases/download/v1.2.3/engine-1.2.3.json"
	requests := 0
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir}, githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != assetURI {
			return nil, fmt.Errorf("unexpected asset URI %s", request.URL)
		}
		return githubResponse(http.StatusOK, nil, string(payload)), nil
	}))
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}
	release := GitHubRelease{ID: 123, TagName: "v1.2.3"}
	asset := GitHubReleaseAsset{
		ID: 456, Name: "engine-1.2.3.json", Size: int64(len(payload)),
		Digest: "sha256:" + digest, BrowserDownloadURL: assetURI,
	}
	first, err := fetcher.FetchGitHubReleaseAssetWithIdentity(context.Background(), source, release, strings.Repeat("a", 40), asset, maxReleaseMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetcher.FetchGitHubReleaseAssetWithIdentity(context.Background(), source, release, strings.Repeat("b", 40), asset, maxReleaseMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(payload) || string(second) != string(payload) || requests != 2 {
		t.Fatalf("asset fetches = %d, payloads = %q / %q", requests, first, second)
	}
	if len(mustGlob(t, filepath.Join(cacheDir, "*.json"))) < 2 {
		t.Fatal("expected distinct identity-addressed asset cache files")
	}
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestGitHubAPISnapshotEnvelopeIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows ACLs are not represented by os.FileMode.Perm(); the cache
		// directory inherits the caller's Windows security descriptor instead.
		t.Skip("POSIX permission bits do not model Windows ACLs")
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir}, nil)
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}
	if err := fetcher.writeGitHubAPISnapshot(source, "https://api.github.com/repos/acme/engine/releases?page=1&per_page=100", []byte(`[]
`)); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.snapshot.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("snapshot files = %v, err = %v", matches, err)
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("snapshot permissions = %o, want private", info.Mode().Perm())
	}
}
