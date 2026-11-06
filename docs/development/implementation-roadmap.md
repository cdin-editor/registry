# Implementation roadmap

A package or interface is not complete until its behavior and failure paths are
covered by tests and traceable to the accepted Spec.

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
- [x] Preserve dependency and contract declarations as typed release data for Resolver.
- [ ] Local fixture catalogs, production HTTP transport integration and broader negative tests.
- [ ] Host allow-list, redirect policy, response-size/time limits, authentication isolation and cache lifecycle.

## Phase 3 — Resolver

- [x] Strict SemVer 2.0.0 parsing, precedence and Registry 1.0.0 constraint evaluation.
- [x] Transitive dependency closure, deterministic candidate selection and constraint intersection.
- [x] Prerelease and withdrawn-release policy.
- [x] Contract provider compatibility, environment providers and ambiguity detection.
- [x] Cycle detection with path, deterministic dependency-first ordering and structured conflicts.
- [ ] Expand conformance vectors for diamond graphs, conflicting transitive constraints, candidate permutations, all official SemVer vectors and search-limit behavior.

See [Resolver implementation](../implementation/resolver.md) for algorithm details, design decisions and test mapping.

## Phase 4 — Collector

- [ ] Fetch only declared source units through an injected Git transport/command boundary.
- [ ] Verify full immutable Git commit object IDs against actual commit objects.
- [ ] Enforce safe subdirectory, symlink and workspace path containment.
- [ ] Support multiple source units and shared repositories without changing visible paths.
- [ ] Never execute component build scripts, hooks, filters, submodules or LFS implicitly.
- [ ] Test real temporary Git repositories, cancellation, missing objects and cleanup.

## Phase 5 — Workspace and manifests

- [ ] Transactional workspace staging and atomic publication; never overwrite an existing destination implicitly.
- [ ] Deterministic workspace paths from stable component/variant/source-unit IDs.
- [ ] Resolved Manifest and Resolution Report production against the existing v1 schemas.
- [ ] RFC 8785 canonical JSON for digest bytes; no timestamps, random IDs or absolute host paths.
- [ ] Reproduction tests from a saved Manifest without re-running version selection.
- [ ] Failure injection proves no partial workspace or successful Manifest is published.

## Phase 6 — CLI operations, diagnostics and cancellation

- [x] Compose validation → catalog load → resolve → collect → workspace publish in a `prepare` command.
- [x] Preserve dry-run `resolve` and add Manifest verification and replay commands with documented inputs/outputs and stable exit codes.
- [x] Define stable machine-readable errors and diagnostics that do not expose credentials, raw remote errors or machine-specific temporary paths.
- [x] Connect Ctrl+C and Unix SIGTERM to command-context cancellation; preserve Workspace staging/lock cleanup.
- [x] Document command arguments, stdout/stderr contracts, exit codes, cancellation and failure cleanup in [Manifest CLI operations](../implementation/cli.md).

Update discovery, withdrawal audit records, cache lifecycle/garbage collection, bounded concurrency and performance budgets belong to Phase 7. Fuzz targets and broader platform/security coverage remain Phase 8.
