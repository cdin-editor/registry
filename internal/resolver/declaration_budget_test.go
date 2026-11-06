package resolver

import (
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

func TestResolveRejectsDeclarationCountAboveBudgetBeforeGraphConstruction(t *testing.T) {
	release := catalog.Release{
		ComponentID:  "root",
		Variant:      "default",
		Version:      "1.0.0",
		Dependencies: make([]catalog.Dependency, maxResolverDeclarations+1),
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "root",
		Variant:           "default",
		VersionConstraint: "=1.0.0",
	}}}
	result := Resolve(definition, []*catalog.Catalog{{ID: "oversized", Releases: []catalog.Release{release}}})
	if result.Graph != nil || len(result.Report.Conflicts) != 1 || result.Report.Conflicts[0].Code != ConflictResolutionLimit {
		t.Fatalf("Resolve() graph=%v conflicts=%+v; want fail-closed declaration budget", result.Graph, result.Report.Conflicts)
	}
	if !strings.Contains(result.Report.Conflicts[0].Message, "declarations exceed") {
		t.Fatalf("resolution-limit message = %q; want declaration-budget reason", result.Report.Conflicts[0].Message)
	}
}
