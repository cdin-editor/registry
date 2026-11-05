// Package catalog contains the production metadata HTTP transport and
// digest-addressed cache used by the transport-neutral Catalog loader.
//
// Spec traceability:
//   - REG-019: bind metadata retrieval to the declared digest and verify exact
//     response bytes before they reach schema parsing.
//   - REG-026: Catalog URIs and response bodies are untrusted; transport policy
//     must reject unsafe hosts, redirects, credentials and unbounded responses.
//   - Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#7-security-and-reproducibility
//   - Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary
//   - Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/catalog-transport.md
//
// Hostname allow-lists are not a substitute for deployment egress controls.
// This implementation resolves every address used for a new connection and
// rejects private, loopback, link-local and reserved ranges to reduce SSRF and
// DNS-rebinding exposure. Deployments should still enforce network egress policy.
package catalog

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout     = 20 * time.Second
	defaultMaximumMetadata = int64(8 << 20)
	defaultMaximumRedirect = 3
)

// HTTPFetcherOptions controls the trusted metadata transport boundary.
type HTTPFetcherOptions struct {
	AllowedHosts []string
	Timeout      time.Duration
	MaxBytes     int64
	MaxRedirects int
	CacheDir     string
}

// HTTPFetcher retrieves Catalog release metadata over HTTPS. The allow-list is
// exact-host matching; wildcards, HTTP, embedded credentials, non-default ports,
// query strings and fragments are rejected.
type HTTPFetcher struct {
	client       *http.Client
	allowedHosts map[string]struct{}
	maxBytes     int64
	cacheDir     string

	// These hooks are private test seams. Production constructors never enable
	// private-address access and always use the system resolver.
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	allowPrivate bool
}

// NewHTTPFetcher creates a bounded HTTPS Fetcher with an exact host allow-list.
// A cache is optional; when configured it is content-addressed by URI+digest.
func NewHTTPFetcher(options HTTPFetcherOptions) (*HTTPFetcher, error) {
	allowed := make(map[string]struct{}, len(options.AllowedHosts))
	for _, host := range options.AllowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if !validHostname(host) {
			return nil, fmt.Errorf("catalog: invalid HTTPS metadata allow-list host %q", host)
		}
		allowed[host] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("catalog: at least one HTTPS metadata host must be allow-listed")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaximumMetadata
	}
	redirects := options.MaxRedirects
	if redirects <= 0 {
		redirects = defaultMaximumRedirect
	}
	cacheDir := options.CacheDir
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("catalog: create metadata cache: %w", err)
		}
		resolvedCacheDir, err := filepath.EvalSymlinks(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("catalog: canonicalize metadata cache: %w", err)
		}
		cacheDir = resolvedCacheDir
	}
	fetcher := &HTTPFetcher{
		allowedHosts: allowed,
		maxBytes:     maxBytes,
		cacheDir:     cacheDir,
		lookupIPAddr: net.DefaultResolver.LookupIPAddr,
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DisableCompression:    false,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:           fetcher.dialContext,
	}
	fetcher.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > redirects {
				return fmt.Errorf("catalog: metadata redirect limit exceeded")
			}
			if err := fetcher.validateURL(request.URL); err != nil {
				return fmt.Errorf("catalog: redirected metadata URI rejected: %w", err)
			}
			return nil
		},
	}
	if fetcher.cacheDir != "" {
		if err := rejectSymlinkAncestors(fetcher.cacheDir); err != nil {
			return nil, fmt.Errorf("catalog: unsafe metadata cache path: %w", err)
		}
		if err := os.Chmod(fetcher.cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("catalog: secure metadata cache directory: %w", err)
		}
	}
	return fetcher, nil
}

// Fetch implements the original transport-neutral Fetcher contract. Without
// an expected digest it deliberately bypasses persistent cache reads/writes.
func (f *HTTPFetcher) Fetch(ctx context.Context, metadataURI string) ([]byte, error) {
	return f.fetch(ctx, metadataURI)
}

// FetchWithDigest binds cache lookup and storage to both the immutable URI and
// expected digest. Cached bytes are re-hashed before reuse; corrupt/stale files
// are removed and fetched again. Catalog.Load independently verifies the bytes
// returned by this method before schema validation.
func (f *HTTPFetcher) FetchWithDigest(ctx context.Context, metadataURI, expectedSHA string) ([]byte, error) {
	expectedSHA = strings.ToLower(expectedSHA)
	if !isSHA256(expectedSHA) {
		return nil, fmt.Errorf("catalog: expected metadata digest is not SHA-256")
	}
	if f.cacheDir == "" {
		data, err := f.fetch(ctx, metadataURI)
		if err != nil {
			return nil, err
		}
		if httpMetadataDigest(data) != expectedSHA {
			return nil, fmt.Errorf("catalog: fetched metadata does not match declared SHA-256")
		}
		return data, nil
	}
	cachePath := f.cachePath(metadataURI, expectedSHA)
	if data, ok := f.readCache(cachePath, expectedSHA); ok {
		return data, nil
	}
	data, err := f.fetch(ctx, metadataURI)
	if err != nil {
		return nil, err
	}
	if httpMetadataDigest(data) != expectedSHA {
		return nil, fmt.Errorf("catalog: fetched metadata does not match declared SHA-256")
	}
	if err := f.writeCache(cachePath, data); err != nil {
		// Cache is an optimization. A verified network response remains valid
		// even when the local cache is read-only or temporarily unavailable.
		return data, nil
	}
	return data, nil
}

