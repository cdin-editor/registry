package resolver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

func benchmarkRelease(component, version string) catalog.Release {
	return catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   component,
		Variant:       "default",
		Version:       version,
		Status:        "active",
		SourceUnits: []catalog.SourceUnit{{
			ID:            "source",
			RepositoryURI: "https://example.invalid/repository.git",
			Revision:      strings.Repeat("a", 40),
		}},
		Dependencies: []catalog.Dependency{},
		Contracts: catalog.Contracts{
			Provides: []catalog.ContractProvider{},
			Requires: []catalog.ContractRequirement{},
		},
	}
}

func BenchmarkResolveLargeCatalog(b *testing.B) {
	const componentCount = 128
	const versionsPerComponent = 8
	catalogValue := &catalog.Catalog{ID: "benchmark"}
	for component := 0; component < componentCount; component++ {
		id := fmt.Sprintf("engine-%03d", component)
		for version := 0; version < versionsPerComponent; version++ {
			release := benchmarkRelease(id, fmt.Sprintf("1.%d.0", version))
			catalogValue.Releases = append(catalogValue.Releases, release)
		}
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "engine-000",
		Variant:           "default",
		VersionConstraint: ">=1.0.0 <2.0.0",
	}}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := Resolve(definition, []*catalog.Catalog{catalogValue})
		if result.Graph == nil {
			b.Fatalf("large catalog did not resolve: %+v", result.Report.Conflicts)
		}
	}
}

func BenchmarkResolveWideGraph(b *testing.B) {
	const width = 128
	catalogValue := &catalog.Catalog{ID: "benchmark"}
	definition := ProductDefinition{Components: make([]Requirement, 0, width)}
	for i := 0; i < width; i++ {
		id := fmt.Sprintf("component-%03d", i)
		catalogValue.Releases = append(catalogValue.Releases, benchmarkRelease(id, "1.0.0"))
		definition.Components = append(definition.Components, Requirement{
			ComponentID:       id,
			Variant:           "default",
			VersionConstraint: "=1.0.0",
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := Resolve(definition, []*catalog.Catalog{catalogValue})
		if result.Graph == nil || len(result.Graph.Components) != width {
			b.Fatalf("wide graph resolved to %d components", func() int {
				if result.Graph == nil {
					return 0
				}
				return len(result.Graph.Components)
			}())
		}
	}
}

func BenchmarkResolveDeepDependencyGraph(b *testing.B) {
	const depth = 128
	catalogValue := &catalog.Catalog{ID: "benchmark"}
	for i := 0; i < depth; i++ {
		id := fmt.Sprintf("node-%03d", i)
		release := benchmarkRelease(id, "1.0.0")
		if i+1 < depth {
			release.Dependencies = []catalog.Dependency{{
				ComponentID:       fmt.Sprintf("node-%03d", i+1),
				Variant:           "default",
				VersionConstraint: ">=1.0.0 <2.0.0",
			}}
		}
		catalogValue.Releases = append(catalogValue.Releases, release)
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "node-000",
		Variant:           "default",
		VersionConstraint: "=1.0.0",
	}}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := Resolve(definition, []*catalog.Catalog{catalogValue})
		if result.Graph == nil || len(result.Graph.Components) != depth {
			b.Fatalf("deep graph did not resolve: %v", result.Report.Conflicts)
		}
	}
}

func BenchmarkResolveBacktracking(b *testing.B) {
	const candidates = 32
	catalogValue := &catalog.Catalog{ID: "benchmark"}
	for i := candidates - 1; i >= 0; i-- {
		release := benchmarkRelease("application", fmt.Sprintf("1.%d.0", i))
		if i > 0 {
			release.Dependencies = []catalog.Dependency{{
				ComponentID:       fmt.Sprintf("missing-%03d", i),
				Variant:           "default",
				VersionConstraint: ">=1.0.0 <2.0.0",
			}}
		}
		catalogValue.Releases = append(catalogValue.Releases, release)
	}
	definition := ProductDefinition{Components: []Requirement{{
		ComponentID:       "application",
		Variant:           "default",
		VersionConstraint: ">=1.0.0 <2.0.0",
	}}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := Resolve(definition, []*catalog.Catalog{catalogValue})
		if result.Graph == nil {
			b.Fatalf("fallback graph did not resolve: %v", result.Report.Conflicts)
		}
	}
}
