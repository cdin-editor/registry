package catalog

import (
	"context"
	"testing"

	"github.com/cdin-editor/registry/internal/validation"
)

func FuzzLoadReleaseMetadata(f *testing.F) {
	f.Add(releaseBytes("engine", "default", "1.0.0"))
	f.Add([]byte(`{"schema_version":"1.0.0","component_id":"engine","variant":"default","version":"1.0.0","status":"active","source_units":[]}`))
	f.Add([]byte("{"))
	f.Add([]byte{})
	validator, err := validation.New()
	if err != nil {
		f.Fatalf("initialize embedded validator: %v", err)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 1<<20 {
			t.Skip()
		}
		entry := Entry{
			ComponentID: "engine",
			Variant:     "default",
			Version:     "1.0.0",
			MetadataURI: "mem://fuzz/release",
			MetadataSHA: digest(payload),
		}
		_, _ = Load(
			context.Background(),
			indexBytes(entry),
			mapFetcher{entry.MetadataURI: payload},
			validator,
		)
	})
}
