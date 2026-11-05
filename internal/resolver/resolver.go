// Package resolver implements the deterministic, consumer-independent selection
// engine for Registry Data Contracts 1.0.0.
//
// Spec traceability:
//   REG-003/004: stable identifiers and default-variant normalization.
//   REG-006/007: SemVer 2.0.0 and the restricted constraint grammar.
//   REG-008–014: dependency closure, constraint intersection, candidate policy,
//   deterministic selection, cycle rejection and dependency-first ordering.
//   REG-015/016: exactly-one compatible contract provider.
//   Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#4-resolution-model
//   Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#5-resolution-semantics
//   Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/resolver.md
//
// The package intentionally performs no I/O. Catalog loading, Git retrieval,
// workspace publication and CLI orchestration are separate boundaries. That
// separation prevents resolution from executing untrusted metadata or leaving
// partially prepared output when a candidate graph is rejected.
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

const schemaVersion = "1.0.0"
const defaultSearchLimit = 100000

// ProductDefinition is the validated public input projected into resolver data.
// Schema validation remains the CLI/composition root's responsibility (REG-005).
type ProductDefinition struct {
	SchemaVersion string `json:"schema_version"`
	Components []Requirement `json:"components"`
	EnvironmentContracts []EnvironmentContract `json:"environment_contracts,omitempty"`
	Policy Policy `json:"policy,omitempty"`
}

// Requirement is one root component/variant requirement. Variant is normalized
// to "default" before keys or output nodes are built (REG-004).
type Requirement struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant,omitempty"`
	VersionConstraint string `json:"version_constraint"`
}

// EnvironmentContract declares one provider supplied by the target environment.
// IDs are opaque strings; Registry never infers consumer identity (REG-002/015).
type EnvironmentContract struct {
	ContractID string `json:"contract_id"`
	Version string `json:"version"`
}

// Policy contains only the selection policy frozen in contract 1.0.0.
type Policy struct { AllowPrerelease bool `json:"allow_prerelease"` }

// Node is a normalized component/variant identity and its selected version.
type Node struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant"`
	Version string `json:"version"`
}

// SelectedCandidate is the public report projection of a chosen release.
type SelectedCandidate struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant"`
	Version string `json:"version"`
}

// RejectedCandidate explains why one release was excluded from selection.
type RejectedCandidate struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant"`
	Version string `json:"version"`
	ReasonCode string `json:"reason_code"`
	Detail string `json:"detail,omitempty"`
}

// Conflict is a machine-readable resolution failure. CyclePath identifies the
// exact dependency cycle for REG-014; involved component IDs are sorted.
type Conflict struct {
	Code string `json:"code"`
	Message string `json:"message"`
	InvolvedComponents []string `json:"involved_components,omitempty"`
	CyclePath []Node `json:"cycle_path,omitempty"`
}

// Warning is a stable non-fatal diagnostic.
type Warning struct {
	Code string `json:"code"`
	Message string `json:"message"`
}

// Report follows the Resolution Report v1 schema. Failure reports never pair
// with a successful graph, satisfying the no-success-on-failure rule (REG-025).
type Report struct {
	SchemaVersion string `json:"schema_version"`
	Result string `json:"result"`
	Selected []SelectedCandidate `json:"selected"`
	RejectedCandidates []RejectedCandidate `json:"rejected_candidates"`
	Conflicts []Conflict `json:"conflicts"`
	Warnings []Warning `json:"warnings"`
}

// CatalogReference records each catalog identity used in this resolution.
type CatalogReference struct { CatalogID string `json:"catalog_id"` }

// ResolvedDependency is a concrete edge after version selection.
type ResolvedDependency struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant"`
	Version string `json:"version"`
}

// ResolvedComponent is the selected release projection consumed by Collector
// and Manifest generation. No source is fetched or executed here.
type ResolvedComponent struct {
	ComponentID string `json:"component_id"`
	Variant string `json:"variant"`
	Version string `json:"version"`
	ReleaseMetadataURI string `json:"release_metadata_uri,omitempty"`
	ReleaseMetadataSHA string `json:"release_metadata_sha256,omitempty"`
	SourceUnits []catalog.SourceUnit `json:"source_units"`
	Dependencies []ResolvedDependency `json:"dependencies"`
	Contracts catalog.Contracts `json:"contracts"`
}

