# Implementation status

**Stage:** Executable Registry preparation vertical slice; not yet complete Registry 1.0 conformance.

The repository now has more than an in-memory Resolver: it includes strict
Catalog transport, deterministic dependency and contract resolution, immutable
Git source collection, transactional workspace staging/publication, canonical
Manifest/Report serialization, and CLI resolve/prepare composition. These
packages remain generic and are composed at the Registry CLI boundary; collected
component code is never executed.

The current implementation has passed the repository Pipeline quality gate,
including formatting, go vet, all Go tests, governance, build, module
boundaries, schema synchronization, PSX structure checks, release checks,
race tests and git diff --check. This is evidence for the code on the tested
feature-branch head, not proof of every REG-001–REG-026 deployment or
cross-platform requirement.

| Capability | Status |
| --- | --- |
| CLI entry point, version, help and schema inspection | Implemented |
| validate command and embedded Registry v1 schemas | Implemented; schemas pinned to a Spec commit |
| Strict JSON duplicate-key/trailing-value rejection | Implemented |
| Product Definition YAML normalization and strict YAML rejection rules | Implemented |
| resolve command: explicit Product Definition, Catalogs and host policy; report only | Implemented; command composition and core packages tested |
| prepare command: resolve, collect, validate outputs and publish | Implemented; underlying workspace integration tested with temporary Git repositories |
| Catalog Index loading and schema validation | Implemented; all-or-nothing loader |
| Production HTTPS metadata transport | Implemented; explicit exact-host allow-list, redirect revalidation, DNS/public-address checks and bounded reads |
| Metadata cache | Implemented; URI+expected-digest identity, byte verification, restrictive files and corrupt-entry refetch |
| SemVer 2.0.0 and Registry 1.0.0 constraint parser/evaluator | Implemented; unit-tested |
| Deterministic version/dependency resolution and backtracking | Implemented; unit-tested |
| Prerelease/withdrawal and contract-provider policies | Implemented; unit-tested |
| Stable duplicate-release provenance across Catalog permutations | Implemented; regression-tested |
| Immutable Git source collection | Implemented; full 40/64-character commit verification and detached checkout |
| Source-unit cardinality, subdirectory validation and symlink/special-file rejection | Implemented; temporary-repository tests |
| Git hook/prompt/global-config/LFS-smudge/submodule protections | Implemented at Collector command boundary; tests cover key protections |
| Transactional workspace staging and publication | Implemented; same-filesystem staging, lock, no-overwrite checks, cleanup and integration tests |
| Resolved Manifest and Resolution Report serialization | Implemented; embedded-schema validation in Workspace |
| RFC 8785-style canonical serialization and canonical-input verification | Implemented; ordering, escaping, number and rejection tests |
| Manifest replay from a persisted Manifest | Not implemented |
| Git-object cache/eviction and update discovery | Not implemented |
| Deployment firewall enforcement for Git transport | External deployment responsibility; HTTPS host allow-list is not an egress firewall |
| Cross-platform Linux/macOS/Windows integration matrix and crash-durability proof | Not yet established |

## Security and portability qualifications

- Catalog HTTP transport resolves and checks public addresses at the dial boundary. Deployments still need firewall/egress controls.
- The Git Collector validates HTTPS host allow-lists but delegates DNS/IP egress enforcement to the deployment boundary. Do not treat a hostname allow-list as a firewall.
- Source symlinks and special files are rejected rather than followed or materialized. Submodules, Git LFS object retrieval and component build execution are not supported.
- Workspace locking coordinates Registry instances that follow the same lock protocol. An unrelated process that ignores the lock can still race to create the destination.
- Same-filesystem directory rename provides the publication boundary used here, but uniform crash-durability and rename semantics across Linux, macOS and Windows still require a cross-platform test matrix.
- The canonical-input verifier verifies byte-level JCS representation; it is not a Manifest replay implementation.

A successful resolve reports a valid selection graph but does not collect source.
A successful prepare is returned only after Collector, Manifest/Report schema
validation and workspace publication complete. Neither command executes
collected component code.
