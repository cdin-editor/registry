# Implementation roadmap

A capability is complete only when its behavior, rejection paths and cleanup are
exercised by tests and traceable to the accepted Registry Spec. This roadmap
describes the Phase 9 stabilization candidate; it is not a claim of crash
durability, publisher authenticity or deployment isolation.

## Phase 1 — Catalog fixtures, transport and credential boundary

- [x] Reusable offline fixtures cover multiple Catalog Indexes, duplicates/conflicts, missing/malformed metadata, invalid digests and schemas.
- [x] Exercise Catalog loading and CLI composition with fixture inputs.
- [x] Cover HTTP timeout/stalls, truncated bodies, redirect limits, unsafe redirects, mixed DNS answers and verified-IP dialing.
- [x] Keep failed multi-entry loads all-or-nothing.
- [x] Make Catalog/Git credential boundaries explicit and injected; redact credentials and untrusted remote diagnostics.
- [x] Define metadata cache retention/eviction policy; enforcement is in Phase 7.

Evidence: `internal/catalog/*_test.go`, `internal/cli/catalog_fixture_test.go`,
[Catalog transport](../implementation/catalog-transport.md).

## Phase 2 — Resolver conformance

- [x] Use closed typed conflict/rejection codes and deterministic sourced evidence.
- [x] Cover diamond/shared graphs, repeated roots/declarations, transitive conflicts, fallback, candidate permutation and search-state exhaustion.
- [x] Cover contract-provider absence, incompatibility, ambiguity, duplicate declarations and self-provider behavior.
- [x] Exercise applicable official SemVer 2.0.0 vectors and stable failure evidence.

Evidence: `internal/resolver/resolver_test.go`, `internal/resolver/resource_test.go`,
[Resolver design](../implementation/resolver.md).

## Phase 3 — Collector trust boundary

- [x] Use a minimal subprocess environment and an explicit non-interactive credential-provider boundary.
- [x] Redact tokens, URLs and remote command output from diagnostics.
- [x] Verify exact full Git commit objects and detached checkout; reject branch/tag shorthand and malformed source trees.
- [x] Terminate Git children/descendants on cancellation and remove temporary repositories, hooks and partial copies.
- [x] Support multiple source units and shared repositories without path collisions.
- [x] Disable hooks, filters, LFS retrieval and automatic submodule recursion; never execute collected code.

Evidence: `internal/collector/*_test.go`, [Collector design](../implementation/collector.md).

## Phase 4 — Workspace transaction

- [x] Stage privately and publish only after collection and Manifest/Report validation.
- [x] Inject failures at source, document write/sync, cancellation, lock and final publication boundaries.
- [x] Preserve foreign destinations and locks; fail closed on stale locks.
- [x] Use native atomic no-replace publication on Linux, macOS and Windows.
- [x] Verify POSIX permissions and Windows DACL/SID/inheritance semantics.
- [x] Document that parent-directory fsync and uniform crash/power-loss durability are not provided.

Evidence: `internal/workspace/*_test.go`,
[Workspace design](../implementation/workspace.md).

## Phase 5 — Manifest integrity and replay

- [x] Pin canonical Manifest/Report bytes and detached digests across equivalent input permutations.
- [x] Test applicable RFC 8785/JCS property ordering, escaping, Unicode and number-format vectors.
- [x] Verify schema, canonical bytes, detached digest, provenance, identities and safe paths.
- [x] Replay a persisted Manifest using exact recorded commits, without rerunning version selection.
- [x] Prove offline replay parity and reject malformed, changed or identity-mismatched inputs.
- [x] Prove Catalog updates do not mutate existing Manifests or prepared Workspaces.

Evidence: `internal/manifest/*_test.go`, `internal/workspace/replay_test.go`,
[Manifest design](../implementation/manifest.md).

## Phase 6 — CLI operations and cancellation

- [x] Preserve validate/resolve/prepare and add Manifest verify, replay and update-discovery operations.
- [x] Define stable machine-readable errors, receipts and file/stdin behavior.
- [x] Wire Ctrl+C and supported Unix SIGTERM cancellation through metadata and Git operations.
- [x] Keep diagnostics free of secrets, raw remote output and host-specific temporary paths.
- [x] Document command contracts and publication boundaries.

