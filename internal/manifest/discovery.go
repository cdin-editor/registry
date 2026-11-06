package manifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/semver"
)

const updateReportSchemaVersion = "1.0.0"

const (
	UpdateResultNoChanges        = "no_changes"
	UpdateResultChangesDetected  = "changes_detected"
	UpdateResultResolutionFailed = "resolution_failed"

	UpdateReasonUnchanged                 = "UNCHANGED"
	UpdateReasonUpdateAvailable           = "UPDATE_AVAILABLE"
	UpdateReasonSelectionChanged          = "SELECTION_CHANGED"
	UpdateReasonReleaseIdentityChanged    = "RELEASE_IDENTITY_CHANGED"
	UpdateReasonHistoricalWithdrawn       = "HISTORICAL_RELEASE_WITHDRAWN"
	UpdateReasonHistoricalUnavailable     = "HISTORICAL_RELEASE_UNAVAILABLE"
	UpdateReasonHistoricalIdentityChanged = "HISTORICAL_RELEASE_IDENTITY_CHANGED"
	UpdateReasonComponentRemoved          = "COMPONENT_REMOVED_FROM_RESOLUTION"
	UpdateReasonResolutionFailed          = "CURRENT_RESOLUTION_FAILED"
	UpdateReasonNewlySelected             = "NEWLY_SELECTED"

	UpdateCandidateSelected    = "SELECTED"
	UpdateCandidateMirrored    = "MIRRORED_IMMUTABLE_RELEASE"
	UpdateCandidateNotSelected = "NOT_SELECTED"
)

// UpdateReleaseIdentity is an immutable release identity, not a mutable alias
// such as a branch or tag. MetadataSHA256 is always the digest verified before
// the Release Metadata document was decoded.
type UpdateReleaseIdentity struct {
	Version        string `json:"version"`
	MetadataURI    string `json:"metadata_uri"`
	MetadataSHA256 string `json:"metadata_sha256"`
}

// UpdateDecision compares one historical component selection with the current
// Resolver result. HistoricalReleaseState describes what the current Catalogs
// say about the exact old URI+digest identity; SelectionReasonCode describes
// the current selection independently. The original Manifest is never altered.
type UpdateDecision struct {
	ComponentID            string                 `json:"component_id"`
	Variant                string                 `json:"variant"`
	Historical             UpdateReleaseIdentity  `json:"historical"`
	HistoricalReleaseState string                 `json:"historical_release_state"`
	Current                *UpdateReleaseIdentity `json:"current,omitempty"`
	SelectionReasonCode    string                 `json:"selection_reason_code"`
}

// UpdateCandidateDecision records why each candidate in the newly loaded
// Catalog set was or was not eligible/selected for this resolution.
type UpdateCandidateDecision struct {
	ComponentID    string `json:"component_id"`
	Variant        string `json:"variant"`
	Version        string `json:"version"`
	CatalogID      string `json:"catalog_id"`
	MetadataURI    string `json:"metadata_uri"`
	MetadataSHA256 string `json:"metadata_sha256"`
	Status         string `json:"status"`
	ReasonCode     string `json:"reason_code"`
}

// UpdateDiscoveryReport is a separate audit artifact. It binds every decision
// to the exact historical Manifest bytes, the exact Product Definition input,
// the exact current Catalog Index bytes and the complete current Resolver
// report. It contains no timestamp or random identifier, so equal inputs yield
// byte-identical reports. It is never embedded into or written over a Manifest.
type UpdateDiscoveryReport struct {
	SchemaVersion             string                      `json:"schema_version"`
	Operation                 string                      `json:"operation"`
	Result                    string                      `json:"result"`
	HistoricalManifestSHA256  string                      `json:"historical_manifest_sha256"`
	HistoricalRegistryVersion string                      `json:"historical_registry_version"`
	ProductDefinitionSHA256   string                      `json:"product_definition_sha256"`
	CurrentCatalogs           []resolver.CatalogReference `json:"current_catalogs"`
	CurrentResolution         resolver.Report             `json:"current_resolution"`
	Decisions                 []UpdateDecision            `json:"decisions"`
	CandidateDecisions        []UpdateCandidateDecision   `json:"candidate_decisions"`
}

