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
