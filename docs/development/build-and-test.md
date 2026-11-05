# Build and test

The authoritative task graph is [`pipeline.toml`](../../pipeline.toml). GitHub workflows call pinned reusable workflows from [m-mdy-m/pipeline](https://github.com/m-mdy-m/pipeline); local commands run the same tasks.

## Requirements

- Go 1.27.3
- Python 3.11 or newer (for Pipeline)
- GNU Make
- Git
- curl (for the pinned Spec-schema drift check)

## Install Pipeline

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"
pipeline validate
```

## Build and inspect the CLI

```sh
pipeline run deps --json
pipeline run build --json
mkdir -p build
go build -trimpath -o build/registry ./cmd/registry
./build/registry --version
./build/registry schema list
```

## Complete verification

```sh
pipeline run check --json
```

The `check` graph includes:

1. Go module download and checksum verification.
2. Formatting, `go vet`, unit tests, CLI integration tests and build.
3. Executable governance tests for the PR template, issue forms, PSX policy and exact GIX topology.
4. GIX's own read-only `config list` parser/validator.
5. Package-boundary checks and byte-for-byte schema synchronization with the pinned Spec commit.
6. PSX repository-structure checks.
7. `git diff --check` and the Go race detector.

The GIX parser task is not a substitute for review: it validates configuration syntax and semantics without creating, merging or deleting branches. The Go governance tests check that the configuration still expresses Registry's intended main/develop flow.

## Individual checks

```sh
pipeline run test --json
pipeline run governance --json
pipeline run gix-config --json
pipeline run vet --json
pipeline run structure --json
pipeline run schema-sync --json
pipeline run boundary --json
pipeline run race --json
```

The schema synchronization command downloads from the immutable Spec commit recorded in `schemas/registry/v1/SOURCE.toml`. It never follows a moving branch. Use `bash scripts/sync-spec-schemas.sh --update` only after changing that lock to an accepted Spec commit.

## Cross-compilation

The release workflow uses Pipeline's release-target runner and calls `scripts/release/build.sh`. It derives GOOS/GOARCH from target metadata and builds with `CGO_ENABLED=0` for Linux, macOS and Windows targets.

A successful cross-build proves compilation for that target; it is not a test that the binary executed on native target hardware.
