package collector

import (
	"bytes"
	"sort"
	"testing"
)

func FuzzGitObjectIDsFromManifest(f *testing.F) {
	f.Add([]byte(`{"components":[{"source_units":[{"revision":"0123456789abcdef0123456789abcdef01234567"}]}]}`))
	f.Add([]byte(`{"components":[{"source_units":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]}`))
	f.Add([]byte(`{"components":[{"source_units":[{"revision":"short"}]}]}`))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		ids, err := gitObjectIDsFromManifest(input)
		if err != nil {
			return
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatal("Git object IDs are not sorted")
		}
		for i, id := range ids {
			if !isFullObjectID(id) || id != string(bytes.ToLower([]byte(id))) {
				t.Fatalf("invalid normalized object ID %q", id)
			}
			if i > 0 && ids[i-1] == id {
				t.Fatalf("duplicate object ID %q", id)
			}
		}
	})
}
