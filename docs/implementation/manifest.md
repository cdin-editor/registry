# Resolved Manifest and Resolution Report

## Responsibility and references

`internal/manifest` projects a complete resolver graph plus verified Collector results into the public Registry v1 output data models. It never performs selection, fetches sources or publishes workspaces.

- REG-023 — exact releases, sources, dependencies, contracts and workspace paths.
- REG-024 — deterministic Manifest/Report output and canonical JSON.
- REG-025 — failure details without a successful Manifest.
- [Registry contracts §7](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#7-resolved-manifest-and-resolution-report)
- [Registry architecture §5](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#5-source-collection-and-handoff)

## Completeness checks

Manifest `Build` requires:
- A non-nil graph with at least one root and selected component.
- Immutable Catalog Index URI and SHA-256 for every Catalog.
- Immutable release metadata URI and SHA-256 for each selected release.
- A one-to-one match between every declared source unit and successful Collector record.
- Exact full revision match and the canonical workspace-relative source path.
- No extra or duplicate Collector records.

A missing source or missing provenance fails closed: the function returns no Document. This prevents a successful-looking manifest that cannot be replayed.

## Canonical JSON

`MarshalCanonical` emits RFC 8785-style canonical JSON:
- Object keys are ordered by UTF-16 code units.
- Strings use the minimal required JSON escapes and are not HTML escaped.
- Numbers are converted to IEEE-754 binary64 and emitted using shortest round-trip decimal notation with ECMAScript fixed/exponent thresholds.
- Negative zero serializes as zero.
- Non-finite/out-of-range values are rejected.

Set-like arrays in Manifest and Report are sorted by documented identity keys. Topological `resolution_order` and cycle paths preserve their semantic order. Detached SHA-256 is computed over canonical bytes and is not embedded in the document it hashes.

## Evidence

Tests validate the constructed Manifest against the embedded Spec-owned schema, reject missing/extra source records, test UTF-16 key ordering, escaping and JCS numeric vectors, verify report array shape/order and prevent Manifest generation from a failed resolution.
