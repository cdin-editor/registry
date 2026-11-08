package workspace

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

type phase9ArtifactFetcher struct {
	payload  []byte
	cache    map[string][]byte
	offline  bool
	requests int
}

func (f *phase9ArtifactFetcher) FetchWithDigest(context.Context, string, string) ([]byte, error) {
	return nil, fmt.Errorf("artifact replay fixture expected the full immutable artifact identity")
}

func (f *phase9ArtifactFetcher) FetchArtifact(ctx context.Context, releaseMetadataSHA string, descriptor catalog.Artifact) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := catalog.ArtifactCacheKey(releaseMetadataSHA, descriptor)
	if err != nil {
		return nil, err
	}
	if cached, ok := f.cache[key]; ok {
		return append([]byte(nil), cached...), nil
	}
	if f.offline {
		return nil, fmt.Errorf("ARTIFACT_OFFLINE_MISS: fixture has no cached bytes for the immutable identity")
	}
	if manifest.Digest(f.payload) != descriptor.SHA256 || int64(len(f.payload)) != descriptor.SizeBytes {
		return nil, fmt.Errorf("fixture artifact bytes do not match the declared digest and size")
	}
	f.requests++
	f.cache[key] = append([]byte(nil), f.payload...)
	return append([]byte(nil), f.payload...), nil
}

func phase9NativePackageZIP(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("include/engine.h")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("#define ENGINE_VERSION 1\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestRegistryArtifactPrepareOfflineReplay proves that artifact preparation
// records an immutable package identity, warms a digest-addressed cache, and
// replays the exact package bytes without an online lookup or source checkout.
// A changed artifact identity must fail closed and must not publish a workspace.
func TestRegistryArtifactPrepareOfflineReplay(t *testing.T) {
	artifactBytes := phase9NativePackageZIP(t)
	target := catalog.Target{OS: "linux", Architecture: "x86_64", ABI: "gnu-glibc-2.28"}
	descriptor := catalog.Artifact{
		ID:            "engine-linux-x86_64",
		Kind:          "native-development-package",
		Target:        target,
		Format:        "zip",
		FileName:      "engine-linux-x86_64.zip",
		URI:           "https://packages.example.invalid/engine/1.0.0/linux-x86_64.zip",
		SHA256:        manifest.Digest(artifactBytes),
		SizeBytes:     int64(len(artifactBytes)),
		Layout:        "cmake-prefix-v1",
		SourceUnitIDs: []string{"source"},
		Build: catalog.ArtifactBuild{
			Recipe:    catalog.ArtifactRecipe{SourceUnitID: "source", Path: "CMakeLists.txt"},
			Toolchain: []catalog.ToolchainEntry{{Name: "CMake", Version: "3.30.0"}},
			Runner:    "ubuntu-24.04",
		},
	}

	resolution := successfulResolution(t.TempDir(), strings.Repeat("a", 40))
	resolution.Graph.SchemaVersion = "1.1.0"
	resolution.Graph.Resolution = resolver.ResolutionPolicy{DeliveryMode: "artifact", Target: &target}
	component := &resolution.Graph.Components[0]
	component.Delivery = "artifact"
	component.Artifacts = []catalog.Artifact{descriptor}
	selected := descriptor
	component.SelectedArtifact = &selected

	fetcher := &phase9ArtifactFetcher{payload: artifactBytes, cache: make(map[string][]byte)}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	preparedPath := filepath.Join(t.TempDir(), "prepared-artifact")
	prepareOptions := workspaceOptions(preparedPath)
	prepareOptions.ArtifactFetcher = fetcher
	prepared, err := Prepare(context.Background(), resolution, prepareOptions)
	if err != nil {
		t.Fatalf("prepare immutable artifact: %v", err)
	}
	if fetcher.requests != 1 {
		t.Fatalf("online artifact fetches = %d, want exactly one", fetcher.requests)
	}
	manifestBytes, err := os.ReadFile(prepared.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(manifestBytes, prepared.ManifestSHA256, validator); err != nil {
		t.Fatalf("verify artifact Manifest: %v", err)
	}

	packageRelative := filepath.Join("components", "engine", "default", "package", "include", "engine.h")
	preparedPackage, err := os.ReadFile(filepath.Join(prepared.WorkspacePath, packageRelative))
	if err != nil {
		t.Fatalf("read prepared artifact package: %v", err)
	}
	if string(preparedPackage) != "#define ENGINE_VERSION 1\n" {
		t.Fatalf("prepared package bytes = %q", preparedPackage)
	}

	fetcher.offline = true
	replayedPath := filepath.Join(t.TempDir(), "replayed-artifact")
	replayOptions := workspaceOptions(replayedPath)
	replayOptions.ArtifactFetcher = fetcher
	replayed, err := Replay(context.Background(), manifestBytes, prepared.ManifestSHA256, ReplayOptions{Workspace: replayOptions})
	if err != nil {
		t.Fatalf("offline artifact replay: %v", err)
	}
	if fetcher.requests != 1 {
		t.Fatalf("offline replay made an online request; fetches=%d, want one initial request", fetcher.requests)
	}
	replayedManifest, err := os.ReadFile(replayed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifestBytes, replayedManifest) || replayed.ManifestSHA256 != prepared.ManifestSHA256 {
		t.Fatal("offline replay did not preserve canonical Manifest bytes and digest")
	}
	replayedPackage, err := os.ReadFile(filepath.Join(replayed.WorkspacePath, packageRelative))
	if err != nil {
		t.Fatalf("read replayed artifact package: %v", err)
	}
	if !reflect.DeepEqual(preparedPackage, replayedPackage) {
		t.Fatal("offline replay did not reproduce exact artifact package bytes")
	}

	var changed manifest.Document
	if err := json.Unmarshal(manifestBytes, &changed); err != nil {
		t.Fatal(err)
	}
	if len(changed.Components) != 1 || changed.Components[0].SelectedArtifact == nil {
		t.Fatal("artifact replay fixture Manifest has no single selected artifact")
	}
	changed.Components[0].SelectedArtifact.ID = "different-artifact-identity"
	changedBytes, err := manifest.MarshalCanonical(changed)
	if err != nil {
		t.Fatal(err)
	}
	rejectedPath := filepath.Join(t.TempDir(), "rejected-artifact-replay")
	rejectedOptions := workspaceOptions(rejectedPath)
	rejectedOptions.ArtifactFetcher = fetcher
	_, err = Replay(context.Background(), changedBytes, manifest.Digest(changedBytes), ReplayOptions{Workspace: rejectedOptions})
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_OFFLINE_MISS") {
		t.Fatalf("replay with changed artifact identity error = %v, want ARTIFACT_OFFLINE_MISS", err)
	}
	if _, err := os.Lstat(rejectedPath); !os.IsNotExist(err) {
		t.Fatalf("changed artifact replay published a partial workspace: %v", err)
	}
}

type phase9MetadataFetcher map[string][]byte

func (f phase9MetadataFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, ok := f[uri]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), payload...), nil
}

