# Implementation status

**Stage:** Phase 9 stabilization is implemented on
`chore/phase9-conformance-and-stabilization`. Registry Core 1.0 is a release
candidate only after the branch's required CI, Documentation/YAML, Security
Scan and native Linux/macOS/Windows workflows pass and the changes are merged
through the protected branch flow.

The engine is a standalone, consumer-independent Go module. It validates
declarative Product Definitions and Catalog metadata, resolves version,
dependency and contract constraints, collects immutable Git source units, and
publishes transactional Workspaces with canonical Manifests and Reports.
Manifest verification, exact-source replay, update discovery and integrity-
checked metadata/Git caches are part of the CLI. Registry never executes
collected component code and does not depend on Kernel, Runtime, Backend or
Frontend.

The Phase 8 baseline on `develop` was
`f12c0eb48290b356c0ca373507874ac8b84b7954`; CI, Documentation/YAML, Security
Scan and native Workspace jobs passed on that exact head. Phase 9 adds a
clean-room Product Definition → Catalog → Resolver → Prepare → Verify → Replay
integration test and reconciles the documentation/traceability set. Its
workflow results must be read from the Phase 9 branch PR, not inferred from
the previous develop run.

## Capability status

|Capability|Verified implementation|
|---|---|
|Standalone module, CLI, package boundaries|Implemented; Pipeline governance and consumer-independence checks|
|Public document schemas|Six Spec-owned schemas embedded and synchronized from the pinned Spec revision, including Update Discovery Report|
|JSON/YAML input|Strict decoding; duplicate keys, trailing values, unsafe YAML forms and unsupported YAML document kinds rejected|
|Catalog ingestion|Digest-before-parse, identity checks, deduplication, all-or-nothing loading and offline fixture coverage|
|HTTPS metadata transport|Exact-host allow-list, redirect revalidation, DNS/public-address validation at dial time, bounded bodies/timeouts and safe error classes|
|Metadata cache|URI+expected-digest identity, verify-before-use, Manifest pins, replay/fetch leases and fail-closed age/budget GC|
|SemVer and constraints|SemVer 2.0.0 parsing/precedence and Registry contract constraint grammar|
|Resolver|Deterministic transitive closure, backtracking, contract-provider matching, typed conflict evidence and fail-closed resource limits|
|Git Collector|Full commit object identity, detached checkout, minimal environment, explicit credential provider, cancellation and source-tree validation|
|Git-object cache|Full-ID content addressing, commit hash and reachable object-graph verification, corruption refetch, Manifest pins, replay leases and fail-closed GC|
|Workspace|Private staging, failure-injection rollback, native atomic no-replace publication and platform-specific private permissions|
|Manifest/Report|Typed models, schema validation, deterministic JCS-style bytes and detached SHA-256|
|Manifest verify/replay|Persisted Manifest verification and exact-commit replay without rerunning Resolver; byte-parity tested|
|Update discovery|Separate schema-validated audit report; historical Manifest/Workspace remain immutable|
|Catalog metadata concurrency|`Load` uses a fixed four-worker default (hard maximum four); callers may choose one to three workers; output/error processing remains Catalog Index ordered|
|Fuzzing / race|Seeded fuzz smoke campaigns and `go test -race ./...` in the required Pipeline gate|
|Performance / resource budgets|Benchmark smoke plus enforced Catalog, Resolver, source and cache budgets|
|Native platform support|All Registry Go packages and Workspace integration run natively on Ubuntu, macOS and Windows|
|Phase 9 end-to-end|Test drives Product Definition YAML through real schema validation, Catalog loading, Resolver, Prepare, Manifest Verify and byte-identical Replay using a temporary local Git repository|

## Security and portability qualifications

- Catalog metadata and Git source hosts require explicit policy. A hostname allow-list and Registry DNS checks are not a deployment firewall; production operators must enforce network egress restrictions.
- Public Catalog/Git retrieval is unauthenticated by default. A future private-source provider must remain explicit and non-interactive.
- Collected symlinks and special files are rejected. Git submodule contents and Git LFS objects are not fetched/materialized.
- Source files and Resolver/Catalog inputs have enforced count/byte/search limits. Git fetching and Source Unit copying remain sequential; only Catalog Release Metadata retrieval uses bounded concurrency.
- Cache roots use private Unix modes or protected Windows DACLs. The caller-selected parent directory remains a separate trust boundary.
- Workspace locks coordinate cooperating Registry processes. Parent-directory fsync and uniform crash/power-loss durability are not implemented.
- SHA-256 protects byte identity; it is not a publisher signature, attestation or provenance authority.
- Registry does not generate publisher signatures/attestations/SBOMs, scan downloaded component vulnerabilities, sandbox CDIN build execution or run component code.

## Release and evidence policy

A passing workflow proves only the tasks and platforms it actually ran. For this
branch, the acceptance record is the branch-head PR workflow set plus the named
tests in [Traceability](traceability.md) and [Conformance](testing/conformance.md).
Do not use the develop baseline's green status as evidence for unrun Phase 9
changes.
