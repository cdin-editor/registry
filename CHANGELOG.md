# Changelog

All notable changes to CDIN Registry are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Implement explicit metadata-cache Manifest pins, strict-replay/fetch leases and fail-closed age/budget garbage collection; expose `cache pin`, `cache unpin` and `cache gc`.

- Add `manifest verify` and `manifest replay` CLI commands with stable machine-readable error codes, explicit host policy, offline/strict metadata modes and signal-driven cancellation/cleanup.

- Bound the canonical JSON UTF-8 preflight for cyclic maps, slices and pointers; cyclic values now reach the encoder's normal error path instead of recursive validation.

- Apply protected Windows workspace/lock DACLs for Owner Rights, LocalSystem and Administrators; verify ACE identities, rights and inheritance on native Windows.

- Pin complete Manifest/Report canonical bytes and detached digests across repeated runs and equivalent input permutations; expand RFC 8785 Unicode/IEEE-754 vectors and reject invalid UTF-8 before serialization.

- Publish prepared Workspaces through OS-native atomic no-replace directory rename on Linux, macOS and Windows; fail closed on unsupported operating systems.
- Add empty-destination race, destination-symlink, staging-symlink swap and simultaneous-publisher tests. Assert temporary Git clone and disabled-hook cleanup across injected Workspace failures.

- Define metadata-cache retention and eviction policy without changing immutable URI+expected-digest cache identity or verify-before-use behavior. Enforce the policy through explicit Manifest pins, replay/fetch leases and serialized GC.
- Add Workspace failure injection for multi-source rollback, Manifest/Report write and sync faults, cancellation, lock contention/stale-lock handling and competing destination creation.
- Add end-to-end Workspace coverage for shared repositories across components/variants and multiple source units, plus native Linux/macOS/Windows test jobs.
- Harden Collector Git execution with an explicit credential-provider environment, non-interactive process policy, process-group cancellation, helper-descendant cleanup and partial source-tree rollback.
- Add Collector tests for repository reuse across logical components/variants, credential isolation, actual child termination, cancellation cleanup, submodule/filter/hook isolation, and missing/malformed repositories.

- Add Registry Resolver 1.1.0 report evidence: sourced applied constraints, immutable candidate provenance, typed conflict/rejection codes and structured contract-provider diagnostics.
- Add adversarial Resolver coverage for diamond graphs, duplicated constraints/providers, transitive conflicts, fallback branch isolation, permutation invariance and bounded search.
- Add SemVer 2.0.0 parser vectors alongside precedence and unbounded-identifier tests.
- Add the provider-neutral Pipeline task graph and pin reusable CI, documentation, security and release workflows to a reviewed Pipeline commit.
- Add PSX repository-structure validation and executable governance tests for PR/Issue templates and repository policy.
- Add GIX branch-flow validation using GIX itself, plus a main/develop Git Flow matching the CDIN repositories.
- Add pull request and issue templates for bugs, feature requests and Spec/contract changes.
- Pin the Registry schema mirror to an immutable CDIN Spec commit and add a drift-check/synchronization script.
- Add cross-platform release target compilation through Pipeline's release runner.
- Add a transport-injected Catalog Index loader with digest-before-parse verification, release identity checks, default-variant normalization, duplicate deduplication and conflict rejection.
- Preserve typed dependency and contract declarations in Catalog releases for resolver consumption.
- Add SemVer 2.0.0 parser/precedence and Registry 1.0.0 constraint parsing/evaluation.
- Add deterministic dependency/version/contract resolver with backtracking, cycle diagnostics, branch-scoped evidence and structured in-memory results.

### Changed

- Make Pipeline the source of truth for local task execution and CI/release orchestration; keep GitHub workflows as thin provider adapters.
- Require governance, GIX configuration, PSX structure and schema-mirror checks in the release verification graph.
- Strengthen JSON/YAML validation by rejecting duplicate JSON keys, unsupported YAML forms, recursive aliases and trailing documents/values.
- Update contributor, development and repository documentation to describe the shared engineering tools and the implemented verification boundary.

### Not yet implemented

- Git-object cache GC and eviction enforcement. Metadata cache GC, retained-Manifest pins and active replay leases are implemented.
- Update discovery and related update/cache lifecycle operations.
- Parent-directory fsync and uniform crash/power-loss durability guarantees across supported filesystems and operating systems.
- Cache corruption/retention, update-versus-replay and performance/fuzz matrices.