// Graph is the complete immutable selection result; its order and set-like
// collections are deterministic, and it contains no machine-specific paths.
type Graph struct {
	SchemaVersion string `json:"schema_version"`
	Catalogs []CatalogReference `json:"catalogs"`
	Roots []Requirement `json:"roots"`
	EnvironmentContracts []EnvironmentContract `json:"environment_contracts"`
	Components []ResolvedComponent `json:"components"`
	ResolutionOrder []Node `json:"resolution_order"`
}

// Result contains a report on every call and a graph only on success.
type Result struct { Graph *Graph; Report Report }

type componentKey struct { id, variant string }
func (k componentKey) label() string { return k.id + "/" + k.variant }

type candidate struct {
	release catalog.Release
	version semver.Version
	identity string
}

type constraint struct { raw, origin string; parsed semver.Constraint }
type state struct {
	constraints map[componentKey][]constraint
	chosen map[componentKey]candidate
	expanded map[componentKey]bool
}
func newState() *state { return &state{constraints:map[componentKey][]constraint{}, chosen:map[componentKey]candidate{}, expanded:map[componentKey]bool{}} }
func (s *state) clone() *state {
	n:=newState()
	for k,v:=range s.constraints { n.constraints[k]=append([]constraint(nil),v...) }
	for k,v:=range s.chosen { n.chosen[k]=v }
	for k,v:=range s.expanded { n.expanded[k]=v }
	return n
}
type solver struct {
	candidates map[componentKey][]candidate
	allowPrerelease bool
	states, limit int
	rejected []RejectedCandidate
}

// Resolve computes a deterministic closure using bounded backtracking.
// Backtracking is required: a highest-version choice can introduce transitive
// constraints that fail, while a lower release may satisfy the complete graph.
// Expected compatibility failures are returned as structured reports; errors
// are reserved for programmer/configuration errors at outer boundaries.
func Resolve(def ProductDefinition, catalogs []*catalog.Catalog) Result {
	out:=Result{Report:Report{SchemaVersion:schemaVersion,Result:"failure",Selected:[]SelectedCandidate{},RejectedCandidates:[]RejectedCandidate{},Conflicts:[]Conflict{},Warnings:[]Warning{}}}
	if len(def.Components)==0 { out.Report.Conflicts=append(out.Report.Conflicts,Conflict{Code:"NO_ROOT_REQUIREMENTS",Message:"Product Definition contains no root component requirements"}); return out }
	index, refs, conflict:=indexCatalogs(catalogs)
	if conflict!=nil { out.Report.Conflicts=append(out.Report.Conflicts,*conflict); return out }
	s:=&solver{candidates:index,allowPrerelease:def.Policy.AllowPrerelease,limit:defaultSearchLimit}
	work:=newState()
	roots:=make([]Requirement,0,len(def.Components))
	for _,root:=range def.Components {
		root.Variant=variantOrDefault(root.Variant)
		if !validID(root.ComponentID,128)||!validID(root.Variant,64) { out.Report.Conflicts=append(out.Report.Conflicts,Conflict{Code:"INVALID_COMPONENT_ID",Message:"invalid root component identity "+root.ComponentID+"/"+root.Variant,InvolvedComponents:[]string{root.ComponentID}}); return out }
		if err:=addConstraint(work,componentKey{root.ComponentID,root.Variant},root.VersionConstraint,"product root"); err!=nil { out.Report.Conflicts=append(out.Report.Conflicts,Conflict{Code:"INVALID_VERSION_CONSTRAINT",Message:err.Error(),InvolvedComponents:[]string{root.ComponentID}}); return out }
		roots=append(roots,root)
	}
	sortRequirements(roots)
	resolved,failure:=s.search(work,def.EnvironmentContracts)
	if failure!=nil { out.Report.Conflicts=append(out.Report.Conflicts,*failure); out.Report.RejectedCandidates=normalizeRejected(s.rejected); return out }
	graph,selected,failure:=makeGraph(resolved,refs,roots,def.EnvironmentContracts)
	if failure!=nil { out.Report.Conflicts=append(out.Report.Conflicts,*failure); return out }
	out.Graph=graph
	out.Report.Result="success"
	out.Report.Selected=selected
	out.Report.RejectedCandidates=normalizeRejected(rejectedFor(s,resolved))
	return out
}

