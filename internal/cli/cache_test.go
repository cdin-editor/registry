package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

func canonicalCacheTestManifest(t *testing.T) []byte {
	t.Helper()
	document := manifest.Document{
		SchemaVersion:   "1.0.0",
		RegistryVersion: "0.1.0-dev",
		Catalogs: []manifest.Catalog{{
			CatalogID: "example", MetadataURI: "https://example.invalid/catalog.json",
			MetadataSHA: "0000000000000000000000000000000000000000000000000000000000000000",
		}},
		Roots: []manifest.Root{{ComponentID: "backend", Variant: "default", Version: "1.0.0"}},
		Components: []manifest.Component{{
			ComponentID: "backend", Variant: "default", Version: "1.0.0",
			ReleaseMetadataURI: "https://example.invalid/backend.json",
			ReleaseMetadataSHA: "0000000000000000000000000000000000000000000000000000000000000000",
			SourceUnits: []manifest.SourceUnit{{
				SourceUnitID: "source", RepositoryURI: "https://github.com/example/backend",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				WorkspacePath: "components/backend/default/sources/source",
			}},
			Dependencies: []manifest.Dependency{},
		}},
		ResolutionOrder: []resolver.Node{{ComponentID: "backend", Variant: "default", Version: "1.0.0"}},
	}
	encoded, err := manifest.MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(encoded, "", validator); err != nil {
		t.Fatalf("test Manifest is invalid: %v", err)
	}
	return encoded
}

func TestRegistryCachePinUnpinAndGC(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	manifestBytes := canonicalCacheTestManifest(t)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	var pinOut, pinErr bytes.Buffer
	code := runCache(context.Background(), []string{"pin", "--manifest", manifestPath, "--cache-dir", cacheDir}, bytes.NewReader(nil), &pinOut, &pinErr)
	if code != exitOK {
		t.Fatalf("pin exit=%d stderr=%q", code, pinErr.String())
	}
	var pinned cacheReceipt
	if err := json.Unmarshal(pinOut.Bytes(), &pinned); err != nil {
		t.Fatal(err)
	}
	if pinned.Operation != "pin" || len(pinned.ManifestSHA256) != 64 {
		t.Fatalf("pin receipt = %+v", pinned)
	}

	var gcOut, gcErr bytes.Buffer
	code = runCache(context.Background(), []string{"gc", "--cache-dir", cacheDir}, bytes.NewReader(nil), &gcOut, &gcErr)
	if code != exitOK {
		t.Fatalf("gc exit=%d stderr=%q", code, gcErr.String())
	}
	var collected cacheReceipt
	if err := json.Unmarshal(gcOut.Bytes(), &collected); err != nil {
		t.Fatal(err)
	}
	if collected.Operation != "gc" || collected.ProtectedEntries != 2 {
		t.Fatalf("GC receipt = %+v, want both Manifest metadata identities protected", collected)
	}

	var unpinOut, unpinErr bytes.Buffer
	code = runCache(context.Background(), []string{"unpin", "--manifest-sha256", pinned.ManifestSHA256, "--cache-dir", cacheDir}, bytes.NewReader(nil), &unpinOut, &unpinErr)
	if code != exitOK {
		t.Fatalf("unpin exit=%d stderr=%q", code, unpinErr.String())
	}
}

func TestRegistryCachePinRejectsInvalidManifest(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	manifestPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(manifestPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runCache(context.Background(), []string{"pin", "--manifest", manifestPath, "--cache-dir", cacheDir}, bytes.NewReader(nil), &stdout, &stderr)
	if code != exitInvalid {
		t.Fatalf("pin exit=%d, want invalid input; stderr=%q", code, stderr.String())
	}
	pins, err := os.ReadDir(filepath.Join(cacheDir, ".registry-cache", "pins"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 0 {
		t.Fatalf("invalid Manifest created %d pin files", len(pins))
	}
}
