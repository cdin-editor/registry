package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cdin-editor/registry/internal/semver"
	"github.com/cdin-editor/registry/internal/validation"
)

func loadFromGitHubReleaseSources(
	ctx context.Context,
	index Index,
	fetcher Fetcher,
	validator Validator,
	concurrency int,
) (*Catalog, error) {
	client, ok := fetcher.(GitHubReleaseClient)
	if !ok {
		return nil, fmt.Errorf("catalog: GitHub Release discovery requires a GitHub Release client")
	}
	sources, err := ValidateReleaseSources(index.ReleaseSources)
	if err != nil {
		return nil, err
	}
	out := &Catalog{
		ID:             index.CatalogID,
		ReleaseSources: sources,
	}
	seen := make(map[identity]Release)
	var totalMetadataBytes int64

	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pagesComplete := false
		for page := 1; page <= maxGitHubReleasePages; page++ {
			releases, err := client.ListGitHubReleases(ctx, source, page, githubReleasePageSize)
			if err != nil {
				return nil, fmt.Errorf("catalog: discover GitHub Releases for source %s: %w", source.SourceID, err)
			}
			if len(releases) > githubReleasePageSize {
				return nil, fmt.Errorf("catalog: GitHub Release page exceeded the configured item limit")
			}
			for _, githubRelease := range releases {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if githubRelease.Draft {
					continue
				}
				versionText, version, err := ValidateGitHubReleaseTag(source, githubRelease.TagName)
				if err != nil {
					return nil, fmt.Errorf("catalog: invalid GitHub Release tag for source %s: %w", source.SourceID, err)
				}
				if githubRelease.ID <= 0 {
					return nil, fmt.Errorf("catalog: GitHub Release identity is incomplete for source %s", source.SourceID)
				}
				owner, repository, repoErr := githubRepositoryParts(source.RepositoryURI)
				if repoErr != nil {
					return nil, repoErr
				}
				expectedReleaseAPIURI := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/%d", owner, repository, githubRelease.ID)
				expectedReleaseHTMLURI := fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repository, githubRelease.TagName)
				if githubRelease.URL != expectedReleaseAPIURI || githubRelease.HTMLURL != expectedReleaseHTMLURI {
					return nil, fmt.Errorf("catalog: GitHub Release API/browser URI does not match the configured source")
				}
				if githubRelease.Prerelease != (len(version.Pre) > 0) {
					return nil, fmt.Errorf("catalog: GitHub Release prerelease flag does not match SemVer for tag %s", githubRelease.TagName)
				}
				expectedAsset, err := ExpectedMetadataAssetName(source, versionText)
				if err != nil {
					return nil, err
				}
				var matched []GitHubReleaseAsset
				for _, asset := range githubRelease.Assets {
					if asset.Name == expectedAsset {
						matched = append(matched, asset)
					}
				}
				if len(matched) != 1 {
					return nil, fmt.Errorf("catalog: GitHub Release %s must contain exactly one metadata asset named %s (found %d)", githubRelease.TagName, expectedAsset, len(matched))
				}
				asset := matched[0]
				if asset.ID <= 0 || asset.Size <= 0 || asset.Size > maxReleaseMetadataBytes {
					return nil, fmt.Errorf("catalog: GitHub Release metadata asset identity or size is invalid for %s", githubRelease.TagName)
				}
				expectedAssetAPIURI := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/assets/%d", owner, repository, asset.ID)
				if asset.URL != expectedAssetAPIURI || !validArtifactHTTPSURI(asset.BrowserDownloadURL) {
					return nil, fmt.Errorf("catalog: GitHub Release metadata asset URI does not match the configured source")
				}
				browserURL, _ := url.Parse(asset.BrowserDownloadURL)
				expectedBrowserPath := fmt.Sprintf("/%s/%s/releases/download/%s/%s", owner, repository, githubRelease.TagName, expectedAsset)
				if browserURL.Host != "github.com" || browserURL.Path != expectedBrowserPath || browserURL.RawQuery != "" || browserURL.Fragment != "" {
					return nil, fmt.Errorf("catalog: GitHub Release metadata download URI does not match the asset identity")
				}
				if githubRelease.PublishedAt != "" {
					if _, timeErr := time.Parse(time.RFC3339, githubRelease.PublishedAt); timeErr != nil {
						return nil, fmt.Errorf("catalog: GitHub Release published_at timestamp is invalid")
					}
				}
				expectedSHA, err := ValidateGitHubAssetDigest(asset.Digest)
				if err != nil {
					return nil, fmt.Errorf("catalog: invalid GitHub Release metadata digest for %s: %w", githubRelease.TagName, err)
				}
				tagCommit, err := client.ResolveGitHubTagCommit(ctx, source, githubRelease.TagName)
				if err != nil {
					return nil, fmt.Errorf("catalog: resolve GitHub Release tag %s: %w", githubRelease.TagName, err)
				}
				if !validGitRevision(tagCommit) {
					return nil, fmt.Errorf("catalog: GitHub tag %s did not resolve to a full commit SHA", githubRelease.TagName)
				}
				var payload []byte
				if digestFetcher, supportsDigest := fetcher.(DigestFetcher); supportsDigest {
					payload, err = digestFetcher.FetchWithDigest(ctx, asset.BrowserDownloadURL, expectedSHA)
				} else {
					payload, err = client.FetchGitHubReleaseAsset(ctx, asset.BrowserDownloadURL, maxReleaseMetadataBytes)
				}
				if err != nil {
					return nil, fmt.Errorf("catalog: fetch GitHub Release metadata asset %s: %w", expectedAsset, err)
				}
				if int64(len(payload)) != asset.Size {
					return nil, fmt.Errorf("catalog: GitHub Release metadata asset size mismatch for %s", expectedAsset)
				}
				sum := sha256.Sum256(payload)
				actualSHA := hex.EncodeToString(sum[:])
				if actualSHA != expectedSHA {
					return nil, fmt.Errorf("catalog: GitHub Release metadata asset digest mismatch for %s", expectedAsset)
				}
				nextTotal, sizeErr := accountCatalogMetadataBytes(totalMetadataBytes, int64(len(payload)))
				if sizeErr != nil {
					return nil, sizeErr
				}
				totalMetadataBytes = nextTotal
				if err := validator.Validate(validation.ComponentRelease, payload, "component-release.json"); err != nil {
					return nil, fmt.Errorf("catalog: validate discovered Release Metadata %s: %w", expectedAsset, err)
				}
				var release Release
				if err := json.Unmarshal(payload, &release); err != nil {
					return nil, fmt.Errorf("catalog: decode discovered Release Metadata %s: %w", expectedAsset, err)
				}
				if release.Variant == "" {
					release.Variant = "default"
				}
				if release.ComponentID != source.ComponentID ||
					release.Variant != source.Variant ||
					release.Version != versionText {
					return nil, fmt.Errorf("catalog: discovered Release Metadata identity does not match source/tag %s", githubRelease.TagName)
				}
				publication := release.Publication
				if publication == nil {
					return nil, fmt.Errorf("catalog: discovered Release Metadata %s has no ADR-0028 publication identity", expectedAsset)
				}
				publicationRepository, repoErr := CanonicalGitHubRepositoryURI(publication.RepositoryURI)
				if repoErr != nil || publication.Provider != githubReleaseProvider ||
					publicationRepository != source.RepositoryURI ||
					publication.Tag != githubRelease.TagName ||
					!strings.EqualFold(publication.TagCommit, tagCommit) ||
					publication.MetadataAssetName != expectedAsset {
					return nil, fmt.Errorf("catalog: ADR-0028 publication identity does not match GitHub Release %s", githubRelease.TagName)
				}
				publisherUnits := 0
				for _, unit := range release.SourceUnits {
					unitRepository, unitErr := CanonicalGitHubRepositoryURI(unit.RepositoryURI)
					if unitErr == nil && unitRepository == source.RepositoryURI {
						publisherUnits++
						if !strings.EqualFold(unit.Revision, tagCommit) {
							return nil, fmt.Errorf("catalog: publisher source revision does not match tag commit for %s", githubRelease.TagName)
						}
					}
				}
				if publisherUnits != 1 {
					return nil, fmt.Errorf("catalog: discovered Release Metadata %s must contain exactly one publisher source unit", expectedAsset)
				}
				for artifactIndex := range release.Artifacts {
					release.Artifacts[artifactIndex].Target = release.Artifacts[artifactIndex].Target.Normalize()
				}
				if err := ValidateReleaseArtifacts(release.SourceUnits, release.Artifacts); err != nil {
					return nil, fmt.Errorf("catalog: validate artifacts for discovered release %s: %w", githubRelease.TagName, err)
				}
				variant := release.Variant
				key := identity{release.ComponentID, variant, release.Version}
				if prior, exists := seen[key]; exists {
					if prior.MetadataSHA != actualSHA || prior.Discovery == nil ||
						prior.Discovery.ReleaseID != githubRelease.ID ||
						prior.Discovery.TagCommit != tagCommit {
						return nil, fmt.Errorf("catalog: ambiguous GitHub Release identity for %s/%s@%s", key.component, key.variant, key.version)
					}
					return nil, fmt.Errorf("catalog: duplicate GitHub Release candidate for %s/%s@%s", key.component, key.variant, key.version)
				}
				release.Raw = append([]byte(nil), payload...)
				release.MetadataURI = asset.BrowserDownloadURL
				release.MetadataSHA = actualSHA
				release.Discovery = &ReleaseDiscoveryProvenance{
					SourceID:          source.SourceID,
					Provider:          githubReleaseProvider,
					RepositoryURI:     source.RepositoryURI,
					ReleaseID:         githubRelease.ID,
					ReleaseAPIURI:     githubRelease.URL,
					Tag:               githubRelease.TagName,
					TagCommit:         strings.ToLower(tagCommit),
					MetadataAssetName: expectedAsset,
					MetadataAssetSize: asset.Size,
					PublishedAt:       githubRelease.PublishedAt,
				}
				seen[key] = release
				if release.Status != "withdrawn" {
					out.Releases = append(out.Releases, release)
				}
			}
			if len(releases) < githubReleasePageSize {
				pagesComplete = true
				break
			}
			if page == maxGitHubReleasePages {
				return nil, fmt.Errorf("catalog: GitHub Release discovery limit reached before all pages were examined")
			}
		}
		if !pagesComplete {
			return nil, fmt.Errorf("catalog: GitHub Release discovery did not complete")
		}
	}
	if len(out.Releases) == 0 {
		return nil, fmt.Errorf("catalog: GitHub Release sources produced no active compatible release candidates")
	}
	sort.Slice(out.Releases, func(i, j int) bool {
		left, _ := semver.Parse(out.Releases[i].Version)
		right, _ := semver.Parse(out.Releases[j].Version)
		if order := semver.ComparePrecedence(left, right); order != 0 {
			return order < 0
		}
		if out.Releases[i].ComponentID != out.Releases[j].ComponentID {
			return out.Releases[i].ComponentID < out.Releases[j].ComponentID
		}
		if out.Releases[i].Variant != out.Releases[j].Variant {
			return out.Releases[i].Variant < out.Releases[j].Variant
		}
		return out.Releases[i].MetadataSHA < out.Releases[j].MetadataSHA
	})
	return out, nil
}

func validGitRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

