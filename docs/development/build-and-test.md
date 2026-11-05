# Build and test

## Requirements

- Go 1.27.3
- GNU Make
- Git

## Build

```sh
make deps
make build
./build/registry --version
```

## Test and checks

```sh
make unit
make integration
make vet
make fmt-check
make boundary
make check
```

make check runs formatting verification, go vet, unit/integration tests and static package-boundary checks.

## Cross-compilation

The initial CLI has no native platform dependencies. Cross-build examples:

```sh
GOOS=linux GOARCH=amd64 go build ./cmd/registry
GOOS=linux GOARCH=arm64 go build ./cmd/registry
GOOS=darwin GOARCH=arm64 go build ./cmd/registry
GOOS=windows GOARCH=amd64 go build ./cmd/registry
```

The CI workflow exercises native tests on Linux, macOS and Windows. Release artifacts are built separately from tests.