Evidence: `internal/cli/*_test.go`, `tests/cli_test.go`,
[CLI operations](../implementation/cli.md).

## Phase 7 — Update discovery, caches and auditability

- [x] Implement immutable Manifest update discovery using the generic Resolver and a separately versioned audit report.
- [x] Preserve historical Manifest and Workspace bytes across Catalog updates and discovery.
- [x] Add content-addressed Git-object caching with full commit/object-graph verification and corruption refetch.
- [x] Add retained-Manifest pins, replay/fetch leases, deterministic age/budget eviction and fail-closed GC for metadata and Git caches.
- [x] Cover cache corruption, cancellation, per-object concurrency, retention and malformed reference snapshots.
- [x] Enable a fixed four-worker Catalog metadata pool only after cancellation/join, deterministic index-order processing, race and native-platform checks; allow callers to request fewer workers.

Evidence: `internal/catalog/cache_test.go`,
`internal/collector/gitcache_test.go`,
`internal/cli/discovery_test.go`, `internal/catalog/concurrency_test.go`.

## Phase 8 — Fuzzing, platform, performance and security

- [x] Add seeded fuzz targets for strict JSON/YAML, SemVer/constraints, Catalog/cache references, Resolver graphs, source paths, credential redaction and Git-object Manifest references.
- [x] Run bounded fuzz smoke campaigns in the required Pipeline gate.
- [x] Keep `go test -race ./...` in the required quality gate.
- [x] Run all Registry Go packages and Workspace integration natively on Ubuntu, macOS and Windows.
- [x] Enforce Catalog, Resolver, Source Unit, Workspace and cache resource budgets.
- [x] Run bounded Catalog, Resolver, source-copy and cache-GC benchmark smoke.
- [x] Document trust roots, authentication, egress, cache permissions, unsupported inputs and guarantees not provided.

Evidence: `scripts/fuzz-smoke.sh`, `scripts/benchmark-smoke.sh`,
`docs/implementation/resource-budgets.md`,
`docs/security/trust-boundaries.md`,
[Native platform workflow](../../.github/workflows/workspace-platforms.yml).

## Phase 9 — Documentation, traceability and release alignment

- [x] Reconcile the six embedded Spec-owned schemas and Registry Data Contract 1.2.0 / REG-001–REG-027 baseline.
- [x] Correct stale default-concurrency comments, CLI schema-kind help and documentation statements.
- [x] Link security-sensitive package entry points to exact REG requirements, normative Spec sections and local design notes.
- [x] Audit every REG-001–REG-027 row against implementation paths and named executable tests.
- [x] Add a clean-room integration test that validates Product Definition YAML, loads a real-schema Catalog fixture, resolves it, prepares a Workspace from a temporary Git repository, verifies the persisted Manifest and replays it byte-for-byte.
- [x] Verify Product identity/name changes do not affect the same declarative component resolution.
- [x] Align README, Status, Roadmap, Conformance, Traceability, design notes and Changelog with verified behavior and explicit non-guarantees.

Evidence: `internal/workspace/phase9_e2e_test.go`,
`docs/traceability.md`, `docs/testing/conformance.md`,
`docs/status.md`.

## Remaining qualifications (not silently promoted to guarantees)

- Parent-directory fsync and uniform crash/power-loss durability remain unimplemented.
- Registry does not sign publishers/releases, create attestations or SBOMs, scan downloaded components for vulnerabilities, execute components in a build sandbox, or enforce deployment firewall policy.
- Git submodule and Git LFS object retrieval are unsupported.
- Longer sustained fuzz campaigns, workload-sized performance baselines and broader discovery/replay/GC interleaving stress remain operational hardening work. The bounded Catalog fetch pool is not generalized to Git fetching or source copying.

The branch is a release candidate only after its required Pipeline,
Documentation/YAML, Security Scan and native-platform workflows pass. Promotion
to `develop` and then `main` is a separate reviewed Git Flow operation.
