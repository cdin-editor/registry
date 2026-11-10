package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxGitHubAPIResponseBytes = int64(8 << 20)

type githubAPISnapshot struct {
	SourceID      string `json:"source_id"`
	RepositoryURI string `json:"repository_uri"`
	RequestURI    string `json:"request_uri"`
	SHA256        string `json:"sha256"`
	Data          []byte `json:"data"`
}

// ListGitHubReleases implements the bounded, paginated GitHub Releases API
// adapter. Every page is bound to the exact Release Source identity in cache.
func (f *HTTPFetcher) ListGitHubReleases(ctx context.Context, source ReleaseSource, page, perPage int) ([]GitHubRelease, error) {
	if page < 1 || page > maxGitHubReleasePages || perPage < 1 || perPage > githubReleasePageSize {
		return nil, fmt.Errorf("catalog: GitHub Release pagination is outside the configured bounds")
	}
	owner, repository, err := githubRepositoryParts(source.RepositoryURI)
	if err != nil {
		return nil, err
	}
	endpoint := url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   "/repos/" + owner + "/" + repository + "/releases",
	}
	query := endpoint.Query()
	query.Set("page", strconv.Itoa(page))
	query.Set("per_page", strconv.Itoa(perPage))
	endpoint.RawQuery = query.Encode()

	data, err := f.fetchGitHubAPI(ctx, source, endpoint.String())
	if err != nil {
		return nil, err
	}
	var releases []GitHubRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, fmt.Errorf("catalog: GitHub Releases API returned malformed JSON")
	}
	if len(releases) > perPage {
		return nil, fmt.Errorf("catalog: GitHub Releases API exceeded the requested page size")
	}
	return releases, nil
}

// ResolveGitHubTagCommit resolves lightweight and annotated tag references.
// Nested annotated tags are followed with a fixed depth ceiling; tree/blob tags
// and incomplete SHA values fail closed.
func (f *HTTPFetcher) ResolveGitHubTagCommit(ctx context.Context, source ReleaseSource, tag string) (string, error) {
	if strings.TrimSpace(tag) == "" || strings.TrimSpace(tag) != tag {
		return "", fmt.Errorf("catalog: GitHub tag name is invalid")
	}
	owner, repository, err := githubRepositoryParts(source.RepositoryURI)
	if err != nil {
		return "", err
	}
	refPath := "/repos/" + owner + "/" + repository + "/git/ref/tags/" + url.PathEscape(tag)
	refURL := (&url.URL{Scheme: "https", Host: "api.github.com", Path: refPath}).String()
	data, err := f.fetchGitHubAPI(ctx, source, refURL)
	if err != nil {
		return "", fmt.Errorf("catalog: fetch GitHub tag reference failed: %w", err)
	}
	var reference struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		return "", fmt.Errorf("catalog: GitHub tag reference is malformed")
	}
	objectType, objectSHA := reference.Object.Type, reference.Object.SHA
	for depth := 0; depth < maxGitHubTagDepth; depth++ {
		if !validGitRevision(objectSHA) {
			return "", fmt.Errorf("catalog: GitHub tag object did not contain a full SHA")
		}
		switch objectType {
		case "commit":
			return strings.ToLower(objectSHA), nil
		case "tag":
			tagURL := (&url.URL{
				Scheme: "https",
				Host:   "api.github.com",
				Path:   "/repos/" + owner + "/" + repository + "/git/tags/" + objectSHA,
			}).String()
			tagData, fetchErr := f.fetchGitHubAPI(ctx, source, tagURL)
			if fetchErr != nil {
				return "", fmt.Errorf("catalog: fetch annotated Git tag object failed: %w", fetchErr)
			}
			var annotated struct {
				Object struct {
					Type string `json:"type"`
					SHA  string `json:"sha"`
				} `json:"object"`
			}
			if err := json.Unmarshal(tagData, &annotated); err != nil {
				return "", fmt.Errorf("catalog: annotated Git tag object is malformed")
			}
			objectType, objectSHA = annotated.Object.Type, annotated.Object.SHA
		default:
			return "", fmt.Errorf("catalog: GitHub tag resolves to unsupported Git object type %q", objectType)
		}
	}
	return "", fmt.Errorf("catalog: annotated Git tag nesting exceeds the %d-object limit", maxGitHubTagDepth)
}

// FetchGitHubReleaseAsset is the uncached provider fallback. Production Catalog
// loading uses FetchGitHubReleaseAssetWithIdentity to get a stronger cache key.
func (f *HTTPFetcher) FetchGitHubReleaseAsset(ctx context.Context, assetURI string, maxBytes int64) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("catalog: HTTP fetcher is not initialized")
	}
	if f.offline {
		return nil, fmt.Errorf("GITHUB_RELEASE_OFFLINE_MISS: no identity-bound cached asset is available")
	}
	return f.fetchBounded(ctx, assetURI, "application/octet-stream", maxBytes)
}

