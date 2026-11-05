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
	contractSchemaVersion  = "1.0.0"
	defaultMaxSearchStates = 100000
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
	Policy               Policy                `json:"policy,omitempty"`
}

// Policy carries only the selection policy frozen in contract 1.0.0.
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

// RejectedCandidate explains why a catalog candidate was not selected.
type RejectedCandidate struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version"`
	ReasonCode  string `json:"reason_code"`
	Detail      string `json:"detail,omitempty"`
}

// Conflict is a structured resolution failure. CyclePath is populated for
// REG-014; InvolvedComponents is stable-sorted for machine-readable diagnostics.
type Conflict struct {
	Code               string   `json:"code"`
	Message            string   `json:"message"`
	InvolvedComponents []string `json:"involved_components,omitempty"`
	CyclePath          []Node   `json:"cycle_path,omitempty"`
}

// Warning is a non-fatal, stable diagnostic.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Report follows the Registry Resolution Report v1 data model. It is returned
// on both success and failure; a failed report never carries a successful Graph
// (REG-025).
type Report struct {
	SchemaVersion      string              `json:"schema_version"`
	Result             string              `json:"result"`
	Selected           []SelectedCandidate `json:"selected"`
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
	SourceUnits        []catalog.SourceUnit `json:"source_units"`
	Dependencies       []ResolvedDependency `json:"dependencies"`
	Contracts          catalog.Contracts    `json:"contracts"`
}

// Graph is the immutable selection result consumed by collection/workspace
// stages. It contains no absolute paths or host-specific values (REG-023/024).
type Graph struct {
	SchemaVersion        string                `json:"schema_version"`
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
	from   string
	parsed semver.Constraint
}

type searchState struct {
	constraints map[key][]constraintSource
	chosen      map[key]candidate
	expanded    map[key]bool
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
	return out
}

type solver struct {
	candidates      map[key][]candidate
	allowPrerelease bool
	states          int
	maxStates       int
	lastRejected    []RejectedCandidate
}