// indexCatalogs validates runtime identity/version fields and makes catalog
// iteration order irrelevant before the solver sees any candidate (REG-003/011).
func indexCatalogs(catalogs []*catalog.Catalog) (map[componentKey][]candidate,[]CatalogReference,*Conflict) {
	index:=map[componentKey][]candidate{}
	ids:=map[string]bool{}
	refs:=[]CatalogReference{}
	for _,cat:=range catalogs {
		if cat==nil||strings.TrimSpace(cat.ID)=="" { return nil,nil,&Conflict{Code:"INVALID_CATALOG",Message:"catalog is nil or catalog_id is empty"} }
		if !ids[cat.ID] { ids[cat.ID]=true; refs=append(refs,CatalogReference{CatalogID:cat.ID}) }
		for _,release:=range cat.Releases {
			release.Variant=variantOrDefault(release.Variant)
			if !validID(release.ComponentID,128)||!validID(release.Variant,64) { return nil,nil,&Conflict{Code:"INVALID_RELEASE_IDENTITY",Message:"invalid release identity "+release.ComponentID+"/"+release.Variant,InvolvedComponents:[]string{release.ComponentID}} }
			version,err:=semver.Parse(release.Version)
			if err!=nil { return nil,nil,&Conflict{Code:"INVALID_RELEASE_VERSION",Message:fmt.Sprintf("invalid version %q: %v",release.Version,err),InvolvedComponents:[]string{release.ComponentID}} }
			if release.Status!="active"&&release.Status!="withdrawn" { return nil,nil,&Conflict{Code:"INVALID_RELEASE_STATUS",Message:"unsupported release status "+release.Status,InvolvedComponents:[]string{release.ComponentID}} }
			if len(release.SourceUnits)==0 { return nil,nil,&Conflict{Code:"MISSING_SOURCE_UNITS",Message:"release has no declared source units",InvolvedComponents:[]string{release.ComponentID}} }
			for _,source:=range release.SourceUnits {
				if !validID(source.ID,64)||source.RepositoryURI==""||!fullGitObjectID(source.Revision) { return nil,nil,&Conflict{Code:"INVALID_SOURCE_IDENTITY",Message:"release contains an invalid declared source unit",InvolvedComponents:[]string{release.ComponentID}} }
			}
			identity:=release.Version+"\x00"+strings.ToLower(release.MetadataSHA)
			if release.MetadataSHA=="" {
				data:=release.Raw
				if len(data)==0 { data,_=json.Marshal(release) }
				sum:=sha256.Sum256(data); identity=release.Version+"\x00"+hex.EncodeToString(sum[:])
			}
			k:=componentKey{release.ComponentID,release.Variant}
			duplicate:=false
			for _,prior:=range index[k] { if prior.identity==identity { duplicate=true; break } }
			if !duplicate { index[k]=append(index[k],candidate{release:release,version:version,identity:identity}) }
		}
	}
	sort.Slice(refs,func(i,j int)bool{return refs[i].CatalogID<refs[j].CatalogID})
	for k:=range index { sort.Slice(index[k],func(i,j int)bool {
		a,b:=index[k][i],index[k][j]
		if c:=semver.ComparePrecedence(a.version,b.version); c!=0 { return c>0 }
		if a.release.Version!=b.release.Version { return a.release.Version<b.release.Version }
		return a.identity<b.identity
	}) }
	return index,refs,nil
}