// FetchGitHubReleaseAssetWithIdentity verifies and caches metadata bytes under
// a key that includes source/repository, Release ID, tag commit, asset ID/name,
// URI, size and SHA-256. Offline mode can only reuse this exact verified entry.
func (f *HTTPFetcher) FetchGitHubReleaseAssetWithIdentity(
	ctx context.Context,
	source ReleaseSource,
	release GitHubRelease,
	tagCommit string,
	asset GitHubReleaseAsset,
	maxBytes int64,
) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("catalog: HTTP fetcher is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("catalog: GitHub Release asset context is required")
	}
	if release.ID <= 0 || asset.ID <= 0 || asset.Size <= 0 ||
		asset.Size > maxBytes || asset.Size > f.maxBytes || !validGitRevision(tagCommit) {
		return nil, fmt.Errorf("catalog: GitHub Release asset identity or size is invalid")
	}
	digest, err := ValidateGitHubAssetDigest(asset.Digest)
	if err != nil {
		return nil, err
	}
	if _, err := CanonicalGitHubRepositoryURI(source.RepositoryURI); err != nil {
		return nil, fmt.Errorf("catalog: GitHub Release source identity is invalid")
	}
	parsed, err := url.Parse(asset.BrowserDownloadURL)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Port() != "" {
		return nil, fmt.Errorf("catalog: GitHub Release asset URI is invalid")
	}
	discovery := ReleaseDiscoveryProvenance{
		SourceID: source.SourceID,
		Provider: source.Provider,
		RepositoryURI: source.RepositoryURI,
		ReleaseID: release.ID,
		ReleaseAPIURI: release.URL,
		Tag: release.TagName,
		TagCommit: strings.ToLower(tagCommit),
		MetadataAssetName: asset.Name,
		MetadataAssetSize: asset.Size,
		PublishedAt: release.PublishedAt,
	}
	cacheKey, err := GitHubReleaseAssetCacheKey(discovery, asset.BrowserDownloadURL, digest)
	if err != nil {
		return nil, err
	}
	return f.fetchWithCacheKey(ctx, asset.BrowserDownloadURL, digest, cacheKey)
}

// FetchGitHubReleaseAssetFromManifest reuses only the exact Release metadata
// bytes named by an immutable Manifest. The cache key is derivable from the
// public Manifest provenance, so replay can pin and reuse it offline without
// querying GitHub for a replacement release or asset.
func (f *HTTPFetcher) FetchGitHubReleaseAssetFromManifest(
	ctx context.Context,
	discovery ReleaseDiscoveryProvenance,
	metadataURI string,
	metadataSHA256 string,
) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("catalog: HTTP fetcher is not initialized")
	}
	cacheKey, err := GitHubReleaseAssetCacheKey(discovery, metadataURI, metadataSHA256)
	if err != nil {
		return nil, err
	}
	return f.fetchWithCacheKey(ctx, metadataURI, strings.ToLower(metadataSHA256), cacheKey)
}

// GitHubReleaseAssetCacheKey derives an immutable cache identity using only
// fields that are persisted in Resolved Manifest schema 1.2.0.
func GitHubReleaseAssetCacheKey(
	discovery ReleaseDiscoveryProvenance,
	metadataURI string,
	metadataSHA256 string,
) (string, error) {
	digest := strings.ToLower(metadataSHA256)
	if discovery.SourceID == "" || discovery.Provider != githubReleaseProvider ||
		discovery.ReleaseID <= 0 || discovery.MetadataAssetName == "" ||
		discovery.MetadataAssetSize <= 0 || !validGitRevision(discovery.TagCommit) ||
		!isSHA256Value(digest) {
		return "", fmt.Errorf("catalog: GitHub Release cache identity is incomplete")
	}
	repositoryURI, err := CanonicalGitHubRepositoryURI(discovery.RepositoryURI)
	if err != nil || repositoryURI != discovery.RepositoryURI {
		return "", fmt.Errorf("catalog: GitHub Release cache repository identity is invalid")
	}
	identity := strings.Join([]string{
		"registry-github-release-asset-v1",
		discovery.SourceID,
		repositoryURI,
		strconv.FormatInt(discovery.ReleaseID, 10),
		discovery.ReleaseAPIURI,
		discovery.Tag,
		strings.ToLower(discovery.TagCommit),
		discovery.MetadataAssetName,
		strconv.FormatInt(discovery.MetadataAssetSize, 10),
		metadataURI,
		digest,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:]), nil
}

