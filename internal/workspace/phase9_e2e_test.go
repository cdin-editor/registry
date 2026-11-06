package workspace

import (
	"context"
	"encoding/json"
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