// search expands chosen releases before selecting the next unresolved key.
// Every branch receives cloned state, so dependencies from a rejected candidate
// cannot leak into another branch (REG-008/010/011).
func (s *solver) search(w *state,environment []EnvironmentContract)(*state,*Conflict) {
	s.states++
	if s.states>s.limit { return nil,&Conflict{Code:"RESOLUTION_LIMIT",Message:"resolution search exceeded its deterministic state limit"} }
	for _,k:=range chosenKeys(w.chosen) {
		if w.expanded[k] { continue }
		w.expanded[k]=true
		deps:=append([]catalog.Dependency(nil),w.chosen[k].release.Dependencies...)
		sort.Slice(deps,func(i,j int)bool { if deps[i].ComponentID!=deps[j].ComponentID{return deps[i].ComponentID<deps[j].ComponentID};return variantOrDefault(deps[i].Variant)<variantOrDefault(deps[j].Variant) })
		for _,dep:=range deps {
			v:=variantOrDefault(dep.Variant)
			if !validID(dep.ComponentID,128)||!validID(v,64) { return nil,&Conflict{Code:"INVALID_DEPENDENCY_ID",Message:"release declares invalid dependency identity",InvolvedComponents:[]string{w.chosen[k].release.ComponentID,dep.ComponentID}} }
			target:=componentKey{dep.ComponentID,v}
			if err:=addConstraint(w,target,dep.VersionConstraint,k.label()+"@"+w.chosen[k].release.Version); err!=nil { return nil,&Conflict{Code:"INVALID_DEPENDENCY_CONSTRAINT",Message:err.Error(),InvolvedComponents:[]string{k.id,dep.ComponentID}} }
			if selected,ok:=w.chosen[target];ok&&!satisfies(selected.version,w.constraints[target]) { return nil,&Conflict{Code:"CONSTRAINT_INTERSECTION_EMPTY",Message:"new dependency constraints exclude selected "+target.label(),InvolvedComponents:[]string{k.id,target.id}} }
		}
		return s.search(w,environment)
	}
	keys:=constraintKeys(w.constraints)
	var next componentKey
	found:=false
	for _,k:=range keys { if _,ok:=w.chosen[k];!ok { next=k;found=true;break } }
	if !found {
		if c:=cycleConflict(w.chosen);c!=nil{return nil,c}
		if c:=contractConflict(w.chosen,environment);c!=nil{return nil,c}
		return w,nil
	}
	eligible,rejected,conflict:=s.candidatesFor(next,w.constraints[next])
	s.rejected=append(s.rejected,rejected...)
	if conflict!=nil{return nil,conflict}
	if len(eligible)==0{return nil,&Conflict{Code:"NO_COMPATIBLE_RELEASE",Message:"no compatible active release satisfies all constraints for "+next.label(),InvolvedComponents:[]string{next.id}}}
	for i:=0;i<len(eligible); {
		end:=i+1
		for end<len(eligible)&&semver.ComparePrecedence(eligible[i].version,eligible[end].version)==0{end++}
		if end-i>1{return nil,&Conflict{Code:"AMBIGUOUS_RELEASE",Message:"distinct release identities have equal SemVer precedence for "+next.label(),InvolvedComponents:[]string{next.id}}}
		branch:=w.clone();branch.chosen[next]=eligible[i]
		answer,failure:=s.search(branch,environment)
		if failure==nil{return answer,nil}
		if failure.Code=="RESOLUTION_LIMIT"{return nil,failure}
		// Preserve deterministic failure priority: the first (highest-version)
		// branch is the most useful explanation if every candidate fails.
		i=end
	}
	return nil,&Conflict{Code:"DEPENDENCY_CONFLICT",Message:"all candidates for "+next.label()+" lead to unsatisfied requirements",InvolvedComponents:[]string{next.id}}
}

// candidatesFor applies withdrawal, prerelease and every accumulated constraint
// before candidate selection. Schema shape alone cannot prove these policies.
func (s *solver) candidatesFor(k componentKey,requirements []constraint)([]candidate,[]RejectedCandidate,*Conflict) {
	if emptyIntersection(requirements){return nil,nil,&Conflict{Code:"CONSTRAINT_INTERSECTION_EMPTY",Message:"version constraints have an empty intersection for "+k.label(),InvolvedComponents:[]string{k.id}}}
	eligible:=[]candidate{};rejected:=[]RejectedCandidate{}
	for _,c:=range s.candidates[k] {
		reason,detail:="",""
		switch {
		case c.release.Status=="withdrawn": reason,detail="WITHDRAWN","withdrawn releases cannot be selected for a new resolution (REG-013)"
		case c.version.HasPrerelease()&&!s.allowPrerelease: reason,detail="PRERELEASE_DISABLED","prerelease selection is disabled by Product Definition policy (REG-012)"
		case !satisfies(c.version,requirements): reason,detail="CONSTRAINT_MISMATCH","candidate does not satisfy every accumulated constraint"
		}
		if reason!="" { rejected=append(rejected,RejectedCandidate{ComponentID:c.release.ComponentID,Variant:c.release.Variant,Version:c.release.Version,ReasonCode:reason,Detail:detail}) } else { eligible=append(eligible,c) }
	}
	return eligible,rejected,nil
}

// addConstraint appends every requirement instead of overwriting earlier roots
// or dependency constraints (REG-010).
func addConstraint(w *state,k componentKey,raw,origin string)error{
	parsed,err:=semver.ParseConstraint(raw);if err!=nil{return fmt.Errorf("invalid version constraint %q from %s: %w",raw,origin,err)}
	w.constraints[k]=append(w.constraints[k],constraint{raw:raw,origin:origin,parsed:parsed})
	return nil
}
func satisfies(v semver.Version,all []constraint)bool{for _,c:=range all{if !c.parsed.Allows(v){return false}};return true}

