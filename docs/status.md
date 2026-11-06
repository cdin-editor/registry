# Implementation status

**Stage:** Registry Core implementation and cross-platform hardening in progress; not yet complete Registry 1.0 conformance.

The repository now has more than an in-memory Resolver: it includes strict
Catalog transport, deterministic dependency and contract resolution, immutable
Git source collection, transactional workspace staging/publication, canonical
Manifest/Report serialization, CLI resolve/prepare composition and read-only update discovery. These
packages remain generic and are composed at the Registry CLI boundary; collected
component code is never executed.

The current implementation has passed the repository Pipeline quality gate,
including formatting, go vet, all Go tests, governance, build, module
boundaries, schema synchronization, PSX structure checks, release checks,
race tests and git diff --check. This is evidence for the code on the tested
feature-branch head, not proof of every REG-001–REG-027 deployment or
cross-platform requirement.

| Capability | Status |
| --- | --- |
| CLI entry point, version, help and schema inspection | Implemented |
| validate command and embedded Registry v1 schemas | Implemented; schemas pinned to a Spec commit |
| Strict JSON duplicate-key/trailing-value rejection | Implemented |
| Product Definition YAML normalization and strict YAML rejection rules | Implemented |
| resolve command: explicit Product Definition, Catalogs and host policy; report only | Implemented; command composition and core packages tested |
| prepare command: resolve, collect, validate outputs and publish | Implemented; underlying workspace integration tested with temporary Git repositories |
| manifest verify command | Implemented; canonical bytes, public schema, provenance and optional external SHA-256; file/stdin tests |
| manifest replay command | Implemented; exact pinned Git commits, offline/strict metadata modes, transactional publication and stable error codes |
| Catalog Index loading and schema validation | Implemented; all-or-nothing loader, opt-in up to 4 concurrent metadata fetches (serial default pending qualification) and 8 MiB/4,096-entry/256 MiB Catalog budgets on the Phase 8 branch |
| Production HTTPS metadata transport | Implemented; explicit exact-host allow-list, redirect revalidation, DNS/public-address checks and bounded reads |
| Metadata cache | Implemented; URI+expected-digest identity, byte verification, retained-Manifest pins, replay/fetch leases, explicit fail-closed GC and corrupt-entry refetch |
| SemVer 2.0.0 and Registry 1.0.0 constraint parser/evaluator | Implemented; unit-tested |
| Deterministic version/dependency resolution and backtracking | Implemented; unit-tested; Phase 8 branch adds 16,384-candidate, 65,536-declaration, 1,024-component and 100,000-search-state limits |
| Prerelease/withdrawal and contract-provider policies | Implemented; unit-tested |
| Stable duplicate-release provenance across Catalog permutations | Implemented; regression-tested |
| Immutable Git source collection | Implemented; full 40/64-character commit verification, detached checkout and optional verified content-addressed object cache |
| Source-unit cardinality, subdirectory validation and symlink/special-file rejection | Implemented; temporary-repository tests |
| Git hook/prompt/global-config/LFS-smudge/submodule protections | Implemented at Collector command boundary; tests cover key protections |
| Transactional workspace staging and publication | Implemented; same-filesystem staging, lock, no-overwrite checks, cleanup and integration tests |
| Resolved Manifest and Resolution Report serialization | Implemented; embedded-schema validation in Workspace |
| RFC 8785-style canonical serialization and canonical-input verification | Implemented; ordering, escaping, number and rejection tests |
| Manifest replay from a persisted Manifest | Implemented; never re-runs Resolver or executes collected code |
| Git-object cache | Implemented; full commit-ID bare object stores, commit hash + object-graph verification, corruption refetch, Manifest pins, replay leases and fail-closed age/budget GC |
| Update discovery and audit decisions | Implemented in the Phase 7 branch; verifies historical Manifest first, records immutable digest-bound decisions, validates its dedicated schema and does not collect/publish source |
| Deployment firewall enforcement for Git transport | External deployment responsibility; HTTPS host allow-list is not an egress firewall |
| Native Linux/macOS/Windows integration | Full `go test ./...` and Workspace Pipeline passed natively on all three OSes on the qualified pre-default head; default-path matrix reruns on this branch. Parent-directory fsync and uniform crash/power-loss durability are not claimed |
| Bounded Catalog concurrency | Default maximum 4 fetches; native Linux/macOS/Windows, race, cancellation/join, out-of-order deterministic processing, validation/fetch failure, cache-integrity and no-partial-Catalog tests passed before enabling the default |
| Fuzzing | Seeded fuzz targets for JSON/YAML, SemVer/constraints, Catalog loading/cache identities, Resolver graphs, source paths, Git credentials and cache references; required Pipeline smoke campaigns passed |
| Performance/resource budgets | Catalog, Resolver, source-copy and cache-GC benchmark smoke passed; enforced byte/count/concurrency/search/graph/source-file/declaration limits documented in `docs/implementation/resource-budgets.md` |

## Security and portability qualifications

- Catalog HTTP transport resolves and checks public addresses at the dial boundary. Deployments still need firewall/egress controls.
- The Git Collector validates HTTPS host allow-lists but delegates DNS/IP egress enforcement to the deployment boundary. Do not treat a hostname allow-list as a firewall.
- Source symlinks and special files are rejected rather than followed or materialized. Submodules, Git LFS object retrieval and component build execution are not supported.
- Workspace locking coordinates Registry instances that follow the same lock protocol. An unrelated process that ignores the lock can still race to create the destination.
- Same-filesystem directory rename provides the publication boundary used here, but uniform crash-durability and rename semantics across Linux, macOS and Windows still require a cross-platform test matrix.
- Manifest verification checks canonical bytes, embedded schema and semantic provenance. Replay verifies the saved Manifest, recollects the exact recorded commits, compares rebuilt canonical bytes before publication, and never invokes the Resolver.

A successful resolve reports a valid selection graph but does not collect source.
A successful prepare is returned only after Collector, Manifest/Report schema
validation and workspace publication complete. Neither command executes
collected component code.
