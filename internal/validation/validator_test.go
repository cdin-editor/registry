package validation

import (
	"os"
	"strings"
	"testing"
)

func TestValidateExampleDocuments(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
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

func TestRejectsInvalidProductVersion(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("{\"schema_version\":\"1.0.0\",\"components\":[{\"component_id\":\"backend\",\"version_constraint\":\"^1.2.0\"}]}")
	if err := validator.Validate(ProductDefinition, input, "product.json"); err == nil {
		t.Fatal("Validate() accepted an unsupported caret constraint")
	}
}

func TestRejectsDuplicateYAMLKeys(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("schema_version: \"1.0.0\"\ncomponents: []\ncomponents: []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "duplicate YAML mapping key") {
		t.Fatalf("Validate() error = %v, want duplicate key error", err)
	}
}

func TestRejectsCustomYAMLTags(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("schema_version: \"1.0.0\"\ncomponents: !custom []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "unsupported YAML tag") {
		t.Fatalf("Validate() error = %v, want unsupported tag error", err)
	}
}

func TestRejectsMultipleYAMLDocuments(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("schema_version: \"1.0.0\"\ncomponents: []\n---\nschema_version: \"1.0.0\"\ncomponents: []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yml"); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("Validate() error = %v, want multiple document error", err)
	}
}

func TestRejectsCustomYAMLCollectionTags(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("schema_version: \"1.0.0\"\ncomponents: !custom []\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "unsupported YAML tag") {
		t.Fatalf("Validate() error = %v, want unsupported collection tag error", err)
	}
}

func TestRejectsNonStringYAMLMappingKeys(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("schema_version: \"1.0.0\"\ncomponents:\n  - component_id: backend\n    version_constraint: \">=1.0.0 <2.0.0\"\n    7: invalid\n")
	if err := validator.Validate(ProductDefinition, input, "product.yaml"); err == nil || !strings.Contains(err.Error(), "mapping keys must be strings") {
		t.Fatalf("Validate() error = %v, want non-string key error", err)
	}
}

func TestYAMLIsNotAcceptedForOtherKinds(t *testing.T) {
	validator, err := New()
	if err != nil {
		t.Fatal(err)
	}
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