// Resolve performs a deterministic backtracking resolution. Backtracking is
// necessary because selecting the highest release greedily can introduce a
// transitive constraint that makes the graph impossible even though a lower
// release would satisfy the entire graph. Candidates are always explored in
// descending SemVer order; no catalog order or publication time is a tie-breaker.
//
// The function assumes the caller already validated public documents against
// the v1 schemas, but it repeats identity/version/constraint checks at this
// trust boundary. Expected conflicts are returned in Report, not as Go errors.
func Resolve(def ProductDefinition, catalogs []*catalog.Catalog) Result {
	result := Result{Report: Report{
		SchemaVersion:      contractSchemaVersion,
		Result:             "failure",
		Selected:           []SelectedCandidate{},
		RejectedCandidates: []RejectedCandidate{},
		Conflicts:          []Conflict{},
		Warnings:           []Warning{},
	}}
	if len(def.Components) == 0 {
		result.Report.Conflicts = append(result.Report.Conflicts, Conflict{Code: "NO_ROOT_REQUIREMENTS", Message: "Product Definition contains no root component requirements"})
		return result
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
			result.Report.Conflicts = append(result.Report.Conflicts, Conflict{Code: "INVALID_COMPONENT_ID", Message: fmt.Sprintf("invalid root component identity %q/%q", root.ComponentID, root.Variant), InvolvedComponents: []string{root.ComponentID}})
			return result
		}
		if err := addConstraint(state, key{root.ComponentID, root.Variant}, root.VersionConstraint, "product root"); err != nil {
			result.Report.Conflicts = append(result.Report.Conflicts, Conflict{Code: "INVALID_VERSION_CONSTRAINT", Message: err.Error(), InvolvedComponents: []string{root.ComponentID}})
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
	engine := &solver{candidates: candidateMap, allowPrerelease: def.Policy.AllowPrerelease, maxStates: defaultMaxSearchStates}
	selected, failure := engine.search(state, def.EnvironmentContracts)
	if failure != nil {
		result.Report.Conflicts = append(result.Report.Conflicts, *failure)
		result.Report.RejectedCandidates = normalizeRejected(engine.lastRejected)
		return result
	}
	graph, reportSelected, graphConflict := buildGraph(selected, catalogRefs, roots, def.EnvironmentContracts)
	if graphConflict != nil {
		result.Report.Conflicts = append(result.Report.Conflicts, *graphConflict)
		return result
	}
	result.Graph = graph
	result.Report.Result = "success"
	result.Report.Selected = reportSelected
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
			return nil, nil, &Conflict{Code: "INVALID_CATALOG", Message: "catalog is nil or has an empty catalog_id"}
		}
		if !catalogIDs[cat.ID] {
			refs = append(refs, CatalogReference{CatalogID: cat.ID, MetadataURI: cat.MetadataURI, MetadataSHA: cat.MetadataSHA})
			catalogIDs[cat.ID] = true
		}
		for _, release := range cat.Releases {
			release.Variant = normalizeVariant(release.Variant)
			if !validID(release.ComponentID, 128) || !validID(release.Variant, 64) {
				return nil, nil, &Conflict{Code: "INVALID_RELEASE_IDENTITY", Message: fmt.Sprintf("invalid release identity %q/%q", release.ComponentID, release.Variant), InvolvedComponents: []string{release.ComponentID}}
			}
			version, err := semver.Parse(release.Version)
			if err != nil {
				return nil, nil, &Conflict{Code: "INVALID_RELEASE_VERSION", Message: fmt.Sprintf("invalid version for %s/%s: %v", release.ComponentID, release.Variant, err), InvolvedComponents: []string{release.ComponentID}}
			}
			if release.Status != "active" && release.Status != "withdrawn" {
				return nil, nil, &Conflict{Code: "INVALID_RELEASE_STATUS", Message: fmt.Sprintf("unsupported status %q for %s/%s@%s", release.Status, release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
			}
			if len(release.SourceUnits) == 0 {
				return nil, nil, &Conflict{Code: "MISSING_SOURCE_UNITS", Message: fmt.Sprintf("release %s/%s@%s declares no source units", release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
			}
			for _, source := range release.SourceUnits {
				if !validID(source.ID, 64) || source.RepositoryURI == "" || !isFullGitObjectID(source.Revision) {
					return nil, nil, &Conflict{Code: "INVALID_SOURCE_IDENTITY", Message: fmt.Sprintf("release %s/%s@%s contains an invalid source unit", release.ComponentID, release.Variant, release.Version), InvolvedComponents: []string{release.ComponentID}}
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
	s.states++
	if s.states > s.maxStates {
		return nil, &Conflict{Code: "RESOLUTION_LIMIT", Message: fmt.Sprintf("resolution exceeded the deterministic search-state limit of %d", s.maxStates)}
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
				return nil, &Conflict{Code: "INVALID_DEPENDENCY_ID", Message: fmt.Sprintf("%s/%s@%s declares invalid dependency identity %q/%q", release.ComponentID, release.Variant, release.Version, dep.ComponentID, depVariant), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
			}
			depKey := key{dep.ComponentID, depVariant}
			if err := addConstraint(state, depKey, dep.VersionConstraint, release.ComponentID+"/"+release.Variant+"@"+release.Version); err != nil {
				return nil, &Conflict{Code: "INVALID_DEPENDENCY_CONSTRAINT", Message: err.Error(), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
			}
			if chosen, ok := state.chosen[depKey]; ok && !satisfiesAll(chosen.version, state.constraints[depKey]) {
				return nil, &Conflict{Code: "CONSTRAINT_INTERSECTION_EMPTY", Message: fmt.Sprintf("new dependency constraints make selected %s@%s incompatible", depKey.String(), chosen.release.Version), InvolvedComponents: []string{release.ComponentID, dep.ComponentID}}
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
			return nil, conflict
		}
		if conflict := validateContracts(state.chosen, environment); conflict != nil {
			return nil, conflict
		}
		return state, nil
	}

	eligible, rejected, conflict := s.candidatesFor(next, state.constraints[next])
	s.lastRejected = append(s.lastRejected, rejected...)
	if conflict != nil {
		return nil, conflict
	}
	var firstFailure *Conflict
	for index := 0; index < len(eligible); {
		// Equal-precedence identities are ambiguous at the point they would be
		// selected. Check each precedence group when reached, including fallback
		// groups after a higher candidate's transitive branch has failed.
		groupEnd := index + 1
		for groupEnd < len(eligible) && semver.ComparePrecedence(eligible[index].version, eligible[groupEnd].version) == 0 {
			groupEnd++
		}
		if groupEnd-index > 1 {
			return nil, &Conflict{Code: "AMBIGUOUS_RELEASE", Message: fmt.Sprintf("multiple distinct releases have equal SemVer precedence for %s", next.String()), InvolvedComponents: []string{next.componentID}}
		}
		candidate := eligible[index]
		branch := state.clone()
		branch.chosen[next] = candidate
		resolved, failure := s.search(branch, environment)
		if failure == nil {
			return resolved, nil
		}
		if firstFailure == nil {
			firstFailure = failure
		}
		if failure.Code == "RESOLUTION_LIMIT" {
			return nil, failure
		}
		index = groupEnd
	}
	if len(eligible) == 0 {
		return nil, &Conflict{Code: "NO_COMPATIBLE_RELEASE", Message: fmt.Sprintf("no compatible active release satisfies all constraints for %s", next.String()), InvolvedComponents: []string{next.componentID}}
	}
	// All candidate branches failed; retain the most deterministic failure from
	// the highest-precedence candidate rather than masking it with map order.
	// The caller still receives no Graph/Manifest for this failed resolution.
	if firstFailure != nil {
		return nil, firstFailure
	}
	return nil, &Conflict{Code: "DEPENDENCY_CONFLICT", Message: fmt.Sprintf("all candidate releases for %s lead to unsatisfied requirements", next.String()), InvolvedComponents: []string{next.componentID}}
}

// candidatesFor applies release status, prerelease and every accumulated constraint before ordering candidates (REG-010/012/013).
func (s *solver) candidatesFor(k key, constraints []constraintSource) ([]candidate, []RejectedCandidate, *Conflict) {
	all := s.candidates[k]
	eligible := make([]candidate, 0, len(all))
	rejected := make([]RejectedCandidate, 0)
	if constraintIntersectionEmpty(constraints) {
		return nil, rejected, &Conflict{Code: "CONSTRAINT_INTERSECTION_EMPTY", Message: fmt.Sprintf("the accumulated version constraints have an empty intersection for %s", k.String()), InvolvedComponents: []string{k.componentID}}
	}
	for _, candidate := range all {
		r := candidate.release
		reason, detail := "", ""
		switch {
		case r.Status == "withdrawn":
			reason, detail = "WITHDRAWN", "withdrawn releases are ineligible for new resolutions (REG-013)"
		case candidate.version.HasPrerelease() && !s.allowPrerelease:
			reason, detail = "PRERELEASE_DISABLED", "Product Definition policy does not allow prerelease releases (REG-012)"
		case !satisfiesAll(candidate.version, constraints):
			reason, detail = "CONSTRAINT_MISMATCH", "release does not satisfy every accumulated version constraint"
		}
		if reason != "" {
			rejected = append(rejected, RejectedCandidate{ComponentID: r.ComponentID, Variant: r.Variant, Version: r.Version, ReasonCode: reason, Detail: detail})
			continue
		}
		eligible = append(eligible, candidate)
	}

	return eligible, rejected, nil
}

// addConstraint appends rather than overwrites a requirement, preserving the full intersection for a component/variant key (REG-010).
func addConstraint(state *searchState, k key, text, from string) error {
	parsed, err := semver.ParseConstraint(text)
	if err != nil {
		return fmt.Errorf("invalid version constraint %q from %s: %w", text, from, err)
	}
	state.constraints[k] = append(state.constraints[k], constraintSource{text: text, from: from, parsed: parsed})
	return nil
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
	return &Conflict{Code: "DEPENDENCY_CYCLE", Message: "selected dependency graph contains a cycle", InvolvedComponents: uniqueSorted(involved), CyclePath: path}
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
func validateContracts(chosen map[key]candidate, environment []EnvironmentContract) *Conflict {
	type provider struct {
		component string
		variant   string
		version   semver.Version
		origin    string
	}
	providers := make(map[string][]provider)
	for _, k := range sortedChosenKeys(chosen) {
		release := chosen[k].release
		seen := make(map[string]bool)
		for _, declaration := range release.Contracts.Provides {
			if !validContractID(declaration.ContractID) {
				return &Conflict{Code: "INVALID_CONTRACT_ID", Message: fmt.Sprintf("%s/%s declares invalid contract ID %q", k.componentID, k.variant, declaration.ContractID), InvolvedComponents: []string{k.componentID}}
			}
			version, err := semver.Parse(declaration.Version)
			if err != nil {
				return &Conflict{Code: "INVALID_CONTRACT_VERSION", Message: fmt.Sprintf("%s/%s provides invalid contract version %q", k.componentID, k.variant, declaration.Version), InvolvedComponents: []string{k.componentID}}
			}
			id := declaration.ContractID + "\x00" + declaration.Version
			if seen[id] {
				continue
			}
			seen[id] = true
			providers[declaration.ContractID] = append(providers[declaration.ContractID], provider{component: k.componentID, variant: k.variant, version: version, origin: "component"})
		}
	}
	for index, declaration := range environment {
		if !validContractID(declaration.ContractID) {
			return &Conflict{Code: "INVALID_ENVIRONMENT_CONTRACT", Message: fmt.Sprintf("environment contract %d has invalid ID %q", index, declaration.ContractID)}
		}
		version, err := semver.Parse(declaration.Version)
		if err != nil {
			return &Conflict{Code: "INVALID_ENVIRONMENT_CONTRACT", Message: fmt.Sprintf("environment contract %q has invalid version: %v", declaration.ContractID, err)}
		}
		providers[declaration.ContractID] = append(providers[declaration.ContractID], provider{version: version, origin: fmt.Sprintf("environment:%d", index)})
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
				return &Conflict{Code: "INVALID_CONTRACT_ID", Message: fmt.Sprintf("%s/%s requires invalid contract ID %q", k.componentID, k.variant, requirement.ContractID), InvolvedComponents: []string{k.componentID}}
			}
			constraint, err := semver.ParseConstraint(requirement.VersionConstraint)
			if err != nil {
				return &Conflict{Code: "INVALID_CONTRACT_CONSTRAINT", Message: fmt.Sprintf("%s/%s requires %q with invalid constraint: %v", k.componentID, k.variant, requirement.ContractID, err), InvolvedComponents: []string{k.componentID}}
			}
			matches := make([]provider, 0)
			for _, candidate := range providers[requirement.ContractID] {
				if constraint.Allows(candidate.version) {
					matches = append(matches, candidate)
				}
			}
			if len(matches) == 0 {
				return &Conflict{Code: "CONTRACT_PROVIDER_MISSING", Message: fmt.Sprintf("%s/%s requires contract %q %s but no compatible provider exists", k.componentID, k.variant, requirement.ContractID, requirement.VersionConstraint), InvolvedComponents: []string{k.componentID}}
			}
			if len(matches) > 1 {
				involved := []string{k.componentID}
				for _, match := range matches {
					if match.component != "" {
						involved = append(involved, match.component)
					}
				}
				return &Conflict{Code: "CONTRACT_PROVIDER_AMBIGUOUS", Message: fmt.Sprintf("%s/%s requires contract %q %s but %d compatible providers exist", k.componentID, k.variant, requirement.ContractID, requirement.VersionConstraint, len(matches)), InvolvedComponents: uniqueSorted(involved)}
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
		SchemaVersion:        contractSchemaVersion,
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
				return nil, nil, &Conflict{Code: "INTERNAL_UNRESOLVED_DEPENDENCY", Message: "resolved graph is missing a selected dependency", InvolvedComponents: []string{r.ComponentID, dependency.ComponentID}}
			}
			dependencies = append(dependencies, ResolvedDependency{ComponentID: depKey.componentID, Variant: depKey.variant, Version: resolved.release.Version})
		}
		graph.Components = append(graph.Components, ResolvedComponent{
			ComponentID: r.ComponentID, Variant: r.Variant, Version: r.Version,
			ReleaseMetadataURI: r.MetadataURI, ReleaseMetadataSHA: r.MetadataSHA,
			SourceUnits:  append([]catalog.SourceUnit(nil), r.SourceUnits...),
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
	out := make([]RejectedCandidate, 0)
	for _, k := range sortedConstraintKeys(state.constraints) {
		_, rejected, _ := engine.candidatesFor(k, state.constraints[k])
		out = append(out, rejected...)
	}
	return out
}

func normalizeRejected(input []RejectedCandidate) []RejectedCandidate {
	seen := make(map[string]bool)
	out := make([]RejectedCandidate, 0, len(input))
	for _, item := range input {
		key := strings.Join([]string{item.ComponentID, item.Variant, item.Version, item.ReasonCode, item.Detail}, "\x00")
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
		return a.Detail < b.Detail
	})
	return out
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
