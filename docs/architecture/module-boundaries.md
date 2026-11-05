# Module boundaries

The Registry implementation uses Go's `cmd/` and `internal/` layout. Package
boundaries follow the responsibility split in CDIN Spec and distinguish the
implemented vertical slices from future modules.

| Package | Owns | Must not own |
| --- | --- | --- |
| `cmd/registry` | Process entry point and exit code | Resolver algorithms or filesystem policy |
| `internal/cli` | Commands, arguments, streams and user-facing diagnostics | Product-specific behavior |
| `internal/validation` | Document decoding and schema validation | Catalog fetching or source checkout |
| `internal/schemas` | Embedded, versioned schema files | A second schema definition |
| `internal/catalog` | Catalog Index loading, digest integrity, identity and typed release declarations | Version-selection policy, Git checkout |
| `internal/semver` | SemVer 2.0.0 and contract 1.0.0 constraints | Catalog access, product policy, I/O |
| `internal/resolver` | Version/dependency/contract solving and in-memory graph/report | Network/Git/filesystem/CLI operations |
| Future `internal/collector` | Exact-revision source collection | Version selection or code execution |
| Future `internal/workspace` | Staging and publication of prepared workspace outputs | Product builds and scripts |
| Future `internal/manifest` | Contract-conformant Manifest/Report projection and canonical serialization | Resolver candidate selection or workspace mutation |

## Dependency direction

CLI/composition is the outer adapter. It validates inputs and injects transport
boundaries into internal packages. Core behavior must not import CLI or read
process arguments. Resolver consumes typed Catalog values and SemVer; it does
not call Catalog transport, Git, or workspace code.

Collector will consume a resolved graph and an injected Git boundary. Workspace
will consume verified source trees and typed output values. Neither may trigger
component builds or hooks. These boundaries keep Registry generic and prevent
consumer identity or product workflows leaking into infrastructure.
