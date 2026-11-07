package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
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
		`{"b":2,"a":1}`,  // Object properties are not in JCS order.
		`{"a":1, "b":2}`, // Canonical JSON contains no insignificant whitespace.
		`{"a":1,"a":1}`,  // Duplicate object names are invalid for deterministic replay.
		`{"n":1.0}`,      // JCS serializes this number as 1.
		`{"a":1}{"b":2}`, // A second top-level JSON value is forbidden.
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

// TestREG024RFC8785PropertyOrderingVector uses the Unicode property-name
// ordering vector published in RFC 8785 section 3.2.3. UTF-16 ordering differs
// from Unicode scalar-value ordering for the supplementary emoji key.
func TestREG024RFC8785PropertyOrderingVector(t *testing.T) {
	input := map[string]any{
		"\u20ac":     "Euro Sign",
		"\r":         "Carriage Return",
		"\ufb33":     "Hebrew Letter Dalet With Dagesh",
		"1":          "One",
		"\U0001F600": "Emoji: Grinning Face",
		"\u0080":     "Control",
		"\u00f6":     "Latin Small Letter O With Diaeresis",
	}
	got, err := MarshalCanonical(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"ö\":\"Latin Small Letter O With Diaeresis\",\"€\":\"Euro Sign\",\"😀\":\"Emoji: Grinning Face\",\"דּ\":\"Hebrew Letter Dalet With Dagesh\"}"
	if string(got) != want {
		t.Fatalf("RFC 8785 property-order vector mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestREG024RFC8785NumberAppendixVectors checks the IEEE-754 bit-pattern
// examples in RFC 8785 Appendix B, including subnormals, exponent boundaries,
// values around 1e-6/1e21 and round-to-even behavior.
func TestREG024RFC8785NumberAppendixVectors(t *testing.T) {
	cases := []struct {
		bits uint64
		want string
	}{
		{0x0000000000000000, "0"},
		{0x8000000000000000, "0"},
		{0x0000000000000001, "5e-324"},
		{0x8000000000000001, "-5e-324"},
		{0x7fefffffffffffff, "1.7976931348623157e+308"},
		{0xffefffffffffffff, "-1.7976931348623157e+308"},
		{0x4340000000000000, "9007199254740992"},
		{0xc340000000000000, "-9007199254740992"},
		{0x4430000000000000, "295147905179352830000"},
		{0x44b52d02c7e14af5, "9.999999999999997e+22"},
		{0x44b52d02c7e14af6, "1e+23"},
		{0x44b52d02c7e14af7, "1.0000000000000001e+23"},
		{0x444b1ae4d6e2ef4e, "999999999999999700000"},
		{0x444b1ae4d6e2ef4f, "999999999999999900000"},
		{0x444b1ae4d6e2ef50, "1e+21"},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"},
		{0x3eb0c6f7a0b5ed8d, "0.000001"},
		{0x41b3de4355555553, "333333333.3333332"},
		{0x41b3de4355555554, "333333333.33333325"},
		{0x41b3de4355555555, "333333333.3333333"},
		{0x41b3de4355555556, "333333333.3333334"},
		{0x41b3de4355555557, "333333333.33333343"},
		{0xbecbf647612f3696, "-0.0000033333333333333333"},
		{0x43143ff3c1cb0959, "1424953923781206.2"},
	}
	for _, testCase := range cases {
		value := math.Float64frombits(testCase.bits)
		input := strconv.FormatFloat(value, 'g', -1, 64)
		got, err := canonicalNumber(input)
		if err != nil {
			t.Fatalf("bits %016x (%s): %v", testCase.bits, input, err)
		}
		if got != testCase.want {
			t.Errorf("bits %016x: got %s, want %s", testCase.bits, got, testCase.want)
		}
	}
	for _, input := range []string{"NaN", "Infinity", "-Infinity", "1e400"} {
		if _, err := canonicalNumber(input); err == nil {
			t.Errorf("canonicalNumber(%q) unexpectedly accepted a non-finite value", input)
		}
	}
}

// TestREG024RejectsInvalidUTF8AndUnpairedSurrogates prevents lossy Unicode
// replacement from changing bytes before a digest is computed.
func TestREG024RejectsInvalidUTF8AndUnpairedSurrogates(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe})
	if _, err := MarshalCanonical(map[string]any{"value": invalid}); err == nil {
		t.Fatal("invalid UTF-8 JSON string was silently replaced")
	}
	if _, err := MarshalCanonical(map[string]any{invalid: "value"}); err == nil {
		t.Fatal("invalid UTF-8 JSON object key was silently replaced")
	}
	for _, input := range []string{
		"{\"value\":\"\\ud800\"}",
		"{\"value\":\"\\udc00\"}",
		"{\"value\":\"\\ud800\\u0061\"}",
	} {
		if err := ParseCanonicalStringOnly([]byte(input)); err == nil {
			t.Errorf("unpaired surrogate input %q unexpectedly passed canonical verification", input)
		}
	}
	// JCS does not normalize Unicode: precomposed and decomposed forms remain
	// distinct byte sequences rather than being folded into one representation.
	precomposed, err := MarshalCanonical(map[string]any{"value": "é"})
	if err != nil {
		t.Fatal(err)
	}
	decomposed, err := MarshalCanonical(map[string]any{"value": "e\u0301"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(precomposed, decomposed) {
		t.Fatal("canonicalization normalized distinct Unicode code-point sequences")
	}
}

// TestREG024ManifestGoldenBytesAndDetachedDigest pins the complete public
// Manifest bytes and the detached SHA-256 independently of repeated runs.
func TestREG024ManifestGoldenBytesAndDetachedDigest(t *testing.T) {
	graph, sources := resolvedFixture()
	document, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"catalogs":[{"catalog_id":"stable","metadata_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","metadata_uri":"https://catalog.example.invalid/index.json"}],"components":[{"component_id":"engine","contracts":{"provides":[],"requires":[]},"dependencies":[],"release_metadata_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","release_metadata_uri":"https://catalog.example.invalid/releases/engine-1.0.0.json","source_units":[{"repository_uri":"https://github.com/example/engine.git","revision":"0123456789abcdef0123456789abcdef01234567","source_unit_id":"source","workspace_path":"components/engine/default/sources/source"}],"variant":"default","version":"1.0.0"}],"registry_version":"1.2.3","resolution_order":[{"component_id":"engine","variant":"default","version":"1.0.0"}],"roots":[{"component_id":"engine","variant":"default","version":"1.0.0"}],"schema_version":"1.0.0"}`
	if string(encoded) != want {
		t.Fatalf("Manifest golden bytes changed\n got: %s\nwant: %s", encoded, want)
	}
	if got := Digest(encoded); got != "54ce1efe106adbd4f8ac951d47a849477838b0bf006f774abbba2bf849a3386b" {
		t.Fatalf("Manifest detached digest = %s", got)
	}
}

// TestREG024ReportGoldenBytesAndDetachedDigest pins the Resolution Report
// bytes and verifies that the detached digest is calculated over those bytes.
func TestREG024ReportGoldenBytesAndDetachedDigest(t *testing.T) {
	report := resolver.Report{
		SchemaVersion: "1.1.0",
		Result:        "success",
		Selected: []resolver.SelectedCandidate{{
			ComponentID: "engine",
			Variant:     "default",
			Version:     "1.0.0",
		}},
		AppliedConstraints: []resolver.AppliedConstraint{{
			ComponentID:       "engine",
			Variant:           "default",
			VersionConstraint: "=1.0.0",
			Source: resolver.ConstraintSource{
				Type: resolver.ConstraintSourceRoot,
			},
		}},
	}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"applied_constraints":[{"component_id":"engine","source":{"type":"root"},"variant":"default","version_constraint":"=1.0.0"}],"conflicts":[],"rejected_candidates":[],"result":"success","schema_version":"1.1.0","selected":[{"component_id":"engine","variant":"default","version":"1.0.0"}],"warnings":[]}`
	if string(encoded) != want {
		t.Fatalf("Report golden bytes changed\n got: %s\nwant: %s", encoded, want)
	}
	if got := Digest(encoded); got != "4226d72d0c89fb2c61e7cbdad5f8d53f1dfe3ad75cfe43ad8107d7edb6b49c92" {
		t.Fatalf("Report detached digest = %s", got)
	}
}

// TestREG024ManifestAndReportDeterministicAcrossInputPermutations verifies
// repeated builds and equivalent set-like input orderings produce identical
// canonical bytes and detached digests. Topological ResolutionOrder is kept
// fixed because its order is semantic rather than set-like.
func TestREG024ManifestAndReportDeterministicAcrossInputPermutations(t *testing.T) {
	graph, sources := resolvedFixture()
	graph.Catalogs = append(graph.Catalogs, resolver.CatalogReference{
		CatalogID: "archive", MetadataURI: "https://archive.example.invalid/index.json", MetadataSHA: testDigest,
	})
	graph.Roots = append(graph.Roots, resolver.Requirement{
		ComponentID: "addon", Variant: "default", VersionConstraint: "=2.0.0",
	})
	addon := graph.Components[0]
	addon.ComponentID = "addon"
	addon.Version = "2.0.0"
	addon.ReleaseMetadataURI = "https://catalog.example.invalid/releases/addon-2.0.0.json"
	addon.SourceUnits = []catalogModel.SourceUnit{{
		ID: "core", RepositoryURI: "https://github.com/example/addon.git", Revision: testRevision,
	}}
	addon.Dependencies = []resolver.ResolvedDependency{}
	addon.Contracts = catalogModel.Contracts{
		Provides: []catalogModel.ContractProvider{{ContractID: "z.contract", Version: "2.0.0"}, {ContractID: "a.contract", Version: "1.0.0"}},
		Requires: []catalogModel.ContractRequirement{{ContractID: "z.required", VersionConstraint: ">=2.0.0"}, {ContractID: "a.required", VersionConstraint: "^1.0.0"}},
	}
	graph.Components[0].Dependencies = []resolver.ResolvedDependency{
		{ComponentID: "addon", Variant: "default", Version: "2.0.0"},
	}
	graph.Components[0].SourceUnits = append(graph.Components[0].SourceUnits, catalogModel.SourceUnit{
		ID: "extra", RepositoryURI: "https://github.com/example/engine-extra.git", Revision: testRevision,
	})
	graph.Components = append(graph.Components, addon)
	graph.ResolutionOrder = []resolver.Node{
		{ComponentID: "addon", Variant: "default", Version: "2.0.0"},
		{ComponentID: "engine", Variant: "default", Version: "1.0.0"},
	}
	sources = append(sources,
		collector.Source{ComponentID: "engine", Variant: "default", SourceUnitID: "extra", Revision: testRevision, WorkspacePath: "components/engine/default/sources/extra"},
		collector.Source{ComponentID: "addon", Variant: "default", SourceUnitID: "core", Revision: testRevision, WorkspacePath: "components/addon/default/sources/core"},
	)

	firstDocument, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	firstManifest, err := MarshalCanonical(firstDocument)
	if err != nil {
		t.Fatal(err)
	}
	firstManifestDigest := Digest(firstManifest)

	permuted := *graph
	permuted.Catalogs = reverseCatalogReferences(graph.Catalogs)
	permuted.Roots = append([]resolver.Requirement(nil), graph.Roots...)
	reverseRequirements(permuted.Roots)
	permuted.Components = append([]resolver.ResolvedComponent(nil), graph.Components...)
	reverseResolvedComponents(permuted.Components)
	permutedSources := append([]collector.Source(nil), sources...)
	reverseCollectorSources(permutedSources)

	secondDocument, err := Build(&permuted, permutedSources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := MarshalCanonical(secondDocument)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstManifest, secondManifest) || firstManifestDigest != Digest(secondManifest) {
		t.Fatalf("Manifest bytes/digest changed after equivalent input permutation\nfirst: %s\nsecond: %s", firstManifest, secondManifest)
	}
	for run := 0; run < 3; run++ {
		repeated, err := MarshalCanonical(firstDocument)
		if err != nil || !bytes.Equal(firstManifest, repeated) || Digest(repeated) != firstManifestDigest {
			t.Fatalf("Manifest changed on repeated run %d: err=%v", run, err)
		}
	}

	report := resolver.Report{
		SchemaVersion: "1.1.0",
		Result:        "success",
		Selected: []resolver.SelectedCandidate{
			{ComponentID: "engine", Variant: "default", Version: "1.0.0"},
			{ComponentID: "addon", Variant: "default", Version: "2.0.0"},
		},
		AppliedConstraints: []resolver.AppliedConstraint{
			{ComponentID: "engine", Variant: "default", VersionConstraint: "=1.0.0", Source: resolver.ConstraintSource{Type: resolver.ConstraintSourceRoot}},
			{ComponentID: "addon", Variant: "default", VersionConstraint: "=2.0.0", Source: resolver.ConstraintSource{Type: resolver.ConstraintSourceDependency, ComponentID: "engine", Variant: "default", Version: "1.0.0"}},
		},
		Warnings: []resolver.Warning{{Code: "Z_LAST", Message: "last"}, {Code: "A_FIRST", Message: "first"}},
	}
	firstReport, err := MarshalReport(report)
	if err != nil {
		t.Fatal(err)
	}
	permutedReport := report
	permutedReport.Selected = append([]resolver.SelectedCandidate(nil), report.Selected...)
	permutedReport.AppliedConstraints = append([]resolver.AppliedConstraint(nil), report.AppliedConstraints...)
	permutedReport.Warnings = append([]resolver.Warning(nil), report.Warnings...)
	for left, right := 0, len(permutedReport.Selected)-1; left < right; left, right = left+1, right-1 {
		permutedReport.Selected[left], permutedReport.Selected[right] = permutedReport.Selected[right], permutedReport.Selected[left]
	}
	for left, right := 0, len(permutedReport.AppliedConstraints)-1; left < right; left, right = left+1, right-1 {
		permutedReport.AppliedConstraints[left], permutedReport.AppliedConstraints[right] = permutedReport.AppliedConstraints[right], permutedReport.AppliedConstraints[left]
	}
	for left, right := 0, len(permutedReport.Warnings)-1; left < right; left, right = left+1, right-1 {
		permutedReport.Warnings[left], permutedReport.Warnings[right] = permutedReport.Warnings[right], permutedReport.Warnings[left]
	}
	secondReport, err := MarshalReport(permutedReport)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstReport, secondReport) || Digest(firstReport) != Digest(secondReport) {
		t.Fatalf("Report bytes/digest changed after equivalent input permutation\nfirst: %s\nsecond: %s", firstReport, secondReport)
	}
}

func reverseCatalogReferences(values []resolver.CatalogReference) []resolver.CatalogReference {
	out := append([]resolver.CatalogReference(nil), values...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func reverseRequirements(values []resolver.Requirement) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseResolvedComponents(values []resolver.ResolvedComponent) {
	for i := range values {
		values[i].SourceUnits = append([]catalogModel.SourceUnit(nil), values[i].SourceUnits...)
		values[i].Dependencies = append([]resolver.ResolvedDependency(nil), values[i].Dependencies...)
		values[i].Contracts.Provides = append([]catalogModel.ContractProvider(nil), values[i].Contracts.Provides...)
		values[i].Contracts.Requires = append([]catalogModel.ContractRequirement(nil), values[i].Contracts.Requires...)
		reverseSourceUnits(values[i].SourceUnits)
		reverseDependencies(values[i].Dependencies)
		reverseContractProviders(values[i].Contracts.Provides)
		reverseContractRequirements(values[i].Contracts.Requires)
	}
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseSourceUnits(values []catalogModel.SourceUnit) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseDependencies(values []resolver.ResolvedDependency) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseContractProviders(values []catalogModel.ContractProvider) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseContractRequirements(values []catalogModel.ContractRequirement) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseCollectorSources(values []collector.Source) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

// TestREG024VerifyManifestAcceptsCompleteCanonicalDocument verifies the public
// Manifest schema, canonical bytes, detached digest and provenance together.
func TestREG024VerifyManifestAcceptsCompleteCanonicalDocument(t *testing.T) {
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
	if err := Verify(encoded, Digest(encoded), validator); err != nil {
		t.Fatalf("complete canonical Manifest failed verification: %v", err)
	}
	// A digest is deliberately optional; without an external receipt the
	// verifier still checks canonical form, schema and semantic provenance.
	if err := Verify(encoded, "", validator); err != nil {
		t.Fatalf("Manifest without an external digest failed verification: %v", err)
	}
}

// TestREG024VerifyManifestRejectsBadBytesDigestAndSchema proves that
// canonicalization alone is not treated as full Manifest verification.
func TestREG024VerifyManifestRejectsBadBytesDigestAndSchema(t *testing.T) {
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
	for _, testCase := range []struct {
		name   string
		input  []byte
		digest string
	}{
		{name: "empty input"},
		{name: "non-canonical bytes", input: []byte("{ \"schema_version\": \"1.0.0\" }")},
		{name: "wrong detached digest", input: encoded, digest: strings.Repeat("0", 64)},
		{name: "malformed detached digest", input: encoded, digest: "sha256:" + Digest(encoded)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := Verify(testCase.input, testCase.digest, validator); err == nil {
				t.Fatal("invalid Manifest input unexpectedly passed verification")
			}
		})
	}

	unknownVersion := document
	unknownVersion.SchemaVersion = "2.0.0"
	unknownBytes, err := MarshalCanonical(unknownVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(unknownBytes, "", validator); err == nil {
		t.Fatal("unsupported Manifest schema version unexpectedly passed verification")
	}

	missingRequired := document
	missingRequired.Components = nil
	missingBytes, err := MarshalCanonical(missingRequired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(missingBytes, "", validator); err == nil {
		t.Fatal("Manifest without required components unexpectedly passed verification")
	}
}

// TestREG024VerifyManifestRejectsUnsafeOrMissingProvenance exercises semantic
// checks beyond the JSON Schema's structural validation.
func TestREG024VerifyManifestRejectsUnsafeOrMissingProvenance(t *testing.T) {
	graph, sources := resolvedFixture()
	document, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*Document)
	}{
		{
			name: "missing catalog digest",
			mutate: func(d *Document) {
				d.Catalogs[0].MetadataSHA = ""
			},
		},
		{
			name: "credential-bearing catalog URI",
			mutate: func(d *Document) {
				d.Catalogs[0].MetadataURI = "https://user:secret@catalog.example.invalid/index.json"
			},
		},
		{
			name: "unsupported metadata URI scheme",
			mutate: func(d *Document) {
				d.Components[0].ReleaseMetadataURI = "ftp://catalog.example.invalid/release.json"
			},
		},
		{
			name: "missing release digest",
			mutate: func(d *Document) {
				d.Components[0].ReleaseMetadataSHA = ""
			},
		},
		{
			name: "non-full Git revision",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].Revision = "abcdef123456"
			},
		},
		{
			name: "uppercase Git revision",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].Revision = strings.ToUpper(testRevision)
			},
		},
		{
			name: "credential-bearing repository URI",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].RepositoryURI = "https://user:secret@github.com/example/engine.git"
			},
		},
		{
			name: "path traversal",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].WorkspacePath = "components/engine/default/sources/../../escape"
			},
		},
		{
			name: "non-canonical source path",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].WorkspacePath = "sources/engine"
			},
		},
		{
			name: "unsafe subdirectory",
			mutate: func(d *Document) {
				d.Components[0].SourceUnits[0].Subdirectory = "../outside"
			},
		},
		{
			name: "root version differs from selected release",
			mutate: func(d *Document) {
				d.Roots[0].Version = "2.0.0"
			},
		},
		{
			name: "dependency refers to unselected version",
			mutate: func(d *Document) {
				d.Components[0].Dependencies = []Dependency{{ComponentID: "engine", Variant: "default", Version: "2.0.0"}}
			},
		},
		{
			name: "incomplete resolution order",
			mutate: func(d *Document) {
				d.ResolutionOrder = nil
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mutated := cloneManifest(t, document)
			testCase.mutate(&mutated)
			encoded, err := MarshalCanonical(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if err := Verify(encoded, "", validator); err == nil {
				t.Fatal("invalid Manifest unexpectedly passed verification")
			}
		})
	}
}

