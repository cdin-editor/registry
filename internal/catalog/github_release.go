package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cdin-editor/registry/internal/semver"
)

const (
	githubReleasePageSize       = 100
	maxGitHubReleasePages       = 10
	maxGitHubReleaseRecords     = githubReleasePageSize * maxGitHubReleasePages
	maxGitHubTagPeelDepth       = 8
	maxGitHubAPIRetries         = 3
	maxGitHubDiscoveryCacheSize = 16 << 20
)

// ReleaseSource is a stable Catalog mapping from one logical component variant
// to the GitHub repository that publishes its Release Assets. It is not a
// version-specific entry.
type ReleaseSource struct {
	SourceID     string `json:"source_id"`
	Provider     string `json:"provider"`
	RepositoryURI string `json:"repository_uri"`
	ComponentID  string `json:"component_id"`
	Variant      string `json:"variant,omitempty"`
	TagPrefix    string `json:"tag_prefix,omitempty"`
}

// ReleasePublication is the publisher-authored, self-contained part of the
// metadata provenance. Its own URI and digest are deliberately not embedded.
type ReleasePublication struct {
	Provider          string `json:"provider"`
	RepositoryURI     string `json:"repository_uri"`
	Tag               string `json:"tag"`
	TagCommit         string `json:"tag_commit"`
	MetadataAssetName string `json:"metadata_asset_name"`
}

// ReleaseDiscovery records the GitHub Release and metadata asset resolved by
// Registry. This is the exact identity required for immutable Manifest replay.
type ReleaseDiscovery struct {
	SourceID               string `json:"source_id"`
	Provider               string `json:"provider"`
	RepositoryURI          string `json:"repository_uri"`
	ReleaseID              int64  `json:"release_id"`
	ReleaseAPIURI           string `json:"release_api_uri"`
	Tag                    string `json:"tag"`
	TagCommit              string `json:"tag_commit"`
	MetadataAssetName      string `json:"metadata_asset_name"`
	MetadataAssetSizeBytes int64  `json:"metadata_asset_size_bytes"`
	PublishedAt            string `json:"published_at,omitempty"`
}

// DiscoveredRelease is returned only after the release listing, tag object,
// metadata asset digest/size, and exact downloaded bytes have been verified.
type DiscoveredRelease struct {
	Payload      []byte
	MetadataURI  string
	MetadataSHA  string
	MetadataSize int64
	Discovery    ReleaseDiscovery
}