// BuildUpdateDiscoveryReport creates the deterministic, separate audit artifact
// required by REG-027. The caller verifies the historical Manifest and loads
// the current Catalogs; this function performs no I/O and cannot mutate those
// inputs or the saved Workspace.
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#update-discovery-report
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/cli.md.
func BuildUpdateDiscoveryReport(
	historicalBytes []byte,
	historical Document,
	productDefinitionBytes []byte,
	catalogs []*catalog.Catalog,
	current resolver.Result,
) (UpdateDiscoveryReport, error) {
	if len(historicalBytes) == 0 || len(productDefinitionBytes) == 0 {
		return UpdateDiscoveryReport{}, fmt.Errorf("manifest: update discovery requires historical Manifest and Product Definition bytes")
	}
	historicalSHA := Digest(historicalBytes)
	catalogRefs := make([]resolver.CatalogReference, 0, len(catalogs))
	for _, cat := range catalogs {
		if cat == nil || strings.TrimSpace(cat.ID) == "" || strings.TrimSpace(cat.MetadataURI) == "" || !validSHA256(cat.MetadataSHA) {
			return UpdateDiscoveryReport{}, fmt.Errorf("manifest: update discovery Catalog lacks immutable index provenance")
		}
		catalogRefs = append(catalogRefs, resolver.CatalogReference{
			CatalogID: cat.ID, MetadataURI: cat.MetadataURI, MetadataSHA: strings.ToLower(cat.MetadataSHA),
		})
	}
	sort.Slice(catalogRefs, func(i, j int) bool {
		if catalogRefs[i].CatalogID != catalogRefs[j].CatalogID {
			return catalogRefs[i].CatalogID < catalogRefs[j].CatalogID
		}
		if catalogRefs[i].MetadataURI != catalogRefs[j].MetadataURI {
			return catalogRefs[i].MetadataURI < catalogRefs[j].MetadataURI
		}
		return catalogRefs[i].MetadataSHA < catalogRefs[j].MetadataSHA
	})

	currentSelections := make(map[string]resolver.ResolvedComponent)
	if current.Graph != nil {
		for _, component := range current.Graph.Components {
			key := componentIdentityKey(component.ComponentID, component.Variant)
			if _, exists := currentSelections[key]; exists {
				return UpdateDiscoveryReport{}, fmt.Errorf("manifest: current Resolver graph contains duplicate component identity")
			}
			currentSelections[key] = component
		}
	}

	historicalKeys := make(map[string]struct{}, len(historical.Components))
	decisions := make([]UpdateDecision, 0, len(historical.Components)+len(currentSelections))
	changed := false
	for _, old := range historical.Components {
		key := componentIdentityKey(old.ComponentID, old.Variant)
		if _, exists := historicalKeys[key]; exists {
			return UpdateDiscoveryReport{}, fmt.Errorf("manifest: historical Manifest contains duplicate component identity")
		}
		historicalKeys[key] = struct{}{}

		oldIdentity := UpdateReleaseIdentity{
			Version: old.Version, MetadataURI: old.ReleaseMetadataURI,
			MetadataSHA256: old.ReleaseMetadataSHA,
		}
		state := historicalReleaseState(old, catalogs)
		decision := UpdateDecision{
			ComponentID: old.ComponentID, Variant: old.Variant,
			Historical: oldIdentity, HistoricalReleaseState: state,
		}
		selected, selectedNow := currentSelections[key]
		if selectedNow {
			decision.Current = &UpdateReleaseIdentity{
				Version: selected.Version, MetadataURI: selected.ReleaseMetadataURI,
				MetadataSHA256: selected.ReleaseMetadataSHA,
			}
		}
		decision.SelectionReasonCode = updateSelectionReason(oldIdentity, state, selected, selectedNow, current.Graph != nil)
		if decision.SelectionReasonCode != UpdateReasonUnchanged {
			changed = true
		}
		decisions = append(decisions, decision)
	}

	for key, selected := range currentSelections {
		if _, existed := historicalKeys[key]; existed {
			continue
		}
		decisions = append(decisions, UpdateDecision{
			ComponentID:            selected.ComponentID,
			Variant:                selected.Variant,
			Historical:             UpdateReleaseIdentity{},
			HistoricalReleaseState: "not_present",
			Current: &UpdateReleaseIdentity{
				Version:        selected.Version,
				MetadataURI:    selected.ReleaseMetadataURI,
				MetadataSHA256: selected.ReleaseMetadataSHA,
			},
			SelectionReasonCode: UpdateReasonNewlySelected,
		})
		changed = true
	}
	sort.Slice(decisions, func(i, j int) bool {
		if decisions[i].ComponentID != decisions[j].ComponentID {
			return decisions[i].ComponentID < decisions[j].ComponentID
		}
		return decisions[i].Variant < decisions[j].Variant
	})

	candidateDecisions := buildUpdateCandidateDecisions(catalogs, current)
	reportResult := UpdateResultNoChanges
	if current.Graph == nil {
		reportResult = UpdateResultResolutionFailed
		changed = true
	} else if changed {
		reportResult = UpdateResultChangesDetected
	}
	return UpdateDiscoveryReport{
		SchemaVersion:             updateReportSchemaVersion,
		Operation:                 "manifest.update-discovery",
		Result:                    reportResult,
		HistoricalManifestSHA256:  historicalSHA,
		HistoricalRegistryVersion: historical.RegistryVersion,
		ProductDefinitionSHA256:   Digest(productDefinitionBytes),
		CurrentCatalogs:           catalogRefs,
		CurrentResolution:         current.Report,
		Decisions:                 decisions,
		CandidateDecisions:        candidateDecisions,
	}, nil
}

