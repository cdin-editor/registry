# Data contracts

The normative contract is owned by the [CDIN Spec Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md), baseline **1.0.0**.

The five local schema files under schemas/registry/v1 are byte-for-byte mirrors of the Spec schemas:

| Kind | Schema |
|---|---|
| Product Definition | product-definition.schema.json |
| Catalog Index | catalog-index.schema.json |
| Component Release Metadata | component-release.schema.json |
| Resolved Manifest | resolved-manifest.schema.json |
| Resolution Report | resolution-report.schema.json |

Do not edit the local copies independently. Update the Spec contract first, then synchronize this directory from the matching Spec revision.

Product Definitions may be authored as YAML 1.2 or JSON. The JSON data model is canonical. Registry accepts YAML only for Product Definitions, rejects duplicate mapping keys and unsupported tags, then validates the resulting JSON-compatible value against the same v1 JSON Schema. Other document kinds are JSON.

The CLI currently implements schema validation only. It does not yet implement catalog resolution, release eligibility, dependency solving, source retrieval or manifest generation.
