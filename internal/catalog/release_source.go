package catalog

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/cdin-editor/registry/internal/semver"
)

const (
	githubReleaseProvider   = "github-releases-v1"
	defaultReleaseTagPrefix = "v"
	maxGitHubReleasePages   = 20
	githubReleasePageSize   = 100
	maxGitHubTagDepth       = 8
)

var catalogIdentifierPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
var releaseTagPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{0,32}$`)

// ReleaseSource declares one repository that publishes versions of one logical
// component/variant. It is deliberately generic: no consumer/product identity
// is encoded in this data model.
type ReleaseSource struct {
	SourceID      string `json:"source_id"`
	Provider      string `json:"provider"`
	RepositoryURI string `json:"repository_uri"`
	ComponentID   string `json:"component_id"`
	Variant       string `json:"variant,omitempty"`
	TagPrefix     string `json:"tag_prefix,omitempty"`
}

// Publication binds Component Release Metadata to the exact GitHub Release tag
// and tag-resolved commit that published it. The metadata asset's own URI,
// digest and size are intentionally supplied by the GitHub Release API.
type Publication struct {
	Provider          string `json:"provider"`
	RepositoryURI     string `json:"repository_uri"`
	Tag               string `json:"tag"`
	TagCommit         string `json:"tag_commit"`
	MetadataAssetName string `json:"metadata_asset_name"`
}

// GitHubReleaseAsset is the bounded subset of the GitHub Release Asset API
// needed to verify and retrieve one metadata asset.
type GitHubReleaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// GitHubRelease is the bounded subset of the GitHub Releases API response used
// for deterministic discovery. API ordering and PublishedAt are provenance,
// never SemVer selection inputs.
type GitHubRelease struct {
	ID          int64                `json:"id"`
	TagName     string               `json:"tag_name"`
	Draft       bool                 `json:"draft"`
	Prerelease  bool                 `json:"prerelease"`
	PublishedAt string               `json:"published_at"`
	HTMLURL     string               `json:"html_url"`
	URL         string               `json:"url"`
	Assets      []GitHubReleaseAsset `json:"assets"`
}

// GitHubReleaseClient is the narrow provider adapter required by dynamic
// Catalog discovery. Implementations must paginate boundedly, resolve
// lightweight and annotated tags to a full commit, enforce HTTPS/host/IP
// policy, bound response sizes, and honor cancellation.
type GitHubReleaseClient interface {
	ListGitHubReleases(context.Context, ReleaseSource, int, int) ([]GitHubRelease, error)
	ResolveGitHubTagCommit(context.Context, ReleaseSource, string) (string, error)
	FetchGitHubReleaseAsset(context.Context, string, int64) ([]byte, error)
}

// GitHubReleaseAssetIdentityFetcher binds downloaded bytes and their cache key
// to the Release Source, Release ID, tag commit and immutable asset identity.
type GitHubReleaseAssetIdentityFetcher interface {
	FetchGitHubReleaseAssetWithIdentity(context.Context, ReleaseSource, GitHubRelease, string, GitHubReleaseAsset, int64) ([]byte, error)
}

type GitHubReleaseManifestAssetFetcher interface {
	FetchGitHubReleaseAssetFromManifest(context.Context, ReleaseDiscoveryProvenance, string, string) ([]byte, error)
}

// ReleaseDiscoveryProvenance preserves the exact publication identity used to
// construct a candidate. It is copied into the Resolved Manifest for replay.
type ReleaseDiscoveryProvenance struct {
	SourceID          string `json:"source_id"`
	Provider          string `json:"provider"`
	RepositoryURI     string `json:"repository_uri"`
	ReleaseID         int64  `json:"release_id"`
	ReleaseAPIURI     string `json:"release_api_uri"`
	Tag               string `json:"tag"`
	TagCommit         string `json:"tag_commit"`
	MetadataAssetName string `json:"metadata_asset_name"`
	MetadataAssetSize int64  `json:"metadata_asset_size_bytes"`
	PublishedAt       string `json:"published_at,omitempty"`
}

// ValidateReleaseSources enforces semantic constraints that are intentionally
// not encoded as consumer-specific rules in the JSON Schema.
func ValidateReleaseSources(sources []ReleaseSource) ([]ReleaseSource, error) {
	if len(sources) == 0 || len(sources) > maxCatalogEntries {
		return nil, fmt.Errorf("catalog: release_sources count must be between 1 and %d", maxCatalogEntries)
	}
	out := make([]ReleaseSource, len(sources))
	seenIDs := make(map[string]struct{}, len(sources))
	seenMappings := make(map[string]struct{}, len(sources))
	for i, source := range sources {
		if !catalogIdentifierPattern.MatchString(source.SourceID) || len(source.SourceID) > 128 {
			return nil, fmt.Errorf("catalog: Release Source %d has an invalid source_id", i)
		}
		if _, exists := seenIDs[source.SourceID]; exists {
			return nil, fmt.Errorf("catalog: duplicate Release Source ID")
		}
		seenIDs[source.SourceID] = struct{}{}
		if source.Provider != githubReleaseProvider {
			return nil, fmt.Errorf("catalog: unsupported Release Source provider")
		}
		repositoryURI, err := CanonicalGitHubRepositoryURI(source.RepositoryURI)
		if err != nil {
			return nil, fmt.Errorf("catalog: Release Source repository URI is invalid")
		}
		source.RepositoryURI = repositoryURI
		if !catalogIdentifierPattern.MatchString(source.ComponentID) || len(source.ComponentID) > 128 {
			return nil, fmt.Errorf("catalog: Release Source component_id is invalid")
		}
		if source.Variant == "" {
			source.Variant = "default"
		}
		if !catalogIdentifierPattern.MatchString(source.Variant) || len(source.Variant) > 64 {
			return nil, fmt.Errorf("catalog: Release Source variant is invalid")
		}
		if source.TagPrefix == "" {
			source.TagPrefix = defaultReleaseTagPrefix
		}
		if len(source.TagPrefix) > 32 || !releaseTagPrefixPattern.MatchString(source.TagPrefix) {
			return nil, fmt.Errorf("catalog: Release Source tag_prefix is invalid")
		}
		mapping := source.ComponentID + "\x00" + source.Variant
		if _, exists := seenMappings[mapping]; exists {
			return nil, fmt.Errorf("catalog: duplicate component/variant Release Source mapping")
		}
		seenMappings[mapping] = struct{}{}
		out[i] = source
	}
	return out, nil
}

// CanonicalGitHubRepositoryURI accepts only a canonical HTTPS GitHub repository
// URI (optional trailing slash). Credentials, ports, queries, fragments,
// nested paths and alternative hosts are rejected.
func CanonicalGitHubRepositoryURI(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil || !parsed.IsAbs() ||
		!strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "github.com") ||
		parsed.Port() != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("invalid GitHub repository URI")
	}
	trimmed := strings.Trim(parsed.EscapedPath(), "/")
	segments := strings.Split(trimmed, "/")
	if len(segments) != 2 {
		return "", fmt.Errorf("GitHub repository URI must contain owner and repository")
	}
	owner, err := url.PathUnescape(segments[0])
	if err != nil || !validGitHubPathSegment(owner) {
		return "", fmt.Errorf("invalid GitHub repository owner")
	}
	repository, err := url.PathUnescape(segments[1])
	if err != nil || !validGitHubPathSegment(repository) {
		return "", fmt.Errorf("invalid GitHub repository name")
	}
	return "https://github.com/" + owner + "/" + strings.TrimSuffix(repository, ".git"), nil
}

func validGitHubPathSegment(value string) bool {
	if value == "" || value == "." || value == ".." || path.Base(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// ExpectedMetadataAssetName returns the canonical ADR-0028 basename for the
// source's configured variant and the exact parsed SemVer release.
func ExpectedMetadataAssetName(source ReleaseSource, version string) (string, error) {
	parsed, err := semver.Parse(version)
	if err != nil {
		return "", fmt.Errorf("catalog: invalid release SemVer")
	}
	if source.Variant == "" || source.Variant == "default" {
		return source.ComponentID + "-" + parsed.Original + ".json", nil
	}
	return source.ComponentID + "-" + source.Variant + "-" + parsed.Original + ".json", nil
}

// ValidateGitHubReleaseTag binds a release tag to a configured prefix and a
// valid SemVer identity. It never uses publication time or API ordering.
func ValidateGitHubReleaseTag(source ReleaseSource, tag string) (string, semver.Version, error) {
	prefix := source.TagPrefix
	if prefix == "" {
		prefix = defaultReleaseTagPrefix
	}
	if !strings.HasPrefix(tag, prefix) || len(tag) == len(prefix) {
		return "", semver.Version{}, fmt.Errorf("catalog: GitHub Release tag does not match configured prefix")
	}
	versionText := strings.TrimPrefix(tag, prefix)
	version, err := semver.Parse(versionText)
	if err != nil {
		return "", semver.Version{}, fmt.Errorf("catalog: GitHub Release tag is not valid SemVer")
	}
	return versionText, version, nil
}

// ValidateGitHubAssetDigest normalizes GitHub's sha256:<hex> asset digest.
func ValidateGitHubAssetDigest(value string) (string, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "sha256") || !isSHA256Value(parts[1]) {
		return "", fmt.Errorf("catalog: GitHub Release metadata asset has no valid SHA-256 digest")
	}
	return strings.ToLower(parts[1]), nil
}

// githubRepositoryParts returns owner/repository path components from a
// canonical GitHub repository URI. It never accepts arbitrary API base URLs.
func githubRepositoryParts(repositoryURI string) (string, string, error) {
	canonical, err := CanonicalGitHubRepositoryURI(repositoryURI)
	if err != nil {
		return "", "", fmt.Errorf("catalog: invalid GitHub repository identity")
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return "", "", fmt.Errorf("catalog: invalid GitHub repository identity")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || !validGitHubPathSegment(parts[0]) || !validGitHubPathSegment(parts[1]) {
		return "", "", fmt.Errorf("catalog: invalid GitHub repository identity")
	}
	return parts[0], parts[1], nil
}
