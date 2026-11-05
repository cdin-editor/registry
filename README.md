# CDIN Registry

[![CI](https://github.com/cdin-editor/registry/actions/workflows/ci.yml/badge.svg)](https://github.com/cdin-editor/registry/actions/workflows/ci.yml)
[![Documentation](https://github.com/cdin-editor/registry/actions/workflows/docs.yml/badge.svg)](https://github.com/cdin-editor/registry/actions/workflows/docs.yml)
[![License: GPL-3.0-or-later](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![Go 1.27.3](https://img.shields.io/badge/Go-1.27.3-00ADD8.svg)](https://go.dev/doc/install)
[![Registry Contracts 1.0.0](https://img.shields.io/badge/Registry%20Contracts-1.0.0-purple.svg)](https://github.com/cdin-editor/spec)

**Generic component delivery and orchestration for independent products.**

CDIN Registry is a standalone delivery-plane engine. Product-specific
requirements enter as declarative Product Definitions. Registry validates
release metadata, resolves compatible component graphs, collects immutable
source revisions and prepares reproducible workspaces for independent products.

> **Registry must understand requirements, not consumer identity.**

Registry code must not branch on product IDs/names, contain consumer plugins or
hardcode product workflows. It is build-time infrastructure, never a runtime
dependency of a product component.

## Status

The repository has a tested foundation, Catalog ingestion and a first executable
in-memory Resolver. It is not yet a complete source collector/workspace engine.

| Capability | Status |
| --- | --- |
| Standalone Go CLI, build metadata and schema inspection | Implemented |
| Five embedded Registry v1 schemas pinned to Spec | Implemented |
| Strict JSON and Product Definition YAML validation | Implemented |
| Pipeline, PSX, GIX and repository governance | Implemented |
| Catalog Index loading, metadata digest-before-parse and identity checks | Implemented; injected Fetcher |
| Typed dependency and contract metadata for the resolver | Implemented |
| SemVer 2.0.0 parser and Registry 1.0.0 constraint evaluator | Implemented; tested |
| Dependency closure, constraint intersection and deterministic selection | Implemented; first resolver slice |
| Prerelease/withdrawal policy, contract providers and cycle detection | Implemented; tested |
| Production catalog HTTP transport, SSRF policy and metadata cache | Not implemented |
| Immutable Git source collection and actual commit verification | Not implemented |
| Safe source subdirectory/symlink containment | Not implemented |
| Transactional workspace staging and publication | Not implemented |
| Serialized Resolved Manifest and Resolution Report | Not implemented; typed in-memory values exist |
| RFC 8785 serialization, manifest replay and update discovery | Not implemented |

A successful resolver call produces only an in-memory selected graph and report.
It does not fetch component sources, write a Manifest, or publish a workspace.

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
./build/registry validate resolution-report report.json
```

Use `-` to read JSON from standard input. YAML is accepted only for Product
Definitions and is selected by the `.yaml` or `.yml` extension.

## Repository architecture

- `cmd/registry/` — executable entry point.
- `internal/cli/` — command parsing and process exit behavior.
- `internal/validation/` — strict JSON/YAML decoding and JSON Schema validation.
- `internal/schemas/` — embedded Registry schemas mirrored from the Spec.
- `internal/catalog/` — Catalog Index ingestion, digest integrity and typed release metadata.
- `internal/semver/` — strict SemVer 2.0.0 and contract 1.0.0 constraint semantics.
- `internal/resolver/` — deterministic dependency/version/contract resolution.
- Future `internal/collector/` — immutable source retrieval.
- Future `internal/workspace/` — transactional staging and publication.
- `schemas/registry/v1/` — versioned schema copies and immutable source lock.
- `tests/` and package-local tests — CLI, validation, resolver and governance.
- `pipeline.toml` — provider-neutral task graph.
- `docs/` — architecture, contracts, implementation, status and verification.

The CLI is the first supported interface. The core packages have no consumer-
specific runtime dependency.

## Shared engineering tools

- [Pipeline](https://github.com/m-mdy-m/pipeline) owns the task runner and reusable GitHub workflows.
- [PSX](https://github.com/m-mdy-m/psx) validates repository structure and required governance files.
- [GIX](https://github.com/m-mdy-m/gix) manages the branch flow declared in `.gix/config`.

See [Development tooling](docs/development/tooling.md),
[Branching](docs/development/branching.md) and [Build and test](docs/development/build-and-test.md).

## Architecture and contracts

The normative source of truth is [CDIN Spec](https://github.com/cdin-editor/spec):

- [Registry architecture](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md)
- [Registry data contracts 1.0.0](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md)
- [ADR-0022](https://github.com/cdin-editor/spec/blob/main/docs/adr/ADR-0022-minimal-static-release-catalog.md)

The five local JSON Schemas are synchronized from the exact Spec commit
recorded in `schemas/registry/v1/SOURCE.toml`. Change public contracts in Spec
first, then update the source lock and synchronize this copy.

## Development

Start with [Build and test](docs/development/build-and-test.md),
[Module boundaries](docs/architecture/module-boundaries.md),
[Implementation roadmap](docs/development/implementation-roadmap.md),
[Resolver implementation](docs/implementation/resolver.md),
[Traceability](docs/traceability.md) and [Conformance](docs/testing/conformance.md).

## License

CDIN Registry is licensed under **GPL-3.0-or-later**. See [LICENSE](LICENSE)
and [NOTICE](NOTICE).
