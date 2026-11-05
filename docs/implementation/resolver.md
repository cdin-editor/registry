# Resolver implementation

## Status and scope

This document records the Resolver implementation for Registry Data Contracts `1.1.0`. The report uses schema `1.1.0`; the Resolved Manifest remains on its independent `1.0.0` schema. It covers SemVer parsing/precedence, the frozen constraint
grammar, candidate selection, dependency closure, constraint intersection,
prerelease/withdrawal policy, contract-provider matching, cycle detection and
deterministic dependency-first ordering.

It does **not** claim that Catalog transport, Git source collection, workspace
publication, Manifest serialization, Resolution Report serialization, canonical
JSON, CLI composition or update discovery are complete. Those remain separate
roadmap stages.

## Normative references

- [Registry architecture — resolution model](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#4-resolution-model)
- [Registry data contracts — identity and version rules](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#2-identity-and-version-rules)
- [Registry data contracts — resolution semantics](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#5-resolution-semantics)
- [REG-006–REG-016](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#11-normative-registry-requirements)
- [Module boundaries](../architecture/module-boundaries.md)
- [Implementation roadmap](../development/implementation-roadmap.md)

## Package responsibilities

| Package | Responsibility | Why this boundary exists |
| --- | --- | --- |
| `internal/semver` | Parse SemVer 2.0.0, compare precedence and evaluate the frozen Registry constraint grammar | One source of truth prevents lexical comparison and accidental support for out-of-contract range syntax. |
| `internal/catalog` | Preserve typed dependencies and contract declarations after digest/schema verification | Resolver must consume validated data, not parse raw JSON or fetch metadata itself. |
| `internal/resolver` | Select exact releases and produce a deterministic in-memory graph plus structured report | Version policy must be independent of CLI, network, Git and filesystem operations. |

The resolver accepts `ProductDefinition` and fully loaded `catalog.Catalog` values.
It does not know a product's name/ID, does not load catalog URIs, does not invoke
Git, and does not write output paths. CLI composition will inject those boundary
adapters later.

## SemVer and constraints

`internal/semver.Version` stores canonical numeric identifiers as decimal strings.
SemVer does not impose a fixed machine-integer width, so comparing via `uint64`
would incorrectly reject or misorder large but valid identifiers. Numeric values
are compared by digit count and then ASCII bytes; prerelease numeric identifiers
use the same method. This avoids overflow and exactly preserves numeric order.

Build metadata is retained for identity/diagnostics but excluded from precedence.
Consequently two distinct release metadata identities with equal precedence are
not silently tie-broken: the resolver returns `AMBIGUOUS_RELEASE` as required by
REG-011.

`ParseConstraint` accepts only one exact version or a space-separated
conjunction of the contract's `=`, `>`, `>=`, `<`, and `<=` comparators. It
intentionally rejects OR, wildcard, partial, hyphen, caret, tilde and comma
ranges. `Constraint.Allows` evaluates every comparator using SemVer precedence.

The resolver also checks whether the accumulated interval is mathematically
empty before searching candidates. This distinguishes contradictory constraints
(e.g. `>=2.0.0` and `<2.0.0`) from a non-empty constraint interval for which the
catalog happens to contain no matching release.

## Deterministic resolution algorithm

1. Build candidate buckets keyed by normalized `(component_id, variant)` across
   all supplied Catalogs. An omitted variant becomes `default` before a key is
   created. Stable identifiers and full source revision shape are checked again
   at this engine boundary even though the caller is expected to schema-validate.
2. Seed a search state with every Product Definition root constraint. All
   constraints for one key are appended; none overwrites another.
3. Expand each selected release's declared dependencies exactly once. Each
   dependency is normalized and appended to the same key's constraint set. If a
   newly added constraint invalidates an already selected release, that search
   branch fails.
4. Choose the lexicographically first unresolved key. Filter out withdrawn
   releases and, unless policy allows it, prereleases. Filter remaining versions
   through every accumulated constraint.
5. Sort eligible candidates by descending SemVer precedence. A distinct
   equal-precedence identity in the highest precedence group is an explicit
   ambiguity, not a catalog-order tie-breaker.
6. Try candidates in precedence order using isolated copied search states.
   Backtracking matters: the highest direct candidate can introduce an
   incompatible transitive dependency even when a lower candidate yields a
   valid complete graph. Candidate exploration remains deterministic.
7. Once the graph is closed, detect dependency cycles and validate each required
   contract against the selected component providers plus explicitly declared
   environment providers. Each required contract must have exactly one
   compatible provider.
8. Compute a dependency-first topological order. Independent nodes at the same
   depth are ordered by component ID, then variant ID.
9. Return a complete in-memory `Graph` and success report, or a structured
   failure report with `Graph == nil`. No successful Manifest or workspace is
   produced by this package.

The search has a fixed `100000` state ceiling. This is a fail-closed CPU guard for
untrusted metadata and pathological dependency graphs; exceeding it produces
`RESOLUTION_LIMIT`, never a partial graph. If production catalogs demonstrate
a need for a different budget, the limit should become an explicit configuration
owned by the composition root, not inferred from product identity.

## Conflict and report behavior

The first implementation emits stable conflict codes for malformed identities,
versions and constraints; empty intersections; no compatible release; equal-
precedence ambiguity; dependency cycles; missing/ambiguous contract providers;
and bounded-search exhaustion. The `Report` type implements the Resolution Report `1.1.0` model and is returned for both success and failure. Conflict and rejection codes are closed Go string types whose serialized values are constrained by the public schema.

A failure returns no Graph. This is deliberate: downstream collector/workspace
code must not mistake a partial selection for a resolved product. The report
preserves each root/dependency constraint with its source identity, including
repeated declarations. Candidate and conflict evidence preserves immutable
catalog/release provenance where available; provider evidence distinguishes the
declared contract version from the selected component release version.

Backtracking evidence is branch-scoped. A candidate whose downstream graph
fails is identified with BRANCH_CONFLICT when a fallback is selected. On a
terminal failure, the report exposes the deterministic surfaced branch and
does not aggregate diagnostics or applied constraints from sibling branches.
Constraint, candidate, provider and involved-node arrays are sorted using the
keys frozen by the contract. Self-provision is allowed: a selected component
may satisfy its own requirement when it declares exactly one compatible
provider. Identical duplicate declarations within that component are coalesced;
separate environment declarations remain separate providers.

## Verification map

| Requirement | Evidence |
| --- | --- |
| REG-006 | SemVer 2.0.0 parser/precedence tests, invalid forms, unbounded numeric identifiers, prerelease order and build metadata precedence. |
| REG-007 | Constraint grammar allow/deny vectors and comparator boundary tests. |
| REG-003/004 | Resolver identity validation and omitted-variant normalization tests. |
| REG-008/009 | Transitive closure, diamond/shared dependencies, backtracking and one selected release per normalized key tests. |
| REG-010 | Repeated roots/dependency declarations, constraints from multiple transitive paths, source evidence and empty-intersection tests. |
| REG-011 | Equal-precedence candidate provenance, candidate/dependency permutation, fallback rejection evidence and bounded search-limit determinism tests. |
| REG-012/013 | Prerelease and withdrawn candidate policy tests. |
| REG-014 | Cycle failure with an explicit cycle path. |
| REG-015/016 | Component/environment providers, self-provider, duplicate declarations, incompatible-provider evidence and ambiguity tests. |

The Phase 2 tests establish the resolver's deterministic evidence and adversarial branch behavior. They do not replace fuzz tests, a platform matrix or end-to-end workspace replay; full Registry conformance must not be claimed from Resolver tests alone.
