# Implementation status

**Stage:** Catalog ingestion and first executable Resolver vertical slice.

The Registry architecture and data contracts are accepted in CDIN Spec. The
repository has a tested CLI/schema-validation foundation, a transport-injected
Catalog Index loader and a deterministic in-memory resolver. The resolver
parses SemVer 2.0.0, applies Registry 1.0.0 constraint grammar, expands
transitive dependencies, intersects constraints, applies prerelease/withdrawal
policy, checks contract providers, detects cycles and returns a deterministic
dependency-first graph with structured conflicts. Git collection, workspace
publication and serialized Manifest/Report output remain unimplemented.

| Capability | Status |
| --- | --- |
| CLI entry point, version, help and schema inspection | Implemented |
| Embedded Registry v1 schemas | Implemented; pinned to a Spec commit |
| JSON Schema compilation and document validation | Implemented |
| JSON duplicate-key rejection and trailing-value rejection | Implemented |
| YAML Product Definition conversion and validation | Implemented |
| YAML duplicate-key, custom-tag, merge-key and recursive-alias rejection | Implemented |
| Pipeline task graph and reusable CI/docs/security/release workflows | Implemented |
| PSX repository-structure checks | Implemented |
| GIX branch-flow configuration | Implemented |
| Catalog Index loading and schema validation | Implemented; injected fetcher |
| SHA-256 verification before metadata parsing | Implemented |
| Release identity consistency and duplicate/conflict detection | Implemented |
| Typed dependency and contract preservation for Resolver | Implemented |
| Production network transport policy, host allow-list and cache | Not implemented |
| SemVer 2.0.0 and Registry 1.0.0 constraint parser/evaluator | Implemented; unit-tested |
| Version/dependency resolution and transitive closure | Implemented; first resolver slice |
| Prerelease/withdrawal and contract-provider policies | Implemented; unit-tested |
| Cycle detection and deterministic resolution order | Implemented; unit-tested |
| Immutable Git object retrieval/verification | Not implemented |
| Safe source subdirectory and symlink containment | Not implemented |
| Transactional workspace staging/publication | Not implemented |
| Serialized Resolved Manifest and Resolution Report | Not implemented; in-memory graph/report values exist |
| RFC 8785 canonical serialization and manifest replay | Not implemented |
| Update discovery | Not implemented |

A successful `registry validate` command proves only that a document is
syntactically safe and conforms to its selected schema. A successful Catalog
load additionally proves fetched metadata bytes match the catalog digest and
metadata conforms to its schema. A successful resolver call proves the
in-memory selected graph satisfies the implemented version/dependency/contract
rules; none of these operations proves that sources have been collected or a
workspace published.
