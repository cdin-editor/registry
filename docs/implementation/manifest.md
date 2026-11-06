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

## JSON value graph safety

The UTF-8 preflight tracks visited pointer, map and slice identities. Cyclic Go values are not traversed indefinitely; the subsequent JSON encoder returns its normal cycle error. This preserves serializer failure behavior while ensuring invalid UTF-8 is detected before the encoder can replace it.

These checks establish deterministic serialization and canonical-byte handling. A detached SHA-256 detects byte changes when compared with a trusted external receipt; it is not a signature and does not prove publisher authenticity.

## Evidence

Tests validate the constructed Manifest against the embedded Spec-owned schema, reject missing/extra source records, test UTF-16 key ordering, escaping and JCS numeric vectors, verify report array shape/order and prevent Manifest generation from a failed resolution.

## Manifest verification

`Verify` validates a persisted Manifest as a complete public document, not
merely as canonical JSON. It checks the exact canonical byte representation,
the embedded Spec-owned Resolved Manifest schema, the supported Manifest and
Registry versions, and the semantic relationships needed for reproducibility:
unique and strictly ordered identities, Catalog/release metadata URI and
lowercase SHA-256 provenance, full lowercase Git object IDs, HTTPS repository
URIs without embedded credentials, canonical confined workspace paths,
root-to-release identity, selected dependency identity, and dependency-first
resolution order.

The detached SHA-256 argument is optional because the digest is stored outside
the document. When supplied, it must be lowercase hexadecimal SHA-256 and must
match the exact canonical input bytes. Omitting it does not skip canonical,
schema or provenance validation; it only means no external digest comparison
was requested. Verification is local and read-only: it does not contact
Catalogs, fetch Git objects, select releases, collect source or write a
Workspace. A successful verification is not a signature or publisher
authenticity check.

## Manifest replay

`workspace.Replay` first verifies the saved canonical Manifest and optional
external digest, then reconstructs the already-selected graph directly from
that document. It does not invoke the Resolver, load a Product Definition, or
select from current Catalog releases. The Collector retrieves each exact full
Git commit ID recorded in the Manifest, verifies the fetched object and detached
checkout, and materializes only those source units. The generated Manifest is
compared byte-for-byte with the input while still in private staging; a mismatch
aborts publication through the normal Workspace cleanup path.

Replay has two explicit metadata modes:

- **Offline metadata mode (default):** no Catalog Index or Component Release
  Metadata URI is contacted. The saved digests and provenance are verified
  locally, while exact Git commits still need to be retrievable by the injected
  Git runner (for example, from a local repository or an available remote).
  Catalog updates cannot change the selected versions or Manifest bytes.
- **Strict remote metadata mode (opt-in):** the injected MetadataFetcher must
  return the exact Catalog Index and selected Release Metadata bytes recorded
  by the Manifest. Replay checks SHA-256 before schema decoding, validates the
  Catalog identity and selected release entries, then compares release
  source-unit identities, dependency constraints/selected targets and contract
  declarations. A changed Catalog Index or Release document fails closed; it
  never triggers a new resolution.

The replayed `resolution-report.json` is a new, deterministic report of the
pinned replay operation. Its `MANIFEST_REPLAY_PINNED` warning makes clear that
the original version constraints/report evidence are not embedded in the
Resolved Manifest and were not re-evaluated. Replay does not execute collected
component code or promise publisher authenticity, signatures or attestations.

## CLI integration

The `registry manifest verify` command delegates to `manifest.Verify` and is
read-only. The `registry manifest replay` command delegates to
`workspace.Replay`, requires an explicit source-host allow-list, and publishes
through the same transactional Workspace boundary as `prepare`. It accepts
an optional detached SHA-256 receipt and supports offline metadata replay by
default plus opt-in strict revalidation of the exact recorded Catalog/Release
metadata. CLI receipts and stable error codes are documented in
[Manifest CLI operations](cli.md).
