package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testHTTPFetcher(t *testing.T, handler roundTripFunc, maxBytes int64, cacheDir string) *HTTPFetcher {
	t.Helper()
	fetcher, err := NewHTTPFetcher(HTTPFetcherOptions{
		AllowedHosts: []string{"catalog.example.invalid"},
		MaxBytes:     maxBytes,
		CacheDir:     cacheDir,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher.allowPrivate = true
	fetcher.lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	fetcher.client.Transport = handler
	return fetcher
}

func httpResponse(status int, body string, contentLength int64) *http.Response {
	headers := make(http.Header)
	if contentLength >= 0 {
		headers.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	return &http.Response{
		StatusCode:    status,
		Header:        headers,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
	}
}


func expectedDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// TestREG019HTTPFetcherValidatesHostAndURIRestrictions rejects schemes and URI
// forms that could bypass the explicit metadata host trust boundary.
func TestREG019HTTPFetcherValidatesHostAndURIRestrictions(t *testing.T) {
	fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, "{}", 2), nil
	}, 1024, "")
	for _, value := range []string{
		"http://catalog.example.invalid/index.json",
		"https://user:secret@catalog.example.invalid/index.json",
		"https://catalog.example.invalid:8443/index.json",
		"https://catalog.example.invalid/index.json?token=value",
		"https://catalog.example.invalid/index.json#fragment",
		"https://other.example.invalid/index.json",
		"https://127.0.0.1/index.json",
	} {
		if _, err := fetcher.Fetch(context.Background(), value); err == nil {
			t.Errorf("Fetch(%q) unexpectedly succeeded", value)
		}
	}
}

// TestREG019HTTPFetcherBoundsResponseBytes checks both Content-Length fast
// rejection and bounded reading when a server omits the length header.
func TestREG019HTTPFetcherBoundsResponseBytes(t *testing.T) {
	fetcher := testHTTPFetcher(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/declared" {
			return httpResponse(http.StatusOK, "123456789", 9), nil
		}
		return httpResponse(http.StatusOK, "123456789", -1), nil
	}, 8, "")
	for _, path := range []string{"/declared", "/streamed"} {
		if _, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid"+path); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Errorf("%s: expected size-limit error, got %v", path, err)
		}
	}
}

// TestREG019HTTPFetcherFollowsOnlyAllowListedRedirects ensures a response
// cannot redirect metadata retrieval to an arbitrary second host.
func TestREG019HTTPFetcherFollowsOnlyAllowListedRedirects(t *testing.T) {
	calls := 0
	fetcher := testHTTPFetcher(t, func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{"Location": []string{"https://other.example.invalid/private.json"}},
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}, 1024, "")
	_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/index.json")
	if err == nil || !strings.Contains(err.Error(), "not allow-listed") {
		t.Fatalf("expected redirect host rejection, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("redirect reached second host; round trips = %d", calls)
	}
}

// TestREG019HTTPFetcherDigestCacheVerifiesBytesOnRead checks the cache identity
// includes the expected digest, and corrupt cached content is not reused.
func TestREG019HTTPFetcherDigestCacheVerifiesBytesOnRead(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "metadata-cache")
	calls := 0
	fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
		calls++
		return httpResponse(http.StatusOK, "{\"component_id\":\"engine\"}", 25), nil
	}, 1024, cacheDir)
	body := "{\"component_id\":\"engine\"}"
	digest := expectedDigest(body)
	first, err := fetcher.FetchWithDigest(context.Background(), "https://catalog.example.invalid/release.json", digest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetcher.FetchWithDigest(context.Background(), "https://catalog.example.invalid/release.json", digest)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != body || string(second) != body || calls != 1 {
		t.Fatalf("cache result mismatch: first=%q second=%q network calls=%d", first, second, calls)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one cache entry, matches=%v err=%v", matches, err)
	}
	if err := os.WriteFile(matches[0], []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.FetchWithDigest(context.Background(), "https://catalog.example.invalid/release.json", digest); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("corrupt cache entry was not refetched; calls=%d", calls)
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cache entry permissions are too broad: %v", info.Mode().Perm())
	}
}

// TestREG019HTTPFetcherDigestMismatchIsNotCached verifies bad network data is
// rejected before a cache file can be created.
func TestREG019HTTPFetcherDigestMismatchIsNotCached(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, "bad", 3), nil
	}, 1024, cacheDir)
	_, err := fetcher.FetchWithDigest(context.Background(), "https://catalog.example.invalid/release.json", expectedDigest("good"))
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("bad response was cached: %v (%v)", matches, globErr)
	}
}

// TestREG019HTTPFetcherRejectsNonPublicResolvedAddresses ensures a host that
// passes the string allow-list still cannot connect to a private IP at dial time.
func TestREG019HTTPFetcherRejectsNonPublicResolvedAddresses(t *testing.T) {
	fetcher := testHTTPFetcher(t, nil, 1024, "")
	fetcher.allowPrivate = false
	_, err := fetcher.dialContext(context.Background(), "tcp", "catalog.example.invalid:443")
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("expected private-address denial, got %v", err)
	}
}

// TestREG019HTTPFetcherPropagatesCancellation prevents a slow response from
// outliving the caller's context.
func TestREG019HTTPFetcherPropagatesCancellation(t *testing.T) {
	fetcher := testHTTPFetcher(t, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}, 1024, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetcher.Fetch(ctx, "https://catalog.example.invalid/index.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestREG019CacheRejectsSymlinkEntry prevents following attacker-controlled
// symlinks placed into the local content-addressed metadata cache.
func TestREG019CacheRejectsSymlinkEntry(t *testing.T) {
	cacheDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, "{}", 2), nil
	}, 1024, cacheDir)
	uri := "https://catalog.example.invalid/index.json"
	digest := expectedDigest("{}")
	cachePath := fetcher.cachePath(uri, digest)
	if err := os.Symlink(outside, cachePath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := fetcher.FetchWithDigest(context.Background(), uri, digest); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(cachePath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("cache symlink must be replaced by verified regular content")
	}
}
