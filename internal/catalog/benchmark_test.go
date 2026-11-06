package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/cdin-editor/registry/internal/validation"
)

type benchmarkValidator struct{}

func (benchmarkValidator) Validate(validation.Kind, []byte, string) error { return nil }

func BenchmarkLoadLargeCatalog(b *testing.B) {
	const entriesCount = 512
	entries := make([]Entry, 0, entriesCount)
	payloads := make(map[string][]byte, entriesCount)
	for i := 0; i < entriesCount; i++ {
		component := fmt.Sprintf("engine-%04d", i)
		uri := fmt.Sprintf("mem://benchmark/%04d", i)
		payload := releaseBytes(component, "default", "1.0.0")
		entries = append(entries, Entry{
			ComponentID: component,
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: uri,
			MetadataSHA: digest(payload),
		})
		payloads[uri] = payload
	}
	index := indexBytes(entries...)
	fetcher := mapFetcher(payloads)
	validator := benchmarkValidator{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := Load(context.Background(), index, fetcher, validator)
		if err != nil {
			b.Fatal(err)
		}
		if len(loaded.Releases) != entriesCount {
			b.Fatalf("loaded %d releases, want %d", len(loaded.Releases), entriesCount)
		}
	}
}
