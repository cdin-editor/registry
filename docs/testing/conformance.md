# Conformance

Registry must implement normative `REG-001` through `REG-027` in the
[CDIN Spec Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).

This page separates verified behavior from schema presence and future work.
A type, interface, schema, comment or document is not conformance evidence
without executable behavior and tests.

## Current evidence

| Area | Verification |
| --- | --- |
| Schema compilation | Go tests compile every embedded v1 schema through the production validator. |
| Valid documents | Example Product Definition, Catalog Index, Component Release Metadata, Manifest and Report are tested. |
| JSON parser safety | Duplicate object keys, malformed input and trailing JSON values are rejected. |
| YAML parser safety | Duplicate keys, non-string keys, custom tags, merge keys, recursive aliases and multiple documents are rejected. |
| Catalog integrity | Exact fetched metadata bytes are SHA-256 verified before Component Release validation/decoding. |
| Catalog identity | Default variant normalization, release identity matching, identical deduplication and conflict rejection are tested. |
| SemVer | SemVer 2.0.0 precedence, prerelease ordering, build metadata, malformed forms and unbounded numeric identifiers are tested. |
| Constraint grammar | Contract 1.0.0 exact/comparator-conjunction grammar, rejected syntax and boundary evaluation are tested. |
| Dependency resolution | Transitive closure, backtracking, repeated constraints and empty intersections are tested. |
| Selection policy | Prerelease/withdrawn policy and catalog permutation invariance are tested. |
| Contracts | Component/environment providers, missing providers and ambiguity are tested. |
| Graph | Cycle path, deterministic dependency-first output and failure-without-graph are tested. |
| Manifest verification | Canonical byte equality, embedded public schema, optional detached digest, semantic provenance, safe paths and dependency-first ordering are verified. |
| Manifest replay | Reconstructs the saved graph without Resolver/version selection, retrieves exact full commits, checks byte-identical Manifest output before Workspace publication and does not execute component code. |
| Manifest CLI | File/stdin verification, JSON receipts, stable error codes, offline/strict replay, source failure, cancellation, destination collision and usage errors are tested. |
| Native Workspace publication | Ubuntu, macOS and Windows native jobs exercise no-replace publication, cleanup and permissions; crash/power-loss durability is not claimed. |
| Update discovery | Verifies the historical Manifest before Catalog fetch, resolves current state with the generic Resolver, validates the deterministic report against its own schema and the referenced Resolution Report schema, and leaves historical files/Workspace untouched; see `TestManifestDiscoverReportsUnchangedAndWritesImmutableAuditFile`, `TestManifestDiscoverPreservesWithdrawalDecisionWhenMetadataDigestChanges`, and validation cross-reference tests. |
| Bounded Catalog loading | `Load` defaults to four concurrent metadata fetches (hard maximum four); `LoadWithOptions` can lower the count. Native Linux/macOS/Windows, race-detector, cache-integrity, cancellation/join, out-of-order index-order processing, failure cleanup, no-partial-Catalog, and Index/document/aggregate byte-budget tests passed before enabling the default. See `internal/catalog/concurrency_test.go`, `internal/catalog/resource_test.go`, and `internal/catalog/httpfetcher_test.go`. |
| Resolver resource budgets | 16,384 Catalog candidates, 65,536 dependency/contract/source declarations, 1,024 root/transitive components and 100,000 search states are fail-closed; no partial Graph. See `internal/resolver/resource_test.go`. |
| Source Collection budgets | Per Source Unit: 100,000 files / 1 GiB. Per Workspace: 500,000 files / 8 GiB. Checks occur before each file copy and changed/growing files are bounded during copy. See `internal/collector/resource_test.go`. |
| Fuzzing | Seeded fuzz targets cover strict JSON/YAML, SemVer/constraints, Catalog metadata/cache references, Resolver graph inputs, source-subdirectory paths, Git credential environment and Git-object Manifest references. Pipeline runs short bounded campaigns; longer campaigns remain a maintainer action. |
| Performance baselines | Pipeline executes one benchmark iteration for large Catalog loading, wide/deep Resolver graphs, backtracking, source-tree copy and metadata/Git cache GC. Wall-time thresholds are recorded, not treated as portable CI gates. |
| Native platform integration | Phase 8 workflow runs all Registry Go packages natively on Ubuntu, macOS and Windows when Catalog/Resolver/validation/collector/workspace paths change. |
| Repository boundaries | Pipeline invokes PSX and package/consumer-independence boundary checks. |

## Remaining qualifications and work

- Longer sustained fuzz campaigns, full large-graph performance trend baselines and production workload sizing remain future work.
- Broader update-versus-replay/GC interleavings and concurrent cache mutation stress remain open. Catalog metadata concurrency is bounded and tested; it is not generalized to Git collection or source copying.
- End-to-end clean-room preparation from a Product Definition plus one or more production Catalogs remains a separate completion gate.
- Parent-directory fsync and uniform crash/power-loss durability are not implemented or claimed.
- Deployment-wide Git DNS/IP egress enforcement remains the deployment firewall's responsibility.

CI evidence applies only to commands/platforms actually run. The first Resolver
slice does not prove Collector, Workspace, Manifest serialization or full
REG-001–REG-027 conformance.
