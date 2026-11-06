# CDIN Registry

CDIN Registry prepares the exact set of CDIN components that a product build needs.

CDIN is made from separately maintained components. Each component can publish its own releases, and those releases may depend on compatible versions of other components. When CDIN is built, someone has to answer three questions:

1. Which component releases should be used together?
2. Which exact source revisions belong to those releases?
3. How can the same selected set be prepared again later?

Registry answers those questions. It reads a **Product Definition** and one or more **Catalogs**, selects compatible releases, downloads the declared source revisions, and writes a prepared **Workspace** with a **Resolved Manifest** and a **Resolution Report**.

Registry is used while preparing a build. It is not linked into the running editor, and it does not compile or execute component code.

## What Registry does for CDIN

When the CDIN product is assembled, its Product Definition can request components such as:

- Kernel — the document and text engine.
- Runtime — editor behavior and extensions.
- Backend — the native host that connects the product services.
- Frontend — the terminal, desktop, or headless user interface.

The names above explain the intended CDIN use. A component is usable only after its maintainer has published valid release metadata and the release is available through a Catalog. Listing a component here does not claim that every CDIN component release has already been published.

Registry checks the requested versions and their declared dependencies and contracts. It then collects the exact Git commits named by the selected releases. CDIN can use the resulting Workspace as input to its own build, integration tests, packaging, and release process.

## The usual workflow

1. A component maintainer publishes a version and its release metadata.
2. A Catalog Index makes that release discoverable and records the metadata digest.
3. CDIN declares the components and acceptable versions in a Product Definition.
4. Registry resolves the request and reports the selected releases.
5. Registry prepares a Workspace and records exactly what was selected.
6. CDIN builds and tests that Workspace.

Registry does not choose what CDIN should contain. The Product Definition expresses that choice; Registry checks whether the request can be satisfied.

## Quick example

Build Registry and inspect its commands:

```sh
make build
./build/registry help
```

Validate a Product Definition:

```sh
./build/registry validate product-definition ./product-definition.yaml
```

See which releases satisfy it:

```sh
./build/registry resolve \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com
```

Prepare the selected source in a new directory:

```sh
./build/registry prepare \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com \
  --output ./build/cdin-workspace
```

The host names and Catalog URL above are examples. Replace them with the hosts used by your Catalog, release metadata, and source repositories. The output directory must not already exist.

## Learn Registry

- [Getting started](docs/getting-started.md) — build Registry and understand the first run.
- [Concepts](docs/concepts.md) — Product Definitions, Catalogs, releases, and Manifests explained.
- [Using Registry](docs/using-registry.md) — the complete resolve and prepare workflow.
- [Product Definition](docs/product-definition.md) — declare the components a product needs.
- [Catalogs and releases](docs/catalogs.md) — publish and describe component releases.
- [Commands](docs/commands.md) — command reference.
- [Outputs](docs/outputs.md) — understand the Workspace, Manifest, and reports.
- [Examples](docs/examples.md) — copyable examples and a CDIN-shaped Product Definition.
- [Limitations and security](docs/limitations.md) — what Registry guarantees and what it does not.
- [Development](docs/development.md) — build and test Registry from source.

## Build Registry

Requirements: Go 1.27.3, Python 3.11 or newer, GNU Make, Git, and curl.

Install the Pipeline version used by this repository, then build:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"

git clone https://github.com/cdin-editor/registry.git
cd registry

make build
make check
```

The executable is written to `build/registry`. The current source identifies itself as `0.1.0-dev`; this is a development version, not a stable Registry 1.0.0 release.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
