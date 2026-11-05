package resolver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func release(component, variant, version, status string, dependencies ...catalog.Dependency) catalog.Release {
	return catalog.Release{
		ComponentID:  component,
		Variant:      variant,
		Version:      version,
		Status:       status,
		SourceUnits:  []catalog.SourceUnit{{ID: "source", RepositoryURI: "https://example.invalid/repo.git", Revision: testRevision}},
		Dependencies: dependencies,
	}
}

func catalogOf(id string, releases ...catalog.Release) *catalog.Catalog {
	return &catalog.Catalog{ID: id, Releases: releases}
}

func product(roots ...Requirement) ProductDefinition {
	return ProductDefinition{SchemaVersion: "1.0.0", Components: roots}
}

// TestREG008REG009SelectsHighestAndExpandsTransitiveDependencies proves that a
// successful resolution includes each selected release's complete dependency
// closure and selects one highest compatible version per normalized key.
func TestREG008REG009SelectsHighestAndExpandsTransitiveDependencies(t *testing.T) {
	cat := catalogOf("test",
		release("app", "", "1.0.0", "active", catalog.Dependency{ComponentID: "engine", VersionConstraint: ">=1.0.0 <3.0.0"}),
		release("engine", "default", "1.0.0", "active"),
		release("engine", "default", "2.0.0", "active", catalog.Dependency{ComponentID: "base", VersionConstraint: "=1.5.0"}),
		release("base", "", "1.5.0", "active"),
	)
	result := Resolve(product(Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if result.Report.Result != "success" || result.Graph == nil {
		t.Fatalf("resolution failed: %+v", result.Report.Conflicts)
	}
	if len(result.Graph.Components) != 3 {
		t.Fatalf("selected %d components, want app+engine+base", len(result.Graph.Components))
	}
	if result.Graph.ResolutionOrder[0].ComponentID != "base" || result.Graph.ResolutionOrder[2].ComponentID != "app" {
		t.Fatalf("resolution order is not dependency-first: %+v", result.Graph.ResolutionOrder)
	}
	for _, item := range result.Report.Selected {
		if item.ComponentID == "engine" && item.Version != "2.0.0" {
			t.Fatalf("engine version = %s, want 2.0.0", item.Version)
		}
	}
}

// TestREG010IntersectsRootAndDependencyConstraints ensures all constraints for
// one component/variant are accumulated; a later dependency cannot silently
// overwrite a constraint contributed by the Product Definition.
func TestREG010IntersectsRootAndDependencyConstraints(t *testing.T) {
	cat := catalogOf("test",
		release("app", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "engine", VersionConstraint: ">=2.0.0 <3.0.0"}),
		release("engine", "default", "1.5.0", "active"),
		release("engine", "default", "2.5.0", "active"),
	)
	result := Resolve(product(
		Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"},
		Requirement{ComponentID: "engine", VersionConstraint: "<2.0.0"},
	), []*catalog.Catalog{cat})
	if result.Graph != nil || result.Report.Result != "failure" {
		t.Fatal("expected a failed resolution with no graph")
	}
	if len(result.Report.Conflicts) == 0 {
		t.Fatal("expected a structured conflict")
	}
}

// TestREG012REG013AppliesPrereleaseAndWithdrawnPolicies ensures prereleases are
// excluded unless enabled and withdrawn candidates are never newly selected.
func TestREG012REG013AppliesPrereleaseAndWithdrawnPolicies(t *testing.T) {
	cat := catalogOf("test",
		release("engine", "default", "2.0.0-rc.1", "active"),
		release("engine", "default", "1.9.0", "withdrawn"),
		release("engine", "default", "1.8.0", "active"),
	)
	result := Resolve(product(Requirement{ComponentID: "engine", VersionConstraint: ">=1.0.0 <3.0.0"}), []*catalog.Catalog{cat})
	if result.Graph == nil || len(result.Graph.Components) != 1 || result.Graph.Components[0].Version != "1.8.0" {
		t.Fatalf("unexpected default-policy result: %+v", result.Report)
	}
	def := product(Requirement{ComponentID: "engine", VersionConstraint: ">=1.0.0 <3.0.0"})
	def.Policy.AllowPrerelease = true
	withPre := Resolve(def, []*catalog.Catalog{cat})
	if withPre.Graph == nil || withPre.Graph.Components[0].Version != "2.0.0-rc.1" {
		t.Fatalf("prerelease policy not honored: %+v", withPre.Report)
	}
}

// TestREG011RejectsEqualPrecedenceMetadataIdentities proves build metadata does
// not create an ordering tie-breaker: different release identities with equal
// SemVer precedence must fail rather than depend on catalog order.
func TestREG011RejectsEqualPrecedenceMetadataIdentities(t *testing.T) {
	a := release("engine", "default", "1.0.0+one", "active")
	a.MetadataSHA = strings.Repeat("a", 64)
	b := release("engine", "default", "1.0.0+two", "active")
	b.MetadataSHA = strings.Repeat("b", 64)
	for _, catalogs := range [][]*catalog.Catalog{{catalogOf("a", a), catalogOf("b", b)}, {catalogOf("b", b), catalogOf("a", a)}} {
		result := Resolve(product(Requirement{ComponentID: "engine", VersionConstraint: "=1.0.0"}), catalogs)
		if result.Graph != nil || len(result.Report.Conflicts) == 0 || result.Report.Conflicts[0].Code != "AMBIGUOUS_RELEASE" {
			t.Fatalf("expected deterministic ambiguity failure: %+v", result.Report)
		}
	}
}

// TestREG014CycleReportsExactPath verifies cycle detection includes the cycle
// path and no graph is emitted on failure.
func TestREG014CycleReportsExactPath(t *testing.T) {
	cat := catalogOf("test",
		release("alpha", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "beta", VersionConstraint: "=1.0.0"}),
		release("beta", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "alpha", VersionConstraint: "=1.0.0"}),
	)
	result := Resolve(product(Requirement{ComponentID: "alpha", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if result.Graph != nil || len(result.Report.Conflicts) == 0 {
		t.Fatalf("expected cycle failure: %+v", result.Report)
	}
	if result.Report.Conflicts[0].Code != "DEPENDENCY_CYCLE" || len(result.Report.Conflicts[0].CyclePath) != 3 {
		t.Fatalf("cycle path missing: %+v", result.Report.Conflicts[0])
	}
}

// TestREG015REG016MatchesExactlyOneProvider verifies selected component and
// target-environment providers are treated equally, while ambiguity is fatal.
func TestREG015REG016MatchesExactlyOneProvider(t *testing.T) {
	consumer := release("consumer", "default", "1.0.0", "active")
	consumer.Contracts.Requires = []catalog.ContractRequirement{{ContractID: "org.example.api", VersionConstraint: ">=1.0.0 <2.0.0"}}
	provider := release("provider", "default", "1.0.0", "active")
	provider.Contracts.Provides = []catalog.ContractProvider{{ContractID: "org.example.api", Version: "1.2.0"}}
	result := Resolve(product(Requirement{ComponentID: "consumer", VersionConstraint: "=1.0.0"}, Requirement{ComponentID: "provider", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{catalogOf("test", consumer, provider)})
	if result.Graph == nil {
		t.Fatalf("compatible provider should satisfy requirement: %+v", result.Report)
	}

	env := product(Requirement{ComponentID: "consumer", VersionConstraint: "=1.0.0"})
	env.EnvironmentContracts = []EnvironmentContract{{ContractID: "org.example.api", Version: "1.5.0"}}
	result = Resolve(env, []*catalog.Catalog{catalogOf("test", consumer)})
	if result.Graph == nil {
		t.Fatalf("environment provider should satisfy requirement: %+v", result.Report)
	}

	ambiguous := release("other-provider", "default", "1.0.0", "active")
	ambiguous.Contracts.Provides = []catalog.ContractProvider{{ContractID: "org.example.api", Version: "1.4.0"}}
	result = Resolve(product(Requirement{ComponentID: "consumer", VersionConstraint: "=1.0.0"}, Requirement{ComponentID: "provider", VersionConstraint: "=1.0.0"}, Requirement{ComponentID: "other-provider", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{catalogOf("test", consumer, provider, ambiguous)})
	if result.Graph != nil || len(result.Report.Conflicts) == 0 || result.Report.Conflicts[0].Code != "CONTRACT_PROVIDER_AMBIGUOUS" {
		t.Fatalf("expected ambiguous provider failure: %+v", result.Report)
	}
}

// TestREG011IsIndependentOfCatalogOrder confirms catalog permutation cannot
// alter the selected graph or deterministic dependency-first output ordering.
func TestREG011IsIndependentOfCatalogOrder(t *testing.T) {
	app := release("app", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "zlib", VersionConstraint: ">=1.0.0"}, catalog.Dependency{ComponentID: "base", VersionConstraint: ">=1.0.0"})
	zlib := release("zlib", "default", "1.2.0", "active")
	base := release("base", "default", "1.2.0", "active")
	first := Resolve(product(Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{catalogOf("z", zlib), catalogOf("a", app, base)})
	second := Resolve(product(Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{catalogOf("a", base, app), catalogOf("z", zlib)})
	if first.Graph == nil || second.Graph == nil {
		t.Fatalf("resolution failed: %+v / %+v", first.Report, second.Report)
	}
	if len(first.Graph.ResolutionOrder) != len(second.Graph.ResolutionOrder) {
		t.Fatal("orders have different lengths")
	}
	for i := range first.Graph.ResolutionOrder {
		if first.Graph.ResolutionOrder[i] != second.Graph.ResolutionOrder[i] {
			t.Fatalf("catalog ordering changed graph order: %+v vs %+v", first.Graph.ResolutionOrder, second.Graph.ResolutionOrder)
		}
	}
}

// TestREG011DuplicateReleaseProvenanceIsIndependentOfCatalogOrder verifies
// identical release bytes mirrored under different metadata URIs always produce
// the same externally visible URI in the Graph and eventual Manifest.
func TestREG011DuplicateReleaseProvenanceIsIndependentOfCatalogOrder(t *testing.T) {
	fromZ := release("engine", "default", "1.0.0", "active")
	fromZ.MetadataSHA = strings.Repeat("a", 64)
	fromZ.MetadataURI = "https://z.example.invalid/releases/engine-1.0.0.json"
	fromA := fromZ
	fromA.MetadataURI = "https://a.example.invalid/releases/engine-1.0.0.json"

	definition := product(Requirement{ComponentID: "engine", VersionConstraint: "=1.0.0"})
	first := Resolve(definition, []*catalog.Catalog{
		catalogOf("z-catalog", fromZ),
		catalogOf("a-catalog", fromA),
	})
	second := Resolve(definition, []*catalog.Catalog{
		catalogOf("a-catalog", fromA),
		catalogOf("z-catalog", fromZ),
	})
	if first.Graph == nil || second.Graph == nil {
		t.Fatalf("identical mirrored releases should resolve: first=%+v second=%+v", first.Report, second.Report)
	}
	const stableURI = "https://a.example.invalid/releases/engine-1.0.0.json"
	for name, result := range map[string]Result{"forward": first, "reverse": second} {
		if got := result.Graph.Components[0].ReleaseMetadataURI; got != stableURI {
			t.Errorf("%s catalog order selected metadata URI %q, want stable URI %q", name, got, stableURI)
		}
	}
}

// TestREG008BacktracksAcrossTransitiveConstraintConflicts prevents a greedy
// resolver from rejecting a valid graph when the highest direct candidate has
// an incompatible transitive dependency but a lower candidate is fully valid.
func TestREG008BacktracksAcrossTransitiveConstraintConflicts(t *testing.T) {
	cat := catalogOf("test",
		release("app", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "engine", VersionConstraint: ">=1.0.0 <3.0.0"}),
		release("engine", "default", "2.0.0", "active", catalog.Dependency{ComponentID: "lib", VersionConstraint: ">=2.0.0"}),
		release("engine", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "lib", VersionConstraint: "<2.0.0"}),
		release("lib", "default", "1.5.0", "active"),
	)
	result := Resolve(product(Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if result.Graph == nil {
		t.Fatalf("valid lower-candidate graph should resolve: %+v", result.Report)
	}
	for _, selected := range result.Report.Selected {
		if selected.ComponentID == "engine" && selected.Version != "1.0.0" {
			t.Fatalf("selected engine %s, want fallback 1.0.0", selected.Version)
		}
	}
}

// TestREG003REG004NormalizesDefaultAndRejectsUnstableIDs verifies identity
// validation occurs at the engine boundary and omitted variants become default.
func TestREG003REG004NormalizesDefaultAndRejectsUnstableIDs(t *testing.T) {
	cat := catalogOf("test", release("engine", "", "1.0.0", "active"))
	result := Resolve(product(Requirement{ComponentID: "engine", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if result.Graph == nil || result.Graph.Components[0].Variant != "default" {
		t.Fatalf("omitted variant was not normalized: %+v", result.Report)
	}
	invalid := Resolve(product(Requirement{ComponentID: "Engine", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if invalid.Graph != nil || len(invalid.Report.Conflicts) == 0 || invalid.Report.Conflicts[0].Code != "INVALID_COMPONENT_ID" {
		t.Fatalf("invalid component id was accepted: %+v", invalid.Report)
	}
}

// TestREG010ReportsEmptyConstraintIntersection distinguishes an impossible
// interval from a merely absent release candidate.
func TestREG010ReportsEmptyConstraintIntersection(t *testing.T) {
	cat := catalogOf("test", release("engine", "default", "1.0.0", "active"))
	result := Resolve(product(
		Requirement{ComponentID: "engine", VersionConstraint: ">=2.0.0"},
		Requirement{ComponentID: "engine", VersionConstraint: "<2.0.0"},
	), []*catalog.Catalog{cat})
	if result.Graph != nil || len(result.Report.Conflicts) == 0 || result.Report.Conflicts[0].Code != "CONSTRAINT_INTERSECTION_EMPTY" {
		t.Fatalf("expected empty-intersection conflict, got %+v", result.Report)
	}
}

// TestREG016ReportsMissingContractProvider ensures missing providers produce a
// stable structured conflict rather than being inferred from contract names.
func TestREG016ReportsMissingContractProvider(t *testing.T) {
	consumer := release("consumer", "default", "1.0.0", "active")
	consumer.Contracts.Requires = []catalog.ContractRequirement{{ContractID: "org.example.api", VersionConstraint: ">=1.0.0 <2.0.0"}}
	result := Resolve(product(Requirement{ComponentID: "consumer", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{catalogOf("test", consumer)})
	if result.Graph != nil || len(result.Report.Conflicts) == 0 || result.Report.Conflicts[0].Code != "CONTRACT_PROVIDER_MISSING" {
		t.Fatalf("expected missing-provider conflict, got %+v", result.Report)
	}
}

// TestREG011RejectsFallbackTie ensures a lower precedence tie is detected when
// a higher release branch fails and the resolver would otherwise fall back.
func TestREG011RejectsFallbackTie(t *testing.T) {
	lowA := release("engine", "default", "1.5.0", "active")
	lowA.MetadataSHA = strings.Repeat("a", 64)
	lowB := release("engine", "default", "1.5.0+build", "active")
	lowB.MetadataSHA = strings.Repeat("b", 64)
	cat := catalogOf("test",
		release("app", "default", "1.0.0", "active", catalog.Dependency{ComponentID: "engine", VersionConstraint: ">=1.0.0 <3.0.0"}),
		release("engine", "default", "2.0.0", "active", catalog.Dependency{ComponentID: "missing", VersionConstraint: "=1.0.0"}),
		lowA,
		lowB,
	)
	result := Resolve(product(Requirement{ComponentID: "app", VersionConstraint: "=1.0.0"}), []*catalog.Catalog{cat})
	if result.Graph != nil || len(result.Report.Conflicts) == 0 || result.Report.Conflicts[0].Code != "AMBIGUOUS_RELEASE" {
		t.Fatalf("fallback tie was not rejected: %+v", result.Report)
	}
}


func countApplied(report Report, id string) int { n:=0; for _,c:=range report.AppliedConstraints { if c.ComponentID==id { n++ } }; return n }
func hasRejected(report Report, id, version string, reason RejectionReasonCode) bool {
	for _,c:=range report.RejectedCandidates { if c.ComponentID==id && c.Version==version && c.ReasonCode==reason { return true } }; return false
}

// REG-008: a diamond selects the shared node once and retains both edge constraints.
func TestREG008DiamondGraphEvidence(t *testing.T) {
	cat:=catalogOf("diamond",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"left",VersionConstraint:"=1.0.0"},catalog.Dependency{ComponentID:"right",VersionConstraint:"=1.0.0"}),
		release("left","default","1.0.0","active",catalog.Dependency{ComponentID:"shared",VersionConstraint:">=1.0.0 <2.0.0"}),
		release("right","default","1.0.0","active",catalog.Dependency{ComponentID:"shared",VersionConstraint:"=1.0.0"}),
		release("shared","default","1.0.0","active"))
	r:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if r.Graph==nil { t.Fatalf("diamond failed: %+v",r.Report) }
	n:=0; sources:=map[string]bool{}
	for _,c:=range r.Graph.Components { if c.ComponentID=="shared" { n++ } }
	for _,c:=range r.Report.AppliedConstraints { if c.ComponentID=="shared" { sources[c.Source.ComponentID]=true } }
	if n!=1 || countApplied(r.Report,"shared")!=2 || !sources["left"] || !sources["right"] { t.Fatalf("shared evidence incomplete: %+v",r.Report) }
}

// REG-010: repeated roots and duplicate dependency declarations remain auditable.
func TestREG010RepeatedRootsAndDependencies(t *testing.T) {
	cat:=catalogOf("dupes",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"lib",VersionConstraint:"=1.0.0"},catalog.Dependency{ComponentID:"lib",VersionConstraint:"=1.0.0"}),
		release("lib","default","1.0.0","active"))
	r:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:">=1.0.0"},Requirement{ComponentID:"app",VersionConstraint:"<2.0.0"}),[]*catalog.Catalog{cat})
	if r.Graph==nil || countApplied(r.Report,"app")!=2 || countApplied(r.Report,"lib")!=2 { t.Fatalf("duplicate constraint evidence lost: %+v",r.Report) }
}

// REG-010: conflicting constraints from two transitive paths retain both sources.
func TestREG010ConflictingDiamondEvidence(t *testing.T) {
	cat:=catalogOf("conflict",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"left",VersionConstraint:"=1.0.0"},catalog.Dependency{ComponentID:"right",VersionConstraint:"=1.0.0"}),
		release("left","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=2.0.0"}),
		release("right","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:"<2.0.0"}),
		release("engine","default","2.0.0","active"))
	r:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if r.Graph!=nil || len(r.Report.Conflicts)!=1 || r.Report.Conflicts[0].Code!=ConflictConstraintIntersectionEmpty { t.Fatalf("expected empty intersection: %+v",r.Report) }
	sources:=map[string]string{}
	for _,c:=range r.Report.AppliedConstraints { if c.ComponentID=="engine" { sources[c.Source.ComponentID]=c.VersionConstraint } }
	if sources["left"]!=">=2.0.0" || sources["right"]!="<2.0.0" { t.Fatalf("missing path provenance: %v",sources) }
}

// REG-011: a failed higher candidate is reported when a lower candidate succeeds.
func TestREG011SuccessfulFallbackReportsBranchRejection(t *testing.T) {
	cat:=catalogOf("fallback",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=1.0.0 <3.0.0"}),
		release("engine","default","2.0.0","active",catalog.Dependency{ComponentID:"missing",VersionConstraint:"=1.0.0"}),
		release("engine","default","1.0.0","active"))
	r:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	found:=false
	for _,c:=range r.Report.RejectedCandidates { if c.ComponentID=="engine" && c.Version=="2.0.0" && c.ReasonCode==RejectionBranchConflict && c.Detail==string(ConflictNoCompatibleRelease) && c.CatalogID=="fallback" { found=true } }
	if r.Graph==nil || !found { t.Fatalf("fallback reason/provenance missing: %+v",r.Report) }
}

// REG-025: failed sibling-branch evidence must not leak into the surfaced failure.
func TestREG025SiblingBranchEvidenceIsolation(t *testing.T) {
	cat:=catalogOf("isolation",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=1.0.0 <3.0.0"}),
		release("engine","default","2.0.0","active",catalog.Dependency{ComponentID:"alpha",VersionConstraint:"=1.0.0"}),
		release("engine","default","1.0.0","active",catalog.Dependency{ComponentID:"beta",VersionConstraint:"=1.0.0"}),
		release("alpha","default","1.0.0","withdrawn"),release("beta","default","1.0.0","withdrawn"))
	r:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if r.Graph!=nil || len(r.Report.Conflicts)!=1 || r.Report.Conflicts[0].Code!=ConflictNoCompatibleRelease { t.Fatalf("expected resolution failure: %+v",r.Report) }
	if countApplied(r.Report,"alpha")!=1 || countApplied(r.Report,"beta")!=0 || !hasRejected(r.Report,"alpha","1.0.0",RejectionWithdrawn) || hasRejected(r.Report,"beta","1.0.0",RejectionWithdrawn) || !hasRejected(r.Report,"engine","2.0.0",RejectionBranchConflict) { t.Fatalf("sibling branch leaked or surfaced evidence missing: %+v",r.Report) }
}

// REG-011: candidate and dependency input permutations cannot change output bytes.
func TestREG011CandidateDependencyPermutation(t *testing.T) {
	a:=release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"left",VersionConstraint:">=1.0.0"},catalog.Dependency{ComponentID:"right",VersionConstraint:">=1.0.0"})
	b:=release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"right",VersionConstraint:">=1.0.0"},catalog.Dependency{ComponentID:"left",VersionConstraint:">=1.0.0"})
	left,right:=release("left","default","1.0.0","active"),release("right","default","1.0.0","active")
	first:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("perm",a,left,right)})
	second:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("perm",right,left,b)})
	if first.Graph==nil || second.Graph==nil { t.Fatalf("permutation failed: %+v / %+v",first.Report,second.Report) }
	fa,_:=json.Marshal(first.Report); sa,_:=json.Marshal(second.Report)
	fg,_:=json.Marshal(first.Graph); sg,_:=json.Marshal(second.Graph)
	if string(fa)!=string(sa) || string(fg)!=string(sg) { t.Fatal("permutation changed report or graph") }
}

