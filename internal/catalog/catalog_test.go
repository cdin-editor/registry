package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/cdin-editor/registry/internal/validation"
)

type mapFetcher map[string][]byte

func (m mapFetcher) Fetch(_ context.Context, uri string) ([]byte, error) {
	data, ok := m[uri]
	if !ok {
		return nil, fmt.Errorf("not found: %s", uri)
	}
	return append([]byte(nil), data...), nil
}

type acceptingValidator struct{ calls []validation.Kind }

func (v *acceptingValidator) Validate(kind validation.Kind, _ []byte, _ string) error {
	v.calls = append(v.calls, kind)
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func releaseBytes(component, variant, version string) []byte {
	variantField := ""
	if variant != "" {
		variantField = fmt.Sprintf(`,"variant":%q`, variant)
	}
	return []byte(fmt.Sprintf(`{"schema_version":"1.0.0","component_id":%q%s,"version":%q,"status":"active","source_units":[{"source_unit_id":"source","repository_uri":"https://example.invalid/repo.git","revision":"0123456789012345678901234567890123456789"}]}`, component, variantField, version))
}

func indexBytes(entries ...Entry) []byte {
	data := []byte(`{"schema_version":"1.0.0","catalog_id":"test.catalog","entries":[`)
	for i, entry := range entries {
		if i > 0 {
			data = append(data, ',')
		}
		item := fmt.Sprintf(`{"component_id":%q,"version":%q,"metadata_uri":%q,"metadata_sha256":%q}`,
			entry.ComponentID, entry.Version, entry.MetadataURI, entry.MetadataSHA)
		if entry.Variant != "" {
			item = fmt.Sprintf(`{"component_id":%q,"variant":%q,"version":%q,"metadata_uri":%q,"metadata_sha256":%q}`,
				entry.ComponentID, entry.Variant, entry.Version, entry.MetadataURI, entry.MetadataSHA)
		}
		data = append(data, item...)
	}
	return append(data, []byte(`]}`)...)
}

func TestLoadVerifiesDigestBeforeReleaseValidation(t *testing.T) {
	payload := releaseBytes("engine", "", "1.2.3")
	validator := &acceptingValidator{}
	got, err := Load(context.Background(), indexBytes(Entry{
		ComponentID: "engine", Version: "1.2.3", MetadataURI: "mem://release", MetadataSHA: digest(payload),
	}), mapFetcher{"mem://release": payload}, validator)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(got.Releases))
	}
	if got.Releases[0].Variant != "default" {
		t.Fatalf("variant = %q, want default", got.Releases[0].Variant)
	}
	if len(validator.calls) != 2 || validator.calls[0] != validation.CatalogIndex || validator.calls[1] != validation.ComponentRelease {
		t.Fatalf("unexpected validation order: %#v", validator.calls)
	}
}

func TestLoadRejectsDigestMismatchBeforeParsing(t *testing.T) {
	payload := releaseBytes("engine", "default", "1.2.3")
	validator := &acceptingValidator{}
	_, err := Load(context.Background(), indexBytes(Entry{
		ComponentID: "engine", Version: "1.2.3", MetadataURI: "mem://release", MetadataSHA: digest([]byte("different")),
	}), mapFetcher{"mem://release": payload}, validator)
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
	if len(validator.calls) != 1 || validator.calls[0] != validation.CatalogIndex {
		t.Fatalf("release metadata was validated before digest verification: %#v", validator.calls)
	}
}

func TestLoadRejectsIndexReleaseIdentityMismatch(t *testing.T) {
	payload := releaseBytes("other", "default", "1.2.3")
	_, err := Load(context.Background(), indexBytes(Entry{
		ComponentID: "engine", Version: "1.2.3", MetadataURI: "mem://release", MetadataSHA: digest(payload),
	}), mapFetcher{"mem://release": payload}, &acceptingValidator{})
	if err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestLoadDeduplicatesIdenticalEntriesAndRejectsConflicts(t *testing.T) {
	payload := releaseBytes("engine", "default", "1.2.3")
	entry := Entry{ComponentID: "engine", Variant: "default", Version: "1.2.3", MetadataURI: "mem://release", MetadataSHA: digest(payload)}
	got, err := Load(context.Background(), indexBytes(entry, entry), mapFetcher{"mem://release": payload}, &acceptingValidator{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Releases) != 1 {
		t.Fatalf("got %d releases, want one deduplicated release", len(got.Releases))
	}

	other := releaseBytes("engine", "default", "1.2.3")
	other = append(other[:len(other)-1], []byte(`,"extensions":{"publisher":"different"}}`)...)
	_, err = Load(context.Background(), indexBytes(entry,
		Entry{ComponentID: "engine", Variant: "default", Version: "1.2.3", MetadataURI: "mem://other", MetadataSHA: digest(other)},
	), mapFetcher{"mem://release": payload, "mem://other": other}, &acceptingValidator{})
	if err == nil {
		t.Fatal("expected conflicting metadata identity")
	}
}

func TestLoadHonorsCancellationAndRequiredDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Load(ctx, []byte("{}"), mapFetcher{}, &acceptingValidator{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	_, err = Load(context.Background(), []byte("{}"), nil, &acceptingValidator{})
	if err == nil {
		t.Fatal("expected missing fetcher error")
	}
}
