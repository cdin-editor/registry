package catalog

import (
	"encoding/json"
	"sort"
	"testing"
)

func FuzzMetadataKeysFromManifest(f *testing.F) {
	f.Add([]byte(`{"catalogs":[{"metadata_uri":"https://example.invalid/index.json","metadata_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"components":[]}`))
	f.Add([]byte(`{"catalogs":[],"components":[{"release_metadata_uri":"https://example.invalid/release.json","release_metadata_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`))
	f.Add([]byte(`{"catalogs":[{"metadata_uri":"","metadata_sha256":"bad"}],"components":[]}`))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		keys, err := metadataKeysFromManifest(input)
		if err != nil {
			return
		}
		if !sort.StringsAreSorted(keys) {
			t.Fatal("metadata cache keys are not sorted")
		}
		for i, key := range keys {
			if len(key) != 64 {
				t.Fatalf("cache key length = %d, want SHA-256", len(key))
			}
			if i > 0 && keys[i-1] == key {
				t.Fatalf("duplicate metadata cache key %q", key)
			}
		}
		if _, err := json.Marshal(keys); err != nil {
			t.Fatalf("cache keys cannot be serialized: %v", err)
		}
	})
}
