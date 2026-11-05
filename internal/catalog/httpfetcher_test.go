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
	return testHTTPFetcherWithOptions(t, handler, maxBytes, cacheDir, time.Second, 0)
}

func testHTTPFetcherWithOptions(t *testing.T, handler roundTripFunc, maxBytes int64, cacheDir string, timeout time.Duration, maxRedirects int) *HTTPFetcher {
	t.Helper()
	fetcher, err := NewHTTPFetcher(HTTPFetcherOptions{
		AllowedHosts: []string{"catalog.example.invalid"},
		MaxBytes:     maxBytes,
		CacheDir:     cacheDir,
		Timeout:      timeout,
		MaxRedirects: maxRedirects,
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher.allowPrivate = true
	fetcher.lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	fetcher.dialIP = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("test dial should not be reached")
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
			Header:     http.Header{"Location": []string{"https://other.example.invalid/private.json"}},
			Body:       io.NopCloser(strings.NewReader("")),
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


type failingReadCloser struct {
	err error
}

func (b failingReadCloser) Read([]byte) (int, error) { return 0, b.err }
func (b failingReadCloser) Close() error             { return nil }

type stalledReadCloser struct {
	ctx context.Context
}

func (b stalledReadCloser) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b stalledReadCloser) Close() error { return nil }

// TestREG019HTTPFetcherTimesOutStalledResponseHeaders proves the HTTP client's
// configured timeout covers a RoundTripper that never produces response headers.
func TestREG019HTTPFetcherTimesOutStalledResponseHeaders(t *testing.T) {
	fetcher := testHTTPFetcherWithOptions(t, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}, 1024, "", 25*time.Millisecond, 0)

	_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/stalled")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

// TestREG019HTTPFetcherTimesOutStalledResponseBody proves the same timeout
// bounds body reads after the server has returned response headers.
func TestREG019HTTPFetcherTimesOutStalledResponseBody(t *testing.T) {
	fetcher := testHTTPFetcherWithOptions(t, func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          stalledReadCloser{ctx: request.Context()},
			ContentLength: -1,
		}, nil
	}, 1024, "", 25*time.Millisecond, 0)

	_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/stalled-body")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

// TestREG019HTTPFetcherRedirectLimitStopsLoops verifies same-host redirect
// loops are bounded even when every individual redirect is otherwise trusted.
func TestREG019HTTPFetcherRedirectLimitStopsLoops(t *testing.T) {
	calls := 0
	fetcher := testHTTPFetcherWithOptions(t, func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"/next"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}, 1024, "", time.Second, 2)

	_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/start")
	if err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("error = %v, want redirect-limit rejection", err)
	}
	if calls != 3 {
		t.Fatalf("RoundTripper calls = %d, want initial request plus two allowed redirects", calls)
	}
}

// TestREG019HTTPFetcherRejectsSensitiveRedirectTargets ensures same-host
// redirects cannot add userinfo or query-like credentials.
func TestREG019HTTPFetcherRejectsSensitiveRedirectTargets(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		location string
	}{
		{name: "userinfo", location: "https://user:redirect-secret@catalog.example.invalid/release.json"},
		{name: "query", location: "https://catalog.example.invalid/release.json?token=redirect-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			location := testCase.location
			calls := 0
			fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{location}},
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}, 1024, "")
			_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/index.json")
			if err == nil || !strings.Contains(err.Error(), "redirected metadata URI rejected") {
				t.Fatalf("error = %v, want safe redirect-policy rejection", err)
			}
			if strings.Contains(err.Error(), "redirect-secret") || calls != 1 {
				t.Fatalf("unsafe redirect detail leaked or was followed: err=%v calls=%d", err, calls)
			}
		})
	}
}

