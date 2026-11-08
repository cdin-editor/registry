// Package resolver implements deterministic, consumer-independent version,
// dependency and contract selection over validated immutable catalog metadata.
//
// Spec traceability:
//   - REG-003/004: stable IDs and default-variant normalization.
//   - REG-006/007: SemVer and the frozen constraint grammar.
//   - REG-008–REG-014: transitive closure, constraint intersection, deterministic
//     selection, prerelease/withdrawn policy, cycle detection and output order.
//   - REG-015/016: exactly-one compatible contract provider.
//   - Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#4-resolution-model
//   - Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#5-resolution-semantics
//   - Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/resolver.md
//
// The package has no CLI, network, Git or filesystem dependencies. It returns an
// in-memory selected graph and a structured report; source retrieval and output
// publication belong to separate packages so a failed selection can never leave
// a partially prepared workspace.
package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/semver"
)

const (
	manifestSchemaVersion   = "1.0.0"
	artifactManifestVersion = "1.1.0"
	reportSchemaVersion     = "1.1.0"
	defaultMaxSearchStates  = 100000
	maxResolverCandidates   = 16384
	maxResolverDeclarations = 65536
	maxResolvedComponents   = 1024
)

// ConflictCode is the closed Registry 1.1 conflict vocabulary.
type ConflictCode string

const (
	ConflictNoRootRequirements           ConflictCode = "NO_ROOT_REQUIREMENTS"
	ConflictInvalidComponentID           ConflictCode = "INVALID_COMPONENT_ID"
	ConflictInvalidVersionConstraint     ConflictCode = "INVALID_VERSION_CONSTRAINT"
	ConflictInvalidCatalog               ConflictCode = "INVALID_CATALOG"
	ConflictInvalidReleaseIdentity       ConflictCode = "INVALID_RELEASE_IDENTITY"
	ConflictInvalidReleaseVersion        ConflictCode = "INVALID_RELEASE_VERSION"
	ConflictInvalidReleaseStatus         ConflictCode = "INVALID_RELEASE_STATUS"
	ConflictMissingSourceUnits           ConflictCode = "MISSING_SOURCE_UNITS"
	ConflictInvalidSourceIdentity        ConflictCode = "INVALID_SOURCE_IDENTITY"
	ConflictResolutionLimit              ConflictCode = "RESOLUTION_LIMIT"
	ConflictInvalidDependencyID          ConflictCode = "INVALID_DEPENDENCY_ID"
	ConflictInvalidDependencyConstraint  ConflictCode = "INVALID_DEPENDENCY_CONSTRAINT"
	ConflictConstraintIntersectionEmpty  ConflictCode = "CONSTRAINT_INTERSECTION_EMPTY"
	ConflictAmbiguousRelease             ConflictCode = "AMBIGUOUS_RELEASE"
	ConflictNoCompatibleRelease          ConflictCode = "NO_COMPATIBLE_RELEASE"
	ConflictDependencyConflict           ConflictCode = "DEPENDENCY_CONFLICT"
	ConflictDependencyCycle              ConflictCode = "DEPENDENCY_CYCLE"
	ConflictInvalidContractID            ConflictCode = "INVALID_CONTRACT_ID"
	ConflictInvalidContractVersion       ConflictCode = "INVALID_CONTRACT_VERSION"
	ConflictInvalidEnvironmentContract   ConflictCode = "INVALID_ENVIRONMENT_CONTRACT"
	ConflictInvalidContractConstraint    ConflictCode = "INVALID_CONTRACT_CONSTRAINT"
	ConflictContractProviderMissing      ConflictCode = "CONTRACT_PROVIDER_MISSING"
	ConflictContractProviderAmbiguous    ConflictCode = "CONTRACT_PROVIDER_AMBIGUOUS"
	ConflictInternalUnresolvedDependency ConflictCode = "INTERNAL_UNRESOLVED_DEPENDENCY"
	ConflictArtifactNotFound             ConflictCode = "ARTIFACT_NOT_FOUND"
	ConflictArtifactAmbiguous            ConflictCode = "ARTIFACT_AMBIGUOUS"
)

// RejectionReasonCode is the closed Registry 1.1 candidate-rejection vocabulary.
type RejectionReasonCode string

const (
	RejectionWithdrawn          RejectionReasonCode = "WITHDRAWN"
	RejectionPrereleaseDisabled RejectionReasonCode = "PRERELEASE_DISABLED"
	RejectionConstraintMismatch RejectionReasonCode = "CONSTRAINT_MISMATCH"
	RejectionBranchConflict     RejectionReasonCode = "BRANCH_CONFLICT"
)

// ConstraintSourceType identifies the declaration that contributed a version constraint.
type ConstraintSourceType string

const (
	ConstraintSourceRoot       ConstraintSourceType = "root"
	ConstraintSourceDependency ConstraintSourceType = "dependency"
)

// Requirement is a root requirement from Product Definition. An omitted
// Variant is normalized to "default" before any key is constructed (REG-004).
type Requirement struct {
	ComponentID       string `json:"component_id"`
	Variant           string `json:"variant,omitempty"`
	VersionConstraint string `json:"version_constraint"`
}

// EnvironmentContract declares a contract supplied by the target environment.
// Registry treats ContractID as opaque and never inspects consumer identity.
type EnvironmentContract struct {
	ContractID string `json:"contract_id"`
	Version    string `json:"version"`
}

// ProductDefinition is the resolver-facing projection of the validated public
// Product Definition. The CLI must validate the source document against the
// embedded schema before constructing this type (REG-005).
type ProductDefinition struct {
	SchemaVersion        string                `json:"schema_version"`
	Components           []Requirement         `json:"components"`
	EnvironmentContracts []EnvironmentContract `json:"environment_contracts,omitempty"`
	Resolution           ResolutionPolicy      `json:"resolution,omitempty"`
	Policy               Policy                `json:"policy,omitempty"`
}

// ResolutionPolicy selects the delivery form for each already-resolved release.
// An omitted policy preserves legacy source collection.
type ResolutionPolicy struct {
	DeliveryMode string          `json:"delivery_mode"`
	Target       *catalog.Target `json:"target,omitempty"`
}

// Policy carries only the selection policy frozen in contract 1.1.0.
type Policy struct {
	AllowPrerelease bool `json:"allow_prerelease"`
}

// Node is the stable identity of one selected component variant.
type Node struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version,omitempty"`
}

type key struct{ componentID, variant string }

func (k key) node() Node     { return Node{ComponentID: k.componentID, Variant: k.variant} }
func (k key) String() string { return k.componentID + "/" + k.variant }

