# Implementation status

**Stage:** Catalog ingestion — first executable catalog vertical slice.

The Registry architecture and data contracts are accepted in CDIN Spec. The repository has a tested CLI/schema-validation foundation and now includes a transport-injected Catalog Index loader. The loader validates the index, verifies the SHA-256 digest over the exact fetched metadata bytes before decoding, validates release metadata, normalizes omitted variants and rejects identity mismatches/conflicting metadata. Resolver, source collector and workspace publication remain unimplemented.

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
| Network transport policy and host allow-list | Not implemented; fetcher responsibility |
| Version/dependency/contract resolution | Not implemented |
| Immutable Git source collection | Not implemented |
| Workspace staging/publication | Not implemented |
| Manifest/report generation | Not implemented |
| Update discovery | Not implemented |

A successful `registry validate` command proves only that a document is syntactically safe and conforms to its selected schema. A successful Catalog load additionally proves the fetched metadata bytes match the catalog digest and the metadata conforms to its schema; neither operation proves that a component set is compatible or can be assembled.
