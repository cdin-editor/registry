# CDIN Registry

<p align="center">
  <a href="https://github.com/cdin-editor/registry/actions/workflows/ci.yml"><img src="https://github.com/cdin-editor/registry/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-blue.svg" alt="License: GPL-3.0-or-later"></a>
  <a href="https://go.dev/doc/install"><img src="https://img.shields.io/badge/Go-1.27.3-00ADD8.svg" alt="Go 1.27.3"></a>
  <a href="https://github.com/cdin-editor/spec"><img src="https://img.shields.io/badge/Registry%20Contracts-1.0.0-purple.svg" alt="Registry contracts 1.0.0"></a>
</p>

<p align="center"><strong>Generic component delivery and orchestration for independent products.</strong></p>

CDIN Registry is a standalone delivery-plane engine. It validates component metadata, resolves declared requirements, collects immutable source revisions and prepares reproducible workspaces. It serves CDIN Editor and unrelated products through the same generic contracts.

> **Registry must understand requirements, not consumer identity.**

The Registry implementation is independent of every consumer. Product-specific requirements enter as declarative Product Definitions; Registry code must not contain product-ID conditionals, consumer plug-ins or hardcoded product workflows.

## Status

This repository is at its foundation stage.

| Capability | Status |
|---|---|
| Go CLI entry point and build metadata | Implemented |
| Embedded Registry v1 schemas | Implemented from CDIN Spec |
| JSON document validation | Implemented |
| Product Definition YAML 1.2 input | Implemented with duplicate-key and custom-tag rejection |
| Catalog ingestion and integrity verification | Planned |
| Version and dependency resolver | Planned |
| Contract compatibility resolver | Planned |
| Git source collector | Planned |
| Prepared workspace and resolved manifest generation | Planned |
| Update discovery | Planned |

The current CLI is a working schema-validation foundation. It is **not yet** a complete package manager, dependency resolver or source collector.

## Quick start

Requirements: Go 1.27.3 and GNU Make.

```sh
git clone https://github.com/cdin-editor/registry.git
cd registry

make deps
make build
make test
make check

./build/registry --version
./build/registry schema list
./build/registry validate product-definition examples/product-definition.yaml
```

Validate JSON documents with their corresponding kind:

```sh
registry validate catalog-index catalog.json
registry validate component-release release.json
registry validate resolved-manifest registry-manifest.json
registry validate resolution-report resolution-report.json
```

A hyphen reads JSON from standard input. YAML is accepted only for Product Definitions and is selected by the .yaml or .yml file extension.

## Repository architecture

- cmd/registry — executable entry point.
- internal/cli — command parsing and process exit behavior.
- internal/validation — document decoding and JSON Schema validation.
- internal/schemas — embedded, versioned schemas mirrored from CDIN Spec.
- examples — small, schema-valid input documents.
- tests — black-box and integration tests.
- docs — architecture, contracts, development, status and verification.
- scripts — repeatable local and CI checks.

The Registry has no public in-process API yet. Its first supported interface is the CLI.

## Architecture and contracts

The normative source of truth is [CDIN Spec](https://github.com/cdin-editor/spec):

- [Registry architecture](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md)
- [Registry data contracts 1.0.0](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md)
- [ADR-0022](https://github.com/cdin-editor/spec/blob/main/docs/adr/ADR-0022-minimal-static-release-catalog.md)

The schemas in schemas/registry/v1 are copied byte-for-byte from the Spec repository for offline validation and release packaging. Changes to those contracts must be made in Spec first, then synchronized here.

## Development

See [Build and test](docs/development/build-and-test.md), [Module boundaries](docs/architecture/module-boundaries.md), [Implementation roadmap](docs/development/implementation-roadmap.md) and [Contributing](CONTRIBUTING.md).

## License

CDIN Registry is licensed under **GPL-3.0-or-later**. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