// emptyIntersection detects impossible comparator bounds independently of the
// available catalog, so REG-010 failures are not misreported as missing releases.
func emptyIntersection(all []constraint)bool{
	type bound struct{v semver.Version;strict bool}
	var exact *semver.Version;var lower,upper *bound
	for _,source:=range all { for _,cmp:=range source.parsed.Comparators { switch cmp.Operator {
		case semver.Equal: x:=cmp.Version;if exact!=nil&&semver.ComparePrecedence(*exact,x)!=0{return true};exact=&x
		case semver.Greater,semver.GreaterEqual: x:=bound{cmp.Version,cmp.Operator==semver.Greater};if lower==nil||semver.ComparePrecedence(x.v,lower.v)>0||semver.ComparePrecedence(x.v,lower.v)==0&&x.strict{lower=&x}
		case semver.Less,semver.LessEqual: x:=bound{cmp.Version,cmp.Operator==semver.Less};if upper==nil||semver.ComparePrecedence(x.v,upper.v)<0||semver.ComparePrecedence(x.v,upper.v)==0&&x.strict{upper=&x}
	} } }
	if exact!=nil{return !satisfies(*exact,all)}
	if lower!=nil&&upper!=nil { c:=semver.ComparePrecedence(lower.v,upper.v);return c>0||c==0&&(lower.strict||upper.strict) }
	return false
}

// cycleConflict performs deterministic DFS and includes the exact cycle path
// instead of merely reporting that a cycle exists (REG-014).
func cycleConflict(chosen map[componentKey]candidate)*Conflict{
	color:=map[componentKey]uint8{};stack:=[]componentKey{};var cycle []componentKey
	var visit func(componentKey)bool
	visit=func(k componentKey)bool{
		color[k]=1;stack=append(stack,k)
		for _,d:=range dependencies(chosen[k].release){
			n:=componentKey{d.ComponentID,variantOrDefault(d.Variant)};if _,ok:=chosen[n];!ok{continue}
			if color[n]==1{start:=0;for i,x:=range stack{if x==n{start=i;break}};cycle=append(append([]componentKey{},stack[start:]...),n);return true}
			if color[n]==0&&visit(n){return true}
		}
		stack=stack[:len(stack)-1];color[k]=2;return false
	}
	for _,k:=range chosenKeys(chosen){if color[k]==0&&visit(k){break}}
	if len(cycle)==0{return nil}
	nodes:=[]Node{};ids:=[]string{}
	for _,k:=range cycle{nodes=append(nodes,Node{ComponentID:k.id,Variant:k.variant,Version:chosen[k].release.Version});ids=append(ids,k.id)}
	return &Conflict{Code:"DEPENDENCY_CYCLE",Message:"selected dependency graph contains a cycle",InvolvedComponents:unique(ids),CyclePath:nodes}
}

