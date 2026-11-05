# CDIN Registry

<p align="center">
  <a href="https://github.com/cdin-editor/registry/actions/workflows/ci.yml"><img src="https://github.com/cdin-editor/registry/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="https://github.com/cdin-editor/registry/actions/workflows/docs.yml"><img src="https://github.com/cdin-editor/registry/actions/workflows/docs.yml/badge.svg" alt="Documentation checks"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-blue.svg" alt="License: GPL-3.0-or-later"></a>
  <a href="https://go.dev/doc/install"><img src="https://img.shields.io/badge/Go-1.27.3-00ADD8.svg" alt="Go 1.27.3"></a>
  <a href="https://github.com/cdin-editor/spec"><img src="https://img.shields.io/badge/Registry%20Contracts-1.0.0-purple.svg" alt="Registry contracts 1.0.0"></a>
</p>

<p align="center"><strong>Generic component delivery and orchestration for independent products.</strong></p>

CDIN Registry is a standalone delivery-plane engine. It validates component metadata, resolves declared requirements, collects immutable source revisions and prepares reproducible workspaces. It serves CDIN Editor and unrelated products through the same generic contracts.

> **Registry must understand requirements, not consumer identity.**

Product-specific requirements enter as declarative Product Definitions. Registry code must not contain product-ID conditionals, consumer plugins or hardcoded product workflows.

## Status

This repository is at its foundation stage.

| Capability | Status |
|---|---|
| Go CLI entry point and build metadata | Implemented |
| Embedded Registry v1 schemas synchronized from Spec | Implemented |
| JSON document validation | Implemented |
| Product Definition YAML validation | Implemented; duplicate keys, custom tags and unsafe YAML forms are rejected |
| Pipeline task graph and reusable CI/release workflows | Implemented |
| PSX repository-structure checks | Implemented |
| GIX branch-flow configuration | Implemented |
| Catalog ingestion and metadata integrity | Planned |
| Version, dependency and contract resolution | Planned |
| Git source collection | Planned |
| Prepared workspace and manifest/report production | Planned |
| Update discovery | Planned |

The current executable is a working schema-validation foundation. It is **not yet** a complete package manager, dependency resolver or source collector.

## Quick start

Requirements: Go 1.27.3, Python 3.11 or newer, GNU Make and Git.

Install the same pinned Pipeline revision used by CI:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"
```

Then:

```sh
git clone https://github.com/cdin-editor/registry.git
cd registry

pipeline validate
pipeline run check --json
pipeline run build --json

mkdir -p build
go build -trimpath -o build/registry ./cmd/registry

./build/registry --version
./build/registry schema list
./build/registry validate product-definition examples/product-definition.yaml
```

Validate the other document kinds as JSON:

```sh
./build/registry validate catalog-index catalog.json
./build/registry validate component-release release.json
./build/registry validate resolved-manifest registry-manifest.json
./build/registry validate resolution-report resolution-report.json
```

Use `-` to read JSON from standard input. YAML is accepted only for Product Definitions and is selected by the `.yaml` or `.yml` extension.

## Repository architecture

- `cmd/registry/` — executable entry point.
- `internal/cli/` — command parsing and process exit behavior.
- `internal/validation/` — strict JSON/YAML decoding and JSON Schema validation.
- `internal/schemas/` — embedded Registry schemas mirrored from the Spec.
- `schemas/registry/v1/` — versioned schema copies and immutable source lock.
- `tests/` and package-local tests — CLI, validation and regression tests.
- `pipeline.toml` — provider-neutral task graph.
- `docs/` — architecture, contracts, development, status and verification.
- `scripts/` — boundary, format, schema-sync and release helpers.

The CLI is the first supported interface. No public Go library API is exposed yet.

## Shared engineering tools

Registry uses the same engineering ecosystem as the other CDIN repositories:

- [Pipeline](https://github.com/m-mdy-m/pipeline) owns the task runner and reusable GitHub CI, documentation, security and release workflows. Its task graph is defined in `pipeline.toml`.
- [PSX](https://github.com/m-mdy-m/psx) validates repository structure and required governance files using `psx.yml`.
- [GIX](https://github.com/m-mdy-m/gix) manages the branch flow declared in `.gix/config`.

See [Development tooling](docs/development/tooling.md) and [Branching](docs/development/branching.md).

## Architecture and contracts

The normative source of truth is [CDIN Spec](https://github.com/cdin-editor/spec):

- [Registry architecture](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md)
- [Registry data contracts 1.0.0](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md)
- [ADR-0022](https://github.com/cdin-editor/spec/blob/main/docs/adr/ADR-0022-minimal-static-release-catalog.md)

The five local JSON Schemas are synchronized from the exact Spec commit recorded in `schemas/registry/v1/SOURCE.toml`. They are embedded for offline validation and release packaging. Change the public contract in Spec first, then update the source lock and synchronize this copy.

## Development

Start with [Build and test](docs/development/build-and-test.md), [Module boundaries](docs/architecture/module-boundaries.md), [Tooling](docs/development/tooling.md), [Branching](docs/development/branching.md) and [Contributing](CONTRIBUTING.md).

## License

CDIN Registry is licensed under **GPL-3.0-or-later**. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
