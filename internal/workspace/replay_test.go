package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

type replayGitRunner struct {
	repositoryURI string
	localPath     string
}

func (g replayGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	mapped := append([]string(nil), args...)
	for i := range mapped {
		if mapped[i] == g.repositoryURI {
			mapped[i] = fileRepositoryURI(g.localPath)
		}
	}
	return (collector.CommandGit{}).Run(ctx, dir, env, mapped...)
}

type replayFixtureFetcher struct {
	payloads map[string][]byte
	calls    int
	uris     []string
}

func (f *replayFixtureFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	f.calls++
	f.uris = append(f.uris, uri)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, ok := f.payloads[uri]
	if !ok {
		return nil, errors.New("fixture metadata not found")
	}
	return append([]byte(nil), payload...), nil
}

func replayResolution(repositoryPath, repositoryURI, revision string) resolver.Result {
	resolution := successfulResolution(repositoryPath, revision)
	resolution.Graph.Components[0].SourceUnits[0].RepositoryURI = repositoryURI
	return resolution
}

func replayOptions(destination, repositoryURI, repositoryPath string) Options {
	options := workspaceOptions(destination)
	options.Git = replayGitRunner{repositoryURI: repositoryURI, localPath: repositoryPath}
	return options
}

func buildReplayManifest(t *testing.T, resolution resolver.Result) ([]byte, string) {
	t.Helper()
	collected := []collector.Source{{
		ComponentID: "engine", Variant: "default", SourceUnitID: "source",
		Revision:      resolution.Graph.Components[0].SourceUnits[0].Revision,
		WorkspacePath: "components/engine/default/sources/source",
	}}
	document, err := manifest.Build(resolution.Graph, collected, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(encoded, manifest.Digest(encoded), validator); err != nil {
		t.Fatalf("replay fixture Manifest is not verifiable: %v", err)
	}
	return encoded, manifest.Digest(encoded)
}

func TestReplayRecreatesCanonicalManifestAndExactSourceTree(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://example.invalid/engine.git"
	resolution := replayResolution(repository, repositoryURI, revision)
	encoded, digest := buildReplayManifest(t, resolution)

	firstDestination := filepath.Join(t.TempDir(), "first")
	first, err := Prepare(context.Background(), resolution, replayOptions(firstDestination, repositoryURI, repository))
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(encoded) || first.ManifestSHA256 != digest {
		t.Fatal("fixture preparation did not preserve the expected canonical Manifest")
	}

	secondDestination := filepath.Join(t.TempDir(), "replayed")
	replayed, err := Replay(context.Background(), firstBytes, first.ManifestSHA256, ReplayOptions{
		Workspace: replayOptions(secondDestination, repositoryURI, repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := os.ReadFile(replayed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayedBytes) != string(firstBytes) {
		t.Fatal("replay did not reproduce byte-identical Manifest bytes")
	}
	if replayed.ManifestSHA256 != first.ManifestSHA256 {
		t.Fatalf("replay digest = %s, want %s", replayed.ManifestSHA256, first.ManifestSHA256)
	}
	sourcePath := filepath.Join(replayed.WorkspacePath, "components", "engine", "default", "sources", "source", "engine.c")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceBytes) != "int engine(void) { return 1; }\n" {
		t.Fatalf("replayed source contents differ: %q", sourceBytes)
	}
	reportBytes, err := os.ReadFile(replayed.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportBytes), "MANIFEST_REPLAY_PINNED") {
		t.Fatal("replay report does not identify pinned Manifest replay")
	}
}

func TestReplayDoesNotConsultChangedCatalogWhenMetadataChecksAreOffline(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://example.invalid/engine.git"
	resolution := replayResolution(repository, repositoryURI, revision)
	encoded, digest := buildReplayManifest(t, resolution)

	changedCatalog, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "stable",
		Entries: []catalog.Entry{{
			ComponentID: "engine", Variant: "default", Version: "2.0.0",
			MetadataURI: "https://metadata.example.invalid/releases/engine-2.0.0.json",
			MetadataSHA: strings.Repeat("a", 64),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &replayFixtureFetcher{payloads: map[string][]byte{
		resolution.Graph.Catalogs[0].MetadataURI: changedCatalog,
	}}
	preparedDestination := filepath.Join(t.TempDir(), "already-prepared")
	prepared, err := Prepare(context.Background(), resolution, replayOptions(preparedDestination, repositoryURI, repository))
	if err != nil {
		t.Fatal(err)
	}
	preparedManifestBefore, err := os.ReadFile(prepared.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	preparedSourcePath := filepath.Join(prepared.WorkspacePath, "components", "engine", "default", "sources", "source", "engine.c")
	preparedSourceBefore, err := os.ReadFile(preparedSourcePath)
	if err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "offline-replay")
	replayed, err := Replay(context.Background(), encoded, digest, ReplayOptions{
		Workspace:       replayOptions(destination, repositoryURI, repository),
		MetadataFetcher: fetcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("offline replay made %d metadata requests, want zero", fetcher.calls)
	}
	got, err := os.ReadFile(replayed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(encoded) {
		t.Fatal("changed Catalog state altered the persisted Manifest during offline replay")
	}
	if prepared.ManifestSHA256 != digest || string(preparedManifestBefore) != string(encoded) {
		t.Fatal("pre-existing Workspace does not contain the expected Manifest before replay")
	}
	preparedManifestAfter, err := os.ReadFile(prepared.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	preparedSourceAfter, err := os.ReadFile(preparedSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(preparedManifestAfter) != string(preparedManifestBefore) {
		t.Fatal("offline replay or changed Catalog mutated the pre-existing Manifest")
	}
	if string(preparedSourceAfter) != string(preparedSourceBefore) {
		t.Fatal("offline replay or changed Catalog mutated the pre-existing Workspace source")
	}
}

func TestReplayRequiresRecordedCatalogAndReleaseMetadataWhenEnabled(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://example.invalid/engine.git"
	resolution := replayResolution(repository, repositoryURI, revision)

	releaseURI := "https://metadata.example.invalid/releases/engine-1.0.0.json"
	releaseBytes, err := json.Marshal(catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   "engine",
		Variant:       "default",
		Version:       "1.0.0",
		Status:        "active",
		SourceUnits:   resolution.Graph.Components[0].SourceUnits,
		Dependencies:  []catalog.Dependency{},
		Contracts:     resolution.Graph.Components[0].Contracts,
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseDigest := manifest.Digest(releaseBytes)
	indexBytes, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "stable",
		Entries: []catalog.Entry{{
			ComponentID: "engine", Variant: "default", Version: "1.0.0",
			MetadataURI: releaseURI, MetadataSHA: releaseDigest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexURI := "https://metadata.example.invalid/index.json"
	resolution.Graph.Catalogs[0].MetadataURI = indexURI
	resolution.Graph.Catalogs[0].MetadataSHA = manifest.Digest(indexBytes)
	resolution.Graph.Components[0].ReleaseMetadataURI = releaseURI
	resolution.Graph.Components[0].ReleaseMetadataSHA = releaseDigest
	encoded, digest := buildReplayManifest(t, resolution)

	fetcher := &replayFixtureFetcher{payloads: map[string][]byte{
		indexURI: indexBytes, releaseURI: releaseBytes,
	}}
	destination := filepath.Join(t.TempDir(), "verified-replay")
	if _, err := Replay(context.Background(), encoded, digest, ReplayOptions{
		Workspace:            replayOptions(destination, repositoryURI, repository),
		MetadataFetcher:      fetcher,
		VerifyRemoteMetadata: true,
	}); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 2 {
		t.Fatalf("remote verification fetched %d documents, want Catalog Index and selected Release Metadata", fetcher.calls)
	}

	changedIndex, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "stable",
		Entries: []catalog.Entry{
			{ComponentID: "engine", Variant: "default", Version: "1.0.0", MetadataURI: releaseURI, MetadataSHA: releaseDigest},
			{ComponentID: "engine", Variant: "default", Version: "2.0.0", MetadataURI: "https://metadata.example.invalid/releases/engine-2.0.0.json", MetadataSHA: strings.Repeat("a", 64)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcher.payloads[indexURI] = changedIndex
	fetcher.calls = 0
	rejectedDestination := filepath.Join(t.TempDir(), "changed-catalog")
	if _, err := Replay(context.Background(), encoded, digest, ReplayOptions{
		Workspace:            replayOptions(rejectedDestination, repositoryURI, repository),
		MetadataFetcher:      fetcher,
		VerifyRemoteMetadata: true,
	}); err == nil {
		t.Fatal("changed Catalog Index unexpectedly passed strict replay metadata verification")
	}
	if _, err := os.Stat(rejectedDestination); !os.IsNotExist(err) {
		t.Fatalf("strict metadata failure published a destination: stat err=%v", err)
	}
}

// replayCountingGitRunner records whether replay reached source retrieval.
type replayCountingGitRunner struct {
	delegate replayGitRunner
	calls    int
}

func (g *replayCountingGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.calls++
	return g.delegate.Run(ctx, dir, env, args...)
}

func TestReplayRejectsInvalidManifestAndUnreplayableCommitsWithoutPublishing(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://example.invalid/engine.git"
	resolution := replayResolution(repository, repositoryURI, revision)
	validManifest, _ := buildReplayManifest(t, resolution)

	mutate := func(change func(*manifest.Document)) []byte {
		t.Helper()
		var document manifest.Document
		if err := json.Unmarshal(validManifest, &document); err != nil {
			t.Fatal(err)
		}
		change(&document)
		encoded, err := manifest.MarshalCanonical(document)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	cases := []struct {
		name      string
		input     []byte
		expectGit bool
	}{
		{
			name:  "non-canonical bytes",
			input: append(append([]byte(nil), validManifest...), ' '),
		},
		{
			name: "missing release provenance",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].ReleaseMetadataSHA = ""
			}),
		},
		{
			name: "unsafe workspace path",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].SourceUnits[0].WorkspacePath = "components/engine/default/sources/../../escape"
			}),
		},
		{
			name: "non-full Git revision",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].SourceUnits[0].Revision = "abcdef123456"
			}),
		},
		{
			name: "missing source repository identity",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].SourceUnits[0].RepositoryURI = ""
			}),
		},
		{
			name: "invalid source unit identity",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].SourceUnits[0].SourceUnitID = ""
			}),
		},
		{
			name: "full revision that is not an available commit",
			input: mutate(func(document *manifest.Document) {
				document.Components[0].SourceUnits[0].Revision = strings.Repeat("0", 40)
			}),
			expectGit: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "rejected-replay")
			options := replayOptions(destination, repositoryURI, repository)
			git := &replayCountingGitRunner{
				delegate: replayGitRunner{repositoryURI: repositoryURI, localPath: repository},
			}
			options.Git = git
			if _, err := Replay(context.Background(), testCase.input, "", ReplayOptions{Workspace: options}); err == nil {
				t.Fatal("invalid or unreplayable Manifest unexpectedly succeeded")
			}
			if testCase.expectGit && git.calls == 0 {
				t.Fatal("validly-shaped but unavailable commit did not reach Git retrieval")
			}
			if !testCase.expectGit && git.calls != 0 {
				t.Fatalf("invalid Manifest reached Git %d times before rejection", git.calls)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("rejected replay published a destination: stat err=%v", err)
			}
		})
	}
}