// TestRegistryCleanRoomPrepareVerifyReplay exercises the public preparation
// chain with the production Spec schemas and a temporary real Git repository.
// It starts from Product Definition YAML and Catalog bytes, not a prebuilt
// Resolver graph, then proves that the persisted Manifest reproduces exactly.
// Spec: REG-001/002/005/017-024/026/027; see contract sections 3, 6, 7 and 8.
// Design: catalog-ingestion.md, resolver.md, collector.md, workspace.md and
// manifest.md under docs/implementation.
func TestRegistryCleanRoomPrepareVerifyReplay(t *testing.T) {
	repository, revision := createSourceRepository(t)
	repositoryURI := "https://source.example.invalid/engine.git"
	releaseURI := "https://metadata.example.invalid/releases/engine-1.0.0.json"
	indexURI := "https://metadata.example.invalid/catalog/index.json"

	releaseBytes, err := json.Marshal(catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   "engine",
		Variant:       "default",
		Version:       "1.0.0",
		Status:        "active",
		SourceUnits: []catalog.SourceUnit{{
			ID: "source", RepositoryURI: repositoryURI, Revision: revision,
		}},
		Dependencies: []catalog.Dependency{},
		Contracts: catalog.Contracts{
			Provides: []catalog.ContractProvider{},
			Requires: []catalog.ContractRequirement{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "phase9-fixture",
		Entries: []catalog.Entry{{
			ComponentID: "engine",
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: releaseURI,
			MetadataSHA: manifest.Digest(releaseBytes),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	productYAML := []byte(`schema_version: "1.0.0"
product:
  id: com.example.registry-phase9
  name: Registry Phase 9 Fixture
components:
  - component_id: engine
    version_constraint: "=1.0.0"
environment_contracts: []
policy:
  allow_prerelease: false
`)
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	loadDefinition := func(input []byte) resolver.ProductDefinition {
		t.Helper()
		normalized, normalizeErr := validator.Normalize(validation.ProductDefinition, input, "product.yaml")
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		var definition resolver.ProductDefinition
		if decodeErr := json.Unmarshal(normalized, &definition); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return definition
	}

	definition := loadDefinition(productYAML)
	alternateIdentity := []byte(strings.ReplaceAll(
		string(productYAML),
		"com.example.registry-phase9",
		"org.example.unrelated-consumer",
	))
	alternateIdentity = []byte(strings.ReplaceAll(
		string(alternateIdentity),
		"Registry Phase 9 Fixture",
		"Unrelated Product Name",
	))

	fetcher := phase9MetadataFetcher{releaseURI: releaseBytes}
	loadedCatalog, err := catalog.LoadFromSource(
		context.Background(), indexBytes, indexURI, fetcher, validator,
	)
	if err != nil {
		t.Fatalf("load real-schema Catalog fixture: %v", err)
	}
	resolution := resolver.Resolve(definition, []*catalog.Catalog{loadedCatalog})
	if resolution.Graph == nil || resolution.Report.Result != "success" {
		t.Fatalf("clean-room resolution failed: %+v", resolution.Report.Conflicts)
	}

	alternateResolution := resolver.Resolve(loadDefinition(alternateIdentity), []*catalog.Catalog{loadedCatalog})
	if alternateResolution.Graph == nil || !reflect.DeepEqual(resolution.Graph, alternateResolution.Graph) ||
		!reflect.DeepEqual(resolution.Report, alternateResolution.Report) {
		t.Fatal("changing Product identity/name changed the same declarative component resolution")
	}

	preparedPath := filepath.Join(t.TempDir(), "prepared")
	options := workspaceOptions(preparedPath)
	options.Git = replayGitRunner{repositoryURI: repositoryURI, localPath: repository}
	prepared, err := Prepare(context.Background(), resolution, options)
	if err != nil {
		t.Fatalf("prepare from Product Definition and Catalog: %v", err)
	}
	manifestBytes, err := os.ReadFile(prepared.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(manifestBytes, prepared.ManifestSHA256, validator); err != nil {
		t.Fatalf("verify persisted Manifest: %v", err)
	}

	sourceRelative := filepath.Join("components", "engine", "default", "sources", "source", "engine.c")
	preparedSource, err := os.ReadFile(filepath.Join(prepared.WorkspacePath, sourceRelative))
	if err != nil {
		t.Fatalf("read prepared source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.WorkspacePath, "registry-manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(prepared.WorkspacePath, "resolution-report.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(prepared.WorkspacePath, "components", "engine", "default", "sources", "source", ".git")); !os.IsNotExist(err) {
		t.Fatalf("prepared source must not expose Git internals: %v", err)
	}

	replayedPath := filepath.Join(t.TempDir(), "replayed")
	replayed, err := Replay(context.Background(), manifestBytes, prepared.ManifestSHA256, ReplayOptions{
		Workspace: replayOptions(replayedPath, repositoryURI, repository),
	})
	if err != nil {
		t.Fatalf("replay persisted Manifest: %v", err)
	}
	replayedBytes, err := os.ReadFile(replayed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifestBytes, replayedBytes) {
		t.Fatal("replay did not reproduce byte-identical canonical Manifest bytes")
	}
	replayedSource, err := os.ReadFile(filepath.Join(replayed.WorkspacePath, sourceRelative))
	if err != nil {
		t.Fatalf("read replayed source: %v", err)
	}
	if !reflect.DeepEqual(preparedSource, replayedSource) {
		t.Fatal("replay did not reproduce the exact source tree bytes")
	}
	if prepared.ManifestSHA256 != replayed.ManifestSHA256 {
		t.Fatalf("Manifest digest changed after replay: %s != %s", prepared.ManifestSHA256, replayed.ManifestSHA256)
	}
}
