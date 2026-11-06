# Changelog

All notable changes to CDIN Registry are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Add the clean-room Product Definition → Catalog → Resolver → Prepare → Verify → Replay integration test using real Spec schemas and a temporary Git repository.
- Add explicit Phase 9 test-to-REG traceability for REG-001–REG-027 and reconcile the normative baseline to Registry Data Contracts 1.2.0.
- Add exact REG/Spec/design references to security-sensitive Catalog, Resolver, cache, replay, update-discovery and private-directory entry points.
- Add the Update Discovery Report to CLI schema-kind help and schema-list regression coverage.

- Enable the qualified four-worker Catalog metadata fetch pool by default after native Linux/macOS/Windows, race, cancellation/join, deterministic-order and cache-integrity verification; retain LoadOptions for callers that need a lower worker count.
- Enforce platform-native private cache permissions: Unix 0700/0600 modes and protected Windows DACLs limited to the current user, Local System and Administrators; document the caller-selected parent-directory trust boundary.
- Bound Resolver dependency/contract/source declarations to 65,536 in addition to existing candidate, graph-node and search-state limits.
- Bound Catalog Release Metadata retrieval to four concurrent requests while preserving index-order validation/output and all-or-nothing failure; add cancellation joining and Catalog byte/count budgets.
- Add seeded fuzz targets and required bounded fuzz smoke campaigns for JSON/YAML, SemVer/constraints, Catalog loading, Resolver graph construction, source/cache references and credential redaction.
- Add Catalog, Resolver, source-tree copy and metadata/Git cache GC benchmark smoke.
- Expand native Ubuntu/macOS/Windows CI to run all Registry Go packages when core paths change.
- Add registry manifest discover, a deterministic read-only audit operation bound to historical Manifest, Product Definition and Catalog digests.
- Add metadata-cache Manifest pins, strict-replay/fetch leases and fail-closed age/budget garbage collection; expose cache pin, unpin and gc.
- Add content-addressed Git-object caching keyed by full commit IDs, commit/object-graph verification, corruption refetch, Manifest pins, replay leases and garbage collection.
- Add manifest verify and manifest replay CLI commands with stable errors, explicit host policy, offline/strict metadata modes and signal-driven cancellation/cleanup.
- Publish prepared Workspaces through OS-native atomic no-replace directory rename on Linux, macOS and Windows; add failure-injection and destination-race tests.
- Harden Collector Git execution with an explicit credential-provider environment, process-group cancellation and partial source-tree rollback.
- Add Resolver 1.1.0 report evidence: sourced constraints, immutable candidate provenance, typed conflict/rejection codes and structured provider diagnostics.
- Add adversarial Resolver tests, official SemVer parser vectors, strict JSON/YAML validation, repository governance, GIX/PSX checks and Spec schema synchronization.

### Changed

- Make Pipeline the source of truth for local tasks and CI/release orchestration; GitHub workflows remain provider adapters.
- Keep the Registry consumer-independent: public domains are composed at the CLI boundary and no consumer-specific resolver policy is introduced.
- Document verified behavior separately from operational assumptions and explicit non-guarantees.

### Explicit non-guarantees and follow-up hardening

- Parent-directory fsync and uniform crash/power-loss durability guarantees across filesystems and operating systems are not provided.
- Publisher signing, signature verification, attestations, SBOM generation, component vulnerability scanning, build sandboxing and deployment firewall enforcement are outside Registry's guarantees.
- Git submodule and Git LFS object retrieval are unsupported.
- Longer sustained fuzz campaigns, workload-sized performance baselines and broader concurrent update/replay/GC interleaving stress remain operational hardening work. The four-worker Catalog metadata pool is bounded; Git fetch and source copying remain sequential.