// SelectedCandidate is the report projection of one chosen immutable release.
type SelectedCandidate struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version"`
}

// ConstraintSource identifies the source of one applied version constraint.
type ConstraintSource struct {
	Type        ConstraintSourceType `json:"type"`
	ComponentID string               `json:"component_id,omitempty"`
	Variant     string               `json:"variant,omitempty"`
	Version     string               `json:"version,omitempty"`
}

// AppliedConstraint preserves one root or dependency constraint without
// coalescing duplicate declarations; each declaration remains auditable.
type AppliedConstraint struct {
	ComponentID       string           `json:"component_id"`
	Variant           string           `json:"variant"`
	VersionConstraint string           `json:"version_constraint"`
	Source            ConstraintSource `json:"source"`
}

// ReleaseIdentity is the deterministic public identity/provenance projection of a candidate.
type ReleaseIdentity struct {
	ComponentID        string `json:"component_id"`
	Variant            string `json:"variant"`
	Version            string `json:"version"`
	CatalogID          string `json:"catalog_id,omitempty"`
	ReleaseMetadataURI string `json:"release_metadata_uri,omitempty"`
	ReleaseMetadataSHA string `json:"release_metadata_sha256,omitempty"`
}

// RejectedCandidate explains why a catalog candidate was not selected.
type RejectedCandidate struct {
	ComponentID        string              `json:"component_id"`
	Variant            string              `json:"variant"`
	Version            string              `json:"version"`
	ReasonCode         RejectionReasonCode `json:"reason_code"`
	Detail             string              `json:"detail,omitempty"`
	CatalogID          string              `json:"catalog_id,omitempty"`
	ReleaseMetadataURI string              `json:"release_metadata_uri,omitempty"`
	ReleaseMetadataSHA string              `json:"release_metadata_sha256,omitempty"`
}

// ContractProviderEvidence identifies one declared provider and whether it
// satisfies the requiring release's version constraint.
type ContractProviderEvidence struct {
	Origin             string `json:"origin"`
	ComponentID        string `json:"component_id,omitempty"`
	Variant            string `json:"variant,omitempty"`
	Version            string `json:"version"`
	ComponentVersion   string `json:"component_version,omitempty"`
	CatalogID          string `json:"catalog_id,omitempty"`
	ReleaseMetadataURI string `json:"release_metadata_uri,omitempty"`
	ReleaseMetadataSHA string `json:"release_metadata_sha256,omitempty"`
	Compatible         bool   `json:"compatible"`
}

// ContractEvidence records the exact requirement and every declared provider,
// including providers that exist but have an incompatible version.
type ContractEvidence struct {
	ContractID          string                     `json:"contract_id"`
	RequestedConstraint string                     `json:"requested_constraint"`
	RequiringComponent  Node                       `json:"requiring_component"`
	Providers           []ContractProviderEvidence `json:"providers"`
}

// Conflict is a structured resolution failure. CyclePath is populated for
// REG-014; participants and contract evidence have deterministic ordering.
type Conflict struct {
	Code               ConflictCode      `json:"code"`
	Message            string            `json:"message"`
	InvolvedComponents []string          `json:"involved_components,omitempty"`
	InvolvedNodes      []Node            `json:"involved_nodes,omitempty"`
	CandidateEvidence  []ReleaseIdentity `json:"candidate_evidence,omitempty"`
	ContractEvidence   *ContractEvidence `json:"contract_evidence,omitempty"`
	CyclePath          []Node            `json:"cycle_path,omitempty"`
}

// Warning is a non-fatal, stable diagnostic.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Report follows the Registry Resolution Report v1.1 data model. It is returned
// on both success and failure; a failed report never carries a successful Graph
// (REG-025).
type Report struct {
	SchemaVersion      string              `json:"schema_version"`
	Result             string              `json:"result"`
	Selected           []SelectedCandidate `json:"selected"`
	AppliedConstraints []AppliedConstraint `json:"applied_constraints"`
	RejectedCandidates []RejectedCandidate `json:"rejected_candidates"`
	Conflicts          []Conflict          `json:"conflicts"`
	Warnings           []Warning           `json:"warnings"`
}

// CatalogReference preserves the catalog identity used by a resolution.
type CatalogReference struct {
	CatalogID   string `json:"catalog_id"`
	MetadataURI string `json:"metadata_uri"`
	MetadataSHA string `json:"metadata_sha256"`
}

// ResolvedDependency is a concrete edge after version selection.
type ResolvedDependency struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version"`
}

// ResolvedComponent is the selected release projection needed by the Collector
// and Manifest writer. Raw metadata bytes remain owned by Catalog.
type ResolvedComponent struct {
	ComponentID        string               `json:"component_id"`
	Variant            string               `json:"variant"`
	Version            string               `json:"version"`
	ReleaseMetadataURI string               `json:"release_metadata_uri"`
	ReleaseMetadataSHA string               `json:"release_metadata_sha256"`
	Delivery           string               `json:"delivery,omitempty"`
	SelectedArtifact   *catalog.Artifact    `json:"selected_artifact,omitempty"`
	Artifacts          []catalog.Artifact   `json:"-"`
	SourceUnits        []catalog.SourceUnit `json:"source_units"`
	Dependencies       []ResolvedDependency `json:"dependencies"`
	Contracts          catalog.Contracts    `json:"contracts"`
}

// Graph is the immutable selection result consumed by collection/workspace
// stages. It contains no absolute paths or host-specific values (REG-023/024).
type Graph struct {
	SchemaVersion        string                `json:"schema_version"`
	Resolution           ResolutionPolicy      `json:"resolution,omitempty"`
	Catalogs             []CatalogReference    `json:"catalogs"`
	Roots                []Requirement         `json:"roots"`
	EnvironmentContracts []EnvironmentContract `json:"environment_contracts"`
	Components           []ResolvedComponent   `json:"components"`
	ResolutionOrder      []Node                `json:"resolution_order"`
}

// Result pairs the deterministic report with either a complete Graph or nil.
type Result struct {
	Graph  *Graph
	Report Report
}

type candidate struct {
	release   catalog.Release
	version   semver.Version
	catalogID string
	identity  string
}

type constraintSource struct {
	text   string
	source ConstraintSource
	parsed semver.Constraint
}

type searchState struct {
	constraints map[key][]constraintSource
	chosen      map[key]candidate
	expanded    map[key]bool
	rejected    []RejectedCandidate
}

func newState() *searchState {
	return &searchState{
		constraints: make(map[key][]constraintSource),
		chosen:      make(map[key]candidate),
		expanded:    make(map[key]bool),
	}
}

