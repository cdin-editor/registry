# cdin-registry

`cdin-registry` is a standalone Go CLI for resolving and preparing CDIN components from release catalogs. It validates product requirements and catalog metadata, selects compatible component versions, collects immutable Git revisions, and publishes a prepared workspace with a Resolved Manifest and Resolution Report.

Registry is build-time infrastructure. It does not execute component code and is not a runtime dependency of product components.

## Build

Requirements: Go 1.27.3, Python 3.11 or newer, GNU Make, Git, and curl.

Install the pinned [Pipeline](https://github.com/m-mdy-m/pipeline) task runner, then build and test:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"

git clone https://github.com/cdin-editor/registry.git
cd registry

make build
make check
```

The CLI is written to `build/registry`.

## Use

Inspect the CLI and available schemas:

```sh
./build/registry --version
./build/registry schema list
./build/registry validate product-definition examples/product-definition.yaml
```

Resolve a Product Definition against one or more Catalog Indexes:

```sh
./build/registry resolve \
  --product ./product-definition.yaml \
  --catalog https://registry.example/catalog/index.json \
  --allow-host registry.example
```

Prepare and publish the selected component workspace:

```sh
./build/registry prepare \
  --product ./product-definition.yaml \
  --catalog https://registry.example/catalog/index.json \
  --allow-host registry.example \
  --output ./prepared
```

Every Catalog metadata host and source repository host must be explicitly allow-listed. Deployments must also enforce their own Git network egress policy. Registry never runs the collected component code.

## Development workflow

The repository uses [Pipeline](https://github.com/m-mdy-m/pipeline) for build and CI tasks, [PSX](https://github.com/m-mdy-m/psx) for repository checks, and [GIX](https://github.com/m-mdy-m/gix) for branch flow.

Run `pipeline validate` to validate the task graph and `make check` for the full local quality checks.

## Documentation

- [Build and test](docs/development/build-and-test.md)
- [Architecture and module boundaries](docs/architecture/module-boundaries.md)
- [Implementation status](docs/status.md)
- [Implementation roadmap](docs/development/implementation-roadmap.md)
- [Traceability](docs/traceability.md)
- [Conformance tests](docs/testing/conformance.md)
- [Examples](examples/)

The normative architecture and data contracts are maintained in [CDIN Spec](https://github.com/cdin-editor/spec).

## License

GPL-3.0-or-later. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