func (f *HTTPFetcher) fetchGitHubAPI(ctx context.Context, source ReleaseSource, requestURI string) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("catalog: HTTP fetcher is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("catalog: GitHub API context is required")
	}
	parsed, err := url.Parse(requestURI)
	if err != nil || parsed == nil || parsed.Scheme != "https" ||
		!strings.EqualFold(parsed.Hostname(), "api.github.com") ||
		parsed.Port() != "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.Opaque != "" || !strings.HasPrefix(parsed.Path, "/repos/") ||
		path.Clean(parsed.Path) != parsed.Path {
		return nil, fmt.Errorf("catalog: GitHub API request URI is invalid")
	}
	if _, ok := f.allowedHosts["api.github.com"]; !ok {
		return nil, fmt.Errorf("catalog: GitHub API host is not enabled by the explicit github.com allow-list")
	}
	if f.offline {
		return f.readGitHubAPISnapshot(source, requestURI, f.maxBytes)
	}
	limit := f.maxBytes
	if limit <= 0 || limit > maxGitHubAPIResponseBytes {
		limit = maxGitHubAPIResponseBytes
	}
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if requestErr != nil {
			return nil, fmt.Errorf("catalog: create GitHub API request failed")
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "cdin-registry/1")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, requestErr := f.client.Do(request)
		if requestErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("catalog: GitHub API transport failed: %w", scrubURLError(requestErr))
		}

		rateLimited := response.StatusCode == http.StatusTooManyRequests ||
			(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0")
		retryableServerError := response.StatusCode == http.StatusInternalServerError ||
			response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusGatewayTimeout
		if rateLimited || retryableServerError {
			retryAfter := response.Header.Get("Retry-After")
			_ = response.Body.Close()
			if attempt+1 == maxAttempts {
				if rateLimited {
					return nil, fmt.Errorf("GITHUB_RATE_LIMIT_EXHAUSTED: GitHub API rate limit prevents complete Release discovery after %d attempts", maxAttempts)
				}
				return nil, fmt.Errorf("GITHUB_API_RETRY_EXHAUSTED: GitHub API transient failure after %d attempts", maxAttempts)
			}
			delay := time.Duration(100*(attempt+1)) * time.Millisecond
			if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds >= 0 && seconds <= 2 {
				delay = time.Duration(seconds) * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("GITHUB_API_FORBIDDEN: GitHub denied Release discovery")
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GITHUB_API_HTTP_ERROR: GitHub API returned HTTP %d", response.StatusCode)
		}
		if response.ContentLength > limit {
			return nil, fmt.Errorf("GITHUB_API_RESPONSE_TOO_LARGE: GitHub API page exceeds the configured response limit")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("catalog: read GitHub API response failed: %w", scrubURLError(readErr))
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("GITHUB_API_RESPONSE_TOO_LARGE: GitHub API page exceeds the configured response limit")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.cacheDir != "" {
			_ = f.writeGitHubAPISnapshot(source, requestURI, data)
		}
		return data, nil
	}
	return nil, fmt.Errorf("GITHUB_API_RETRY_EXHAUSTED: GitHub API retry budget was exhausted")
}

func githubAPISnapshotKey(source ReleaseSource, requestURI string) string {
	identity := strings.Join([]string{
		"registry-github-release-api-snapshot-v1",
		source.SourceID,
		source.RepositoryURI,
		requestURI,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func (f *HTTPFetcher) githubAPISnapshotPath(source ReleaseSource, requestURI string) string {
	return filepath.Join(f.cacheDir, "github-"+githubAPISnapshotKey(source, requestURI)+".snapshot.json")
}

func (f *HTTPFetcher) writeGitHubAPISnapshot(source ReleaseSource, requestURI string, data []byte) error {
	if f == nil || f.cacheDir == "" || len(data) == 0 || int64(len(data)) > maxGitHubAPIResponseBytes {
		return fmt.Errorf("catalog: GitHub API snapshot cache is unavailable")
	}
	sum := sha256.Sum256(data)
	snapshot := githubAPISnapshot{
		SourceID:      source.SourceID,
		RepositoryURI: source.RepositoryURI,
		RequestURI:    requestURI,
		SHA256:        hex.EncodeToString(sum[:]),
		Data:          append([]byte(nil), data...),
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return f.writeCache(f.githubAPISnapshotPath(source, requestURI), encoded)
}

func (f *HTTPFetcher) readGitHubAPISnapshot(source ReleaseSource, requestURI string, maxBytes int64) ([]byte, error) {
	if f == nil || f.cacheDir == "" {
		return nil, fmt.Errorf("GITHUB_RELEASE_OFFLINE_MISS: no verified GitHub Release API snapshot is cached")
	}
	snapshotPath := f.githubAPISnapshotPath(source, requestURI)
	info, err := os.Lstat(snapshotPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() > maxBytes*2+4096 {
		return nil, fmt.Errorf("GITHUB_RELEASE_OFFLINE_MISS: no verified GitHub Release API snapshot is cached")
	}
	encoded, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("GITHUB_RELEASE_OFFLINE_MISS: cached GitHub Release API snapshot is unavailable")
	}
	var snapshot githubAPISnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil ||
		snapshot.SourceID != source.SourceID ||
		snapshot.RepositoryURI != source.RepositoryURI ||
		snapshot.RequestURI != requestURI ||
		int64(len(snapshot.Data)) > maxBytes ||
		snapshot.SHA256 == "" {
		return nil, fmt.Errorf("GITHUB_RELEASE_CACHE_CORRUPT: cached GitHub Release API snapshot identity is invalid")
	}
	sum := sha256.Sum256(snapshot.Data)
	if hex.EncodeToString(sum[:]) != snapshot.SHA256 {
		return nil, fmt.Errorf("GITHUB_RELEASE_CACHE_CORRUPT: cached GitHub Release API snapshot digest mismatch")
	}
	return append([]byte(nil), snapshot.Data...), nil
}