func TestReplayRejectsRemoteReleaseSourceIdentityMismatch(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://example.invalid/engine.git"
	resolution := replayResolution(repository, repositoryURI, revision)

	releaseURI := "https://metadata.example.invalid/releases/engine-1.0.0.json"
	release := catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   "engine",
		Variant:       "default",
		Version:       "1.0.0",
		Status:        "active",
		SourceUnits:   append([]catalog.SourceUnit(nil), resolution.Graph.Components[0].SourceUnits...),
		Dependencies:  []catalog.Dependency{},
		Contracts:     resolution.Graph.Components[0].Contracts,
	}
	release.SourceUnits[0].RepositoryURI = "https://different.example.invalid/engine.git"
	releaseBytes, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseDigest := manifest.Digest(releaseBytes)
	indexURI := "https://metadata.example.invalid/index.json"
	indexBytes, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "stable",
		Entries: []catalog.Entry{{
			ComponentID: "engine", Variant: "default", Version: "1.0.0",
			MetadataURI: releaseURI, MetadataSHA: releaseDigest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution.Graph.Catalogs[0].MetadataURI = indexURI
	resolution.Graph.Catalogs[0].MetadataSHA = manifest.Digest(indexBytes)
	resolution.Graph.Components[0].ReleaseMetadataURI = releaseURI
	resolution.Graph.Components[0].ReleaseMetadataSHA = releaseDigest
	encoded, digest := buildReplayManifest(t, resolution)

	fetcher := &replayFixtureFetcher{payloads: map[string][]byte{
		indexURI: indexBytes, releaseURI: releaseBytes,
	}}
	destination := filepath.Join(t.TempDir(), "mismatched-release")
	options := replayOptions(destination, repositoryURI, repository)
	git := &replayCountingGitRunner{
		delegate: replayGitRunner{repositoryURI: repositoryURI, localPath: repository},
	}
	options.Git = git
	if _, err := Replay(context.Background(), encoded, digest, ReplayOptions{
		Workspace:            options,
		MetadataFetcher:      fetcher,
		VerifyRemoteMetadata: true,
	}); err == nil {
		t.Fatal("release metadata with a different source repository unexpectedly passed replay")
	}
	if fetcher.calls != 2 {
		t.Fatalf("remote metadata requests = %d, want 2", fetcher.calls)
	}
	if git.calls != 0 {
		t.Fatalf("source identity mismatch reached Git retrieval %d times", git.calls)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("source identity mismatch published a destination: stat err=%v", err)
	}
}


func TestReplayVerifiesExactADR0028ReleaseAssetAndDoesNotRediscover(t *testing.T) {
	repository, revision := createSourceRepository(t)
	const repositoryURI = "https://github.com/acme/engine"
	const indexURI = "https://metadata.example.invalid/catalog-index.json"
	const tag = "v1.0.0"
	const metadataURI = "https://github.com/acme/engine/releases/download/v1.0.0/engine-1.0.0.json"

	resolution := replayResolution(repository, repositoryURI, revision)
	resolution.Graph.SchemaVersion = "1.2.0"

	releaseDocument := map[string]any{
		"schema_version": "1.2.0",
		"component_id": "engine",
		"variant": "default",
		"version": "1.0.0",
		"status": "active",
		"source_units": []any{
			map[string]any{
				"source_unit_id": "source",
				"repository_uri": repositoryURI,
				"revision": revision,
			},
		},
		"dependencies": []any{},
		"contracts": map[string]any{"provides": []any{}, "requires": []any{}},
		"publication": map[string]any{
			"provider": "github-releases-v1",
			"repository_uri": repositoryURI,
			"tag": tag,
			"tag_commit": revision,
			"metadata_asset_name": "engine-1.0.0.json",
		},
	}
	releaseBytes, err := json.Marshal(releaseDocument)
	if err != nil { t.Fatal(err) }
	releaseDigest := manifest.Digest(releaseBytes)

	indexBytes, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.1.0",
		CatalogID: "stable",
		ReleaseSources: []catalog.ReleaseSource{{
			SourceID: "engine-source", Provider: "github-releases-v1",
			RepositoryURI: repositoryURI, ComponentID: "engine", Variant: "default", TagPrefix: "v",
		}},
	})
	if err != nil { t.Fatal(err) }
	resolution.Graph.Catalogs[0].MetadataURI = indexURI
	resolution.Graph.Catalogs[0].MetadataSHA = manifest.Digest(indexBytes)
	resolution.Graph.Components[0].ReleaseMetadataURI = metadataURI
	resolution.Graph.Components[0].ReleaseMetadataSHA = releaseDigest
	resolution.Graph.Components[0].ReleaseDiscovery = &catalog.ReleaseDiscoveryProvenance{
		SourceID: "engine-source",
		Provider: "github-releases-v1",
		RepositoryURI: repositoryURI,
		ReleaseID: 123,
		ReleaseAPIURI: "https://api.github.com/repos/acme/engine/releases/123",
		Tag: tag,
		TagCommit: revision,
		MetadataAssetName: "engine-1.0.0.json",
		MetadataAssetSize: int64(len(releaseBytes)),
		PublishedAt: "2026-11-10T18:00:00Z",
	}
	encoded, manifestDigest := buildReplayManifest(t, resolution)

	fetcher := &replayFixtureFetcher{payloads: map[string][]byte{
		indexURI: indexBytes,
		metadataURI: releaseBytes,
	}}
	destination := filepath.Join(t.TempDir(), "adr0028-replay")
	options := replayOptions(destination, repositoryURI, repository)
	git := &replayCountingGitRunner{delegate: replayGitRunner{repositoryURI: repositoryURI, localPath: repository}}
	options.Git = git
	replayed, err := Replay(context.Background(), encoded, manifestDigest, ReplayOptions{
		Workspace: options,
		MetadataFetcher: fetcher,
		VerifyRemoteMetadata: true,
	})
	if err != nil { t.Fatal(err) }
	if fetcher.calls != 2 {
		t.Fatalf("replay fetched %d metadata documents, want only the pinned Catalog and exact Release Asset", fetcher.calls)
	}
	if len(fetcher.uris) != 2 || fetcher.uris[0] != indexURI || fetcher.uris[1] != metadataURI {
		t.Fatalf("replay metadata requests = %v; want exact immutable Catalog and Release Asset URIs", fetcher.uris)
	}
	if replayed.ManifestSHA256 != manifestDigest {
		t.Fatalf("replay Manifest digest = %s, want %s", replayed.ManifestSHA256, manifestDigest)
	}
	reportBytes, err := os.ReadFile(replayed.ReportPath)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(reportBytes), "release_discovery") || !strings.Contains(string(reportBytes), "engine-1.0.0.json") {
		t.Fatalf("replay report did not preserve selected Release provenance: %s", reportBytes)
	}

	// A payload with a different tag commit at the same immutable URI/digest
	// identity must be rejected before any source checkout or destination publish.
	var changed map[string]any
	if err := json.Unmarshal(releaseBytes, &changed); err != nil { t.Fatal(err) }
	publication := changed["publication"].(map[string]any)
	publication["tag_commit"] = strings.Repeat("0", 40)
	changedBytes, err := json.Marshal(changed)
	if err != nil { t.Fatal(err) }
	badFetcher := &replayFixtureFetcher{payloads: map[string][]byte{
		indexURI: indexBytes,
		metadataURI: changedBytes,
	}}
	badDestination := filepath.Join(t.TempDir(), "rejected-adr0028-replay")
	badOptions := replayOptions(badDestination, repositoryURI, repository)
	badGit := &replayCountingGitRunner{delegate: replayGitRunner{repositoryURI: repositoryURI, localPath: repository}}
	badOptions.Git = badGit
	if _, err := Replay(context.Background(), encoded, manifestDigest, ReplayOptions{
		Workspace: badOptions,
		MetadataFetcher: badFetcher,
		VerifyRemoteMetadata: true,
	}); err == nil {
		t.Fatal("replay accepted a Release Asset whose publisher tag commit differs from the Manifest")
	}
	if badGit.calls != 0 {
		t.Fatalf("invalid Release Asset reached Git retrieval %d times", badGit.calls)
	}
	if _, err := os.Stat(badDestination); !os.IsNotExist(err) {
		t.Fatalf("rejected Release Asset replay published a destination: %v", err)
	}
}
