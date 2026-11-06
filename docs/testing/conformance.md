# Conformance

The normative baseline is [Registry Data Contracts 1.2.0](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md),
covering REG-001 through REG-027. The architecture boundary is defined in
[Registry architecture](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md).

A schema, interface, comment or passing unrelated workflow is not conformance
evidence. Each requirement is mapped to implementation behavior and named tests
in [Traceability](../traceability.md).

## Executable verification

| Boundary | Evidence |
|---|---|
| Schema inventory | Six embedded Spec-owned v1 schemas compile with the production validator; schema synchronization checks the pinned Spec revision |
| JSON/YAML | Strict duplicate-key/trailing-value rejection; YAML aliases, merge keys, custom tags, multiple documents and non-string keys are rejected |
| Declarative Product Definition | Validated/normalized before Resolver consumption; clean-room test changes Product identity/name while retaining equivalent resolution |
| Catalog loading | Local Catalog fixtures and production loader exercise digest-before-parse, schema, identity, duplicate/conflict and all-or-nothing behavior |
| HTTP transport | Timeout/stall, truncated body, redirect, mixed DNS, dialed-IP, response-size, digest/cache and redaction tests |
| Resolver | SemVer, constraints, transitive closure, backtracking, provider matching, cycle paths, stable conflict evidence, permutation and search-limit tests |
| Collector | Temporary Git repositories prove full commit verification, detached checkout, source-unit path layout, unsafe-tree rejection, process isolation and cancellation cleanup |
| Workspace | Failure injection covers staged sources, Manifest/Report write+sync, cancellation, locks, destination races, cleanup and atomic no-replace publication |
| Manifest | Canonical bytes, JCS vectors, schema/provenance/path verification and detached digest checks |
| Replay | Saved Manifest drives exact-commit recollection without Resolver selection; output Manifest bytes must equal the input before publication |
| Update discovery | Historical Manifest is verified before current Catalog loading; deterministic reason-coded audit report is schema-validated and historical files remain unchanged |
| Metadata/Git caches | Verify-before-use, corruption refetch, pins, leases, bounded retention/eviction and fail-closed reference-snapshot GC |
| Bounded Catalog loading | Four-worker maximum/default, configurable lower count, deterministic index-order processing, cancellation/join, no partial Catalog and byte/count limits |
| Fuzzing | Required short seeded fuzz smoke campaigns cover parser, Catalog, Resolver, source path, credential and cache identity trust boundaries |
| Race / platforms | Required race task plus native Workspace and all-package tests on Ubuntu, macOS and Windows |
| Performance / budgets | One-iteration Catalog, Resolver, source-copy and cache-GC benchmark smoke; deterministic resource ceilings are hard gates |
| End-to-end clean-room flow | `TestRegistryCleanRoomPrepareVerifyReplay` uses real schemas, local Catalog bytes and a temporary Git repository to exercise Product Definition → Catalog → Resolver → Prepare → Verify → Replay and compare canonical Manifest/source bytes |

## Deliberate non-guarantees

- No parent-directory fsync or uniform crash/power-loss durability guarantee.
- No publisher signing, signature verification, cryptographic attestation or SBOM.
- No vulnerability scan or license/policy assessment of downloaded components.
- No component build sandbox, runtime isolation or component-code execution.
- No Registry-enforced deployment firewall or egress policy.
- No Git submodule or LFS object retrieval.
- Git source fetching and Source Unit copying remain sequential; bounded concurrency applies only to Catalog metadata.
- Longer sustained fuzz campaigns, workload-sized performance baselines and broader concurrent update/replay/GC stress remain additional operational hardening.

CI applies only to its exact branch head and executed jobs. The Phase 9 PR
workflows are the acceptance evidence for these documentation and test changes;
previous green results on develop are not substituted for them.
