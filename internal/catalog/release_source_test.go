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

type fakeGitHubReleaseClient struct {
	metadata []byte
	releases map[int][]GitHubRelease
	commits  map[string]string
}

func (f *fakeGitHubReleaseClient) Fetch(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("unexpected ordinary catalog fetch")
}

func (f *fakeGitHubReleaseClient) ListGitHubReleases(_ context.Context, _ ReleaseSource, page, perPage int) ([]GitHubRelease, error) {
	if perPage != githubReleasePageSize {
		return nil, fmt.Errorf("unexpected page size %d", perPage)
	}
	return append([]GitHubRelease(nil), f.releases[page]...), nil
}

func (f *fakeGitHubReleaseClient) ResolveGitHubTagCommit(_ context.Context, _ ReleaseSource, tag string) (string, error) {
	commit, ok := f.commits[tag]
	if !ok {
		return "", fmt.Errorf("missing tag")
	}
	return commit, nil
}

func (f *fakeGitHubReleaseClient) FetchGitHubReleaseAsset(_ context.Context, _ string, _ int64) ([]byte, error) {
	return append([]byte(nil), f.metadata...), nil
}

func testADR0028Fixture(t *testing.T, version, publishedAt string, id int64) (ReleaseSource, GitHubRelease, *fakeGitHubReleaseClient) {
	t.Helper()
	tag := "v" + version
	commit := fmt.Sprintf("%040x", id+100)
	assetName := "editor-" + version + ".json"
	repository := "https://github.com/acme/editor"
	metadata := map[string]any{
		"schema_version": "1.2.0",
		"component_id":   "editor",
		"variant":        "default",
		"version":        version,
		"status":         "active",
		"source_units": []any{
			map[string]any{
				"source_unit_id": "publisher",
				"repository_uri": repository,
				"revision":       commit,
			},
		},
		"dependencies": []any{},
		"contracts": map[string]any{
			"provides": []any{},
			"requires": []any{},
		},
		"publication": map[string]any{
			"provider":             "github-releases-v1",
			"repository_uri":       repository,
			"tag":                  tag,
			"tag_commit":           commit,
			"metadata_asset_name":  assetName,
		},
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	source := ReleaseSource{
		SourceID: "editor-source", Provider: githubReleaseProvider,
		RepositoryURI: repository, ComponentID: "editor", Variant: "default",
	}
	release := GitHubRelease{
		ID: id, TagName: tag, Prerelease: false, PublishedAt: publishedAt,
		URL: fmt.Sprintf("https://api.github.com/repos/acme/editor/releases/%d", id),
		HTMLURL: fmt.Sprintf("https://github.com/acme/editor/releases/tag/%s", tag),
		Assets: []GitHubReleaseAsset{{
			ID: id + 1000, Name: assetName, Size: int64(len(payload)), Digest: "sha256:" + digest,
			URL: fmt.Sprintf("https://api.github.com/repos/acme/editor/releases/assets/%d", id+1000),
			BrowserDownloadURL: fmt.Sprintf("https://github.com/acme/editor/releases/download/%s/%s", tag, assetName),
		}},
	}
	client := &fakeGitHubReleaseClient{
		metadata: payload,
		releases: map[int][]GitHubRelease{1: {release}},
		commits: map[string]string{tag: commit},
	}
	return source, release, client
}

func TestValidateReleaseSourcesRejectsDuplicateLogicalVariantAcrossRepositories(t *testing.T) {
	sources := []ReleaseSource{
		{SourceID: "one", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/one", ComponentID: "editor", Variant: "default"},
		{SourceID: "two", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/two", ComponentID: "editor", Variant: "default"},
	}
	if _, err := ValidateReleaseSources(sources); err == nil {
		t.Fatal("expected duplicate component/variant mapping to fail")
	}
}

func TestValidateReleaseSourcesCanonicalizesDefaults(t *testing.T) {
	validated, err := ValidateReleaseSources([]ReleaseSource{{
		SourceID: "editor-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/editor/", ComponentID: "editor",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if validated[0].Variant != "default" || validated[0].TagPrefix != "v" ||
		validated[0].RepositoryURI != "https://github.com/acme/editor" {
		t.Fatalf("unexpected normalization: %#v", validated[0])
	}
}

func TestDynamicCatalogRejectsMixedStaticAndReleaseSources(t *testing.T) {
	index := Index{
		SchemaVersion: "1.1.0", CatalogID: "test.catalog",
		Entries: []Entry{{ComponentID: "editor", Version: "1.0.0", MetadataURI: "https://example.invalid/release.json", MetadataSHA: strings.Repeat("a", 64)}},
		ReleaseSources: []ReleaseSource{{SourceID: "editor-source", Provider: githubReleaseProvider, RepositoryURI: "https://github.com/acme/editor", ComponentID: "editor"}},
	}
	indexBytes, marshalErr := json.Marshal(index)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	_, err := Load(context.Background(), indexBytes, &fakeGitHubReleaseClient{}, &acceptingValidator{})
	if err == nil {
		t.Fatal("expected mixed catalog models to be rejected by loader dispatch boundary")
	}
}

func TestLoadFromGitHubReleaseSourcesVerifiesAndRecordsPublication(t *testing.T) {
	source, release, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 10)
	index := Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}}
	got, err := loadFromGitHubReleaseSources(context.Background(), index, client, &acceptingValidator{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(got.Releases))
	}
	selected := got.Releases[0]
	if selected.Version != "1.2.0" || selected.MetadataURI != release.Assets[0].BrowserDownloadURL {
		t.Fatalf("unexpected selected release: %#v", selected)
	}
	if selected.Discovery == nil {
		t.Fatal("release discovery provenance was not retained")
	}
	if selected.Discovery.ReleaseID != release.ID ||
		selected.Discovery.ReleaseAPIURI != release.URL ||
		selected.Discovery.MetadataAssetName != "editor-1.2.0.json" ||
		selected.Discovery.MetadataAssetSize != release.Assets[0].Size ||
		selected.Discovery.MetadataAssetSize <= 0 {
		t.Fatalf("incomplete release provenance: %#v", selected.Discovery)
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsMissingDigest(t *testing.T) {
	source, _, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 11)
	releases := client.releases[1]
	releases[0].Assets[0].Digest = ""
	client.releases[1] = releases
	_, err := loadFromGitHubReleaseSources(context.Background(),
		Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}},
		client, &acceptingValidator{}, 1)
	if err == nil {
		t.Fatal("expected missing GitHub digest to fail closed")
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsMovedTag(t *testing.T) {
	source, _, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 12)
	client.commits["v1.2.0"] = fmt.Sprintf("%040x", 999)
	_, err := loadFromGitHubReleaseSources(context.Background(),
		Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}},
		client, &acceptingValidator{}, 1)
	if err == nil {
		t.Fatal("expected moved tag/source commit mismatch to fail closed")
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsPrereleaseFlagMismatch(t *testing.T) {
	source, _, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 13)
	releases := client.releases[1]
	releases[0].Prerelease = true
	client.releases[1] = releases
	_, err := loadFromGitHubReleaseSources(context.Background(),
		Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}},
		client, &acceptingValidator{}, 1)
	if err == nil {
		t.Fatal("expected GitHub prerelease flag mismatch to fail closed")
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsNonCanonicalAssetURI(t *testing.T) {
	source, _, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 14)
	releases := client.releases[1]
	releases[0].Assets[0].BrowserDownloadURL = "https://attacker.example/metadata.json"
	client.releases[1] = releases
	_, err := loadFromGitHubReleaseSources(context.Background(),
		Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}},
		client, &acceptingValidator{}, 1)
	if err == nil {
		t.Fatal("expected non-canonical release asset URI to fail closed")
	}
}


func TestADR0028SpecSchemasAcceptPublicationAndReleaseSources(t *testing.T) {
	source, _, client := testADR0028Fixture(t, "1.2.0", "2026-11-10T18:00:00Z", 15)
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(validation.ComponentRelease, client.metadata, "component-release.json"); err != nil {
		t.Fatalf("Spec Component Release Metadata schema rejected ADR-0028 fixture: %v", err)
	}
	index, err := json.Marshal(Index{
		SchemaVersion: "1.1.0",
		CatalogID: "test.catalog",
		ReleaseSources: []ReleaseSource{source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(validation.CatalogIndex, index, "catalog-index.json"); err != nil {
		t.Fatalf("Spec Catalog Index schema rejected release_sources: %v", err)
	}
}