// contractConflict checks declared providers from selected releases and the
// target environment. Every required contract must have exactly one compatible
// provider; names are never assigned implicit semantics (REG-015/016).
func contractConflict(chosen map[componentKey]candidate,environment []EnvironmentContract)*Conflict{
	type provider struct{version semver.Version;component string}
	providers:=map[string][]provider{}
	for _,k:=range chosenKeys(chosen){
		r:=chosen[k].release;seen:=map[string]bool{}
		for _,p:=range r.Contracts.Provides{
			if !validContractID(p.ContractID){return &Conflict{Code:"INVALID_CONTRACT_ID",Message:"release declares invalid contract ID",InvolvedComponents:[]string{k.id}}}
			v,e:=semver.Parse(p.Version);if e!=nil{return &Conflict{Code:"INVALID_CONTRACT_VERSION",Message:"release declares invalid contract version",InvolvedComponents:[]string{k.id}}}
			key:=p.ContractID+"\x00"+p.Version;if !seen[key]{providers[p.ContractID]=append(providers[p.ContractID],provider{v,k.id});seen[key]=true}
		}
	}
	for _,p:=range environment{
		if !validContractID(p.ContractID){return &Conflict{Code:"INVALID_ENVIRONMENT_CONTRACT",Message:"environment declares invalid contract ID"}}
		v,e:=semver.Parse(p.Version);if e!=nil{return &Conflict{Code:"INVALID_ENVIRONMENT_CONTRACT",Message:"environment declares invalid contract version"}}
		providers[p.ContractID]=append(providers[p.ContractID],provider{v,""})
	}
	for _,k:=range chosenKeys(chosen){r:=chosen[k].release;requires:=append([]catalog.ContractRequirement(nil),r.Contracts.Requires...)
		sort.Slice(requires,func(i,j int)bool{if requires[i].ContractID!=requires[j].ContractID{return requires[i].ContractID<requires[j].ContractID};return requires[i].VersionConstraint<requires[j].VersionConstraint})
		for _,req:=range requires{
			if !validContractID(req.ContractID){return &Conflict{Code:"INVALID_CONTRACT_ID",Message:"release requires invalid contract ID",InvolvedComponents:[]string{k.id}}}
			c,e:=semver.ParseConstraint(req.VersionConstraint);if e!=nil{return &Conflict{Code:"INVALID_CONTRACT_CONSTRAINT",Message:"release requires contract with invalid version constraint",InvolvedComponents:[]string{k.id}}}
			matches:=[]provider{};for _,p:=range providers[req.ContractID]{if c.Allows(p.version){matches=append(matches,p)}}
			if len(matches)==0{return &Conflict{Code:"CONTRACT_PROVIDER_MISSING",Message:"no compatible provider for "+req.ContractID+" "+req.VersionConstraint,InvolvedComponents:[]string{k.id}}}
			if len(matches)>1{ids:=[]string{k.id};for _,p:=range matches{ids=append(ids,p.component)};return &Conflict{Code:"CONTRACT_PROVIDER_AMBIGUOUS",Message:"multiple compatible providers for "+req.ContractID,InvolvedComponents:unique(ids)}}
		}
	}
	return nil
}

// makeGraph materializes only a complete state and topologically orders
// dependencies before dependents (REG-009/011/023/025).
func makeGraph(w *state,refs []CatalogReference,roots []Requirement,environment []EnvironmentContract)(*Graph,[]SelectedCandidate,*Conflict){
	order,conflict:=topological(w.chosen);if conflict!=nil{return nil,nil,conflict}
	g:=&Graph{SchemaVersion:schemaVersion,Catalogs:append([]CatalogReference{},refs...),Roots:append([]Requirement{},roots...),EnvironmentContracts:append([]EnvironmentContract{},environment...),Components:[]ResolvedComponent{},ResolutionOrder:order}
	selected:=[]SelectedCandidate{}
	for _,node:=range order{
		k:=componentKey{node.ComponentID,node.Variant};r:=w.chosen[k].release;edges:=[]ResolvedDependency{}
		for _,d:=range dependencies(r){dk:=componentKey{d.ComponentID,variantOrDefault(d.Variant)};v,ok:=w.chosen[dk];if !ok{return nil,nil,&Conflict{Code:"INTERNAL_UNRESOLVED_DEPENDENCY",Message:"completed graph has an unresolved dependency",InvolvedComponents:[]string{k.id,dk.id}}}
			edges=append(edges,ResolvedDependency{ComponentID:dk.id,Variant:dk.variant,Version:v.release.Version})}
		g.Components=append(g.Components,ResolvedComponent{ComponentID:r.ComponentID,Variant:r.Variant,Version:r.Version,ReleaseMetadataURI:r.MetadataURI,ReleaseMetadataSHA:r.MetadataSHA,SourceUnits:append([]catalog.SourceUnit{},r.SourceUnits...),Dependencies:edges,Contracts:r.Contracts})
		selected=append(selected,SelectedCandidate{ComponentID:r.ComponentID,Variant:r.Variant,Version:r.Version})
	}
	return g,selected,nil
}