// ReleaseDiscoveryFetcher is the generic optional transport capability needed
// by a Catalog containing GitHub Release Sources. Static Catalogs still need
// only Fetcher and DigestFetcher.
type ReleaseDiscoveryFetcher interface {
	DiscoverGitHubReleases(context.Context, ReleaseSource) ([]DiscoveredRelease, error)
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubReleaseRecord struct {
	ID          int64                `json:"id"`
	URL         string               `json:"url"`
	TagName     string               `json:"tag_name"`
	Draft       bool                 `json:"draft"`
	Prerelease  bool                 `json:"prerelease"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubGitObject struct {
	SHA  string `json:"sha"`
	Type string `json:"type"`
}

type githubGitRefResponse struct {
	Object githubGitObject `json:"object"`
}

type githubTagObjectResponse struct {
	Object githubGitObject `json:"object"`
}

type githubReleaseSnapshot struct {
	ReleaseID         int64  `json:"release_id"`
	ReleaseAPIURI      string `json:"release_api_uri"`
	Tag               string `json:"tag"`
	TagCommit         string `json:"tag_commit"`
	MetadataAssetName string `json:"metadata_asset_name"`
	MetadataAssetURI  string `json:"metadata_asset_uri"`
	MetadataSHA       string `json:"metadata_sha256"`
	MetadataSize      int64  `json:"metadata_size_bytes"`
	PublishedAt       string `json:"published_at,omitempty"`
}

type githubDiscoverySnapshotEnvelope struct {
	Version             int                     `json:"version"`
	SourceIdentitySHA256 string                  `json:"source_identity_sha256"`
	PayloadSHA256       string                  `json:"payload_sha256"`
	Releases            []githubReleaseSnapshot `json:"releases"`
}

type githubRateLimitError struct {
	retryAfter time.Duration
}

func (e *githubRateLimitError) Error() string { return "GitHub API rate limit was reached" }

// DiscoverGitHubReleases enumerates a complete, bounded release inventory.
// Offline mode uses only a previously verified discovery snapshot and the
// digest-addressed metadata cache; it never queries for a newer release.
func (f *HTTPFetcher) DiscoverGitHubReleases(ctx context.Context, source ReleaseSource) ([]DiscoveredRelease, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("RELEASE_SOURCE_INVALID: GitHub Release transport is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("RELEASE_SOURCE_INVALID: context is required")
	}
	if err := validateReleaseSource(source); err != nil {
		return nil, err
	}

	var snapshots []githubReleaseSnapshot
	if f.offline {
		if f.metadataCache == nil {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_OFFLINE_MISS: no discovery cache is configured")
		}
		cached, ok, err := f.metadataCache.readGitHubReleaseSnapshot(source)
		if err != nil {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_OFFLINE_MISS: cached discovery snapshot is invalid")
		}
		if !ok {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_OFFLINE_MISS: no verified snapshot exists for this Release Source")
		}
		snapshots = cached
	} else {
		var err error
		snapshots, err = f.discoverGitHubReleaseSnapshot(ctx, source)
		if err != nil {
			return nil, err
		}
	}

	if len(snapshots) > maxGitHubReleaseRecords {
		return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: cached release inventory exceeds the configured limit")
	}
	out := make([]DiscoveredRelease, 0, len(snapshots))
	var totalBytes int64
	for _, item := range snapshots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.MetadataSize <= 0 || item.MetadataSize > maxReleaseMetadataBytes {
			return nil, fmt.Errorf("RELEASE_METADATA_SIZE_MISMATCH: metadata asset declares an invalid byte size")
		}
		if totalBytes > maxCatalogMetadataBytes-item.MetadataSize {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: discovered metadata exceeds the Catalog byte budget")
		}
		totalBytes += item.MetadataSize

		payload, err := f.FetchWithDigest(ctx, item.MetadataAssetURI, item.MetadataSHA)
		if err != nil {
			if f.offline {
				return nil, fmt.Errorf("RELEASE_DISCOVERY_OFFLINE_MISS: verified metadata asset is not cached")
			}
			return nil, fmt.Errorf("RELEASE_METADATA_ASSET_UNAVAILABLE: metadata Release Asset could not be retrieved")
		}
		if int64(len(payload)) != item.MetadataSize {
			return nil, fmt.Errorf("RELEASE_METADATA_SIZE_MISMATCH: downloaded metadata byte count differs from the Release API")
		}
		out = append(out, DiscoveredRelease{
			Payload:      append([]byte(nil), payload...),
			MetadataURI:  item.MetadataAssetURI,
			MetadataSHA:  item.MetadataSHA,
			MetadataSize: item.MetadataSize,
			Discovery: ReleaseDiscovery{
				SourceID:               source.SourceID,
				Provider:               source.Provider,
				RepositoryURI:          source.RepositoryURI,
				ReleaseID:              item.ReleaseID,
				ReleaseAPIURI:           item.ReleaseAPIURI,
				Tag:                    item.Tag,
				TagCommit:              item.TagCommit,
				MetadataAssetName:      item.MetadataAssetName,
				MetadataAssetSizeBytes: item.MetadataSize,
				PublishedAt:            item.PublishedAt,
			},
		})
	}
	if !f.offline && f.metadataCache != nil {
		if err := f.metadataCache.writeGitHubReleaseSnapshot(source, snapshots); err != nil {
			// A cache is an optimization. Failure to persist a fully verified
			// online result must not turn a successful resolution into failure.
		}
	}
	return out, nil
}

func (f *HTTPFetcher) discoverGitHubReleaseSnapshot(ctx context.Context, source ReleaseSource) ([]githubReleaseSnapshot, error) {
	owner, repository, err := parseGitHubRepository(source.RepositoryURI)
	if err != nil {
		return nil, fmt.Errorf("RELEASE_SOURCE_INVALID: repository URI is not a canonical GitHub repository")
	}
	prefix := source.TagPrefix
	if prefix == "" {
		prefix = "v"
	}
	releases := make([]githubReleaseRecord, 0)
	base := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repository)
	for page := 1; page <= maxGitHubReleasePages; page++ {
		endpoint := fmt.Sprintf("%s?per_page=%d&page=%d", base, githubReleasePageSize, page)
		payload, fetchErr := f.fetchGitHubAPI(ctx, endpoint)
		if fetchErr != nil {
			return nil, fetchErr
		}
		var records []githubReleaseRecord
		if err := json.Unmarshal(payload, &records); err != nil {
			return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: GitHub returned an invalid Releases response")
		}
		if len(records) > githubReleasePageSize {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: GitHub returned more Releases than the requested page size")
		}
		if len(releases) > maxGitHubReleaseRecords-len(records) {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: release inventory exceeds %d records", maxGitHubReleaseRecords)
		}
		releases = append(releases, records...)
		if len(records) < githubReleasePageSize {
			break
		}
		if page == maxGitHubReleasePages {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: GitHub Releases pagination was not complete")
		}
	}

	snapshots := make([]githubReleaseSnapshot, 0, len(releases))
	seenReleaseIDs := make(map[int64]bool, len(releases))
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if release.Draft {
			continue
		}
		if !strings.HasPrefix(release.TagName, prefix) {
			// Repositories may host unrelated Release families. A source only
			// owns tags with its configured package-version prefix.
			continue
		}
		versionText := strings.TrimPrefix(release.TagName, prefix)
		version, parseErr := semver.Parse(versionText)
		if parseErr != nil {
			return nil, fmt.Errorf("RELEASE_TAG_INVALID: tag %q is not prefix + SemVer", release.TagName)
		}
		if release.ID <= 0 || seenReleaseIDs[release.ID] {
			return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: Release IDs are missing or duplicated")
		}
		seenReleaseIDs[release.ID] = true
		if version.HasPrerelease() != release.Prerelease {
			return nil, fmt.Errorf("RELEASE_PRERELEASE_FLAG_MISMATCH: tag %q and GitHub Release prerelease flag disagree", release.TagName)
		}
		if release.PublishedAt != "" {
			if _, err := time.Parse(time.RFC3339, release.PublishedAt); err != nil {
				return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: Release published_at is invalid")
			}
		}

		expectedAsset := releaseMetadataAssetName(source.ComponentID, source.Variant, versionText)
		var matched *githubReleaseAsset
		for index := range release.Assets {
			if release.Assets[index].Name != expectedAsset {
				continue
			}
			if matched != nil {
				return nil, fmt.Errorf("RELEASE_METADATA_ASSET_AMBIGUOUS: Release %q contains duplicate %q assets", release.TagName, expectedAsset)
			}
			matched = &release.Assets[index]
		}
		if matched == nil {
			return nil, fmt.Errorf("RELEASE_METADATA_ASSET_MISSING: Release %q has no %q metadata asset", release.TagName, expectedAsset)
		}
		if matched.Size <= 0 || matched.Size > maxReleaseMetadataBytes {
			return nil, fmt.Errorf("RELEASE_METADATA_SIZE_MISMATCH: metadata asset size is invalid")
		}
		digest, digestErr := parseGitHubAssetDigest(matched.Digest)
		if digestErr != nil {
			return nil, fmt.Errorf("RELEASE_METADATA_DIGEST_UNAVAILABLE: GitHub did not publish a valid SHA-256 asset digest")
		}
		if err := validateGitHubReleaseAssetURI(matched.BrowserDownloadURL, owner, repository, release.TagName, expectedAsset); err != nil {
			return nil, fmt.Errorf("RELEASE_METADATA_ASSET_URI_REJECTED: asset URI does not identify the expected Release Asset")
		}
		releaseAPIURI := fmt.Sprintf("%s/%d", base, release.ID)
		if release.URL != "" && release.URL != releaseAPIURI {
			return nil, fmt.Errorf("RELEASE_REPOSITORY_MISMATCH: Release API URI does not match the declared repository")
		}
		commit, resolveErr := f.resolveGitHubTagCommit(ctx, owner, repository, release.TagName)
		if resolveErr != nil {
			return nil, resolveErr
		}
		snapshots = append(snapshots, githubReleaseSnapshot{
			ReleaseID:         release.ID,
			ReleaseAPIURI:      releaseAPIURI,
			Tag:               release.TagName,
			TagCommit:         commit,
			MetadataAssetName: expectedAsset,
			MetadataAssetURI:  matched.BrowserDownloadURL,
			MetadataSHA:       digest,
			MetadataSize:      matched.Size,
			PublishedAt:       release.PublishedAt,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Tag != snapshots[j].Tag {
			return snapshots[i].Tag < snapshots[j].Tag
		}
		return snapshots[i].ReleaseID < snapshots[j].ReleaseID
	})
	return snapshots, nil
}

func (f *HTTPFetcher) fetchGitHubAPI(ctx context.Context, endpoint string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < maxGitHubAPIRetries; attempt++ {
		payload, err := f.fetch(ctx, endpoint)
		if err == nil {
			return payload, nil
		}
		var rateLimit *githubRateLimitError
		if !errors.As(err, &rateLimit) {
			return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: GitHub API request failed")
		}
		last = err
		delay := rateLimit.retryAfter
		if delay <= 0 {
			delay = time.Duration(attempt+1) * 250 * time.Millisecond
		}
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	_ = last
	return nil, fmt.Errorf("RELEASE_API_RATE_LIMITED: bounded GitHub API retry budget was exhausted")
}

func (f *HTTPFetcher) resolveGitHubTagCommit(ctx context.Context, owner, repository, tag string) (string, error) {
	base := fmt.Sprintf("https://api.github.com/repos/%s/%s/git", owner, repository)
	refURI := base + "/ref/tags/" + url.PathEscape(tag)
	payload, err := f.fetchGitHubAPI(ctx, refURI)
	if err != nil {
		return "", err
	}
	var ref githubGitRefResponse
	if err := json.Unmarshal(payload, &ref); err != nil {
		return "", fmt.Errorf("RELEASE_TAG_INVALID: tag reference response is invalid")
	}
	object := ref.Object
	seen := make(map[string]bool)
	for depth := 0; depth < maxGitHubTagPeelDepth; depth++ {
		if !isGitObjectID(object.SHA) || seen[strings.ToLower(object.SHA)] {
			return "", fmt.Errorf("RELEASE_TAG_MOVED: tag object chain is malformed or cyclic")
		}
		seen[strings.ToLower(object.SHA)] = true
		switch object.Type {
		case "commit":
			return strings.ToLower(object.SHA), nil
		case "tag":
			tagPayload, fetchErr := f.fetchGitHubAPI(ctx, base+"/tags/"+url.PathEscape(object.SHA))
			if fetchErr != nil {
				return "", fetchErr
			}
			var annotated githubTagObjectResponse
			if err := json.Unmarshal(tagPayload, &annotated); err != nil {
				return "", fmt.Errorf("RELEASE_TAG_INVALID: annotated tag object is invalid")
			}
			object = annotated.Object
		default:
			return "", fmt.Errorf("RELEASE_TAG_INVALID: tag does not resolve to a commit")
		}
	}
	return "", fmt.Errorf("RELEASE_TAG_INVALID: annotated tag depth exceeds the configured limit")
}

func validateReleaseSource(source ReleaseSource) error {
	if strings.TrimSpace(source.SourceID) == "" || !validCatalogIdentity(source.SourceID) {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: source_id is invalid")
	}
	if source.Provider != "github-releases-v1" {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: unsupported Release Source provider")
	}
	if !validCatalogIdentity(source.ComponentID) {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: component_id is invalid")
	}
	if source.Variant == "" {
		source.Variant = "default"
	}
	if !validCatalogIdentity(source.Variant) {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: variant is invalid")
	}
	if source.TagPrefix == "" {
		source.TagPrefix = "v"
	}
	if len(source.TagPrefix) > 32 {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: tag_prefix is too long")
	}
	for _, character := range source.TagPrefix {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._+-", character)) {
			return fmt.Errorf("RELEASE_SOURCE_INVALID: tag_prefix contains unsupported characters")
		}
	}
	if _, _, err := parseGitHubRepository(source.RepositoryURI); err != nil {
		return fmt.Errorf("RELEASE_SOURCE_INVALID: repository_uri must be a canonical HTTPS GitHub repository")
	}
	return nil
}

func parseGitHubRepository(repositoryURI string) (string, string, error) {
	parsed, err := url.Parse(repositoryURI)
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return "", "", fmt.Errorf("invalid GitHub repository URI")
	}
	value := strings.Trim(parsed.Path, "/")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("GitHub repository URI must contain exactly owner/repository")
	}
	repository := strings.TrimSuffix(parts[1], ".git")
	if !validGitHubPathSegment(parts[0]) || !validGitHubPathSegment(repository) {
		return "", "", fmt.Errorf("GitHub repository path contains invalid characters")
	}
	return parts[0], repository, nil
}

func validGitHubPathSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character)) {
			return false
		}
	}
	return true
}

func validCatalogIdentity(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	previousSeparator := true
	for _, character := range value {
		separator := character == '.' || character == '-'
		if separator {
			if previousSeparator {
				return false
			}
			previousSeparator = true
			continue
		}
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return false
		}
		previousSeparator = false
	}
	return !previousSeparator
}

func releaseMetadataAssetName(componentID, variant, version string) string {
	if variant == "" || variant == "default" {
		return componentID + "-" + version + ".json"
	}
	return componentID + "-" + variant + "-" + version + ".json"
}

func parseGitHubAssetDigest(value string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(value), "sha256:") {
		return "", fmt.Errorf("missing GitHub SHA-256 digest")
	}
	digest := strings.ToLower(strings.TrimPrefix(strings.ToLower(value), "sha256:"))
	if len(digest) != 64 {
		return "", fmt.Errorf("invalid GitHub SHA-256 digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid GitHub SHA-256 digest")
	}
	return digest, nil
}

func validateGitHubReleaseAssetURI(value, owner, repository, tag, name string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return fmt.Errorf("asset URI is not canonical HTTPS")
	}
	expected := path.Join("/", owner, repository, "releases", "download", tag, name)
	if path.Clean(parsed.Path) != expected {
		return fmt.Errorf("asset URI path does not match expected Release")
	}
	return nil
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *MetadataCache) readGitHubReleaseSnapshot(source ReleaseSource) ([]githubReleaseSnapshot, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	directory := filepath.Join(c.control, "release-sources")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("unsafe Release Source cache directory")
	}
	key := sha256.Sum256([]byte(source.SourceID))
	cachePath := filepath.Join(directory, hex.EncodeToString(key[:])+".json")
	fileInfo, err := os.Lstat(cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() > maxGitHubDiscoveryCacheSize {
		return nil, false, fmt.Errorf("cached Release Source snapshot is unsafe")
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false, err
	}
	var envelope githubDiscoverySnapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Version != 1 {
		return nil, false, fmt.Errorf("cached Release Source snapshot is malformed")
	}
	identity, err := githubReleaseSourceIdentity(source)
	if err != nil || envelope.SourceIdentitySHA256 != identity {
		return nil, false, fmt.Errorf("cached Release Source identity changed")
	}
	payload, err := json.Marshal(envelope.Releases)
	if err != nil || sha256Hex(payload) != envelope.PayloadSHA256 {
		return nil, false, fmt.Errorf("cached Release Source snapshot digest mismatch")
	}
	return envelope.Releases, true, nil
}

func (c *MetadataCache) writeGitHubReleaseSnapshot(source ReleaseSource, releases []githubReleaseSnapshot) error {
	if c == nil {
		return nil
	}
	identity, err := githubReleaseSourceIdentity(source)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(releases)
	if err != nil {
		return err
	}
	envelope := githubDiscoverySnapshotEnvelope{
		Version:              1,
		SourceIdentitySHA256: identity,
		PayloadSHA256:        sha256Hex(payload),
		Releases:             releases,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if len(data) > maxGitHubDiscoveryCacheSize {
		return fmt.Errorf("Release Source snapshot exceeds cache size limit")
	}
	directory := filepath.Join(c.control, "release-sources")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkAncestors(directory); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	key := sha256.Sum256([]byte(source.SourceID))
	destination := filepath.Join(directory, hex.EncodeToString(key[:])+".json")
	temporary, err := os.CreateTemp(directory, ".release-source-*.tmp")
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
	return os.Rename(temporaryPath, destination)
}

func githubReleaseSourceIdentity(source ReleaseSource) (string, error) {
	if source.Variant == "" {
		source.Variant = "default"
	}
	if source.TagPrefix == "" {
		source.TagPrefix = "v"
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}


// loadGitHubReleaseSources resolves one Catalog's dynamic Release Sources into
// the same immutable Release candidates consumed by static Catalog resolution.
// The operation is all-or-nothing: one malformed or incomplete source prevents
// publication of a partial candidate set.
func loadGitHubReleaseSources(ctx context.Context, index Index, fetcher Fetcher, validator Validator) (*Catalog, error) {
	discoveryFetcher, ok := fetcher.(ReleaseDiscoveryFetcher)
	if !ok {
		return nil, fmt.Errorf("catalog: fetcher does not support GitHub Release discovery")
	}
	if ctx == nil || validator == nil {
		return nil, fmt.Errorf("catalog: context and validator are required for Release Source discovery")
	}
	if len(index.ReleaseSources) == 0 || len(index.ReleaseSources) > maxCatalogReleaseSources {
		return nil, fmt.Errorf("catalog: invalid Release Source count")
	}

	sources := append([]ReleaseSource(nil), index.ReleaseSources...)
	seenSourceIDs := make(map[string]bool, len(sources))
	seenComponentVariants := make(map[string]bool, len(sources))
	for i := range sources {
		source := &sources[i]
		if err := validateReleaseSource(*source); err != nil {
			return nil, err
		}
		if source.Variant == "" {
			source.Variant = "default"
		}
		if source.TagPrefix == "" {
			source.TagPrefix = "v"
		}
		if seenSourceIDs[source.SourceID] {
			return nil, fmt.Errorf("RELEASE_SOURCE_INVALID: duplicate source_id %q", source.SourceID)
		}
		seenSourceIDs[source.SourceID] = true
		key := source.ComponentID + "\x00" + source.Variant
		if seenComponentVariants[key] {
			return nil, fmt.Errorf("RELEASE_SOURCE_INVALID: duplicate component/variant mapping %s/%s", source.ComponentID, source.Variant)
		}
		seenComponentVariants[key] = true
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ComponentID != sources[j].ComponentID {
			return sources[i].ComponentID < sources[j].ComponentID
		}
		if sources[i].Variant != sources[j].Variant {
			return sources[i].Variant < sources[j].Variant
		}
		return sources[i].SourceID < sources[j].SourceID
	})

	out := &Catalog{ID: index.CatalogID}
	seenReleases := make(map[identity]Release)
	var totalMetadataBytes int64
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		discovered, err := discoveryFetcher.DiscoverGitHubReleases(ctx, source)
		if err != nil {
			return nil, err
		}
		if len(discovered) > maxGitHubReleaseRecords {
			return nil, fmt.Errorf("RELEASE_DISCOVERY_LIMIT: source %q returned too many metadata documents", source.SourceID)
		}
		for _, item := range discovered {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(item.Payload) == 0 || len(item.Payload) > maxReleaseMetadataBytes || item.MetadataSize != int64(len(item.Payload)) {
				return nil, fmt.Errorf("RELEASE_METADATA_SIZE_MISMATCH: metadata bytes do not match the declared size")
			}
			nextTotal, sizeErr := accountCatalogMetadataBytes(totalMetadataBytes, int64(len(item.Payload)))
			if sizeErr != nil {
				return nil, sizeErr
			}
			totalMetadataBytes = nextTotal

			actualDigest := sha256.Sum256(item.Payload)
			actualSHA := hex.EncodeToString(actualDigest[:])
			if !isSHA256(item.MetadataSHA) || strings.ToLower(item.MetadataSHA) != actualSHA {
				return nil, fmt.Errorf("RELEASE_METADATA_DIGEST_MISMATCH: downloaded metadata bytes do not match the GitHub Release Asset digest")
			}
			if strings.TrimSpace(item.MetadataURI) == "" || item.Discovery.MetadataAssetName == "" {
				return nil, fmt.Errorf("RELEASE_METADATA_ASSET_MISSING: discovered metadata asset identity is incomplete")
			}
			if item.Discovery.SourceID != source.SourceID ||
				item.Discovery.Provider != "github-releases-v1" ||
				item.Discovery.RepositoryURI != source.RepositoryURI ||
				item.Discovery.MetadataAssetName != releaseMetadataAssetName(source.ComponentID, source.Variant, strings.TrimSuffix(strings.TrimPrefix(item.Discovery.Tag, source.TagPrefix), "")) {
				return nil, fmt.Errorf("RELEASE_REPOSITORY_MISMATCH: discovery result does not match its Catalog Release Source")
			}
			if !isGitObjectID(item.Discovery.TagCommit) {
				return nil, fmt.Errorf("RELEASE_TAG_INVALID: discovered tag commit is not a full Git object ID")
			}
			if item.Discovery.ReleaseID <= 0 || item.Discovery.ReleaseAPIURI == "" ||
				item.Discovery.MetadataAssetSizeBytes != int64(len(item.Payload)) {
				return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: discovered Release identity is incomplete")
			}
			if err := validator.Validate(validation.ComponentRelease, item.Payload, "component-release.json"); err != nil {
				return nil, fmt.Errorf("catalog: validate discovered release %s: %w", item.Discovery.MetadataAssetName, err)
			}
			var release Release
			if err := json.Unmarshal(item.Payload, &release); err != nil {
				return nil, fmt.Errorf("catalog: decode discovered release metadata: %w", err)
			}
			if release.Variant == "" {
				release.Variant = "default"
			}
			version, versionErr := semver.Parse(release.Version)
			if versionErr != nil {
				return nil, fmt.Errorf("RELEASE_TAG_VERSION_MISMATCH: metadata version is not valid SemVer")
			}
			prefix := source.TagPrefix
			if prefix == "" {
				prefix = "v"
			}
			if item.Discovery.Tag != prefix+release.Version {
				return nil, fmt.Errorf("RELEASE_TAG_VERSION_MISMATCH: release tag and metadata version disagree")
			}
			expectedAsset := releaseMetadataAssetName(source.ComponentID, source.Variant, release.Version)
			if item.Discovery.MetadataAssetName != expectedAsset {
				return nil, fmt.Errorf("RELEASE_METADATA_ASSET_MISSING: metadata asset name does not match the canonical component/variant/version name")
			}
			publication := release.Publication
			if publication.Provider != "github-releases-v1" ||
				publication.RepositoryURI != source.RepositoryURI ||
				publication.Tag != item.Discovery.Tag ||
				!strings.EqualFold(publication.TagCommit, item.Discovery.TagCommit) ||
				publication.MetadataAssetName != expectedAsset {
				return nil, fmt.Errorf("RELEASE_TAG_MOVED: publisher metadata does not match the discovered immutable GitHub Release identity")
			}
			publisherSources := 0
			for _, sourceUnit := range release.SourceUnits {
				canonicalRepo, repoErr := canonicalGitHubRepository(sourceUnit.RepositoryURI)
				sourceRepo, sourceRepoErr := canonicalGitHubRepository(source.RepositoryURI)
				if repoErr == nil && sourceRepoErr == nil && canonicalRepo == sourceRepo {
					publisherSources++
					if !strings.EqualFold(sourceUnit.Revision, item.Discovery.TagCommit) {
						return nil, fmt.Errorf("RELEASE_SOURCE_REVISION_MISMATCH: publisher source unit does not match the Git tag commit")
					}
				}
			}
			if publisherSources != 1 {
				return nil, fmt.Errorf("RELEASE_REPOSITORY_MISMATCH: metadata must declare exactly one source unit for its publishing repository")
			}
			if release.ComponentID != source.ComponentID || release.Variant != source.Variant {
				return nil, fmt.Errorf("RELEASE_REPOSITORY_MISMATCH: metadata component/variant does not match its Catalog Release Source")
			}
			for artifactIndex := range release.Artifacts {
				release.Artifacts[artifactIndex].Target = release.Artifacts[artifactIndex].Target.Normalize()
			}
			if err := ValidateReleaseArtifacts(release.SourceUnits, release.Artifacts); err != nil {
				return nil, fmt.Errorf("catalog: validate artifacts for %s/%s@%s: %w", release.ComponentID, release.Variant, release.Version, err)
			}

			release.Raw = append([]byte(nil), item.Payload...)
			release.MetadataURI = item.MetadataURI
			release.MetadataSHA = actualSHA
			discovery := item.Discovery
			release.Discovery = &discovery

			key := identity{component: release.ComponentID, variant: release.Variant, version: release.Version}
			if previous, exists := seenReleases[key]; exists {
				if previous.MetadataSHA != release.MetadataSHA ||
					previous.Discovery == nil || previous.Discovery.ReleaseID != discovery.ReleaseID {
					return nil, fmt.Errorf("RELEASE_VERSION_AMBIGUOUS: multiple immutable GitHub Releases publish %s/%s@%s", key.component, key.variant, key.version)
				}
				continue
			}
			seenReleases[key] = release
			out.Releases = append(out.Releases, release)
			_ = version // SemVer is validated here; ordering is owned by the resolver.
		}
	}
	sort.Slice(out.Releases, func(i, j int) bool {
		left, right := out.Releases[i], out.Releases[j]
		if left.ComponentID != right.ComponentID {
			return left.ComponentID < right.ComponentID
		}
		if left.Variant != right.Variant {
			return left.Variant < right.Variant
		}
		if left.Version != right.Version {
			leftVersion, _ := semver.Parse(left.Version)
			rightVersion, _ := semver.Parse(right.Version)
			return semver.ComparePrecedence(leftVersion, rightVersion) > 0
		}
		return left.MetadataSHA < right.MetadataSHA
	})
	return out, nil
}

func canonicalGitHubRepository(repositoryURI string) (string, error) {
	owner, repository, err := parseGitHubRepository(repositoryURI)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + strings.ToLower(owner) + "/" + strings.ToLower(repository), nil
}

func (f *HTTPFetcher) FetchGitHubAPIForRelease(ctx context.Context, endpoint string) ([]byte, error) {
	if f == nil || f.offline {
		return nil, fmt.Errorf("RELEASE_DISCOVERY_OFFLINE_MISS: GitHub API is unavailable in offline mode")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "api.github.com" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" {
		return nil, fmt.Errorf("RELEASE_API_UNAVAILABLE: invalid GitHub API endpoint")
	}
	return f.fetchGitHubAPI(ctx, endpoint)
}

func (f *HTTPFetcher) fetchWithRetry(ctx context.Context, endpoint string) ([]byte, error) {
	return f.fetchGitHubAPI(ctx, endpoint)
}

var _ ReleaseDiscoveryFetcher = (*HTTPFetcher)(nil)
