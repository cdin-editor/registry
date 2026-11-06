package validation

import (
	"encoding/json"
	"testing"
)

func FuzzDecodeJSONStrict(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{}`),
		[]byte(`{"a":1}`),
		[]byte(`{"a":1,"a":2}`),
		[]byte(`{"a":1} {}`),
		[]byte(`[null,true,1,"text"]`),
		[]byte(`{"number":1e400}`),
		[]byte("{"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		document, err := decodeJSON(input)
		if err != nil {
			return
		}
		if _, err := json.Marshal(document); err != nil {
			t.Fatalf("decodeJSON returned a value that cannot be re-encoded: %v", err)
		}
	})
}

func FuzzDecodeYAMLStrict(f *testing.F) {
	seeds := []string{
		"schema_version: \"1.0.0\"\ncomponents: []\n",
		"a: 1\na: 2\n",
		"base: &base {x: 1}\nvalue: *base\n",
		"loop: &loop [*loop]\n",
		"items: !custom []\n",
		"first: 1\n---\nsecond: 2\n",
		"\tbad: indent\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		document, err := decodeYAML([]byte(input))
		if err != nil {
			return
		}
		if _, err := json.Marshal(document); err != nil {
			t.Fatalf("decodeYAML returned a value that cannot be re-encoded: %v", err)
		}
	})
}