func historicalReleaseState(old Component, catalogs []*catalog.Catalog) string {
	sameVersion := false
	withdrawn := false
	exactIdentityStatus := ""
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		for _, release := range cat.Releases {
			if release.ComponentID != old.ComponentID ||
				normalizeUpdateVariant(release.Variant) != normalizeUpdateVariant(old.Variant) ||
				release.Version != old.Version {
				continue
			}
			sameVersion = true
			if release.Status == "withdrawn" {
				withdrawn = true
			}
			if release.MetadataURI == old.ReleaseMetadataURI &&
				strings.EqualFold(release.MetadataSHA, old.ReleaseMetadataSHA) {
				exactIdentityStatus = release.Status
			}
		}
	}
	// A release can be withdrawn by publishing a new, digest-distinct metadata
	// document for the same immutable version. Preserve that event instead of
	// misclassifying it as merely an identity change.
	if withdrawn || exactIdentityStatus == "withdrawn" {
		return "withdrawn"
	}
	if exactIdentityStatus != "" {
		return exactIdentityStatus
	}
	if sameVersion {
		return "identity_changed"
	}
	return "unavailable"
}

func updateSelectionReason(
	old UpdateReleaseIdentity,
	historicalState string,
	current resolver.ResolvedComponent,
	selected bool,
	resolutionSucceeded bool,
) string {
	if !selected {
		switch historicalState {
		case "withdrawn":
			return UpdateReasonHistoricalWithdrawn
		case "unavailable":
			return UpdateReasonHistoricalUnavailable
		case "identity_changed":
			return UpdateReasonHistoricalIdentityChanged
		}
		if !resolutionSucceeded {
			return UpdateReasonResolutionFailed
		}
		return UpdateReasonComponentRemoved
	}
	if !resolutionSucceeded {
		return UpdateReasonResolutionFailed
	}
	oldVersion, oldErr := semver.Parse(old.Version)
	currentVersion, currentErr := semver.Parse(current.Version)
	if oldErr == nil && currentErr == nil {
		switch semver.ComparePrecedence(currentVersion, oldVersion) {
		case 1:
			return UpdateReasonUpdateAvailable
		case -1:
			return UpdateReasonSelectionChanged
		}
	}
	if old.Version == current.Version &&
		old.MetadataURI == current.ReleaseMetadataURI &&
		strings.EqualFold(old.MetadataSHA256, current.ReleaseMetadataSHA) {
		return UpdateReasonUnchanged
	}
	return UpdateReasonReleaseIdentityChanged
}

func buildUpdateCandidateDecisions(catalogs []*catalog.Catalog, current resolver.Result) []UpdateCandidateDecision {
	selected := make(map[string]resolver.ResolvedComponent)
	if current.Graph != nil {
		for _, component := range current.Graph.Components {
			selected[releaseIdentityKey(component.ComponentID, component.Variant, component.Version, component.ReleaseMetadataSHA)] = component
		}
	}
	rejected := make(map[string]string)
	for _, candidate := range current.Report.RejectedCandidates {
		key := releaseIdentityKey(candidate.ComponentID, candidate.Variant, candidate.Version, candidate.ReleaseMetadataSHA)
		code := string(candidate.ReasonCode)
		if prior, exists := rejected[key]; !exists || code < prior {
			rejected[key] = code
		}
	}

	out := make([]UpdateCandidateDecision, 0)
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		for _, release := range cat.Releases {
			reason := UpdateCandidateNotSelected
			key := releaseIdentityKey(release.ComponentID, release.Variant, release.Version, release.MetadataSHA)
			if release.Status == "withdrawn" {
				reason = string(resolver.RejectionWithdrawn)
			} else if chosen, ok := selected[key]; ok {
				if chosen.ReleaseMetadataURI == release.MetadataURI &&
					strings.EqualFold(chosen.ReleaseMetadataSHA, release.MetadataSHA) {
					reason = UpdateCandidateSelected
				} else {
					reason = UpdateCandidateMirrored
				}
			} else if rejectedCode, ok := rejected[key]; ok {
				reason = rejectedCode
			}
			out = append(out, UpdateCandidateDecision{
				ComponentID:    release.ComponentID,
				Variant:        normalizeUpdateVariant(release.Variant),
				Version:        release.Version,
				CatalogID:      cat.ID,
				MetadataURI:    release.MetadataURI,
				MetadataSHA256: strings.ToLower(release.MetadataSHA),
				Status:         release.Status,
				ReasonCode:     reason,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.CatalogID != b.CatalogID {
			return a.CatalogID < b.CatalogID
		}
		return a.MetadataURI < b.MetadataURI
	})
	return out
}

func componentIdentityKey(componentID, variant string) string {
	return componentID + "\x00" + normalizeUpdateVariant(variant)
}

func releaseIdentityKey(componentID, variant, version, digest string) string {
	return componentIdentityKey(componentID, variant) + "\x00" + version + "\x00" + strings.ToLower(digest)
}

func normalizeUpdateVariant(variant string) string {
	if variant == "" {
		return "default"
	}
	return variant
}
