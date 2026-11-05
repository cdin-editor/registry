# Conformance

Registry must implement normative `REG-001` through `REG-026` in the
[CDIN Spec Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).

This page separates verified behavior from schema presence and future work.
A type, interface, schema, comment or document is not conformance evidence
without executable behavior and tests.

## Current evidence

| Area | Verification |
| --- | --- |
| Schema compilation | Go tests compile every embedded v1 schema through the production validator. |
| Valid documents | Example Product Definition, Catalog Index, Component Release Metadata, Manifest and Report are tested. |
| JSON parser safety | Duplicate object keys, malformed input and trailing JSON values are rejected. |
| YAML parser safety | Duplicate keys, non-string keys, custom tags, merge keys, recursive aliases and multiple documents are rejected. |
| Catalog integrity | Exact fetched metadata bytes are SHA-256 verified before Component Release validation/decoding. |
| Catalog identity | Default variant normalization, release identity matching, identical deduplication and conflict rejection are tested. |
| SemVer | SemVer 2.0.0 precedence, prerelease ordering, build metadata, malformed forms and unbounded numeric identifiers are tested. |
| Constraint grammar | Contract 1.0.0 exact/comparator-conjunction grammar, rejected syntax and boundary evaluation are tested. |
| Dependency resolution | Transitive closure, backtracking, repeated constraints and empty intersections are tested. |
| Selection policy | Prerelease/withdrawn policy and catalog permutation invariance are tested. |
| Contracts | Component/environment providers, missing providers and ambiguity are tested. |
| Graph | Cycle path, deterministic dependency-first output and failure-without-graph are tested. |
| Repository boundaries | Pipeline invokes PSX and package/consumer-independence boundary checks. |

## Not yet verified or implemented

- Production HTTP Catalog transport, allow-list/redirect/SSRF enforcement, response bounds and cache integrity.
- Complete adversarial Resolver conformance matrix, fuzz vectors and large-graph performance bounds.
- Actual Git commit object retrieval and verification.
- Source subdirectory/symlink path confinement and no-execution guarantees over real source trees.
- Transactional workspace staging, atomic publication and failure cleanup.
- Serialized Resolved Manifest and Resolution Report against v1 schemas.
- RFC 8785 canonical JSON, manifest replay and update discovery.
- End-to-end clean-room preparation across Catalog → Resolver → Collector → Workspace.

CI evidence applies only to commands/platforms actually run. The first Resolver
slice does not prove Collector, Workspace, Manifest serialization or full
REG-001–REG-026 conformance.
