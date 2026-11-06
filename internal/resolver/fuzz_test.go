package resolver

import (
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

func FuzzResolveCatalogGraph(f *testing.F) {
	f.Add("1.0.0", ">=1.0.0 <2.0.0", "dependency")
	f.Add("2.0.0-alpha.1", "=2.0.0-alpha.1", "")
	f.Add("invalid", "^1.2.3", "bad/id")
	f.Add("1.0.0", ">=2.0.0 <1.0.0", "root")
	f.Fuzz(func(t *testing.T, rootVersion, constraint, dependencyID string) {
		if len(rootVersion) > 256 || len(constraint) > 512 || len(dependencyID) > 128 {
			t.Skip()
		}
		root := catalog.Release{
			SchemaVersion: "1.0.0",
			ComponentID:   "root",
			Variant:       "default",
			Version:       rootVersion,
			Status:        "active",
			SourceUnits: []catalog.SourceUnit{{
				ID: "source", RepositoryURI: "https://example.invalid/root.git",
				Revision: strings.Repeat("a", 40),
			}},
			Dependencies: []catalog.Dependency{},
			Contracts: catalog.Contracts{
				Provides: []catalog.ContractProvider{},
				Requires: []catalog.ContractRequirement{},
			},
		}
		releases := []catalog.Release{root}
		if dependencyID != "" {
			root.Dependencies = append(root.Dependencies, catalog.Dependency{
				ComponentID:       dependencyID,
				Variant:           "default",
				VersionConstraint: ">=1.0.0 <2.0.0",
			})
			releases[0] = root
			if dependencyID != "root" {
				releases = append(releases, catalog.Release{
					SchemaVersion: "1.0.0",
					ComponentID:   dependencyID,
					Variant:       "default",
					Version:       "1.0.0",
					Status:        "active",
					SourceUnits: []catalog.SourceUnit{{
						ID: "source", RepositoryURI: "https://example.invalid/dependency.git",
						Revision: strings.Repeat("b", 40),
					}},
					Dependencies: []catalog.Dependency{},
					Contracts: catalog.Contracts{
						Provides: []catalog.ContractProvider{},
						Requires: []catalog.ContractRequirement{},
					},
				})
			}
		}
		definition := ProductDefinition{
			SchemaVersion: "1.0.0",
			Components: []Requirement{{
				ComponentID: "root", Variant: "default", VersionConstraint: constraint,
			}},
		}
		_ = Resolve(definition, []*catalog.Catalog{{ID: "fuzz", Releases: releases}})
	})
}