// topological applies Kahn's algorithm, sorting every ready set by component ID
// then variant ID so map iteration cannot affect output (REG-011).
func topological(chosen map[componentKey]candidate)([]Node,*Conflict){
	degree:=map[componentKey]int{};dependents:=map[componentKey][]componentKey{}
	for k:=range chosen{degree[k]=0}
	for k,c:=range chosen{seen:=map[componentKey]bool{};for _,d:=range c.release.Dependencies{n:=componentKey{d.ComponentID,variantOrDefault(d.Variant)};if _,ok:=chosen[n];!ok||seen[n]{continue};seen[n]=true;degree[k]++;dependents[n]=append(dependents[n],k)}}
	ready:=[]componentKey{};for k,n:=range degree{if n==0{ready=append(ready,k)}};sortKeys(ready)
	out:=[]Node{}
	for len(ready)>0{k:=ready[0];ready=ready[1:];out=append(out,Node{ComponentID:k.id,Variant:k.variant,Version:chosen[k].release.Version})
		sortKeys(dependents[k]);for _,child:=range dependents[k]{degree[child]--;if degree[child]==0{ready=append(ready,child);sortKeys(ready)}}}
	if len(out)!=len(chosen){return nil,cycleConflict(chosen)}
	return out,nil
}

// rejectedFor reports candidate exclusions for the successful branch only,
// avoiding diagnostics leaked from discarded backtracking branches.
func rejectedFor(s *solver,w *state)[]RejectedCandidate{out:=[]RejectedCandidate{};for _,k:=range constraintKeys(w.constraints){_,r,_:=s.candidatesFor(k,w.constraints[k]);out=append(out,r...)};return normalizeRejected(out)}
func normalizeRejected(values []RejectedCandidate)[]RejectedCandidate{seen:=map[string]bool{};out:=[]RejectedCandidate{};for _,v:=range values{k:=strings.Join([]string{v.ComponentID,v.Variant,v.Version,v.ReasonCode,v.Detail},"\x00");if !seen[k]{seen[k]=true;out=append(out,v)}};sort.Slice(out,func(i,j int)bool{a,b:=out[i],out[j];if a.ComponentID!=b.ComponentID{return a.ComponentID<b.ComponentID};if a.Variant!=b.Variant{return a.Variant<b.Variant};if a.Version!=b.Version{return a.Version<b.Version};if a.ReasonCode!=b.ReasonCode{return a.ReasonCode<b.ReasonCode};return a.Detail<b.Detail});return out}
func dependencies(r catalog.Release)[]catalog.Dependency{out:=append([]catalog.Dependency{},r.Dependencies...);sort.Slice(out,func(i,j int)bool{if out[i].ComponentID!=out[j].ComponentID{return out[i].ComponentID<out[j].ComponentID};return variantOrDefault(out[i].Variant)<variantOrDefault(out[j].Variant)});return out}
func variantOrDefault(v string)string{if v==""{return "default"};return v}
func validID(v string,max int)bool{if v==""||len(v)>max{return false};if !lowerAlphaNum(v[0]){return false};lastSep:=false;for _,r:=range v{ok:=r>='a'&&r<='z'||r>='0'&&r<='9';sep:=r=='.'||r=='-';if !ok&&!sep||sep&&lastSep{return false};lastSep=sep};return !lastSep}
func lowerAlphaNum(b byte)bool{return b>='a'&&b<='z'||b>='0'&&b<='9'}
func validContractID(v string)bool{if v==""||len(v)>200{return false};for i,r:=range v{ok:=r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='.'||r=='_'||r==':'||r=='-';if !ok||i==0&&!(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'){return false}};return true}
func fullGitObjectID(v string)bool{if len(v)!=40&&len(v)!=64{return false};for _,r:=range v{if !(r>='0'&&r<='9'||r>='a'&&r<='f'||r>='A'&&r<='F'){return false}};return true}
func sortRequirements(v []Requirement){sort.Slice(v,func(i,j int)bool{if v[i].ComponentID!=v[j].ComponentID{return v[i].ComponentID<v[j].ComponentID};if v[i].Variant!=v[j].Variant{return v[i].Variant<v[j].Variant};return v[i].VersionConstraint<v[j].VersionConstraint})}
func sortKeys(v []componentKey){sort.Slice(v,func(i,j int)bool{if v[i].id!=v[j].id{return v[i].id<v[j].id};return v[i].variant<v[j].variant})}
func chosenKeys(m map[componentKey]candidate)[]componentKey{v:=make([]componentKey,0,len(m));for k:=range m{v=append(v,k)};sortKeys(v);return v}
func constraintKeys(m map[componentKey][]constraint)[]componentKey{v:=make([]componentKey,0,len(m));for k:=range m{v=append(v,k)};sortKeys(v);return v}
func unique(v []string)[]string{seen:=map[string]bool{};out:=[]string{};for _,x:=range v{if x!=""&&!seen[x]{seen[x]=true;out=append(out,x)}};sort.Strings(out);return out}
