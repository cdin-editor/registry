package resolver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

func TestResolveRejectsRootRequirementCountAboveBudget(t *testing.T) {
	components := make([]Requirement, maxResolvedComponents+1)
	for i := range components {
		components[i] = Requirement{
			ComponentID:       fmt.Sprintf("component-%04d", i),
			Variant:           "default",
			VersionConstraint: "=1.0.0",
		}
	}
	result := Resolve(ProductDefinition{Components: components}, nil)
	if result.Graph != nil || len(result.Report.Conflicts) != 1 || result.Report.Conflicts[0].Code != ConflictResolutionLimit {
		t.Fatalf("Resolve() graph=%v conflicts=%+v; want fail-closed component budget", result.Graph, result.Report.Conflicts)
	}
}

func TestResolveRejectsCatalogCandidateCountAboveBudget(t *testing.T) {
	catalogValue := &catalog.Catalog{
		ID:       "oversized",
		Releases: make([]catalog.Release, maxResolverCandidates+1),
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "root",
		Variant:           "default",
		VersionConstraint: "=1.0.0",
	}}}
	result := Resolve(definition, []*catalog.Catalog{catalogValue})
	if result.Graph != nil || len(result.Report.Conflicts) != 1 || result.Report.Conflicts[0].Code != ConflictResolutionLimit {
		t.Fatalf("Resolve() graph=%v conflicts=%+v; want fail-closed candidate budget", result.Graph, result.Report.Conflicts)
	}
}

func TestResolveRejectsDependencyGraphAboveComponentBudget(t *testing.T) {
	const depth = maxResolvedComponents + 1
	catalogValue := &catalog.Catalog{ID: "deep"}
	for i := 0; i < depth; i++ {
		component := fmt.Sprintf("node-%04d", i)
		release := catalog.Release{
			SchemaVersion: "1.0.0",
			ComponentID:   component,
			Variant:       "default",
			Version:       "1.0.0",
			Status:        "active",
			SourceUnits: []catalog.SourceUnit{{
				ID:            "source",
				RepositoryURI: "https://example.invalid/source.git",
				Revision:      strings.Repeat("a", 40),
			}},
			Dependencies: []catalog.Dependency{},
			Contracts: catalog.Contracts{
				Provides: []catalog.ContractProvider{},
				Requires: []catalog.ContractRequirement{},
			},
		}
		if i+1 < depth {
			release.Dependencies = []catalog.Dependency{{
				ComponentID:       fmt.Sprintf("node-%04d", i+1),
				Variant:           "default",
				VersionConstraint: ">=1.0.0 <2.0.0",
			}}
		}
		catalogValue.Releases = append(catalogValue.Releases, release)
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "node-0000",
		Variant:           "default",
		VersionConstraint: "=1.0.0",
	}}}
	result := Resolve(definition, []*catalog.Catalog{catalogValue})
	if result.Graph != nil || len(result.Report.Conflicts) != 1 || result.Report.Conflicts[0].Code != ConflictResolutionLimit {
		t.Fatalf("Resolve() graph=%v conflicts=%+v; want fail-closed deep-graph budget", result.Graph, result.Report.Conflicts)
	}
}
