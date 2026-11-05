# Conformance

The Registry must implement normative `REG-001` through `REG-026` in the [CDIN Spec Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).

The current vertical slice validates external documents against the v1 schemas. It does not claim resolver, collector or workspace conformance.

## Current evidence

| Area | Verification |
|---|---|
| Schema compilation | Go tests compile every embedded v1 schema through the production validator. |
| Valid documents | Example Product Definition, Catalog Index, Component Release Metadata, Manifest and Report are tested. |
| JSON parser safety | Duplicate object keys, malformed input and trailing JSON values are rejected. |
| YAML parser safety | Duplicate keys, non-string keys, custom tags, merge keys, recursive aliases and multiple documents are rejected. |
| Schema constraints | Unsupported version ranges and malformed component identifiers are rejected. |
| CLI behavior | Schema listing/display, stdin validation and error exit codes have integration tests. |
| Schema source integrity | A task downloads the exact Spec commit and compares every local schema byte-for-byte. |
| Repository boundaries | Pipeline invokes PSX and the explicit package/consumer-independence boundary script. |

## Not yet verified

Dependency solving, contract-provider selection, catalog digest verification, Git object verification, path traversal protection, transactional workspace publication and manifest reproducibility are not implemented. They must receive conformance vectors and integration tests before those requirements can be claimed as verified.

CI evidence applies only to commands and platforms actually run. A green foundation gate is not proof of complete Registry conformance or CDIN product integration.