// TestREG019HTTPFetcherRedactsTransportAndBodyReadErrors verifies that
// RoundTripper and remote response-body errors cannot inject secret-bearing URLs
// or arbitrary server-controlled text into diagnostics.
func TestREG019HTTPFetcherRedactsTransportAndBodyReadErrors(t *testing.T) {
	const secret = "credential-marker-9f83"
	remoteError := errors.New("upstream failed at https://user:" + secret + "@catalog.example.invalid/path?token=" + secret + " Authorization: Bearer " + secret)

	t.Run("untrusted input URI", func(t *testing.T) {
		fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("unsafe URI must be rejected before RoundTripper execution")
			return nil, nil
		}, 1024, "")
		for _, input := range []string{
			"https://user:" + secret + "@catalog.example.invalid/index.json",
			"https://catalog.example.invalid/index.json?token=" + secret,
		} {
			_, err := fetcher.Fetch(context.Background(), input)
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("unsafe URI error leaked secret or succeeded: input=%q error=%v", input, err)
			}
		}
	})

	t.Run("transport", func(t *testing.T) {
		fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
			return nil, remoteError
		}, 1024, "")
		_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/index.json")
		if err == nil || !strings.Contains(err.Error(), "transport failure") {
			t.Fatalf("error = %v, want safe transport classification", err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "catalog.example.invalid/path") {
			t.Fatalf("transport error leaked remote text: %v", err)
		}
	})

	t.Run("body read", func(t *testing.T) {
		fetcher := testHTTPFetcher(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingReadCloser{err: remoteError}, ContentLength: -1}, nil
		}, 1024, "")
		_, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/index.json")
		if err == nil || !strings.Contains(err.Error(), "transport failure") {
			t.Fatalf("error = %v, want safe body-read classification", err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "catalog.example.invalid/path") {
			t.Fatalf("body-read error leaked remote text: %v", err)
		}
	})
}

// TestREG019HTTPFetcherDoesNotInferCredentialsFromAmbientState verifies the
// production client sends no Authorization/Cookie headers and uses no ambient
// proxy or cookie jar.
func TestREG019HTTPFetcherDoesNotInferCredentialsFromAmbientState(t *testing.T) {
	var observed *http.Request
	fetcher := testHTTPFetcher(t, func(request *http.Request) (*http.Response, error) {
		observed = request.Clone(request.Context())
		return httpResponse(http.StatusOK, "{}", 2), nil
	}, 1024, "")
	if _, err := fetcher.Fetch(context.Background(), "https://catalog.example.invalid/index.json"); err != nil {
		t.Fatal(err)
	}
	if observed == nil {
		t.Fatal("RoundTripper did not observe a request")
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		if value := observed.Header.Get(header); value != "" {
			t.Errorf("unexpected %s header %q", header, value)
		}
	}

	production, err := NewHTTPFetcher(HTTPFetcherOptions{AllowedHosts: []string{"catalog.example.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := production.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("production transport has type %T, want *http.Transport", production.client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("production metadata transport must not use ambient proxy configuration")
	}
	if production.client.Jar != nil {
		t.Fatal("production metadata transport must not use an ambient cookie jar")
	}
}

// TestREG019HTTPFetcherRejectsMixedPublicAndProhibitedDNSAnswersBeforeDial
// prevents a host with one public and one prohibited address from being used.
func TestREG019HTTPFetcherRejectsMixedPublicAndProhibitedDNSAnswersBeforeDial(t *testing.T) {
	fetcher := testHTTPFetcher(t, nil, 1024, "")
	fetcher.allowPrivate = false
	fetcher.lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("10.0.0.8")}}, nil
	}
	dialCalls := 0
	fetcher.dialIP = func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("test dial")
	}

	_, err := fetcher.dialContext(context.Background(), "tcp", "catalog.example.invalid:443")
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("error = %v, want prohibited-address rejection", err)
	}
	if dialCalls != 0 {
		t.Fatalf("dialed %d addresses before rejecting the mixed DNS answer", dialCalls)
	}
}

// TestREG019HTTPFetcherDialsTheExactValidatedIP proves the address passed to
// the socket dialer is the same literal IP that passed the resolver checks.
func TestREG019HTTPFetcherDialsTheExactValidatedIP(t *testing.T) {
	fetcher := testHTTPFetcher(t, nil, 1024, "")
	fetcher.allowPrivate = false
	fetcher.lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	var dialed []string
	fetcher.dialIP = func(_ context.Context, network, address string) (net.Conn, error) {
		dialed = append(dialed, network+" "+address)
		return nil, errors.New("test dial stopped")
	}

	_, err := fetcher.dialContext(context.Background(), "tcp", "catalog.example.invalid:443")
	if err == nil {
		t.Fatal("expected injected dial failure")
	}
	want := []string{"tcp 1.1.1.1:443", "tcp 8.8.8.8:443"}
	if strings.Join(dialed, "|") != strings.Join(want, "|") {
		t.Fatalf("dialed addresses = %#v, want %#v", dialed, want)
	}
}
