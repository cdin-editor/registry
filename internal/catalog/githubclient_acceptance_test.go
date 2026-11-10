package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPFetcherResolvesLightweightGitHubTagToCommit(t *testing.T) {
	commit := strings.Repeat("c", 40)
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/repos/acme/engine/git/ref/tags/v1.2.3" {
			return nil, errors.New("unexpected GitHub API path")
		}
		return githubResponse(http.StatusOK, nil, `{"object":{"type":"commit","sha":"`+commit+`"}}`), nil
	}))
	got, err := fetcher.ResolveGitHubTagCommit(context.Background(), ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got != commit {
		t.Fatalf("resolved lightweight tag = %q, want %q", got, commit)
	}
}

func TestHTTPFetcherBoundsGitHubAPIPageBytes(t *testing.T) {
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{
		AllowedHosts: []string{"github.com"},
		MaxBytes:     64,
	}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		response := githubResponse(http.StatusOK, nil, strings.Repeat("x", 65))
		response.ContentLength = -1
		return response, nil
	}))
	_, err := fetcher.ListGitHubReleases(context.Background(), ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine",
	}, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_API_RESPONSE_TOO_LARGE") {
		t.Fatalf("error = %v, want bounded GitHub API page rejection", err)
	}
}

func TestHTTPFetcherPropagatesGitHubAPICancellationAndTimeout(t *testing.T) {
	requests := 0
	fetcher := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}}, githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine",
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetcher.ListGitHubReleases(canceled, source, 1, 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error = %v, want context.Canceled", err)
	}
	if requests != 0 {
		t.Fatalf("already-canceled context made %d requests, want zero", requests)
	}

	timed, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	_, err = fetcher.ListGitHubReleases(timed, source, 1, 100)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed request error = %v, want context.DeadlineExceeded", err)
	}
	if requests != 1 {
		t.Fatalf("timed request count = %d, want one", requests)
	}
}

func TestGitHubAPISnapshotRejectsCorruptedCachedEnvelope(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	source := ReleaseSource{
		SourceID: "engine-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}
	requestURI := "https://api.github.com/repos/acme/engine/releases?page=1&per_page=100"
	online := githubTestFetcher(t, HTTPFetcherOptions{AllowedHosts: []string{"github.com"}, CacheDir: cacheDir}, nil)
	if err := online.writeGitHubAPISnapshot(source, requestURI, []byte("[]")); err != nil {
		t.Fatal(err)
	}

	path := online.githubAPISnapshotPath(source, requestURI)
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot githubAPISnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Data = []byte("[{}]")
	encoded, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	offline := githubTestFetcher(t, HTTPFetcherOptions{
		AllowedHosts: []string{"github.com"}, CacheDir: cacheDir, Offline: true,
	}, githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("offline cache verification attempted a network request")
		return nil, nil
	}))
	_, err = offline.ListGitHubReleases(context.Background(), source, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_RELEASE_CACHE_CORRUPT") {
		t.Fatalf("error = %v, want corrupted snapshot rejection", err)
	}
}
