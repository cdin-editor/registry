# Traceability

| Spec requirement | Current implementation | Evidence |
| --- | --- | --- |
| REG-001 | Product Definition is parsed as declarative data; no product-specific execution exists. | `internal/validation` |
| REG-002 | No consumer-identity branching is introduced in the validation/catalog packages; full engine conformance remains pending. | `scripts/check-boundaries.sh`; resolver/collector not implemented |
| REG-004 | Catalog loader normalizes an omitted variant to `default` before identity checks. | `internal/catalog/catalog.go:Load`; `TestLoadVerifiesDigestBeforeReleaseValidation` |
| REG-005 | Five versioned schemas are embedded and compiled; catalog loader invokes schema validation for index and release metadata. | `internal/schemas`, `internal/validation`, `internal/catalog` |
| REG-019 | Exact fetched metadata bytes are SHA-256 checked before release validation and JSON decoding. | `internal/catalog/catalog.go:Load`; `TestLoadRejectsDigestMismatchBeforeParsing` |
| REG-003, REG-006–REG-018, REG-020–REG-026 | Not yet implemented or not covered by executable conformance evidence. | [Implementation roadmap](development/implementation-roadmap.md) |

The catalog loader is a partial implementation slice, not full Registry conformance. This table records behavior and test evidence only; schema presence, interfaces and documentation do not establish runtime conformance.
