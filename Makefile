GO ?= go
BUILD_DIR ?= build
BINARY := $(BUILD_DIR)/registry
VERSION ?= 0.1.0-dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/cdin-editor/registry/internal/buildinfo.Version=$(VERSION) -X github.com/cdin-editor/registry/internal/buildinfo.Commit=$(COMMIT) -X github.com/cdin-editor/registry/internal/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: all deps build test unit integration vet fmt fmt-check boundary check clean help

all: build

help:
	@printf '%s\n' \
	  'make deps        Download pinned Go modules' \
	  'make build       Build ./build/registry' \
	  'make test        Run unit and integration tests' \
	  'make check       Run formatting, vet, tests and boundary checks' \
	  'make clean       Remove generated build artifacts'

deps:
	$(GO) mod download

build:
	mkdir -p "$(BUILD_DIR)"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/registry

test: unit integration

unit:
	$(GO) test ./internal/...

integration:
	$(GO) test ./tests/...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

fmt-check:
	@files="$$(gofmt -l $$(find cmd internal tests -name '*.go' -type f))"; \
	if [ -n "$$files" ]; then printf 'Go files require gofmt:\n%s\n' "$$files"; exit 1; fi

boundary:
	bash scripts/check-boundaries.sh

check: fmt-check vet test boundary

clean:
	rm -rf "$(BUILD_DIR)" dist coverage
