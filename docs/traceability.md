# Traceability

Each row below describes behavior currently present in code and evidence
currently present in tests. “Schema-defined” is not treated as “runtime
implemented”.

| Spec requirement | Current implementation | Evidence |
| --- | --- | --- |
| REG-001 | Product Definition is consumed as declarative input; no product-specific execution exists. | `internal/validation`; `internal/resolver` |
| REG-002 | Validation, Catalog and Resolver packages contain no consumer-identity branch; boundary checks must expand as Collector/Workspace arrive. | `scripts/check-boundaries.sh`; `internal/resolver` |
| REG-003 | Component/variant identities are checked at Resolver input; source-unit IDs are checked at candidate validation. | `internal/resolver/resolver.go`; `TestREG003REG004NormalizesDefaultAndRejectsUnstableIDs` |
| REG-004 | Omitted variants normalize to `default` in Catalog ingestion and resolver root/dependency/release keys. | `internal/catalog/catalog.go`; `TestLoadVerifiesDigestBeforeReleaseValidation`; `TestREG003REG004NormalizesDefaultAndRejectsUnstableIDs` |
| REG-005 | Five versioned schemas are embedded/compiled; Catalog Index and Component Release input are validated by the schema validator. | `internal/schemas`; `internal/validation`; `internal/catalog` |
| REG-006 | SemVer 2.0.0 parser and precedence, including arbitrary-width numeric identifiers and build-metadata handling. | `internal/semver`; `TestREG006SemVerPrecedence`; `TestREG006RejectsInvalidSemVer`; `TestREG006SupportsUnboundedNumericIdentifiers` |
| REG-007 | Exact/conjunction grammar and comparator evaluation. | `internal/semver`; `TestREG007ConstraintGrammar`; `TestREG007ConstraintEvaluation` |
| REG-008 | Resolver expands selected releases' required dependencies transitively and backtracks from unsatisfiable branches. | `internal/resolver`; `TestREG008REG009SelectsHighestAndExpandsTransitiveDependencies`; `TestREG008BacktracksAcrossTransitiveConstraintConflicts` |
| REG-009 | Resolver selects one release per normalized component/variant key. | `internal/resolver.Graph`; resolver selection tests |
| REG-010 | Constraints for a key are accumulated; empty intersections and candidate incompatibility fail structurally. | `internal/resolver`; `TestREG010IntersectsRootAndDependencyConstraints`; `TestREG010ReportsEmptyConstraintIntersection` |
| REG-011 | Selection is SemVer-descending and catalog-order independent; distinct equal-precedence metadata identities fail. | `internal/resolver`; `TestREG011IsIndependentOfCatalogOrder`; `TestREG011RejectsEqualPrecedenceMetadataIdentities`; `TestREG011RejectsFallbackTie` |
| REG-012 | Prereleases are filtered unless Product Definition policy explicitly allows them. | `internal/resolver`; `TestREG012REG013AppliesPrereleaseAndWithdrawnPolicies` |
| REG-013 | Withdrawn releases are excluded from new resolution. | `internal/resolver`; `TestREG012REG013AppliesPrereleaseAndWithdrawnPolicies` |
| REG-014 | Cycles fail with explicit cycle path; successful graphs use deterministic dependency-first order. | `internal/resolver`; `TestREG014CycleReportsExactPath`; `topologicalOrder` |
| REG-015 | Component and target-environment contract declarations are matched by opaque ID and SemVer constraints. | `internal/resolver`; `TestREG015REG016MatchesExactlyOneProvider` |
| REG-016 | Missing and ambiguous contract providers produce distinct structured conflict codes. | `internal/resolver`; `TestREG015REG016MatchesExactlyOneProvider`; `TestREG016ReportsMissingContractProvider` |
| REG-017 | Catalog model preserves multiple source units; actual source materialization remains pending. | `internal/catalog`; `internal/resolver`; Collector pending |
| REG-018 | Schema and Resolver enforce full-length hexadecimal revision shape; checking the retrieved Git commit object remains pending. | `schemas/registry/v1/component-release.schema.json`; `internal/resolver`; Collector pending |
| REG-019 | Exact fetched metadata bytes are SHA-256 checked before release validation and JSON decoding. | `internal/catalog/catalog.go:Load`; `TestLoadRejectsDigestMismatchBeforeParsing` |
| REG-020 | Source subdirectory/workspace path confinement and symlink safety remain pending with Collector/Workspace. | Implementation roadmap Phases 4/5 |
| REG-021 | Canonical workspace layout and publication remain pending. | Implementation roadmap Phase 5 |
| REG-022 | Transactional all-or-nothing workspace publication remains pending. | Implementation roadmap Phase 5 |
| REG-023 | Resolver returns a typed in-memory graph containing selected release/source/dependency/contract data; serialized Manifest remains pending. | `internal/resolver.Graph`; implementation roadmap Phase 5 |
| REG-024 | Resolver ordering is deterministic; canonical Manifest serialization and JCS remain pending. | `internal/resolver.topologicalOrder`; implementation roadmap Phase 5 |
| REG-025 | Resolver returns structured success/failure reports and no Graph on resolution failure; schema-conformant serialization remains pending. | `internal/resolver.Report`; `Resolve`; resolver failure tests |
| REG-026 | JSON/YAML metadata is parsed as data; component scripts/hooks are not executed. Real Git materialization/fuzz evidence remains pending. | `internal/validation`; `internal/catalog`; roadmap Phase 4 |

This table separates executable resolver behavior from still-pending Collector,
Workspace and serialized-output behavior. Schema presence, interfaces, comments
and documentation do not establish runtime conformance by themselves.
