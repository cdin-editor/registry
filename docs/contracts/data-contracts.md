# Data contracts

The normative contract is owned by [CDIN Spec Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md), baseline **1.0.0**.

The five local schema files under `schemas/registry/v1/` are exact mirrors of the accepted Spec revision recorded in [SOURCE.toml](../../schemas/registry/v1/SOURCE.toml).

| Kind | Schema |
| --- | --- |
| Product Definition | `product-definition.schema.json` |
| Catalog Index | `catalog-index.schema.json` |
| Component Release Metadata | `component-release.schema.json` |
| Resolved Manifest | `resolved-manifest.schema.json` |
| Resolution Report | `resolution-report.schema.json` |

Do not edit local copies independently. Update the Spec contract first, then advance the pinned Spec revision and synchronize through `scripts/sync-spec-schemas.sh --update`.

Product Definitions may be authored as YAML 1.2 or JSON. The JSON data model is canonical. JSON object keys must be unique. Registry accepts YAML only for Product Definitions, rejects duplicate/non-string keys, custom tags, merge keys, recursive aliases and multiple documents, converts to JSON-compatible values, then validates against the same v1 JSON Schema. Other document kinds are JSON.

The CLI currently implements schema validation only. It does not yet implement catalog resolution, dependency solving, source retrieval or manifest generation.