// TestREG024VerifyManifestReportsSafeFailureStages keeps canonical-byte,
// public-schema and semantic provenance failures distinguishable to consumers
// without echoing untrusted Manifest content in the public error message.
func TestREG024VerifyManifestReportsSafeFailureStages(t *testing.T) {
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	assertStage := func(name string, input []byte, want VerificationStage, secret string) {
		t.Helper()
		err := Verify(input, "", validator)
		var verificationError *VerificationError
		if !errors.As(err, &verificationError) || verificationError.Stage != want {
			t.Fatalf("%s stage = %#v, want %q (error: %v)", name, verificationError, want, err)
		}
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("%s error leaked untrusted content: %v", name, err)
		}
	}
	assertStage("canonical", []byte("{ \"schema_version\": \"1.0.0\" }"), VerificationStageCanonicalBytes, "")

	graph, sources := resolvedFixture()
	document, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	missingRequired := cloneManifest(t, document)
	missingRequired.Components = nil
	missingBytes, err := MarshalCanonical(missingRequired)
	if err != nil {
		t.Fatal(err)
	}
	assertStage("schema", missingBytes, VerificationStagePublicSchema, "")

	invalidProvenance := cloneManifest(t, document)
	invalidProvenance.Components[0].SourceUnits[0].RepositoryURI =
		"https://secret-user:secret-token@example.invalid/private.git"
	provenanceBytes, err := MarshalCanonical(invalidProvenance)
	if err != nil {
		t.Fatal(err)
	}
	assertStage("provenance", provenanceBytes, VerificationStageProvenance, "secret-token")
}

