package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

type offlineCatalogFetcher map[string][]byte

func (f offlineCatalogFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, ok := f[uri]
	if !ok {
		return nil, fmt.Errorf("fixture release metadata missing")
	}
	return append([]byte(nil), payload...), nil
}

func cliFixtureDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func cliFixtureRelease(t *testing.T, component string) []byte {
	t.Helper()
	payload, err := json.Marshal(catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   component,
		Variant:       "default",
		Version:       "1.0.0",
		Status:        "active",
		SourceUnits: []catalog.SourceUnit{{
			ID:            "source",
			RepositoryURI: "https://source.example.invalid/" + component + ".git",
			Revision:      "0123456789abcdef0123456789abcdef01234567",
		}},
		Dependencies: []catalog.Dependency{},
		Contracts:    catalog.Contracts{Provides: []catalog.ContractProvider{}, Requires: []catalog.ContractRequirement{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeCLIIndexFixture(t *testing.T, directory, catalogID, component string, payload []byte) (string, string) {
	t.Helper()
	metadataURI := "https://metadata.example.invalid/" + catalogID + "/" + component + ".json"
	index, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     catalogID,
		Entries: []catalog.Entry{{
			ComponentID: component,
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: metadataURI,
			MetadataSHA: cliFixtureDigest(payload),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, catalogID+"-index.json")
	if err := os.WriteFile(path, index, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, metadataURI
}

// TestREG019ResolveComposesMultipleLocalFixtureCatalogsOffline exercises the
// production CLI composition path with local Catalog Index files and injected
// offline release bytes. No external network or product-specific branch is used.
func TestREG019ResolveComposesMultipleLocalFixtureCatalogsOffline(t *testing.T) {
	directory := t.TempDir()
	productPath := filepath.Join(directory, "product.json")
	product := []byte("{\"schema_version\":\"1.0.0\",\"components\":[{\"component_id\":\"engine\",\"version_constraint\":\"=1.0.0\"},{\"component_id\":\"frontend\",\"version_constraint\":\"=1.0.0\"}]}")
	if err := os.WriteFile(productPath, product, 0o600); err != nil {
		t.Fatal(err)
	}

	engine := cliFixtureRelease(t, "engine")
	frontend := cliFixtureRelease(t, "frontend")
	firstIndex, engineURI := writeCLIIndexFixture(t, directory, "catalog-one", "engine", engine)
	secondIndex, frontendURI := writeCLIIndexFixture(t, directory, "catalog-two", "frontend", frontend)
	fetcher := offlineCatalogFetcher{
		engineURI:    engine,
		frontendURI: frontend,
	}

	args := []string{
		"resolve",
		"--product", productPath,
		"--catalog", firstIndex,
		"--catalog", secondIndex,
		"--allow-host", "metadata.example.invalid",
	}
	var stdout, stderr bytes.Buffer
	factoryCalls := 0
	code := runExecutionWithFetcherFactory(context.Background(), args[1:], false, &stdout, &stderr,
		func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
			factoryCalls++
			if len(options.AllowedHosts) != 1 || options.AllowedHosts[0] != "metadata.example.invalid" {
				t.Fatalf("fetcher received unexpected host policy: %#v", options.AllowedHosts)
			}
			return fetcher, nil
		})
	if code != exitOK {
		t.Fatalf("resolve exit code = %d, stderr=%s", code, stderr.String())
	}
	if factoryCalls != 1 {
		t.Fatalf("fetcher factory called %d times, want once", factoryCalls)
	}
	for _, component := range []string{"engine", "frontend"} {
		if !strings.Contains(stdout.String(), component) {
			t.Errorf("Resolution Report omitted %q: %s", component, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "\"result\":\"success\"") {
		t.Fatalf("unexpected Resolution Report: %s", stdout.String())
	}
}
