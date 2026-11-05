package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	catalogModel "github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

const (
	testRevision = "0123456789abcdef0123456789abcdef01234567"
	testDigest   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func resolvedFixture() (*resolver.Graph, []collector.Source) {
	graph := &resolver.Graph{
		SchemaVersion: schemaVersion,
		Catalogs: []resolver.CatalogReference{{
			CatalogID:   "stable",
			MetadataURI: "https://catalog.example.invalid/index.json",
			MetadataSHA: testDigest,
		}},
		Roots: []resolver.Requirement{{ComponentID: "engine", Variant: "default", VersionConstraint: "=1.0.0"}},
		Components: []resolver.ResolvedComponent{{
			ComponentID:        "engine",
			Variant:            "default",
			Version:            "1.0.0",
			ReleaseMetadataURI: "https://catalog.example.invalid/releases/engine-1.0.0.json",
			ReleaseMetadataSHA: testDigest,
			SourceUnits: []catalogModel.SourceUnit{{
				ID:            "source",
				RepositoryURI: "https://github.com/example/engine.git",
				Revision:      testRevision,
			}},
			Dependencies: []resolver.ResolvedDependency{},
			Contracts: catalogModel.Contracts{
				Provides: []catalogModel.ContractProvider{},
				Requires: []catalogModel.ContractRequirement{},
			},
		}},
		ResolutionOrder: []resolver.Node{{ComponentID: "engine", Variant: "default", Version: "1.0.0"}},
	}
	collected := []collector.Source{{
		ComponentID:   "engine",
		Variant:       "default",
		SourceUnitID:  "source",
		Revision:      testRevision,
		WorkspacePath: "components/engine/default/sources/source",
	}}
	return graph, collected
}

// TestREG023BuildManifestMatchesPublicContract builds a document from exact
// resolver and collector identities, then validates it against the embedded
// Spec-owned Manifest schema instead of merely testing struct construction.
func TestREG023BuildManifestMatchesPublicContract(t *testing.T) {
	graph, sources := resolvedFixture()
	document, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(validation.ResolvedManifest, encoded, "registry-manifest.json"); err != nil {
		t.Fatalf("manifest does not satisfy embedded Spec schema: %v\n%s", err, encoded)
	}
	if !bytes.Contains(encoded, []byte(testRevision)) || !bytes.Contains(encoded, []byte(testDigest)) {
		t.Fatal("manifest does not preserve immutable source/catalog identities")
	}
	if bytes.Contains(encoded, []byte("/tmp/")) || bytes.Contains(encoded, []byte("C:\\")) {
		t.Fatal("manifest must not contain absolute machine paths")
	}
}

// TestREG023RejectsMissingOrExtraCollectorResults prevents a manifest from
// claiming a complete workspace when a selected or undeclared source is absent.
func TestREG023RejectsMissingOrExtraCollectorResults(t *testing.T) {
	graph, sources := resolvedFixture()
	if _, err := Build(graph, nil, "1.2.3"); err == nil {
		t.Fatal("expected missing Collector result to fail")
	}
	sources = append(sources, collector.Source{
		ComponentID:   "engine",
		Variant:       "default",
		SourceUnitID:  "extra",
		Revision:      testRevision,
		WorkspacePath: "components/engine/default/sources/extra",
	})
	if _, err := Build(graph, sources, "1.2.3"); err == nil {
		t.Fatal("expected undeclared Collector result to fail")
	}
}

// TestREG024CanonicalJSONOrdersKeysByUTF16 verifies JCS property ordering and
// string escaping, including a supplementary Unicode code point.
func TestREG024CanonicalJSONOrdersKeysByUTF16(t *testing.T) {
	input := map[string]any{
		"z":          "<&",
		"\U0001F600": "emoji",
		"\uE000":     "bmp",
		"a":          "line\nfeed",
	}
	got, err := MarshalCanonical(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":\"line\\nfeed\",\"z\":\"<&\",\"\U0001F600\":\"emoji\",\"\uE000\":\"bmp\"}"
	if string(got) != want {
		t.Fatalf("canonical output\n got: %s\nwant: %s", got, want)
	}
	if err := ParseCanonicalStringOnly(got); err != nil {
		t.Fatalf("canonical output did not verify: %v", err)
	}
}

// TestREG024CanonicalInputVerifierRejectsNonCanonicalBytes ensures replay
// inputs are verified byte-for-byte rather than silently normalized. In
// particular, duplicate properties cannot be accepted as ordinary JSON maps.
func TestREG024CanonicalInputVerifierRejectsNonCanonicalBytes(t *testing.T) {
	for _, input := range []string{
		`{"b":2,"a":1}`,       // Object properties are not in JCS order.
		`{"a":1, "b":2}`,      // Canonical JSON contains no insignificant whitespace.
		`{"a":1,"a":1}`,       // Duplicate object names are invalid for deterministic replay.
		`{"n":1.0}`,           // JCS serializes this number as 1.
		`{"a":1}{"b":2}`,      // A second top-level JSON value is forbidden.
	} {
		if err := ParseCanonicalStringOnly([]byte(input)); err == nil {
			t.Errorf("ParseCanonicalStringOnly(%q) unexpectedly succeeded", input)
		}
	}
}

// TestREG024CanonicalJSONNumbersUseECMAScriptFormatting verifies the number
// formatting edge cases required by RFC 8785, including notation thresholds.
func TestREG024CanonicalJSONNumbersUseECMAScriptFormatting(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: "333333333.33333329", want: "333333333.3333333"},
		{input: "4.50", want: "4.5"},
		{input: "2e-3", want: "0.002"},
		{input: "1e-6", want: "0.000001"},
		{input: "1e-7", want: "1e-7"},
		{input: "1e20", want: "100000000000000000000"},
		{input: "1e21", want: "1e+21"},
		{input: "-0", want: "0"},
	}
	for _, test := range cases {
		got, err := MarshalCanonical(map[string]any{"n": json.Number(test.input)})
		if err != nil {
			t.Fatalf("number %s: %v", test.input, err)
		}
		want := "{\"n\":" + test.want + "}"
		if string(got) != want {
			t.Errorf("number %s: got %s, want %s", test.input, got, want)
		}
	}
	if _, err := MarshalCanonical(map[string]any{"n": json.Number("1e400")}); err == nil {
		t.Fatal("expected non-finite/out-of-range number to be rejected")
	}
}

// TestREG024ReportSerializationNormalizesEmptyCollections ensures required
// report arrays remain JSON arrays and conflict ordering is deterministic.
func TestREG024ReportSerializationNormalizesEmptyCollections(t *testing.T) {
	report := resolver.Report{
		SchemaVersion: schemaVersion,
		Result:        "failure",
		Conflicts: []resolver.Conflict{
			{Code: "Z_CONFLICT", Message: "last", InvolvedComponents: []string{"z", "a"}},
			{Code: "A_CONFLICT", Message: "first"},
		},
	}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"selected", "rejected_candidates", "conflicts", "warnings"} {
		if _, ok := decoded[field].([]any); !ok {
			t.Fatalf("%s must be an array, got %#v", field, decoded[field])
		}
	}
	if strings.Index(string(encoded), "A_CONFLICT") > strings.Index(string(encoded), "Z_CONFLICT") {
		t.Fatal("report conflicts are not sorted")
	}
}

func TestREG025FailureCannotBuildManifest(t *testing.T) {
	graph, sources := resolvedFixture()
	graph = nil
	if _, err := Build(graph, sources, "1.2.3"); err == nil {
		t.Fatal("failed resolution must not produce a manifest")
	}
}
