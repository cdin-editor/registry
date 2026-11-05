# Module boundaries

The Registry implementation uses Go's cmd/ and internal/ layout. Its package boundaries follow the responsibility split in CDIN Spec without pretending that planned modules are already implemented.

| Package | Owns | Must not own |
| --- | --- | --- |
| cmd/registry | Process entry point and exit code | Resolver or filesystem policy |
| internal/cli | Commands, arguments, streams and user-facing diagnostics | Product-specific behavior |
| internal/validation | Document decoding and schema validation | Catalog fetching or source checkout |
| internal/schemas | Embedded, versioned schema files | A second schema definition |
| Future internal/catalog | Catalog loading, metadata integrity and catalog identity | Dependency selection policy |
| Future internal/resolver | Version, dependency and contract solving | Git/network retrieval |
| Future internal/collector | Exact-revision source collection | Version selection or build execution |
| Future internal/workspace | Staging and publication of prepared workspace outputs | Product builds and scripts |

## Dependency direction

The CLI is the outer adapter. It calls internal packages. Core behavior must not import the CLI package. Future catalog, resolver, collector and workspace packages should exchange typed data and interfaces, not invoke each other through command parsing.

Registry must treat metadata as untrusted data. It must never evaluate Product Definitions or component release metadata as code, templates or plug-ins.
