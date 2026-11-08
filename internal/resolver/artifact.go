package resolver

import (
	"fmt"
	"sort"

	"github.com/cdin-editor/registry/internal/catalog"
)

// validateDeliveryPolicy checks the consumer-selected delivery mode without
// inferring a host target or silently changing an ABI.
func validateDeliveryPolicy(policy *ResolutionPolicy) *Conflict {
	if policy == nil {
		return nil
	}
	switch policy.DeliveryMode {
	case "source":
		if policy.Target != nil && (!policy.Target.Valid() || policy.Target.IsUniversal()) {
			return &Conflict{Code: ConflictInvalidDeliveryPolicy, Message: "source delivery target, when supplied, must be a concrete supported target"}
		}
		return nil
	case "artifact", "artifact-or-source":
		if policy.Target == nil || !policy.Target.Valid() || policy.Target.IsUniversal() {
			return &Conflict{Code: ConflictInvalidDeliveryPolicy, Message: "artifact delivery requires a concrete OS, architecture and ABI target"}
		}
		return nil
	default:
		return &Conflict{Code: ConflictInvalidDeliveryPolicy, Message: fmt.Sprintf("unsupported delivery mode %q", policy.DeliveryMode)}
	}
}

// applyDeliveryPolicy makes a deterministic artifact choice for every selected
// release. Source fallback is used only when artifact-or-source has no match;
// integrity and transport failures happen later and never reach this function.
func applyDeliveryPolicy(graph *Graph) *Conflict {
	if graph == nil || graph.Resolution == nil {
		return nil
	}
	policy := graph.Resolution
	for index := range graph.Components {
		component := &graph.Components[index]
		switch policy.DeliveryMode {
		case "source":
			component.Delivery = "source"
		case "artifact", "artifact-or-source":
			selected, conflict := selectArtifact(component, *policy.Target)
			if conflict != nil {
				if policy.DeliveryMode == "artifact-or-source" && conflict.Code == ConflictArtifactNotFound {
					component.Delivery = "source"
					continue
				}
				return conflict
			}
			component.Delivery = "artifact"
			artifactCopy := selected
			component.Artifact = &artifactCopy
		default:
			return &Conflict{Code: ConflictInvalidDeliveryPolicy, Message: "unsupported delivery mode"}
		}
		// Do not carry all release candidates beyond resolution. Only the exact
		// selected descriptor remains in the immutable Graph.
		component.AvailableArtifacts = nil
	}
	return nil
}

func selectArtifact(component *ResolvedComponent, target catalog.Target) (catalog.Artifact, *Conflict) {
	exact := make([]catalog.Artifact, 0, 1)
	universal := make([]catalog.Artifact, 0, 1)
	for _, candidate := range component.AvailableArtifacts {
		if candidate.Target == target {
			exact = append(exact, candidate)
		} else if candidate.Target.IsUniversal() {
			universal = append(universal, candidate)
		}
	}
	candidates := exact
	if len(candidates) == 0 {
		candidates = universal
	}
	if len(candidates) == 0 {
		return catalog.Artifact{}, &Conflict{
			Code: ConflictArtifactNotFound,
			Message: fmt.Sprintf("release %s/%s@%s has no artifact for target %s/%s/%s",
				component.ComponentID, component.Variant, component.Version, target.OS, target.Architecture, target.ABI),
			InvolvedComponents: []string{component.ComponentID},
		}
	}
	if len(candidates) != 1 {
		ids := make([]string, 0, len(candidates))
		for _, item := range candidates {
			ids = append(ids, item.ID)
		}
		sort.Strings(ids)
		return catalog.Artifact{}, &Conflict{
			Code: ConflictArtifactAmbiguous,
			Message: fmt.Sprintf("release %s/%s@%s has multiple equally specific artifacts for target %s/%s/%s: %v",
				component.ComponentID, component.Variant, component.Version, target.OS, target.Architecture, target.ABI, ids),
			InvolvedComponents: []string{component.ComponentID},
		}
	}
	return candidates[0], nil
}
