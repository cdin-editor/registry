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

## Deterministic evidence and Unicode handling

`MarshalReport` sorts every top-level set-like report collection, including applied constraints by component, variant, constraint and source identity. It preserves graph sequences whose order is semantic.

`MarshalCanonical` rejects invalid UTF-8 before Go's JSON encoder can replace invalid byte sequences. Canonicalization does not normalize Unicode. The persisted-input parser rejects unpaired surrogate escapes because their decoded replacement character cannot reproduce the supplied canonical bytes.

Golden tests pin complete Manifest and Report bytes plus detached SHA-256 digests. They repeat serialization and permute equivalent Catalog, root, component, source-unit, dependency, contract and report inputs. RFC 8785 tests cover the section 3.2.3 UTF-16 property-order vector and Appendix B IEEE-754 bit-pattern examples, including subnormals, negative zero, exponent thresholds and non-finite values.

These checks establish deterministic serialization and canonical-byte handling; they do not verify an external detached digest, prove authenticity, or implement Manifest verification/replay.

## Evidence

Tests validate the constructed Manifest against the embedded Spec-owned schema, reject missing/extra source records, test UTF-16 key ordering, escaping and JCS numeric vectors, verify report array shape/order and prevent Manifest generation from a failed resolution.


## JSON value graph safety

The UTF-8 preflight tracks visited pointer, map and slice identities. Cyclic Go values are not traversed indefinitely; the subsequent JSON encoder returns its normal cycle error. This preserves the serializer's failure behavior while ensuring invalid UTF-8 is detected before the encoder can replace it.