func (s *searchState) clone() *searchState {
	out := newState()
	for k, values := range s.constraints {
		out.constraints[k] = append([]constraintSource(nil), values...)
	}
	for k, value := range s.chosen {
		out.chosen[k] = value
	}
	for k, value := range s.expanded {
		out.expanded[k] = value
	}
	out.rejected = append([]RejectedCandidate(nil), s.rejected...)
	return out
}

type solver struct {
	candidates      map[key][]candidate
	allowPrerelease bool
	states          int
	maxStates       int
}

// Resolve performs deterministic, consumer-independent version/dependency/
// contract resolution (REG-006–REG-016). Backtracking is necessary because a
// greedy highest-version choice can make transitive constraints impossible.
// Candidates are explored in descending SemVer order; catalog order and
// publication time are not tie-breakers.
//
// The caller validates public input schemas first; Resolve repeats identity,
// version and constraint checks at this trust boundary. Expected conflicts are
// returned in Report rather than as Go errors.
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#5-resolution-semantics
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/resolver.md.
func Resolve(def ProductDefinition, catalogs []*catalog.Catalog) Result {
	return resolveWithMaxSearchStates(def, catalogs, defaultMaxSearchStates)
}

// resolveWithMaxSearchStates keeps the production ceiling fixed while allowing
// deterministic boundary tests to exercise limit exhaustion without huge graphs.
func resolveWithMaxSearchStates(def ProductDefinition, catalogs []*catalog.Catalog, maxSearchStates int) Result {
	if maxSearchStates < 1 {
		maxSearchStates = defaultMaxSearchStates
	}
	result := Result{Report: Report{
		SchemaVersion:      reportSchemaVersion,
		Result:             "failure",
		Selected:           []SelectedCandidate{},
		AppliedConstraints: []AppliedConstraint{},
		RejectedCandidates: []RejectedCandidate{},
		Conflicts:          []Conflict{},
		Warnings:           []Warning{},
	}}
	if len(def.Components) == 0 {
		result.Report.Conflicts = append(result.Report.Conflicts, Conflict{Code: ConflictNoRootRequirements, Message: "Product Definition contains no root component requirements"})
		return result
	}
	if len(def.Components) > maxResolvedComponents {
		result.Report.Conflicts = append(result.Report.Conflicts, Conflict{
			Code:    ConflictResolutionLimit,
			Message: fmt.Sprintf("Product Definition has %d root requirements; the Registry component limit is %d", len(def.Components), maxResolvedComponents),
		})
		return result
	}
	totalCandidates := 0
	totalDeclarations := len(def.EnvironmentContracts)
	if totalDeclarations > maxResolverDeclarations {
		result.Report.Conflicts = append(result.Report.Conflicts, Conflict{
			Code:    ConflictResolutionLimit,
			Message: fmt.Sprintf("Product Definition declarations exceed the Registry limit of %d", maxResolverDeclarations),
		})
		return result
	}
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		if len(cat.Releases) > maxResolverCandidates-totalCandidates {
			result.Report.Conflicts = append(result.Report.Conflicts, Conflict{
				Code:    ConflictResolutionLimit,
				Message: fmt.Sprintf("Catalog release candidates exceed the Registry limit of %d", maxResolverCandidates),
			})
			return result
		}
		totalCandidates += len(cat.Releases)
		for _, release := range cat.Releases {
			declarations := []int{
				len(release.SourceUnits),
				len(release.Dependencies),
				len(release.Contracts.Provides),
				len(release.Contracts.Requires),
			}
			for _, count := range declarations {
				if count > maxResolverDeclarations-totalDeclarations {
					result.Report.Conflicts = append(result.Report.Conflicts, Conflict{
						Code:    ConflictResolutionLimit,
						Message: fmt.Sprintf("Catalog dependency, contract and source declarations exceed the Registry limit of %d", maxResolverDeclarations),
					})
					return result
				}
				totalDeclarations += count
			}
		}
	}
	candidateMap, catalogRefs, conflict := buildCandidates(catalogs)
	if conflict != nil {
		result.Report.Conflicts = append(result.Report.Conflicts, *conflict)
		return result
	}
	state := newState()
	roots := make([]Requirement, 0, len(def.Components))
	for _, root := range def.Components {
		root.Variant = normalizeVariant(root.Variant)
		if !validID(root.ComponentID, 128) || !validID(root.Variant, 64) {
			failure := Conflict{Code: ConflictInvalidComponentID, Message: fmt.Sprintf("invalid root component identity %q/%q", root.ComponentID, root.Variant), InvolvedComponents: []string{root.ComponentID}}
			populateConflictNodes(&failure, state)
			result.Report.AppliedConstraints = appliedConstraintsForState(state)
			result.Report.Conflicts = append(result.Report.Conflicts, failure)
			return result
		}
		if err := addConstraint(state, key{root.ComponentID, root.Variant}, root.VersionConstraint, ConstraintSource{Type: ConstraintSourceRoot}); err != nil {
			failure := Conflict{Code: ConflictInvalidVersionConstraint, Message: err.Error(), InvolvedComponents: []string{root.ComponentID}}
			populateConflictNodes(&failure, state)
			result.Report.AppliedConstraints = appliedConstraintsForState(state)
			result.Report.Conflicts = append(result.Report.Conflicts, failure)
			return result
		}
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].ComponentID != roots[j].ComponentID {
			return roots[i].ComponentID < roots[j].ComponentID
		}
		if roots[i].Variant != roots[j].Variant {
			return roots[i].Variant < roots[j].Variant
		}
		return roots[i].VersionConstraint < roots[j].VersionConstraint
	})
	engine := &solver{candidates: candidateMap, allowPrerelease: def.Policy.AllowPrerelease, maxStates: maxSearchStates}
	selected, failure := engine.search(state, def.EnvironmentContracts)
	if failure != nil {
		result.Report.AppliedConstraints = appliedConstraintsForState(selected)
		result.Report.RejectedCandidates = normalizeRejected(rejectedForFinalState(engine, selected))
		populateConflictNodes(failure, selected)
		result.Report.Conflicts = append(result.Report.Conflicts, *failure)
		return result
	}
	graph, reportSelected, graphConflict := buildGraph(selected, catalogRefs, roots, def.EnvironmentContracts)
	if graphConflict != nil {
		result.Report.AppliedConstraints = appliedConstraintsForState(selected)
		result.Report.RejectedCandidates = normalizeRejected(rejectedForFinalState(engine, selected))
		populateConflictNodes(graphConflict, selected)
		result.Report.Conflicts = append(result.Report.Conflicts, *graphConflict)
		return result
	}
	if conflict := applyDeliveryPolicy(graph, def.Resolution); conflict != nil {
		result.Report.Conflicts = append(result.Report.Conflicts, *conflict)
		return result
	}
	result.Graph = graph
	result.Report.Result = "success"
	result.Report.Selected = reportSelected
	result.Report.AppliedConstraints = appliedConstraintsForState(selected)
	result.Report.RejectedCandidates = normalizeRejected(rejectedForFinalState(engine, selected))
	return result
}

