package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type discoveryMapFetcher struct {
	releases map[string][]DiscoveredRelease
	calls    int
}

func (f *discoveryMapFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("static Fetch must not be called for release_sources")
}

func (f *discoveryMapFetcher) DiscoverGitHubReleases(_ context.Context, source ReleaseSource) ([]DiscoveredRelease, error) {
	f.calls++
	items, ok := f.releases[source.SourceID]
	if !ok {
		return nil, fmt.Errorf("missing source %s", source.SourceID)
	}
	return append([]DiscoveredRelease(nil), items...), nil
}

func dynamicReleaseFixture(t *testing.T, component, variant, version, sourceID, repository, tag string, releaseID int64) DiscoveredRelease {
	t.Helper()
	commit := strings.Repeat("a", 40)
	assetName := releaseMetadataAssetName(component, variant, version)
	apiOwner, apiRepository, err := parseGitHubRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	metadataURI := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", apiOwner, apiRepository, tag, assetName)
	apiURI := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/%d", apiOwner, apiRepository, releaseID)
	document := map[string]any{
		"schema_version": "1.2.0",
		"component_id":   component,
		"variant":        variant,
		"version":        version,
		"status":         "active",
		"source_units": []map[string]any{{
			"source_unit_id": "publisher-source",
			"repository_uri": repository,
			"revision":       commit,
		}},
		"publication": map[string]any{
			"provider":            "github-releases-v1",
			"repository_uri":      repository,
			"tag":                 tag,
			"tag_commit":          commit,
			"metadata_asset_name": assetName,
		},
		"dependencies": []any{},
		"contracts": map[string]any{
			"provides": []any{},
			"requires": []any{},
		},
		"artifacts": []any{},
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	return DiscoveredRelease{
		Payload:      payload,
		MetadataURI:  metadataURI,
		MetadataSHA:  hex.EncodeToString(sum[:]),
		MetadataSize: int64(len(payload)),
		Discovery: ReleaseDiscovery{
			SourceID:               sourceID,
			Provider:               "github-releases-v1",
			RepositoryURI:          repository,
			ReleaseID:              releaseID,
			ReleaseAPIURI:           apiURI,
			Tag:                    tag,
			TagCommit:              commit,
			MetadataAssetName:      assetName,
			MetadataAssetSizeBytes: int64(len(payload)),
			PublishedAt:            "2026-11-10T12:00:00Z",
		},
	}
}

func dynamicCatalogBytes(t *testing.T, sources ...ReleaseSource) []byte {
	t.Helper()
	data, err := json.Marshal(Index{
		SchemaVersion:  "1.1.0",
		CatalogID:      "test.dynamic",
		ReleaseSources: sources,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLoadDiscoversAndValidatesGitHubReleaseMetadata(t *testing.T) {
	source := ReleaseSource{
		SourceID:      "backend-default",
		Provider:      "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend",
		ComponentID:   "backend",
		Variant:       "default",
		TagPrefix:     "v",
		MinimumVersion: "1.2.1",
	}
	item := dynamicReleaseFixture(t, "backend", "default", "1.2.1", source.SourceID, source.RepositoryURI, "v1.2.1", 42)
	fetcher := &discoveryMapFetcher{releases: map[string][]DiscoveredRelease{source.SourceID: {item}}}
	catalogue, err := Load(context.Background(), dynamicCatalogBytes(t, source), fetcher, &acceptingValidator{})
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("discovery calls = %d, want 1", fetcher.calls)
	}
	if len(catalogue.Releases) != 1 {
		t.Fatalf("discovered releases = %d, want 1", len(catalogue.Releases))
	}
	release := catalogue.Releases[0]
	if release.ComponentID != "backend" || release.Variant != "default" || release.Version != "1.2.1" {
		t.Fatalf("unexpected release identity: %#v", release)
	}
	if release.MetadataURI != item.MetadataURI || release.MetadataSHA != item.MetadataSHA {
		t.Fatalf("metadata identity was not preserved: %#v", release)
	}
	if release.Discovery == nil || release.Discovery.ReleaseID != 42 || release.Discovery.TagCommit != item.Discovery.TagCommit {
		t.Fatalf("Release discovery provenance was not preserved: %#v", release.Discovery)
	}
}

func TestLoadRejectsMixedStaticAndDynamicCatalog(t *testing.T) {
	index := Index{
		SchemaVersion: "1.1.0",
		CatalogID:     "test.mixed",
		Entries:       []Entry{{ComponentID: "backend", Version: "1.2.1", MetadataURI: "https://example.invalid/release.json", MetadataSHA: strings.Repeat("a", 64)}},
		ReleaseSources: []ReleaseSource{{
			SourceID: "backend-default", Provider: "github-releases-v1",
			RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
		}},
	}
	payload, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &discoveryMapFetcher{}
	_, err = Load(context.Background(), payload, fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "cannot be mixed") {
		t.Fatalf("expected static/dynamic mode conflict, got %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("discovery ran for a malformed mixed Catalog: %d calls", fetcher.calls)
	}
}

func TestLoadRejectsDuplicateReleaseSourceIdentity(t *testing.T) {
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
	}
	other := source
	other.RepositoryURI = "https://github.com/example/another"
	fetcher := &discoveryMapFetcher{}
	_, err := Load(context.Background(), dynamicCatalogBytes(t, source, other), fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "duplicate source_id") {
		t.Fatalf("expected duplicate source identity rejection, got %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("discovery ran for duplicate source IDs: %d calls", fetcher.calls)
	}
}

func TestLoadRejectsDuplicateComponentVariantReleaseSources(t *testing.T) {
	first := ReleaseSource{
		SourceID: "backend-primary", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend", Variant: "default",
	}
	second := first
	second.SourceID = "backend-secondary"
	second.RepositoryURI = "https://github.com/example/mirror"
	fetcher := &discoveryMapFetcher{}
	_, err := Load(context.Background(), dynamicCatalogBytes(t, first, second), fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "duplicate component/variant mapping") {
		t.Fatalf("expected duplicate component/variant rejection, got %v", err)
	}
}

func TestLoadRejectsPublisherSourceRevisionMismatch(t *testing.T) {
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend", Variant: "default",
	}
	item := dynamicReleaseFixture(t, "backend", "default", "1.2.1", source.SourceID, source.RepositoryURI, "v1.2.1", 42)
	var document map[string]any
	if err := json.Unmarshal(item.Payload, &document); err != nil {
		t.Fatal(err)
	}
	sourceUnits := document["source_units"].([]any)
	sourceUnit := sourceUnits[0].(map[string]any)
	sourceUnit["revision"] = strings.Repeat("b", 40)
	item.Payload, _ = json.Marshal(document)
	sum := sha256.Sum256(item.Payload)
	item.MetadataSHA = hex.EncodeToString(sum[:])
	item.MetadataSize = int64(len(item.Payload))
	item.Discovery.MetadataAssetSizeBytes = int64(len(item.Payload))
	fetcher := &discoveryMapFetcher{releases: map[string][]DiscoveredRelease{source.SourceID: {item}}}
	_, err := Load(context.Background(), dynamicCatalogBytes(t, source), fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "RELEASE_SOURCE_REVISION_MISMATCH") {
		t.Fatalf("expected source revision mismatch, got %v", err)
	}
}

func TestLoadRejectsMetadataDigestMismatchForDiscoveredRelease(t *testing.T) {
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend", Variant: "default",
	}
	item := dynamicReleaseFixture(t, "backend", "default", "1.2.1", source.SourceID, source.RepositoryURI, "v1.2.1", 42)
	item.MetadataSHA = strings.Repeat("0", 64)
	fetcher := &discoveryMapFetcher{releases: map[string][]DiscoveredRelease{source.SourceID: {item}}}
	_, err := Load(context.Background(), dynamicCatalogBytes(t, source), fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "RELEASE_METADATA_DIGEST_MISMATCH") {
		t.Fatalf("expected metadata digest mismatch, got %v", err)
	}
}

func TestLoadRejectsMissingCanonicalMetadataAssetIdentity(t *testing.T) {
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend", Variant: "default",
	}
	item := dynamicReleaseFixture(t, "backend", "default", "1.2.1", source.SourceID, source.RepositoryURI, "v1.2.1", 42)
	item.Discovery.MetadataAssetName = "wrong.json"
	fetcher := &discoveryMapFetcher{releases: map[string][]DiscoveredRelease{source.SourceID: {item}}}
	_, err := Load(context.Background(), dynamicCatalogBytes(t, source), fetcher, &acceptingValidator{})
	if err == nil || !strings.Contains(err.Error(), "RELEASE_TAG_VERSION_MISMATCH") {
		t.Fatalf("expected canonical asset identity rejection, got %v", err)
	}
}

var _ ReleaseDiscoveryFetcher = (*discoveryMapFetcher)(nil)
var _ Fetcher = (*discoveryMapFetcher)(nil)
var _ Validator = (*acceptingValidator)(nil)

func TestReleaseMetadataAssetNameForVariants(t *testing.T) {
	cases := []struct {
		component string
		variant   string
		version   string
		want      string
	}{
		{component: "backend", variant: "default", version: "1.2.1", want: "backend-1.2.1.json"},
		{component: "frontend", variant: "tui", version: "1.1.0", want: "frontend-tui-1.1.0.json"},
		{component: "frontend", variant: "gui-sdl", version: "1.1.0", want: "frontend-gui-sdl-1.1.0.json"},
	}
	for _, item := range cases {
		if got := releaseMetadataAssetName(item.component, item.variant, item.version); got != item.want {
			t.Errorf("releaseMetadataAssetName(%q,%q,%q) = %q, want %q", item.component, item.variant, item.version, got, item.want)
		}
	}
}

func TestMinimumVersionValidation(t *testing.T) {
	source := ReleaseSource{
		SourceID: "backend-default", Provider: "github-releases-v1",
		RepositoryURI: "https://github.com/example/backend", ComponentID: "backend",
		MinimumVersion: "not-semver",
	}
	if err := validateReleaseSource(source); err == nil || !strings.Contains(err.Error(), "minimum_version") {
		t.Fatalf("expected invalid minimum_version error, got %v", err)
	}
}
