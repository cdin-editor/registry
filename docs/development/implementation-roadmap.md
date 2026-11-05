# Implementation roadmap

A package or interface is not complete until its behavior and failure paths are covered by tests and traceable to the accepted Spec.

## Phase 1 — Foundation

- [x] Standalone Go module and CLI entry point.
- [x] Embedded Registry v1 schemas synchronized from an immutable Spec commit.
- [x] Strict JSON and Product Definition YAML validation.
- [x] Unit and CLI integration tests for parser/validation failure paths.
- [x] Pipeline task graph and reusable CI/docs/security/release workflows.
- [x] PSX structure checks, GIX branch-flow config, PR and issue templates.

## Phase 2 — Catalog

- [x] Catalog Index loading and schema validation.
- [x] Metadata SHA-256 verification before release metadata validation/decoding.
- [x] Duplicate identical entry deduplication and conflicting metadata identity rejection.
- [ ] Local fixture catalogs, transport integration and broader negative tests.

## Phase 3 — Resolver

- [ ] Strict SemVer parsing and version-constraint evaluation.
- [ ] Transitive dependency closure and constraint intersection.
- [ ] Prerelease and withdrawn-release policy.
- [ ] Contract provider compatibility and ambiguity detection.
- [ ] Cycle detection, deterministic ordering and structured conflicts.

## Phase 4 — Collector

- [ ] Fetch only declared source units.
- [ ] Verify full immutable Git commit object IDs.
- [ ] Enforce safe subdirectory handling.
- [ ] Never execute component build scripts or hooks.

## Phase 5 — Workspace and manifests

- [ ] Transactional workspace staging and publication.
- [ ] Deterministic workspace paths.
- [ ] Resolved Manifest and Resolution Report generation.
- [ ] Reproduction tests from a saved manifest.

## Phase 6 — Updates and release hardening

- [ ] Discover new releases without mutating existing workspaces.
- [ ] Security withdrawal behavior and audit records.
- [ ] Cache lifecycle, performance budgets and broader platform tests.
