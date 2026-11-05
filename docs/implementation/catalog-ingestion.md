# Catalog ingestion

## Scope

The catalog package loads one Catalog Index and materializes the referenced Component Release Metadata as validated, immutable input records. It is deliberately transport-agnostic: callers provide a `Fetcher`, so transport, authentication and host policy remain outside the contract parser.

## Processing order

1. Validate the Catalog Index against the embedded Registry v1 schema.
2. Normalize an omitted variant to `default` before constructing the release identity.
3. Fetch the exact metadata bytes from the declared URI.
4. Compute SHA-256 over those exact bytes and compare it with the index digest.
5. Only after digest verification, validate the metadata against the Component Release schema and decode it.
6. Verify that component, normalized variant and version match the index entry.
7. Deduplicate byte-identical metadata for the same identity; reject a conflicting digest for that identity.

The loader returns no partially populated Catalog when any entry fails. The input bytes are copied into the returned release record so later caller mutation cannot change the validated payload.

## Security boundary

The package does not execute metadata and does not implement network policy. A production Fetcher must impose request timeouts, response-size limits, redirect policy, host allow-lists and credential isolation. In particular, untrusted catalog URIs must not be fetched by an unrestricted HTTP client. The transport adapter is intentionally injected rather than hidden inside the schema/contract layer.

## Evidence

| Behavior | Implementation | Tests |
| --- | --- | --- |
| Index schema validation | `internal/catalog/catalog.go:Load` | `TestLoadVerifiesDigestBeforeReleaseValidation` |
| SHA-256 before release parsing | `internal/catalog/catalog.go:Load` | `TestLoadRejectsDigestMismatchBeforeParsing` |
| Identity consistency and default variant | `internal/catalog/catalog.go:Load` | `TestLoadRejectsIndexReleaseIdentityMismatch`, `TestLoadVerifiesDigestBeforeReleaseValidation` |
| Duplicate/conflict policy | `internal/catalog/catalog.go:Load` | `TestLoadDeduplicatesIdenticalEntriesAndRejectsConflicts` |
| Cancellation and required dependencies | `internal/catalog/catalog.go:Load` | `TestLoadHonorsCancellationAndRequiredDependencies` |

## Normative references

- [Registry architecture — public inputs and outputs, resolution and security](https://github.com/cdin-editor/spec/blob/14734091dfe80118e4c3c7180782ba24144524e0/docs/architecture/registry.md#L20-L85)
- [Registry data contracts — Catalog Index and release metadata, lines 60–71](https://github.com/cdin-editor/spec/blob/14734091dfe80118e4c3c7180782ba24144524e0/docs/architecture/registry-contracts.md#L60-L71)
- [Registry data contracts — digest and source identity requirements, REG-017–REG-020, lines 159–162](https://github.com/cdin-editor/spec/blob/14734091dfe80118e4c3c7180782ba24144524e0/docs/architecture/registry-contracts.md#L159-L162)
- [Registry data contracts — untrusted metadata, lines 123–128](https://github.com/cdin-editor/spec/blob/14734091dfe80118e4c3c7180782ba24144524e0/docs/architecture/registry-contracts.md#L123-L128)

This document describes only the Catalog ingestion vertical slice. It does not claim resolver, collector, workspace or update-discovery conformance.
