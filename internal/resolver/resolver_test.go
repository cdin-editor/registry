package resolver

import (
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func release(component, variant, version, status string, dependencies ...catalog.Dependency) catalog.Release {
	return catalog.Release{
		ComponentID: component, Variant: variant, Version: version, Status: status,
		SourceUnits: []catalog.SourceUnit{{ID: "source", RepositoryURI: "https://example.invalid/repo.git", Revision: testRevision}},
		Dependencies: dependencies,
	}
}
func catalogOf(id string, releases ...catalog.Release) *catalog.Catalog {
	return &catalog.Catalog{ID: id, Releases: releases}
}
func product(roots ...Requirement) ProductDefinition {
	return ProductDefinition{SchemaVersion: "1.0.0", Components: roots}
}

// TestREG008REG009 verifies transitive dependency closure and exactly one
// highest-compatible release per normalized component/variant key.
func TestREG008REG009SelectsHighestAndExpandsTransitiveDependencies(t *testing.T) {
	cat := catalogOf("test",
		release("app", "", "1.0.0", "active", catalog.Dependency{ComponentID:"engine", VersionConstraint:">=1.0.0 <3.0.0"}),
		release("engine", "default", "1.0.0", "active"),
		release("engine", "default", "2.0.0", "active", catalog.Dependency{ComponentID:"base", VersionConstraint:"=1.5.0"}),
		release("base", "", "1.5.0", "active"),
	)
	got:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph==nil||got.Report.Result!="success"{t.Fatalf("resolution failed: %+v",got.Report)}
	if len(got.Graph.Components)!=3{t.Fatalf("selected %d components, want 3",len(got.Graph.Components))}
	if got.Graph.ResolutionOrder[0].ComponentID!="base"||got.Graph.ResolutionOrder[2].ComponentID!="app"{t.Fatalf("order is not dependency-first: %+v",got.Graph.ResolutionOrder)}
	for _,item:=range got.Report.Selected{if item.ComponentID=="engine"&&item.Version!="2.0.0"{t.Fatalf("engine=%s, want 2.0.0",item.Version)}}
}

// TestREG010 confirms root and transitive constraints are intersected, never overwritten.
func TestREG010IntersectsRootAndDependencyConstraints(t *testing.T) {
	cat:=catalogOf("test",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=2.0.0 <3.0.0"}),
		release("engine","default","1.5.0","active"),release("engine","default","2.5.0","active"))
	got:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"},Requirement{ComponentID:"engine",VersionConstraint:"<2.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph!=nil||got.Report.Result!="failure"||len(got.Report.Conflicts)==0{t.Fatalf("expected structured failure, got %+v",got.Report)}
}

// TestREG012REG013 checks prerelease policy and withdrawn-release exclusion.
func TestREG012REG013AppliesPrereleaseAndWithdrawnPolicies(t *testing.T) {
	cat:=catalogOf("test",release("engine","default","2.0.0-rc.1","active"),release("engine","default","1.9.0","withdrawn"),release("engine","default","1.8.0","active"))
	def:=product(Requirement{ComponentID:"engine",VersionConstraint:">=1.0.0 <3.0.0"})
	got:=Resolve(def,[]*catalog.Catalog{cat})
	if got.Graph==nil||got.Graph.Components[0].Version!="1.8.0"{t.Fatalf("unexpected default policy: %+v",got.Report)}
	def.Policy.AllowPrerelease=true
	got=Resolve(def,[]*catalog.Catalog{cat})
	if got.Graph==nil||got.Graph.Components[0].Version!="2.0.0-rc.1"{t.Fatalf("prerelease policy not honored: %+v",got.Report)}
}

// TestREG011 rejects distinct metadata identities with equal SemVer precedence.
func TestREG011RejectsEqualPrecedenceMetadataIdentities(t *testing.T) {
	a:=release("engine","default","1.0.0+one","active");a.MetadataSHA=strings.Repeat("a",64)
	b:=release("engine","default","1.0.0+two","active");b.MetadataSHA=strings.Repeat("b",64)
	for _,catalogs:=range [][]*catalog.Catalog{{catalogOf("a",a),catalogOf("b",b)},{catalogOf("b",b),catalogOf("a",a)}}{
		got:=Resolve(product(Requirement{ComponentID:"engine",VersionConstraint:"=1.0.0"}),catalogs)
		if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="AMBIGUOUS_RELEASE"{t.Fatalf("expected ambiguity: %+v",got.Report)}
	}
}

// TestREG014 requires a cycle path in the failure report.
func TestREG014CycleReportsExactPath(t *testing.T) {
	cat:=catalogOf("test",
		release("alpha","default","1.0.0","active",catalog.Dependency{ComponentID:"beta",VersionConstraint:"=1.0.0"}),
		release("beta","default","1.0.0","active",catalog.Dependency{ComponentID:"alpha",VersionConstraint:"=1.0.0"}))
	got:=Resolve(product(Requirement{ComponentID:"alpha",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="DEPENDENCY_CYCLE"||len(got.Report.Conflicts[0].CyclePath)!=3{t.Fatalf("cycle path missing: %+v",got.Report)}
}

// TestREG015REG016 requires exactly one compatible provider from selected
// components or declared environment contracts, with ambiguity rejected.
func TestREG015REG016MatchesExactlyOneProvider(t *testing.T) {
	consumer:=release("consumer","default","1.0.0","active")
	consumer.Contracts.Requires=[]catalog.ContractRequirement{{ContractID:"org.example.api",VersionConstraint:">=1.0.0 <2.0.0"}}
	provider:=release("provider","default","1.0.0","active")
	provider.Contracts.Provides=[]catalog.ContractProvider{{ContractID:"org.example.api",Version:"1.2.0"}}
	got:=Resolve(product(Requirement{ComponentID:"consumer",VersionConstraint:"=1.0.0"},Requirement{ComponentID:"provider",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("test",consumer,provider)})
	if got.Graph==nil{t.Fatalf("compatible provider rejected: %+v",got.Report)}
	env:=product(Requirement{ComponentID:"consumer",VersionConstraint:"=1.0.0"})
	env.EnvironmentContracts=[]EnvironmentContract{{ContractID:"org.example.api",Version:"1.5.0"}}
	got=Resolve(env,[]*catalog.Catalog{catalogOf("test",consumer)})
	if got.Graph==nil{t.Fatalf("environment provider rejected: %+v",got.Report)}
	other:=release("other-provider","default","1.0.0","active")
	other.Contracts.Provides=[]catalog.ContractProvider{{ContractID:"org.example.api",Version:"1.4.0"}}
	got=Resolve(product(Requirement{ComponentID:"consumer",VersionConstraint:"=1.0.0"},Requirement{ComponentID:"provider",VersionConstraint:"=1.0.0"},Requirement{ComponentID:"other-provider",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("test",consumer,provider,other)})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="CONTRACT_PROVIDER_AMBIGUOUS"{t.Fatalf("expected provider ambiguity: %+v",got.Report)}
}

// TestREG011 proves catalog order cannot change the dependency-first result.
func TestREG011IsIndependentOfCatalogOrder(t *testing.T) {
	app:=release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"zlib",VersionConstraint:">=1.0.0"},catalog.Dependency{ComponentID:"base",VersionConstraint:">=1.0.0"})
	zlib:=release("zlib","default","1.2.0","active");base:=release("base","default","1.2.0","active")
	a:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("z",zlib),catalogOf("a",app,base)})
	b:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("a",base,app),catalogOf("z",zlib)})
	if a.Graph==nil||b.Graph==nil{t.Fatalf("resolution failed: %+v / %+v",a.Report,b.Report)}
	if len(a.Graph.ResolutionOrder)!=len(b.Graph.ResolutionOrder){t.Fatal("order lengths differ")}
	for i:=range a.Graph.ResolutionOrder{if a.Graph.ResolutionOrder[i]!=b.Graph.ResolutionOrder[i]{t.Fatalf("catalog order changed result: %+v / %+v",a.Graph.ResolutionOrder,b.Graph.ResolutionOrder)}}
}