// REG-011: the configured state ceiling has a stable structured failure report.
func TestREG011SearchLimitReportStable(t *testing.T) {
	cat:=catalogOf("limit",release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"base",VersionConstraint:"=1.0.0"}),release("base","default","1.0.0","active"))
	def:=product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"})
	a:=resolveWithMaxSearchStates(def,[]*catalog.Catalog{cat},1); b:=resolveWithMaxSearchStates(def,[]*catalog.Catalog{cat},1)
	if a.Graph!=nil || len(a.Report.Conflicts)!=1 || a.Report.Conflicts[0].Code!=ConflictResolutionLimit || len(a.Report.AppliedConstraints)!=1 { t.Fatalf("limit failure incomplete: %+v",a.Report) }
	aa,_:=json.Marshal(a.Report); bb,_:=json.Marshal(b.Report); if string(aa)!=string(bb) { t.Fatalf("limit report unstable: %s != %s",aa,bb) }
}

// REG-009: a missing required component fails without producing a graph.
func TestREG009MissingReleaseReportsNoCompatibleCandidate(t *testing.T) {
	r:=Resolve(product(Requirement{ComponentID:"missing",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("empty")})
	if r.Graph!=nil || len(r.Report.Conflicts)!=1 || r.Report.Conflicts[0].Code!=ConflictNoCompatibleRelease { t.Fatalf("missing release not reported: %+v",r.Report) }
}
