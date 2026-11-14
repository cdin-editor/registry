# Component Registry

Registry is a generic, consumer-independent component delivery engine. It turns declarative product requirements and one or more Catalog Indexes into a reproducible source Workspace, a Resolved Manifest, and a Resolution Report.

A product supplies **what it needs**. Component publishers supply immutable **release metadata**. A Catalog Index makes those releases discoverable. Registry validates the inputs, resolves versions and contracts, collects the exact declared source revisions, and prepares the Workspace.

Registry does not infer component identity from repository names, inspect a consumer's source tree, or contain product-specific component lists and workflows. Component and repository topology is described only by the published Release Metadata data. The same engine can serve unrelated products without product-specific code.

## Responsibilities

- **Component publishers** own component source, package versions, tests, and immutable Component Release Metadata.
- **Catalog publishers** maintain Catalog Index data that references immutable Release Metadata by URI and SHA-256 digest.
- **Consumers** provide Product Definitions and own integration, builds, product tests, packaging, and launch.
- **Registry** owns Catalog ingestion, metadata validation, dependency and contract resolution, update discovery, immutable source collection, Workspace preparation, and Manifest/report generation.

Registry is a build-time delivery-plane tool. It does not compile or execute collected source, and it is not a runtime dependency of a product or component.

## Catalog data in this repository

Version-controlled Catalog Index snapshots may be stored under:

`catalogs/<catalog-id>/catalog-index.json`

For example, this repository carries the `cdin-components` Catalog at `catalogs/cdin-components/catalog-index.json`. It is data consumed by the generic Registry engine, not a CDIN-specific code path. A consumer can pin a Registry commit and pass the Catalog path from that exact checkout to the CLI. That makes both the engine and the catalog snapshot reproducible without copying repository URLs or release metadata references into the consumer's Composition Root.

The Catalog Index contains only component identity, variant, package version, immutable Release Metadata URI, and metadata digest. Source repository URIs and full Git commit IDs remain in component-owned Release Metadata.

## Typical workflow

1. A component maintainer publishes an immutable Release Metadata document for a release.
2. The Catalog publisher adds a Catalog Index entry containing that metadata URI and its SHA-256 digest.
3. A consumer supplies a Product Definition describing its root components and version constraints.
4. Registry reads the Catalog Index, fetches and verifies Release Metadata, then resolves dependencies and contracts.
5. Registry collects exact Git revisions and publishes a Workspace, Resolved Manifest, and Resolution Report.
6. The consumer builds and tests the prepared Workspace.

## Quick start

Build Registry and inspect its commands:

```sh
make build
./build/registry help
```

Validate a Product Definition:

```sh
./build/registry validate product-definition ./product-definition.yaml
```

Resolve against a Catalog:

```sh
./build/registry resolve \
  --product ./product-definition.yaml \
  --catalog ./catalogs/cdin-components/catalog-index.json \
  --allow-host raw.githubusercontent.com \
  --allow-host github.com
```

Prepare a Workspace:

```sh
./build/registry prepare \
  --product ./product-definition.yaml \
  --catalog ./catalogs/cdin-components/catalog-index.json \
  --allow-host raw.githubusercontent.com \
  --allow-host github.com \
  --output ./build/prepared
```

The example Catalog path is a local file. Registry itself performs the remote metadata and Git retrieval. Explicit `--allow-host` entries are transport security policy; they are not component mappings or product configuration. The output directory must not already exist.

## Learn Registry

- [Getting started](docs/getting-started.md)
- [Concepts](docs/concepts.md)
- [Using Registry](docs/using-registry.md)
- [Product Definitions](docs/product-definition.md)
- [Catalogs and releases](docs/catalogs.md)
- [Commands](docs/commands.md)
- [Outputs](docs/outputs.md)
- [Examples](docs/examples.md)
- [Limitations and security](docs/limitations.md)
- [Development](docs/development.md)
- [Registry 1.0.0 release readiness](docs/release-readiness-1.0.0.md)

## Build Registry

Requirements: Go 1.27.3, Python 3.11 or newer, GNU Make, Git, and curl.

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@1682c520bb5be073eeaa07db03a254fb2e247545"
git clone https://github.com/cdin-editor/registry.git
cd registry
make build
make check
```

The executable is written to `build/registry`. Release builds take their version from the exact Git tag checked out for that build (for example, `v1.0.0` produces `1.0.0`). An untagged developer build reports `development`; the release workflow rejects a tag/commit mismatch.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