// TestREG008BacktracksAcrossTransitiveConstraintConflicts guards against a
// greedy resolver rejecting a valid graph when a lower release is compatible.
func TestREG008BacktracksAcrossTransitiveConstraintConflicts(t *testing.T) {
	cat:=catalogOf("test",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=1.0.0 <3.0.0"}),
		release("engine","default","2.0.0","active",catalog.Dependency{ComponentID:"lib",VersionConstraint:">=2.0.0"}),
		release("engine","default","1.0.0","active",catalog.Dependency{ComponentID:"lib",VersionConstraint:"<2.0.0"}),
		release("lib","default","1.5.0","active"))
	got:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph==nil{t.Fatalf("lower candidate should resolve: %+v",got.Report)}
	for _,x:=range got.Report.Selected{if x.ComponentID=="engine"&&x.Version!="1.0.0"{t.Fatalf("engine=%s, want 1.0.0",x.Version)}}
}

// TestREG003REG004 validates stable IDs and default normalization at runtime.
func TestREG003REG004NormalizesDefaultAndRejectsUnstableIDs(t *testing.T) {
	cat:=catalogOf("test",release("engine","","1.0.0","active"))
	got:=Resolve(product(Requirement{ComponentID:"engine",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph==nil||got.Graph.Components[0].Variant!="default"{t.Fatalf("variant not defaulted: %+v",got.Report)}
	got=Resolve(product(Requirement{ComponentID:"Engine",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="INVALID_COMPONENT_ID"{t.Fatalf("invalid id accepted: %+v",got.Report)}
}

// TestREG010ReportsEmptyConstraintIntersection rejects impossible intervals
// explicitly rather than disguising them as a missing candidate.
func TestREG010ReportsEmptyConstraintIntersection(t *testing.T) {
	got:=Resolve(product(Requirement{ComponentID:"engine",VersionConstraint:">=2.0.0"},Requirement{ComponentID:"engine",VersionConstraint:"<2.0.0"}),[]*catalog.Catalog{catalogOf("test",release("engine","default","1.0.0","active"))})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="CONSTRAINT_INTERSECTION_EMPTY"{t.Fatalf("expected empty intersection: %+v",got.Report)}
}

// TestREG016ReportsMissingContractProvider checks the structured missing-provider path.
func TestREG016ReportsMissingContractProvider(t *testing.T) {
	consumer:=release("consumer","default","1.0.0","active")
	consumer.Contracts.Requires=[]catalog.ContractRequirement{{ContractID:"org.example.api",VersionConstraint:">=1.0.0 <2.0.0"}}
	got:=Resolve(product(Requirement{ComponentID:"consumer",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{catalogOf("test",consumer)})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="CONTRACT_PROVIDER_MISSING"{t.Fatalf("expected missing provider: %+v",got.Report)}
}

// TestREG011RejectsFallbackTie also checks equal-precedence ambiguity after
// higher candidates fail through transitive dependencies.
func TestREG011RejectsFallbackTie(t *testing.T) {
	lowA:=release("engine","default","1.5.0","active");lowA.MetadataSHA=strings.Repeat("a",64)
	lowB:=release("engine","default","1.5.0+build","active");lowB.MetadataSHA=strings.Repeat("b",64)
	cat:=catalogOf("test",
		release("app","default","1.0.0","active",catalog.Dependency{ComponentID:"engine",VersionConstraint:">=1.0.0 <3.0.0"}),
		release("engine","default","2.0.0","active",catalog.Dependency{ComponentID:"missing",VersionConstraint:"=1.0.0"}),lowA,lowB)
	got:=Resolve(product(Requirement{ComponentID:"app",VersionConstraint:"=1.0.0"}),[]*catalog.Catalog{cat})
	if got.Graph!=nil||len(got.Report.Conflicts)==0||got.Report.Conflicts[0].Code!="AMBIGUOUS_RELEASE"{t.Fatalf("fallback ambiguity was not rejected: %+v",got.Report)}
}
