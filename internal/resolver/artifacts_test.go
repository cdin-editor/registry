package resolver

import (
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

func nativeTarget() *catalog.Target {
	return &catalog.Target{
		OS:           "linux",
		Architecture: "x86_64",
		ABI:          "gnu-glibc-2.28",
	}
}

func testArtifact(id string, target catalog.Target) catalog.Artifact {
	return catalog.Artifact{
		ID:     id,
		Target: target,
		Kind:   "native-development-package",
		Layout: "cmake-prefix-v1",
	}
}

func TestApplyDeliveryPolicyExactTargetWinsUniversal(t *testing.T) {
	graph := &Graph{
		Components: []ResolvedComponent{
			{
				ComponentID: "backend",
				Variant:     "default",
				Artifacts: []catalog.Artifact{
					testArtifact("universal", catalog.Target{OS: "any", Architecture: "any", ABI: "none"}),
					testArtifact("linux", *nativeTarget()),
				},
			},
		},
	}
	if conflict := applyDeliveryPolicy(graph, ResolutionPolicy{DeliveryMode: "artifact", Target: nativeTarget()}); conflict != nil {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	got := graph.Components[0]
	if got.Delivery != "artifact" || got.SelectedArtifact == nil || got.SelectedArtifact.ID != "linux" {
		t.Fatalf("wrong selected artifact: %#v", got)
	}
	if graph.SchemaVersion != artifactManifestVersion {
		t.Fatalf("schema version=%q", graph.SchemaVersion)
	}
}

func TestApplyDeliveryPolicyPreservesReleaseProvenanceManifestSchema(t *testing.T) {
	graph := &Graph{
		SchemaVersion: "1.2.0",
		Components: []ResolvedComponent{
			{
				ComponentID: "backend",
				Variant:     "default",
				ReleaseDiscovery: &catalog.ReleaseDiscoveryProvenance{
					SourceID:          "backend-default",
					Provider:          "github-releases-v1",
					RepositoryURI:     "https://github.com/example/backend",
					ReleaseID:         42,
					ReleaseAPIURI:     "https://api.github.com/repos/example/backend/releases/42",
					Tag:               "v1.2.2",
					TagCommit:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					MetadataAssetName: "backend-1.2.2.json",
					MetadataAssetSize: 1234,
				},
				Artifacts: []catalog.Artifact{
					testArtifact("backend-linux", *nativeTarget()),
				},
			},
		},
	}
	if conflict := applyDeliveryPolicy(graph, ResolutionPolicy{
		DeliveryMode: "artifact",
		Target:       nativeTarget(),
	}); conflict != nil {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	if graph.SchemaVersion != "1.2.0" {
		t.Fatalf("artifact delivery downgraded provenance Manifest schema: got %q, want 1.2.0", graph.SchemaVersion)
	}
	if graph.Components[0].Delivery != "artifact" || graph.Components[0].SelectedArtifact == nil {
		t.Fatalf("artifact selection was not applied: %#v", graph.Components[0])
	}
}

func TestResolveArtifactDeliveryPreservesADR0028ManifestSchema(t *testing.T) {
	release := releaseWithGitHubDiscovery("backend", "1.2.2", 42, "2026-11-14T00:00:00Z")
	release.Artifacts = []catalog.Artifact{
		testArtifact("backend-linux", *nativeTarget()),
	}
	definition := product(Requirement{
		ComponentID: "backend",
		Variant: "default",
		VersionConstraint: "=1.2.2",
	})
	definition.Resolution = ResolutionPolicy{
		DeliveryMode: "artifact",
		Target: nativeTarget(),
	}

	result := Resolve(definition, []*catalog.Catalog{
		catalogOf("release-source", release),
	})
	if result.Graph == nil || result.Report.Result != "success" {
		t.Fatalf("artifact resolution failed: %+v", result.Report)
	}
	if result.Graph.SchemaVersion != "1.2.0" {
		t.Fatalf("Resolver graph schema=%q, want 1.2.0", result.Graph.SchemaVersion)
	}
	if len(result.Graph.Components) != 1 ||
		result.Graph.Components[0].ReleaseDiscovery == nil ||
		result.Graph.Components[0].Delivery != "artifact" {
		t.Fatalf("selected artifact lost ADR-0028 provenance or delivery mode: %#v", result.Graph.Components)
	}
}

func TestApplyDeliveryPolicyUniversalFallback(t *testing.T) {
	graph := &Graph{
		Components: []ResolvedComponent{
			{
				ComponentID: "runtime",
				Variant:     "default",
				Artifacts: []catalog.Artifact{
					{
						ID:     "lua",
						Target: catalog.Target{OS: "any", Architecture: "any", ABI: "none"},
						Kind:   "runtime-module-archive",
						Layout: "lua-module-tree-v1",
					},
				},
			},
		},
	}
	if conflict := applyDeliveryPolicy(graph, ResolutionPolicy{DeliveryMode: "artifact", Target: nativeTarget()}); conflict != nil {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	if graph.Components[0].SelectedArtifact == nil || graph.Components[0].SelectedArtifact.ID != "lua" {
		t.Fatalf("universal artifact was not selected: %#v", graph.Components[0])
	}
}

func TestApplyDeliveryPolicyStrictMissingFails(t *testing.T) {
	graph := &Graph{
		Components: []ResolvedComponent{
			{ComponentID: "frontend", Variant: "gui-sdl"},
		},
	}
	conflict := applyDeliveryPolicy(graph, ResolutionPolicy{DeliveryMode: "artifact", Target: nativeTarget()})
	if conflict == nil || conflict.Code != ConflictArtifactNotFound {
		t.Fatalf("expected ARTIFACT_NOT_FOUND, got %#v", conflict)
	}
}

func TestApplyDeliveryPolicyAmbiguousTargetFails(t *testing.T) {
	a := testArtifact("one", *nativeTarget())
	b := testArtifact("two", *nativeTarget())
	graph := &Graph{
		Components: []ResolvedComponent{
			{ComponentID: "backend", Variant: "default", Artifacts: []catalog.Artifact{a, b}},
		},
	}
	conflict := applyDeliveryPolicy(graph, ResolutionPolicy{DeliveryMode: "artifact", Target: nativeTarget()})
	if conflict == nil || conflict.Code != ConflictArtifactAmbiguous {
		t.Fatalf("expected ARTIFACT_AMBIGUOUS, got %#v", conflict)
	}
}

func TestApplyDeliveryPolicyFallbackOnlyWhenNoArtifactMatches(t *testing.T) {
	other := catalog.Target{OS: "windows", Architecture: "x86_64", ABI: "mingw-ucrt"}
	graph := &Graph{
		Components: []ResolvedComponent{
			{ComponentID: "backend", Variant: "default", Artifacts: []catalog.Artifact{testArtifact("windows", other)}},
		},
	}
	if conflict := applyDeliveryPolicy(graph, ResolutionPolicy{DeliveryMode: "artifact-or-source", Target: nativeTarget()}); conflict != nil {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	if graph.Components[0].Delivery != "source" || graph.Components[0].SelectedArtifact != nil {
		t.Fatalf("expected source fallback on no match, got %#v", graph.Components[0])
	}
}

func TestApplyDeliveryPolicyLegacySourceKeepsManifestSchema(t *testing.T) {
	graph := &Graph{
		SchemaVersion: manifestSchemaVersion,
		Components: []ResolvedComponent{
			{ComponentID: "runtime", Variant: "default"},
		},
	}
	if conflict := applyDeliveryPolicy(graph, ResolutionPolicy{}); conflict != nil {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	if graph.SchemaVersion != manifestSchemaVersion {
		t.Fatalf("legacy manifest schema changed: %q", graph.SchemaVersion)
	}
	if graph.Components[0].Delivery != "source" {
		t.Fatalf("default delivery=%q", graph.Components[0].Delivery)
	}
}
