package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/validation"
)

type countingFetcher struct {
	calls int
}

func (f *countingFetcher) Fetch(context.Context, string) ([]byte, error) {
	f.calls++
	return nil, fmt.Errorf("unexpected fetch")
}

func TestLoadEnforcesCatalogIndexByteBudgetBeforeParsing(t *testing.T) {
	validator := &acceptingValidator{}
	fetcher := &countingFetcher{}
	index := make([]byte, maxCatalogIndexBytes+1)
	_, err := Load(context.Background(), index, fetcher, validator)
	if err == nil || !strings.Contains(err.Error(), "Catalog Index exceeds") {
		t.Fatalf("Load() error = %v, want Catalog Index byte-budget error", err)
	}
	if len(validator.calls) != 0 || fetcher.calls != 0 {
		t.Fatalf("oversized index reached validator/fetcher: validator=%v fetches=%d", validator.calls, fetcher.calls)
	}
}

func TestLoadEnforcesCatalogEntryCountBeforeFetching(t *testing.T) {
	entries := make([]Entry, maxCatalogEntries+1)
	for i := range entries {
		entries[i] = Entry{
			ComponentID: fmt.Sprintf("engine-%04d", i),
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: fmt.Sprintf("mem://release/%04d", i),
			MetadataSHA: strings.Repeat("a", 64),
		}
	}
	validator := &acceptingValidator{}
	fetcher := &countingFetcher{}
	_, err := Load(context.Background(), indexBytes(entries...), fetcher, validator)
	if err == nil || !strings.Contains(err.Error(), "entry count exceeds the limit") {
		t.Fatalf("Load() error = %v, want Catalog entry-count error", err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("oversized Catalog started %d metadata requests", fetcher.calls)
	}
}

func TestLoadEnforcesPerReleaseMetadataByteBudgetBeforeSchemaValidation(t *testing.T) {
	payload := make([]byte, maxReleaseMetadataBytes+1)
	validator := &acceptingValidator{}
	entry := Entry{
		ComponentID: "engine",
		Variant:     "default",
		Version:     "1.0.0",
		MetadataURI: "mem://release/oversized",
		MetadataSHA: digest(payload),
	}
	_, err := Load(context.Background(), indexBytes(entry), mapFetcher{entry.MetadataURI: payload}, validator)
	if err == nil || !strings.Contains(err.Error(), "Release Metadata exceeds") {
		t.Fatalf("Load() error = %v, want per-release metadata byte-budget error", err)
	}
	if len(validator.calls) != 1 || validator.calls[0] != validation.CatalogIndex {
		t.Fatalf("oversized Release Metadata reached schema validation: %v", validator.calls)
	}
}

func TestLoadRejectsOversizedFetchedPayloadBeforeSchemaValidation(t *testing.T) {
	payload := make([]byte, maxReleaseMetadataBytes+1)
	entry := Entry{
		ComponentID: "engine",
		Variant:     "default",
		Version:     "1.0.0",
		MetadataURI: "mem://release/oversized-fetch",
		MetadataSHA: digest(payload),
	}
	validator := &acceptingValidator{}
	loaded, err := LoadWithOptions(
		context.Background(),
		indexBytes(entry),
		mapFetcher{entry.MetadataURI: payload},
		validator,
		LoadOptions{MaxConcurrentMetadataFetches: 1},
	)
	if err == nil || loaded != nil || !strings.Contains(err.Error(), "Release Metadata exceeds") {
		t.Fatalf("LoadWithOptions() catalog=%v err=%v; want oversized fetch rejection and no partial Catalog", loaded, err)
	}
	if len(validator.calls) != 1 || validator.calls[0] != validation.CatalogIndex {
		t.Fatalf("oversized payload reached release schema validation: %v", validator.calls)
	}
}

func TestAccountCatalogMetadataBytesEnforcesAggregateBudget(t *testing.T) {
	got, err := accountCatalogMetadataBytes(0, maxReleaseMetadataBytes)
	if err != nil || got != maxReleaseMetadataBytes {
		t.Fatalf("account exact per-entry limit = %d, %v", got, err)
	}
	if _, err := accountCatalogMetadataBytes(0, maxReleaseMetadataBytes+1); err == nil {
		t.Fatal("account accepted a Release Metadata entry above the per-entry limit")
	}
	if _, err := accountCatalogMetadataBytes(maxCatalogMetadataBytes-1, 2); err == nil {
		t.Fatal("account accepted an aggregate metadata size above the Catalog budget")
	}
	if _, err := accountCatalogMetadataBytes(-1, 1); err == nil {
		t.Fatal("account accepted a negative current-byte total")
	}
}
