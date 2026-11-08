package resolver

import (
	"fmt"

	"github.com/cdin-editor/registry/internal/catalog"
)

// applyDeliveryPolicy runs after the ordinary version/dependency/contract
// solver. Artifact selection does not influence semver ordering or graph edges.
func applyDeliveryPolicy(graph *Graph, policy ResolutionPolicy) *Conflict {
	if graph == nil {
		return &Conflict{Code: ConflictInvalidCatalog, Message: "resolved graph is nil"}
	}
	mode := policy.DeliveryMode
	if mode == "" {
		mode = "source"
	}
	if mode != "source" && mode != "artifact" && mode != "artifact-or-source" {
		return &Conflict{Code: ConflictInvalidCatalog, Message: "unsupported delivery mode"}
	}
	if mode != "source" {
		if policy.Target == nil {
			return &Conflict{Code: ConflictInvalidCatalog, Message: "artifact delivery requires an explicit target"}
		}
		if err := policy.Target.Validate(); err != nil || policy.Target.IsUniversal() {
			return &Conflict{Code: ConflictInvalidCatalog, Message: "artifact delivery requires a concrete target tuple"}
		}
	}
	if policy.DeliveryMode != "" {
		graph.Resolution = ResolutionPolicy{DeliveryMode: mode}
		if policy.Target != nil {
			target := *policy.Target
			graph.Resolution.Target = &target
		}
		graph.SchemaVersion = artifactManifestVersion
	}
	if mode == "source" {
		for i := range graph.Components {
			graph.Components[i].Delivery = "source"
		}
		return nil
	}

	target := *policy.Target
	for i := range graph.Components {
		component := &graph.Components[i]
		exact := make([]catalog.Artifact, 0, 1)
		universal := make([]catalog.Artifact, 0, 1)
		for _, artifact := range component.Artifacts {
			if artifact.Target == target {
				exact = append(exact, artifact)
			} else if artifact.Target.IsUniversal() {
				universal = append(universal, artifact)
			}
		}
		var selected *catalog.Artifact
		switch {
		case len(exact) > 1:
			return &Conflict{Code: ConflictArtifactAmbiguous, Message: fmt.Sprintf("multiple artifacts match target for %s/%s", component.ComponentID, component.Variant), InvolvedComponents: []string{component.ComponentID}}
		case len(exact) == 1:
			selected = &exact[0]
		case len(universal) > 1:
			return &Conflict{Code: ConflictArtifactAmbiguous, Message: fmt.Sprintf("multiple universal artifacts match %s/%s", component.ComponentID, component.Variant), InvolvedComponents: []string{component.ComponentID}}
		case len(universal) == 1:
			selected = &universal[0]
		}
		if selected == nil {
			if mode == "artifact" {
				return &Conflict{Code: ConflictArtifactNotFound, Message: fmt.Sprintf("no artifact for target is published by %s/%s", component.ComponentID, component.Variant), InvolvedComponents: []string{component.ComponentID}}
			}
			component.Delivery = "source"
			continue
		}
		copy := *selected
		component.Delivery = "artifact"
		component.SelectedArtifact = &copy
	}
	return nil
}
