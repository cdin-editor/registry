package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/validation"
)

type fixtureFileFetcher map[string]string

func (f fixtureFileFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, ok := f[uri]
	if !ok {
		return nil, fmt.Errorf("fixture metadata not found")
	}
	return os.ReadFile(path)
}

type localCatalogFixture struct {
	indexPath string
	fetcher   fixtureFileFetcher
}

func fixtureReleasePayload(t *testing.T, component, version, repository string) []byte {
	t.Helper()
	payload, err := json.Marshal(Release{
		SchemaVersion: "1.0.0",
		ComponentID:   component,
		Variant:       "default",
		Version:       version,
		Status:        "active",
		SourceUnits: []SourceUnit{{
			ID:            "source",
			RepositoryURI: repository,
			Revision:      "0123456789abcdef0123456789abcdef01234567",
		}},
		Dependencies: []Dependency{},
		Contracts:    Contracts{Provides: []ContractProvider{}, Requires: []ContractRequirement{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func fixtureEntry(catalogID, component, version string, payload []byte) Entry {
	uri := fmt.Sprintf("https://metadata.example.invalid/%s/%s-%s.json", catalogID, component, version)
	return Entry{
		ComponentID: component,
		Variant:     "default",
		Version:     version,
		MetadataURI: uri,
		MetadataSHA: digest(payload),
	}
}

func writeLocalCatalogFixture(t *testing.T, catalogID string, entries []Entry, payloads map[string][]byte) localCatalogFixture {
	t.Helper()
	root := t.TempDir()
	indexBytes, err := json.Marshal(Index{
		SchemaVersion: "1.0.0",
		CatalogID:     catalogID,
		Entries:       entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.json")
	if err := os.WriteFile(indexPath, indexBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	fetcher := make(fixtureFileFetcher)
	for index, entry := range entries {
		payload, ok := payloads[entry.MetadataURI]
		if !ok {
			continue
		}
		releasePath := filepath.Join(root, "releases", fmt.Sprintf("%02d.json", index))
		if err := os.MkdirAll(filepath.Dir(releasePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(releasePath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		fetcher[entry.MetadataURI] = releasePath
	}
	return localCatalogFixture{indexPath: indexPath, fetcher: fetcher}
}

func loadLocalCatalogFixture(t *testing.T, fixture localCatalogFixture, fetcher Fetcher, validator Validator) (*Catalog, error) {
	t.Helper()
	indexBytes, err := os.ReadFile(fixture.indexPath)
	if err != nil {
		t.Fatal(err)
	}
	indexURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(fixture.indexPath)}).String()
	return LoadFromSource(context.Background(), indexBytes, indexURI, fetcher, validator)
}

func mergeFixtureFetchers(fixtures ...localCatalogFixture) fixtureFileFetcher {
	merged := make(fixtureFileFetcher)
	for _, fixture := range fixtures {
		for uri, path := range fixture.fetcher {
			merged[uri] = path
		}
	}
	return merged
}

// TestREG019LocalFixtureCatalogsLoadMultipleIndexesOffline exercises actual
// local Catalog Index files, the production schema validator, duplicate
// identity handling and multiple independent Catalogs without network access.
func TestREG019LocalFixtureCatalogsLoadMultipleIndexesOffline(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/engine.git")
	ui := fixtureReleasePayload(t, "ui", "2.0.0", "https://source.example.invalid/ui.git")

	engineEntry := fixtureEntry("catalog-one", "engine", "1.0.0", engine)
	uiEntry := fixtureEntry("catalog-two", "ui", "2.0.0", ui)
	first := writeLocalCatalogFixture(t, "catalog.one", []Entry{engineEntry, engineEntry}, map[string][]byte{
		engineEntry.MetadataURI: engine,
	})
	second := writeLocalCatalogFixture(t, "catalog.two", []Entry{uiEntry}, map[string][]byte{
		uiEntry.MetadataURI: ui,
	})
	fetcher := mergeFixtureFetchers(first, second)

	loadedFirst, err := loadLocalCatalogFixture(t, first, fetcher, validator)
	if err != nil {
		t.Fatal(err)
	}
	loadedSecond, err := loadLocalCatalogFixture(t, second, fetcher, validator)
	if err != nil {
		t.Fatal(err)
	}
	if loadedFirst.ID != "catalog.one" || loadedSecond.ID != "catalog.two" {
		t.Fatalf("Catalog IDs = %q, %q", loadedFirst.ID, loadedSecond.ID)
	}
	if len(loadedFirst.Releases) != 1 || len(loadedSecond.Releases) != 1 {
		t.Fatalf("identical duplicate was not deduplicated or second Catalog missing: first=%d second=%d",
			len(loadedFirst.Releases), len(loadedSecond.Releases))
	}
}

// TestREG019LocalFixtureCatalogConflictingIdentityFailsClosed verifies two
// different verified metadata documents cannot claim the same release identity.
func TestREG019LocalFixtureCatalogConflictingIdentityFailsClosed(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	firstPayload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/one.git")
	secondPayload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/two.git")
	firstEntry := fixtureEntry("conflict", "engine", "1.0.0", firstPayload)
	secondEntry := fixtureEntry("conflict", "engine", "1.0.0", secondPayload)
	fixture := writeLocalCatalogFixture(t, "catalog.conflict", []Entry{firstEntry, secondEntry}, map[string][]byte{
		firstEntry.MetadataURI: firstPayload,
		secondEntry.MetadataURI: secondPayload,
	})

	got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
	if err == nil || got != nil {
		t.Fatalf("LoadFromSource returned %#v, %v; want nil Catalog and conflict", got, err)
	}
}

// TestREG019LocalFixtureCatalogRejectsInvalidInputs checks missing metadata,
// malformed JSON, invalid release schemas, invalid digests and invalid Index
// schemas using deterministic local files and no network access.
func TestREG019LocalFixtureCatalogRejectsInvalidInputs(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing release metadata", func(t *testing.T) {
		payload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/repo.git")
		entry := fixtureEntry("missing", "engine", "1.0.0", payload)
		fixture := writeLocalCatalogFixture(t, "catalog.missing", []Entry{entry}, nil)
		got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
		if err == nil || got != nil {
			t.Fatalf("LoadFromSource returned %#v, %v; want missing-metadata failure and nil Catalog", got, err)
		}
	})

	t.Run("malformed release JSON", func(t *testing.T) {
		payload := []byte("{")
		entry := fixtureEntry("malformed", "engine", "1.0.0", payload)
		fixture := writeLocalCatalogFixture(t, "catalog.malformed", []Entry{entry}, map[string][]byte{entry.MetadataURI: payload})
		got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
		if err == nil || got != nil {
			t.Fatalf("LoadFromSource returned %#v, %v; want malformed-JSON failure and nil Catalog", got, err)
		}
	})

	t.Run("invalid release schema", func(t *testing.T) {
		payload := []byte("{\"schema_version\":\"1.0.0\",\"component_id\":\"engine\"}")
		entry := fixtureEntry("invalid-schema", "engine", "1.0.0", payload)
		fixture := writeLocalCatalogFixture(t, "catalog.invalid-schema", []Entry{entry}, map[string][]byte{entry.MetadataURI: payload})
		got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
		if err == nil || got != nil {
			t.Fatalf("LoadFromSource returned %#v, %v; want schema failure and nil Catalog", got, err)
		}
	})

	t.Run("invalid digest", func(t *testing.T) {
		payload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/repo.git")
		entry := fixtureEntry("bad-digest", "engine", "1.0.0", payload)
		entry.MetadataSHA = digest([]byte("different bytes"))
		fixture := writeLocalCatalogFixture(t, "catalog.bad-digest", []Entry{entry}, map[string][]byte{entry.MetadataURI: payload})
		got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
		if err == nil || got != nil {
			t.Fatalf("LoadFromSource returned %#v, %v; want digest failure and nil Catalog", got, err)
		}
	})

	t.Run("invalid Catalog Index schema", func(t *testing.T) {
		got, err := Load(context.Background(),
			[]byte("{\"schema_version\":\"1.0.0\",\"catalog_id\":\"broken\",\"entries\":\"not-an-array\"}"),
			fixtureFileFetcher{}, validator)
		if err == nil || got != nil {
			t.Fatalf("Load returned %#v, %v; want Index-schema failure and nil Catalog", got, err)
		}
	})
}

// TestREG019LocalFixtureCatalogDoesNotExposePartialResults verifies that a
// valid first entry is discarded when any later release cannot be fetched.
func TestREG019LocalFixtureCatalogDoesNotExposePartialResults(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	payload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/repo.git")
	first := fixtureEntry("partial", "engine", "1.0.0", payload)
	second := fixtureEntry("partial", "ui", "1.0.0", fixtureReleasePayload(t, "ui", "1.0.0", "https://source.example.invalid/ui.git"))
	fixture := writeLocalCatalogFixture(t, "catalog.partial", []Entry{first, second}, map[string][]byte{
		first.MetadataURI: payload,
	})

	got, err := loadLocalCatalogFixture(t, fixture, fixture.fetcher, validator)
	if err == nil || got != nil {
		t.Fatalf("LoadFromSource returned %#v, %v; failure must not expose a partial Catalog", got, err)
	}
}


type hostileFixtureFetcher struct {
	err error
}

func (f hostileFixtureFetcher) Fetch(context.Context, string) ([]byte, error) {
	return nil, f.err
}

// TestREG019CatalogLoaderRedactsUntrustedFetcherErrors proves the Catalog
// boundary does not re-emit arbitrary error text from an injected Fetcher.
func TestREG019CatalogLoaderRedactsUntrustedFetcherErrors(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	const secret = "fixture-secret-marker-a81d"
	payload := fixtureReleasePayload(t, "engine", "1.0.0", "https://source.example.invalid/repo.git")
	entry := fixtureEntry("redaction", "engine", "1.0.0", payload)
	entry.MetadataURI = "https://metadata.example.invalid/release.json?token=" + secret
	indexBytes, err := json.Marshal(Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "catalog.redaction",
		Entries:       []Entry{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetcherError := fmt.Errorf("remote failure at https://user:%s@metadata.example.invalid/release.json?token=%s", secret, secret)
	got, err := Load(context.Background(), indexBytes, hostileFixtureFetcher{err: fetcherError}, validator)
	if err == nil || got != nil {
		t.Fatalf("Load returned %#v, %v; want fetch failure and nil Catalog", got, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "metadata.example.invalid") {
		t.Fatalf("Catalog diagnostic leaked untrusted Fetcher error or URI: %v", err)
	}
	if !strings.Contains(err.Error(), "transport failure") {
		t.Fatalf("Catalog diagnostic did not preserve a safe failure classification: %v", err)
	}
}
