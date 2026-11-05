# Conformance

The Registry must implement normative REG-001 through REG-026 in [CDIN Spec](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).

The first implementation slice covers input schema validation, not resolver or collector conformance.

## Current evidence

| Area | Current verification |
|---|---|
| Schema compilation | Unit tests compile all embedded v1 schemas during validator initialization. |
| Valid documents | Example Product Definition, Catalog Index, Release Metadata, Manifest and Report are tested. |
| Invalid constraints | Unsupported caret ranges are rejected by the Product Definition schema. |
| YAML safety | Duplicate keys, custom tags and multiple documents are rejected. |
| CLI behavior | Schema listing, stdin validation and usage exit codes have integration tests. |

## Not yet verified

Dependency solving, conflict reporting, compatibility provider rules, Git object verification, path traversal protection, staged workspace publication and manifest reproducibility are not implemented. They must receive conformance vectors and integration tests before their requirements can be claimed as verified.

The repository's CI is evidence for the tests it actually runs; it does not prove the complete Registry contract or CDIN product integration.
