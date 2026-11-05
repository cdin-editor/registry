package validation

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/schemas"
)

func newTestValidator(t *testing.T) *Validator {
	t.Helper()
	validator, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return validator
}

func TestEmbeddedSchemasMatchCanonicalCopies(t *testing.T) {
	names := []string{
		"product-definition.schema.json",
		"catalog-index.schema.json",
		"component-release.schema.json",
		"resolved-manifest.schema.json",
		"resolution-report.schema.json",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			canonical, err := os.ReadFile("../../schemas/registry/v1/" + name)
			if err != nil {
				t.Fatal(err)
			}
			embedded, err := schemas.FS.ReadFile("registry/v1/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, embedded) {
				t.Fatalf("embedded schema %s differs from canonical Spec mirror", name)
			}
		})
	}
}

func TestValidateExampleDocuments(t *testing.T) {
	validator := newTestValidator(t)
	cases := []struct {
		kind Kind
		path string
	}{
		{ProductDefinition, "../../examples/product-definition.yaml"},
		{CatalogIndex, "../../examples/catalog-index.json"},
		{ComponentRelease, "../../examples/component-release.json"},
		{ResolvedManifest, "../../examples/resolved-manifest.json"},
		{ResolutionReport, "../../examples/resolution-report.json"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(tc.kind, data, tc.path); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestRejectsUnsupportedVersionConstraint(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("{\"schema_version\":\"1.0.0\",\"components\":[{\"component_id\":\"backend\",\"version_constraint\":\"^1.2.0\"}]}")
	if err := validator.Validate(ProductDefinition, input, "product.json"); err == nil {
		t.Fatal("Validate() accepted an unsupported caret constraint")
	}
}

func TestRejectsMalformedJSON(t *testing.T) {
	validator := newTestValidator(t)
	if err := validator.Validate(ProductDefinition, []byte("{"), "product.json"); err == nil {
		t.Fatal("Validate() accepted malformed JSON")
	}
}

func TestRejectsDuplicateJSONKeys(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("{\"schema_version\":\"1.0.0\",\"components\":[],\"policy\":{\"allow_prerelease\":false,\"allow_prerelease\":true}}")
	if err := validator.Validate(ProductDefinition, input, "product.json"); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("Validate() error = %v, want duplicate JSON key error", err)
	}
}

func TestRejectsTrailingJSONValues(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("{\"schema_version\":\"1.0.0\",\"components\":[]} {}")
	if err := validator.Validate(ProductDefinition, input, "product.json"); err == nil || !strings.Contains(err.Error(), "unexpected trailing JSON value") {
		t.Fatalf("Validate() error = %v, want trailing JSON error", err)
	}
}

func TestRejectsDuplicateYAMLKeys(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("schema_version: \"1.0.0\"\ncomponents: []\ncomponents: []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "duplicate YAML mapping key") {
		t.Fatalf("Validate() error = %v, want duplicate key error", err)
	}
}

func TestRejectsCustomYAMLScalarAndCollectionTags(t *testing.T) {
	validator := newTestValidator(t)
	cases := []string{
		"schema_version: \"1.0.0\"\ncomponents: !custom []\n",
		"schema_version: !custom \"1.0.0\"\ncomponents: []\n",
	}
	for i, input := range cases {
		if err := validator.Validate(ProductDefinition, []byte(input), "product.yaml"); err == nil || !strings.Contains(err.Error(), "unsupported YAML tag") {
			t.Errorf("case %d: Validate() error = %v, want unsupported tag error", i, err)
		}
	}
}

func TestRejectsNonStringYAMLMappingKeys(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("schema_version: \"1.0.0\"\ncomponents:\n  - component_id: backend\n    version_constraint: \">=1.0.0 <2.0.0\"\n    7: invalid\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "mapping keys must be strings") {
		t.Fatalf("Validate() error = %v, want non-string key error", err)
	}
}

func TestRejectsYAMLMergeKeys(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("base: &base {value: ignored}\nschema_version: \"1.0.0\"\ncomponents: []\nextra: {<<: *base}\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "merge keys are not supported") {
		t.Fatalf("Validate() error = %v, want merge key error", err)
	}
}

func TestRejectsRecursiveYAMLAliases(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("schema_version: \"1.0.0\"\ncomponents: &components [*components]\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "recursive YAML alias") {
		t.Fatalf("Validate() error = %v, want recursive alias error", err)
	}
}

func TestRejectsMultipleYAMLDocuments(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("schema_version: \"1.0.0\"\ncomponents: []\n---\nschema_version: \"1.0.0\"\ncomponents: []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yml"); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("Validate() error = %v, want multiple document error", err)
	}
}

func TestYAMLIsNotAcceptedForOtherKinds(t *testing.T) {
	validator := newTestValidator(t)
	if err := validator.Validate(CatalogIndex, []byte("schema_version: \"1.0.0\""), "catalog.yaml"); err == nil {
		t.Fatal("Validate() accepted YAML for a Catalog Index")
	}
}

func TestKindsAreStableAndSorted(t *testing.T) {
	kinds := Kinds()
	if len(kinds) != 5 {
		t.Fatalf("Kinds() length = %d, want 5", len(kinds))
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] > kinds[i] {
			t.Fatalf("Kinds() is not sorted: %v", kinds)
		}
	}
}


// TestREG005NormalizeProductDefinitionYAMLReturnsValidatedJSON ensures the CLI
// and Resolver can consume one strict normalized data model without a second
// YAML parser or a weaker validation path.
func TestREG005NormalizeProductDefinitionYAMLReturnsValidatedJSON(t *testing.T) {
	validator := newTestValidator(t)
	input := []byte("schema_version: \"1.0.0\"\ncomponents: []\n")
	normalized, err := validator.Normalize(ProductDefinition, input, "product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(normalized) || !bytes.Contains(normalized, []byte("\"schema_version\"")) {
		t.Fatalf("Normalize() did not return JSON: %s", normalized)
	}
	if _, err := validator.Normalize(ProductDefinition, []byte("{\"schema_version\":\"1.0.0\",\"components\":[],\"components\":[]}"), "product.json"); err == nil {
		t.Fatal("Normalize() accepted duplicate JSON keys")
	}
}