// buildCandidates normalizes identity before indexing and deduplicates only the same immutable metadata identity.
// This keeps candidate selection independent of catalog order (REG-004/011).
func buildCandidates(catalogs []*catalog.Catalog) (map[key][]candidate, []CatalogReference, *Conflict) {
	byKey := make(map[key][]candidate)
	catalogIDs := make(map[string]bool)
	refs := make([]CatalogReference, 0, len(catalogs))
	for _, cat := range catalogs {
		if cat == nil || strings.TrimSpace(cat.ID) == "" {
			return nil, nil, &Conflict{Code: ConflictInvalidCatalog, Message: "catalog is nil or has an empty catalog_id"}
		}
		if !catalogIDs[cat.ID] {
			refs = append(refs, CatalogReference{CatalogID: cat.ID, MetadataURI: cat.MetadataURI, MetadataSHA: cat.MetadataSHA})
			catalogIDs[cat.ID] = true
		}
		for _, release := range cat.Releases {
			release.Variant = normalizeVariant(release.Variant)
			if !validID(release.ComponentID, 128) || !validID(release.Variant, 64) {
				return nil, nil, &Conflict{Code: ConflictInvalidReleaseIdentity, Message: fmt.Sprintf("invalid release identity %q/%q", release.ComponentID, release.Variant), InvolvedComponents: []string{release.ComponentID}}
			}
			version, err := semver.Parse(release.Version)
			if err != nil {
				return nil, nil, &Conflict{Code: ConflictInvalidReleaseVersion, Message: fmt.Sprintf("invalid version for %s/%s: %v", release.ComponentID, release.Variant, err), InvolvedComponents: []string{release.ComponentID}}
			}
			if release.Status != "active" && release.Status != "withdrawn" {
				return nil, nil, &Conflict{Code: ConflictInvalidReleaseStatus, Message: fmt.Sprintf("unsupported status %q for %s/%s@%s", release.Status, release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
			}
			if len(release.SourceUnits) == 0 {
				return nil, nil, &Conflict{Code: ConflictMissingSourceUnits, Message: fmt.Sprintf("release %s/%s@%s declares no source units", release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
			}
			for _, source := range release.SourceUnits {
				if !validID(source.ID, 64) || source.RepositoryURI == "" || !isFullGitObjectID(source.Revision) {
					return nil, nil, &Conflict{Code: ConflictInvalidSourceIdentity, Message: fmt.Sprintf("release %s/%s@%s contains an invalid source unit", release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
				}
			}
			identity := release.Version + "\x00" + strings.ToLower(release.MetadataSHA)
			if release.MetadataSHA == "" {
				identityBytes := release.Raw
				if len(identityBytes) == 0 {
					// Programmatically supplied Catalog values still need a stable
					// identity for tests/in-process callers; hash the typed record.
					identityBytes, _ = json.Marshal(release)
				}
				sum := sha256.Sum256(identityBytes)
				identity = release.Version + "\x00" + hex.EncodeToString(sum[:])
			}
			k := key{release.ComponentID, release.Variant}
			incoming := candidate{release: release, version: version, catalogID: cat.ID, identity: identity}
			duplicate := false
			for index, existing := range byKey[k] {
				if existing.identity == identity {
					// One immutable release may be mirrored at multiple metadata
					// URIs. The selected URI is written into the Manifest, so retain
					// the lexically smallest provenance instead of first-seen input
					// order (REG-011/024).
					if candidateProvenanceLess(incoming, existing) {
						byKey[k][index] = incoming
					}
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			byKey[k] = append(byKey[k], incoming)
		}
	}
	for k := range byKey {
		sort.Slice(byKey[k], func(i, j int) bool {
			order := semver.ComparePrecedence(byKey[k][i].version, byKey[k][j].version)
			if order != 0 {
				return order > 0
			}
			if byKey[k][i].release.Version != byKey[k][j].release.Version {
				return byKey[k][i].release.Version < byKey[k][j].release.Version
			}
			return byKey[k][i].identity < byKey[k][j].identity
		})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].CatalogID < refs[j].CatalogID })
	return byKey, refs, nil
}

// candidateProvenanceLess selects a stable source for identical immutable bytes.
// MetadataURI is externally visible in the graph and Manifest; catalogID is a
// deterministic secondary key for equivalent URI values.
func candidateProvenanceLess(left, right candidate) bool {
	if left.release.MetadataURI != right.release.MetadataURI {
		return left.release.MetadataURI < right.release.MetadataURI
	}
	return left.catalogID < right.catalogID
}

// search performs bounded deterministic backtracking. Candidate branches get cloned state so dependencies from an unsuccessful release cannot leak (REG-008/010/011).
func (s *solver) search(state *searchState, environment []EnvironmentContract) (*searchState, *Conflict) {
	if len(state.chosen) > maxResolvedComponents {
		return state, &Conflict{
			Code:    ConflictResolutionLimit,
			Message: fmt.Sprintf("resolved graph exceeds the Registry component limit of %d", maxResolvedComponents),
		}
	}
	s.states++
	if s.states > s.maxStates {
		return state, &Conflict{Code: ConflictResolutionLimit, Message: fmt.Sprintf("resolution exceeded the deterministic search-state limit of %d", s.maxStates)}
	}
	// Expand one chosen release at a time. A copied state makes backtracking
	// safe: dependencies introduced by a rejected candidate cannot leak into
	// another candidate branch (REG-008/010).
	for _, k := range sortedChosenKeys(state.chosen) {
		if state.expanded[k] {
			continue
		}
		state.expanded[k] = true
		release := state.chosen[k].release
		deps := append([]catalog.Dependency(nil), release.Dependencies...)
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].ComponentID != deps[j].ComponentID {
				return deps[i].ComponentID < deps[j].ComponentID
			}
			if normalizeVariant(deps[i].Variant) != normalizeVariant(deps[j].Variant) {
				return normalizeVariant(deps[i].Variant) < normalizeVariant(deps[j].Variant)
			}
			return deps[i].VersionConstraint < deps[j].VersionConstraint
		})
		for _, dep := range deps {
			depVariant := normalizeVariant(dep.Variant)
			if !validID(dep.ComponentID, 128) || !validID(depVariant, 64) {
				return state, &Conflict{Code: ConflictInvalidDependencyID, Message: fmt.Sprintf("%s/%s@%s declares invalid dependency identity %q/%q", release.ComponentID, release.Variant, release.Version, dep.ComponentID, depVariant), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
			}
			depKey := key{dep.ComponentID, depVariant}
			source := ConstraintSource{Type: ConstraintSourceDependency, ComponentID: release.ComponentID, Variant: release.Variant, Version: release.Version}
			if err := addConstraint(state, depKey, dep.VersionConstraint, source); err != nil {
				return state, &Conflict{Code: ConflictInvalidDependencyConstraint, Message: err.Error(), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
			}
			if chosen, ok := state.chosen[depKey]; ok && !satisfiesAll(chosen.version, state.constraints[depKey]) {
				return state, &Conflict{Code: ConflictConstraintIntersectionEmpty, Message: fmt.Sprintf("new dependency constraints make selected %s@%s incompatible", depKey.String(), chosen.release.Version), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
			}
		}
		return s.search(state, environment)
	}

	var next key
	found := false
	for _, k := range sortedConstraintKeys(state.constraints) {
		if _, exists := state.chosen[k]; !exists {
			next = k
			found = true
			break
		}
	}
	if !found {
		if conflict := validateCycles(state.chosen); conflict != nil {
			return state, conflict
		}
		if conflict := validateContracts(state.chosen, environment); conflict != nil {
			return state, conflict
		}
		return state, nil
	}

	eligible, rejected, conflict := s.candidatesFor(next, state.constraints[next])
	state.rejected = append(state.rejected, rejected...)
	if conflict != nil {
		return state, conflict
	}
	var firstFailure *Conflict
	var firstFailureState *searchState
	branchRejections := make([]RejectedCandidate, 0)
	for index := 0; index < len(eligible); {
		// Equal-precedence identities are ambiguous at the point they would be
		// selected. Check each precedence group when reached, including fallback
		// groups after a higher candidate's transitive branch has failed.
		groupEnd := index + 1
		for groupEnd < len(eligible) && semver.ComparePrecedence(eligible[index].version, eligible[groupEnd].version) == 0 {
			groupEnd++
		}
		if groupEnd-index > 1 {
			conflict := &Conflict{
				Code:               ConflictAmbiguousRelease,
				Message:            fmt.Sprintf("multiple distinct releases have equal SemVer precedence for %s", next.String()),
				InvolvedComponents: []string{next.componentID},
			}
			for i := index; i < groupEnd; i++ {
				conflict.CandidateEvidence = append(conflict.CandidateEvidence, identityOfCandidate(eligible[i]))
			}
			sortReleaseIdentities(conflict.CandidateEvidence)
			return state, conflict
		}
		candidate := eligible[index]
		branch := state.clone()
		branch.rejected = append(branch.rejected, branchRejections...)
		branch.chosen[next] = candidate
		resolved, failure := s.search(branch, environment)
		if failure == nil {
			return resolved, nil
		}
		if failure.Code == ConflictResolutionLimit {
			return resolved, failure
		}
		marker := rejectedBranchCandidate(candidate, failure.Code)
		resolved.rejected = append(resolved.rejected, marker)
		branchRejections = append(branchRejections, marker)
		if firstFailure == nil {
			firstFailure = failure
			firstFailureState = resolved
		}
		index = groupEnd
	}
	if len(eligible) == 0 {
		return state, &Conflict{Code: ConflictNoCompatibleRelease, Message: fmt.Sprintf("no compatible active release satisfies all constraints for %s", next.String()), InvolvedComponents: []string{next.componentID}}
	}
	// Surface only the evidence from the deterministic failure branch selected
	// by candidate precedence. Rejections from other abandoned branches are
	// recomputed against that exact state by the report builder.
	if firstFailure != nil {
		return firstFailureState, firstFailure
	}
	return state, &Conflict{Code: ConflictDependencyConflict, Message: fmt.Sprintf("all candidate releases for %s lead to unsatisfied requirements", next.String()), InvolvedComponents: []string{next.componentID}}
}

// candidatesFor applies release status, prerelease and every accumulated constraint before ordering candidates (REG-010/012/013).
func (s *solver) candidatesFor(k key, constraints []constraintSource) ([]candidate, []RejectedCandidate, *Conflict) {
	all := s.candidates[k]
	eligible := make([]candidate, 0, len(all))
	rejected := make([]RejectedCandidate, 0)
	if constraintIntersectionEmpty(constraints) {
		return nil, rejected, &Conflict{Code: ConflictConstraintIntersectionEmpty, Message: fmt.Sprintf("the accumulated version constraints have an empty intersection for %s", k.String()), InvolvedComponents: []string{k.componentID}}
	}
	for _, candidate := range all {
		r := candidate.release
		var reason RejectionReasonCode
		detail := ""
		switch {
		case r.Status == "withdrawn":
			reason, detail = RejectionWithdrawn, "withdrawn releases are ineligible for new resolutions (REG-013)"
		case candidate.version.HasPrerelease() && !s.allowPrerelease:
			reason, detail = RejectionPrereleaseDisabled, "Product Definition policy does not allow prerelease releases (REG-012)"
		case !satisfiesAll(candidate.version, constraints):
			reason, detail = RejectionConstraintMismatch, "release does not satisfy every accumulated version constraint"
		}
		if reason != "" {
			rejected = append(rejected, RejectedCandidate{
				ComponentID: r.ComponentID, Variant: r.Variant, Version: r.Version,
				ReasonCode: reason, Detail: detail, CatalogID: candidate.catalogID,
				ReleaseMetadataURI: r.MetadataURI, ReleaseMetadataSHA: r.MetadataSHA,
			})
			continue
		}
		eligible = append(eligible, candidate)
	}

	return eligible, rejected, nil
}

// addConstraint appends rather than overwrites a requirement, preserving the full intersection for a component/variant key (REG-010).
func addConstraint(state *searchState, k key, text string, source ConstraintSource) error {
	parsed, err := semver.ParseConstraint(text)
	if err != nil {
		return fmt.Errorf("invalid version constraint %q from %s: %w", text, constraintSourceLabel(source), err)
	}
	state.constraints[k] = append(state.constraints[k], constraintSource{text: text, source: source, parsed: parsed})
	return nil
}

func constraintSourceLabel(source ConstraintSource) string {
	if source.Type == ConstraintSourceRoot {
		return "product root"
	}
	return source.ComponentID + "/" + source.Variant + "@" + source.Version
}

// constraintIntersectionEmpty checks the mathematical interval formed by all
// exact/lower/upper comparators before consulting the catalog. This gives REG-010
// a stable conflict even when the catalog is also missing a candidate.
func constraintIntersectionEmpty(sources []constraintSource) bool {
	type bound struct {
		version semver.Version
		strict  bool
	}
	var exact *semver.Version
	var lower *bound
	var upper *bound
	for _, source := range sources {
		for _, comparator := range source.parsed.Comparators {
			switch comparator.Operator {
			case semver.Equal:
				value := comparator.Version
				if exact != nil && semver.ComparePrecedence(*exact, value) != 0 {
					return true
				}
				exact = &value
			case semver.Greater, semver.GreaterEqual:
				candidate := bound{version: comparator.Version, strict: comparator.Operator == semver.Greater}
				if lower == nil || semver.ComparePrecedence(candidate.version, lower.version) > 0 || semver.ComparePrecedence(candidate.version, lower.version) == 0 && candidate.strict {
					lower = &candidate
				}
			case semver.Less, semver.LessEqual:
				candidate := bound{version: comparator.Version, strict: comparator.Operator == semver.Less}
				if upper == nil || semver.ComparePrecedence(candidate.version, upper.version) < 0 || semver.ComparePrecedence(candidate.version, upper.version) == 0 && candidate.strict {
					upper = &candidate
				}
			}
		}
	}
	if exact != nil {
		for _, source := range sources {
			if !source.parsed.Allows(*exact) {
				return true
			}
		}
		return false
	}
	if lower != nil && upper != nil {
		order := semver.ComparePrecedence(lower.version, upper.version)
		return order > 0 || order == 0 && (lower.strict || upper.strict)
	}
	return false
}

// satisfiesAll checks every root and transitive requirement for the candidate.
func satisfiesAll(version semver.Version, constraints []constraintSource) bool {
	for _, source := range constraints {
		if !source.parsed.Allows(version) {
			return false
		}
	}
	return true
}

// validateCycles uses a deterministic depth-first traversal and returns the concrete back-edge cycle path (REG-014).
func validateCycles(chosen map[key]candidate) *Conflict {
	state := make(map[key]uint8)
	stack := make([]key, 0, len(chosen))
	var found []key
	var visit func(key) bool
	visit = func(current key) bool {
		state[current] = 1
		stack = append(stack, current)
		for _, dep := range dependenciesOf(chosen[current].release) {
			next := key{dep.ComponentID, normalizeVariant(dep.Variant)}
			if _, ok := chosen[next]; !ok {
				continue
			}
			if state[next] == 1 {
				start := 0
				for i, item := range stack {
					if item == next {
						start = i
						break
					}
				}
				found = append(append([]key(nil), stack[start:]...), next)
				return true
			}
			if state[next] == 0 && visit(next) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[current] = 2
		return false
	}
	for _, k := range sortedChosenKeys(chosen) {
		if state[k] == 0 && visit(k) {
			break
		}
	}
	if len(found) == 0 {
		return nil
	}
	path := make([]Node, 0, len(found))
	involved := make([]string, 0, len(found))
	for _, k := range found {
		c := chosen[k]
		path = append(path, Node{ComponentID: k.componentID, Variant: k.variant, Version: c.release.Version})
		involved = append(involved, k.componentID)
	}
	return &Conflict{Code: ConflictDependencyCycle, Message: "selected dependency graph contains a cycle", InvolvedComponents: uniqueSorted(involved), CyclePath: path}
}

// dependenciesOf returns a stable dependency sequence so diagnostics and graph traversal do not inherit metadata array order.
func dependenciesOf(release catalog.Release) []catalog.Dependency {
	deps := append([]catalog.Dependency(nil), release.Dependencies...)
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].ComponentID != deps[j].ComponentID {
			return deps[i].ComponentID < deps[j].ComponentID
		}
		return normalizeVariant(deps[i].Variant) < normalizeVariant(deps[j].Variant)
	})
	return deps
}

// validateContracts requires exactly one compatible provider across selected releases and declared environment contracts (REG-015/016).
// A release's own declaration participates in the provider set; self-provision is
// deliberately not excluded. Identical declarations within one release coalesce.
func validateContracts(chosen map[key]candidate, environment []EnvironmentContract) *Conflict {
	type providerCandidate struct {
		version  semver.Version
		evidence ContractProviderEvidence
	}
	providers := make(map[string][]providerCandidate)
	for _, k := range sortedChosenKeys(chosen) {
		selected := chosen[k]
		release := selected.release
		seen := make(map[string]bool)
		for _, declaration := range release.Contracts.Provides {
			if !validContractID(declaration.ContractID) {
				return &Conflict{Code: ConflictInvalidContractID, Message: fmt.Sprintf("%s/%s declares invalid contract ID %q", k.componentID, k.variant, declaration.ContractID), InvolvedComponents: []string{k.componentID}}
			}
			version, err := semver.Parse(declaration.Version)
			if err != nil {
				return &Conflict{Code: ConflictInvalidContractVersion, Message: fmt.Sprintf("%s/%s provides invalid contract version %q", k.componentID, k.variant, declaration.Version), InvolvedComponents: []string{k.componentID}}
			}
			id := declaration.ContractID + "\x00" + declaration.Version
			if seen[id] {
				continue
			}
			seen[id] = true
			providers[declaration.ContractID] = append(providers[declaration.ContractID], providerCandidate{
				version: version,
				evidence: ContractProviderEvidence{
					Origin: "component", ComponentID: k.componentID, Variant: k.variant, Version: declaration.Version, ComponentVersion: release.Version,
					CatalogID: selected.catalogID, ReleaseMetadataURI: release.MetadataURI,
					ReleaseMetadataSHA: release.MetadataSHA,
				},
			})
		}
	}
	for _, declaration := range environment {
		if !validContractID(declaration.ContractID) {
			return &Conflict{Code: ConflictInvalidEnvironmentContract, Message: fmt.Sprintf("environment contract has invalid ID %q", declaration.ContractID)}
		}
		version, err := semver.Parse(declaration.Version)
		if err != nil {
			return &Conflict{Code: ConflictInvalidEnvironmentContract, Message: fmt.Sprintf("environment contract %q has invalid version: %v", declaration.ContractID, err)}
		}
		providers[declaration.ContractID] = append(providers[declaration.ContractID], providerCandidate{
			version:  version,
			evidence: ContractProviderEvidence{Origin: "environment", Version: declaration.Version},
		})
	}

	for _, k := range sortedChosenKeys(chosen) {
		release := chosen[k].release
		requires := append([]catalog.ContractRequirement(nil), release.Contracts.Requires...)
		sort.Slice(requires, func(i, j int) bool {
			if requires[i].ContractID != requires[j].ContractID {
				return requires[i].ContractID < requires[j].ContractID
			}
			return requires[i].VersionConstraint < requires[j].VersionConstraint
		})
		for _, requirement := range requires {
			if !validContractID(requirement.ContractID) {
				return &Conflict{Code: ConflictInvalidContractID, Message: fmt.Sprintf("%s/%s requires invalid contract ID %q", k.componentID, k.variant, requirement.ContractID), InvolvedComponents: []string{k.componentID}}
			}
			constraint, err := semver.ParseConstraint(requirement.VersionConstraint)
			if err != nil {
				return &Conflict{Code: ConflictInvalidContractConstraint, Message: fmt.Sprintf("%s/%s requires %q with invalid constraint: %v", k.componentID, k.variant, requirement.ContractID, err), InvolvedComponents: []string{k.componentID}}
			}

			evidence := &ContractEvidence{
				ContractID:          requirement.ContractID,
				RequestedConstraint: requirement.VersionConstraint,
				RequiringComponent:  Node{ComponentID: k.componentID, Variant: k.variant, Version: release.Version},
				Providers:           make([]ContractProviderEvidence, 0, len(providers[requirement.ContractID])),
			}
			compatibleCount := 0
			involved := []string{k.componentID}
			for _, candidate := range providers[requirement.ContractID] {
				item := candidate.evidence
				item.Compatible = constraint.Allows(candidate.version)
				if item.Compatible {
					compatibleCount++
				}
				if item.Origin == "component" {
					involved = append(involved, item.ComponentID)
				}
				evidence.Providers = append(evidence.Providers, item)
			}
			sort.Slice(evidence.Providers, func(i, j int) bool {
				a, b := evidence.Providers[i], evidence.Providers[j]
				if a.Origin != b.Origin {
					return a.Origin < b.Origin
				}
				if a.ComponentID != b.ComponentID {
					return a.ComponentID < b.ComponentID
				}
				if a.Variant != b.Variant {
					return a.Variant < b.Variant
				}
				if a.ComponentVersion != b.ComponentVersion {
					return a.ComponentVersion < b.ComponentVersion
				}
				if a.Version != b.Version {
					return a.Version < b.Version
				}
				if a.CatalogID != b.CatalogID {
					return a.CatalogID < b.CatalogID
				}
				if a.ReleaseMetadataURI != b.ReleaseMetadataURI {
					return a.ReleaseMetadataURI < b.ReleaseMetadataURI
				}
				return a.ReleaseMetadataSHA < b.ReleaseMetadataSHA
			})
			if compatibleCount == 0 {
				return &Conflict{
					Code:               ConflictContractProviderMissing,
					Message:            fmt.Sprintf("%s/%s requires contract %q %s but no compatible provider exists", k.componentID, k.variant, requirement.ContractID, requirement.VersionConstraint),
					InvolvedComponents: uniqueSorted(involved), ContractEvidence: evidence,
				}
			}
			if compatibleCount > 1 {
				return &Conflict{
					Code:               ConflictContractProviderAmbiguous,
					Message:            fmt.Sprintf("%s/%s requires contract %q %s but %d compatible providers exist", k.componentID, k.variant, requirement.ContractID, requirement.VersionConstraint, compatibleCount),
					InvolvedComponents: uniqueSorted(involved), ContractEvidence: evidence,
				}
			}
		}
	}
	return nil
}

// buildGraph projects only a complete selected state into stable, typed output; callers never receive a partial graph (REG-009/023/025).
func buildGraph(state *searchState, catalogs []CatalogReference, roots []Requirement, environment []EnvironmentContract) (*Graph, []SelectedCandidate, *Conflict) {
	order, cycle := topologicalOrder(state.chosen)
	if cycle != nil {
		return nil, nil, cycle
	}
	graph := &Graph{
		SchemaVersion:        manifestSchemaVersion,
		Catalogs:             append([]CatalogReference(nil), catalogs...),
		Roots:                append([]Requirement(nil), roots...),
		EnvironmentContracts: append([]EnvironmentContract(nil), environment...),
		Components:           make([]ResolvedComponent, 0, len(state.chosen)),
		ResolutionOrder:      order,
	}
	selected := make([]SelectedCandidate, 0, len(state.chosen))
	for _, node := range order {
		k := key{node.ComponentID, node.Variant}
		candidate := state.chosen[k]
		r := candidate.release
		dependencies := make([]ResolvedDependency, 0, len(r.Dependencies))
		for _, dependency := range dependenciesOf(r) {
			depKey := key{dependency.ComponentID, normalizeVariant(dependency.Variant)}
			resolved, ok := state.chosen[depKey]
			if !ok {
				return nil, nil, &Conflict{Code: ConflictInternalUnresolvedDependency, Message: "resolved graph is missing a selected dependency", InvolvedComponents: []string{r.ComponentID, dependency.ComponentID}}
			}
			dependencies = append(dependencies, ResolvedDependency{ComponentID: depKey.componentID, Variant: depKey.variant, Version: resolved.release.Version})
		}
		graph.Components = append(graph.Components, ResolvedComponent{
			ComponentID: r.ComponentID, Variant: r.Variant, Version: r.Version,
			ReleaseMetadataURI: r.MetadataURI, ReleaseMetadataSHA: r.MetadataSHA,
			SourceUnits:  append([]catalog.SourceUnit(nil), r.SourceUnits...),
			Artifacts:    append([]catalog.Artifact(nil), r.Artifacts...),
			Dependencies: dependencies, Contracts: r.Contracts,
		})
		selected = append(selected, SelectedCandidate{ComponentID: r.ComponentID, Variant: r.Variant, Version: r.Version})
	}
	return graph, selected, nil
}

// topologicalOrder emits dependencies before dependents and lexically sorts simultaneously-ready nodes (REG-011/014).
func topologicalOrder(chosen map[key]candidate) ([]Node, *Conflict) {
	indegree := make(map[key]int, len(chosen))
	dependents := make(map[key][]key, len(chosen))
	for k := range chosen {
		indegree[k] = 0
	}
	for k, candidate := range chosen {
		seen := make(map[key]bool)
		for _, dependency := range candidate.release.Dependencies {
			dep := key{dependency.ComponentID, normalizeVariant(dependency.Variant)}
			if _, ok := chosen[dep]; !ok {
				continue
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			indegree[k]++
			dependents[dep] = append(dependents[dep], k)
		}
	}
	ready := make([]key, 0)
	for k, count := range indegree {
		if count == 0 {
			ready = append(ready, k)
		}
	}
	sortKeys(ready)
	order := make([]Node, 0, len(chosen))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		candidate := chosen[current]
		order = append(order, Node{ComponentID: current.componentID, Variant: current.variant, Version: candidate.release.Version})
		children := dependents[current]
		sortKeys(children)
		for _, child := range children {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sortKeys(ready)
			}
		}
	}
	if len(order) != len(chosen) {
		return nil, validateCycles(chosen)
	}
	return order, nil
}

// rejectedForFinalState recomputes rejections against the winning branch, avoiding diagnostics leaked from discarded branches.
func rejectedForFinalState(engine *solver, state *searchState) []RejectedCandidate {
	out := append([]RejectedCandidate(nil), state.rejected...)
	for _, k := range sortedConstraintKeys(state.constraints) {
		_, rejected, _ := engine.candidatesFor(k, state.constraints[k])
		out = append(out, rejected...)
	}
	return out
}

func rejectedBranchCandidate(candidate candidate, failure ConflictCode) RejectedCandidate {
	return RejectedCandidate{
		ComponentID:        candidate.release.ComponentID,
		Variant:            candidate.release.Variant,
		Version:            candidate.release.Version,
		ReasonCode:         RejectionBranchConflict,
		Detail:             string(failure),
		CatalogID:          candidate.catalogID,
		ReleaseMetadataURI: candidate.release.MetadataURI,
		ReleaseMetadataSHA: candidate.release.MetadataSHA,
	}
}

func normalizeRejected(input []RejectedCandidate) []RejectedCandidate {
	seen := make(map[string]bool)
	out := make([]RejectedCandidate, 0, len(input))
	for _, item := range input {
		key := strings.Join([]string{
			item.ComponentID, item.Variant, item.Version, string(item.ReasonCode), item.Detail,
			item.CatalogID, item.ReleaseMetadataURI, item.ReleaseMetadataSHA,
		}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
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
		if a.ReasonCode != b.ReasonCode {
			return a.ReasonCode < b.ReasonCode
		}
		if a.CatalogID != b.CatalogID {
			return a.CatalogID < b.CatalogID
		}
		if a.ReleaseMetadataURI != b.ReleaseMetadataURI {
			return a.ReleaseMetadataURI < b.ReleaseMetadataURI
		}
		if a.ReleaseMetadataSHA != b.ReleaseMetadataSHA {
			return a.ReleaseMetadataSHA < b.ReleaseMetadataSHA
		}
		return a.Detail < b.Detail
	})
	return out
}

func appliedConstraintsForState(state *searchState) []AppliedConstraint {
	out := make([]AppliedConstraint, 0)
	if state == nil {
		return out
	}
	for _, k := range sortedConstraintKeys(state.constraints) {
		for _, source := range state.constraints[k] {
			out = append(out, AppliedConstraint{
				ComponentID: k.componentID, Variant: k.variant,
				VersionConstraint: source.text, Source: source.source,
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
		if a.VersionConstraint != b.VersionConstraint {
			return a.VersionConstraint < b.VersionConstraint
		}
		if a.Source.Type != b.Source.Type {
			return a.Source.Type < b.Source.Type
		}
		if a.Source.ComponentID != b.Source.ComponentID {
			return a.Source.ComponentID < b.Source.ComponentID
		}
		if a.Source.Variant != b.Source.Variant {
			return a.Source.Variant < b.Source.Variant
		}
		return a.Source.Version < b.Source.Version
	})
	return out
}

func identityOfCandidate(candidate candidate) ReleaseIdentity {
	return ReleaseIdentity{
		ComponentID: candidate.release.ComponentID, Variant: candidate.release.Variant,
		Version: candidate.release.Version, CatalogID: candidate.catalogID,
		ReleaseMetadataURI: candidate.release.MetadataURI,
		ReleaseMetadataSHA: candidate.release.MetadataSHA,
	}
}

func sortReleaseIdentities(values []ReleaseIdentity) {
	sort.Slice(values, func(i, j int) bool {
		a, b := values[i], values[j]
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
		if a.ReleaseMetadataURI != b.ReleaseMetadataURI {
			return a.ReleaseMetadataURI < b.ReleaseMetadataURI
		}
		return a.ReleaseMetadataSHA < b.ReleaseMetadataSHA
	})
}

// populateConflictNodes adds every involved selected or constrained variant,
// without inferring a version for a component that was never selected.
func populateConflictNodes(conflict *Conflict, state *searchState) {
	if conflict == nil || state == nil {
		return
	}
	wanted := make(map[string]bool, len(conflict.InvolvedComponents))
	for _, id := range conflict.InvolvedComponents {
		wanted[id] = true
	}
	seen := make(map[string]bool)
	add := func(k key) {
		if !wanted[k.componentID] || !validID(k.componentID, 128) || !validID(k.variant, 64) {
			return
		}
		identity := k.componentID + "\x00" + k.variant
		if seen[identity] {
			return
		}
		seen[identity] = true
		node := Node{ComponentID: k.componentID, Variant: k.variant}
		if selected, ok := state.chosen[k]; ok {
			node.Version = selected.release.Version
		}
		conflict.InvolvedNodes = append(conflict.InvolvedNodes, node)
	}
	for _, k := range sortedConstraintKeys(state.constraints) {
		add(k)
	}
	for _, k := range sortedChosenKeys(state.chosen) {
		add(k)
	}
	sort.Slice(conflict.InvolvedNodes, func(i, j int) bool {
		a, b := conflict.InvolvedNodes[i], conflict.InvolvedNodes[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		return a.Version < b.Version
	})
}

func normalizeVariant(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

// validID applies the stable lowercase ASCII identifier grammar from REG-003.
func validID(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	lastSeparator := false
	for _, r := range value {
		alnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		separator := r == '.' || r == '-'
		if !alnum && !separator {
			return false
		}
		if separator && lastSeparator {
			return false
		}
		lastSeparator = separator
	}
	return !lastSeparator
}

// validContractID enforces the opaque namespaced contract identifier syntax without assigning meaning to its segments (REG-015/016).
func validContractID(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == ':' || r == '-'
		if !valid || i == 0 && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// isFullGitObjectID checks only declared full object-id shape. Collector must later verify the actual fetched commit object (REG-018).
func isFullGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func sortedConstraintKeys(values map[key][]constraintSource) []key {
	out := make([]key, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	sortKeys(out)
	return out
}
func sortedChosenKeys(values map[key]candidate) []key {
	out := make([]key, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	sortKeys(out)
	return out
}
func sortKeys(values []key) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].componentID != values[j].componentID {
			return values[i].componentID < values[j].componentID
		}
		return values[i].variant < values[j].variant
	})
}
func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