func (f *HTTPFetcher) fetch(ctx context.Context, metadataURI string) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("catalog: HTTP fetcher is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("catalog: HTTP fetch context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(metadataURI)
	if err != nil {
		return nil, fmt.Errorf("catalog: invalid metadata URI")
	}
	if err := f.validateURL(parsed); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("catalog: create metadata request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "cdin-registry/1")
	response, err := f.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("catalog: metadata request failed: %w", scrubURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog: metadata server returned HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > f.maxBytes {
		return nil, fmt.Errorf("catalog: metadata response exceeds configured size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("catalog: read metadata response: %w", err)
	}
	if int64(len(data)) > f.maxBytes {
		return nil, fmt.Errorf("catalog: metadata response exceeds configured size limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// validateURL is called before every request and for every redirect. Exact host
// checks prevent a catalog entry from escalating into an arbitrary network fetch.
func (f *HTTPFetcher) validateURL(parsed *url.URL) error {
	if parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return fmt.Errorf("catalog: metadata URI must be absolute and hierarchical")
	}
	if strings.ToLower(parsed.Scheme) != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("catalog: metadata URI must use HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("catalog: metadata URI must not contain credentials, query or fragment")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return fmt.Errorf("catalog: metadata URI must use HTTPS port 443")
	}
	host := strings.ToLower(parsed.Hostname())
	if net.ParseIP(host) != nil {
		return fmt.Errorf("catalog: metadata URI must use an allow-listed DNS hostname, not an IP literal")
	}
	if _, ok := f.allowedHosts[host]; !ok {
		return fmt.Errorf("catalog: metadata host %q is not allow-listed", host)
	}
	return nil
}

// dialContext pins each new socket to an address resolved and checked in this
// call, avoiding a second DNS lookup between address validation and connect.
func (f *HTTPFetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("catalog: invalid metadata socket address")
	}
	if _, err := strconv.Atoi(port); err != nil || port != "443" {
		return nil, fmt.Errorf("catalog: metadata connection must use port 443")
	}
	addresses, err := f.lookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("catalog: metadata host lookup failed")
	}
	for _, resolved := range addresses {
		if !f.allowPrivate && !publicUnicast(resolved.IP) {
			return nil, fmt.Errorf("catalog: metadata host resolved to a non-public address")
		}
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	var failures []error
	for _, resolved := range addresses {
		target := net.JoinHostPort(resolved.IP.String(), port)
		connection, dialErr := dialer.DialContext(ctx, network, target)
		if dialErr == nil {
			return connection, nil
		}
		failures = append(failures, dialErr)
	}
	return nil, fmt.Errorf("catalog: could not connect to metadata host: %w", errors.Join(failures...))
}

func publicUnicast(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4.IsPrivate() || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsLinkLocalMulticast() || v4.IsUnspecified() || v4.IsMulticast() {
			return false
		}
		for _, block := range []string{
			"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
			"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24",
			"203.0.113.0/24", "240.0.0.0/4",
		} {
			_, subnet, _ := net.ParseCIDR(block)
			if subnet.Contains(v4) {
				return false
			}
		}
		return true
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, block := range []string{"2001:db8::/32", "2001:10::/28", "2001:20::/28"} {
		_, subnet, _ := net.ParseCIDR(block)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}

func (f *HTTPFetcher) cachePath(metadataURI, expectedSHA string) string {
	sum := sha256.Sum256([]byte(metadataURI + "\x00" + expectedSHA))
	return filepath.Join(f.cacheDir, hex.EncodeToString(sum[:])+".json")
}

func (f *HTTPFetcher) readCache(path, expectedSHA string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > f.maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) > f.maxBytes || httpMetadataDigest(data) != expectedSHA {
		_ = os.Remove(path)
		return nil, false
	}
	return data, true
}

func (f *HTTPFetcher) writeCache(path string, data []byte) error {
	if err := rejectSymlinkAncestors(filepath.Dir(path)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(f.cacheDir, ".registry-metadata-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func rejectSymlinkAncestors(value string) error {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	current := filepath.Clean(absolute)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("catalog: cache path contains symlink ancestor %q", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, value := range label {
			if !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '-') {
				return false
			}
		}
	}
	return true
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func httpMetadataDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// scrubURLError keeps transport errors useful without retaining a full URL
// object that could include sensitive query strings or userinfo.
func scrubURLError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return urlError.Err
	}
	return err
}
