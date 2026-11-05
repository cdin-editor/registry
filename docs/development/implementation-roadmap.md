# Implementation roadmap

This roadmap describes staged implementation work. A package or interface is not considered complete until its behavior and failure paths are tested.

## Phase 1 — Foundation

- [x] Standalone Go module and CLI entry point.
- [x] Embedded Registry v1 schemas synchronized from Spec.
- [x] JSON and Product Definition YAML validation.
- [x] Build/test commands, CI and repository documentation.

## Phase 2 — Catalog

- [ ] Catalog Index loading and validation.
- [ ] Metadata SHA-256 verification before parsing.
- [ ] Duplicate and conflicting catalog-entry detection.
- [ ] Local fixture catalogs and negative tests.

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