func cloneManifest(t *testing.T, document Document) Document {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone Document
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

// TestREG024VerifyManifestChecksDependencyOrderAndSetOrdering ensures a
// canonical JSON document cannot smuggle semantically unstable array order or
// a dependency graph inconsistent with the recorded topological order.
func TestREG024VerifyManifestChecksDependencyOrderAndSetOrdering(t *testing.T) {
	graph, sources := resolvedFixture()
	document, err := Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	engine := document.Components[0]
	addon := engine
	addon.ComponentID = "addon"
	addon.Version = "2.0.0"
	addon.ReleaseMetadataURI = "https://catalog.example.invalid/releases/addon-2.0.0.json"
	addon.SourceUnits = []SourceUnit{{
		SourceUnitID:  "source",
		RepositoryURI: "https://github.com/example/addon.git",
		Revision:      testRevision,
		WorkspacePath: "components/addon/default/sources/source",
	}}
	addon.Dependencies = []Dependency{{ComponentID: "engine", Variant: "default", Version: "1.0.0"}}
	engine.Dependencies = []Dependency{}
	document.Components = []Component{addon, engine}
	document.ResolutionOrder = []resolver.Node{
		{ComponentID: "engine", Variant: "default", Version: "1.0.0"},
		{ComponentID: "addon", Variant: "default", Version: "2.0.0"},
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(encoded, "", validator); err != nil {
		t.Fatalf("valid dependency-first Manifest order failed: %v", err)
	}
	document.ResolutionOrder[0], document.ResolutionOrder[1] = document.ResolutionOrder[1], document.ResolutionOrder[0]
	encoded, err = MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(encoded, "", validator); err == nil {
		t.Fatal("dependent-before-dependency order unexpectedly passed verification")
	}
}

// TestREG024CanonicalRejectsCyclicValues ensures the UTF-8 preflight does not
// recurse indefinitely on values that json.Marshal must reject as cyclic.
func TestREG024CanonicalRejectsCyclicValues(t *testing.T) {
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	if _, err := MarshalCanonical(cyclicMap); err == nil {
		t.Fatal("cyclic map unexpectedly passed canonical serialization")
	}

	cyclicSlice := make([]any, 1)
	cyclicSlice[0] = cyclicSlice
	if _, err := MarshalCanonical(cyclicSlice); err == nil {
		t.Fatal("cyclic slice unexpectedly passed canonical serialization")
	}

	type cyclicNode struct {
		Next *cyclicNode `json:"next"`
	}
	node := &cyclicNode{}
	node.Next = node
	if _, err := MarshalCanonical(node); err == nil {
		t.Fatal("cyclic pointer unexpectedly passed canonical serialization")
	}
}
