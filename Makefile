PIPELINE ?= pipeline
GO ?= go
BUILD_DIR ?= build
BINARY := $(BUILD_DIR)/registry

.PHONY: all help deps build test vet format boundary schema-sync structure race release-check check clean

all: build

help:
	@printf '%s\n' \
	  'make deps          Download the Go module graph through Pipeline' \
	  'make build         Compile all Go packages through Pipeline' \
	  'make test          Run unit and CLI integration tests through Pipeline' \
	  'make structure     Run PSX repository checks through Pipeline' \
	  'make schema-sync   Verify schemas against the pinned Spec revision' \
	  'make release-check Run portable release checks through Pipeline' \
	  'make check         Run the full CI quality task graph' \
	  'make race          Run Go race tests on a supported host' \
	  'make clean         Remove generated build and release artifacts'

deps:
	$(PIPELINE) run deps --json

build:
	$(PIPELINE) run build --json
	mkdir -p "$(BUILD_DIR)"
	$(GO) build -trimpath -o "$(BINARY)" ./cmd/registry

test:
	$(PIPELINE) run test --json

vet:
	$(PIPELINE) run vet --json

format:
	$(PIPELINE) run format --json

boundary:
	$(PIPELINE) run boundary --json

schema-sync:
	$(PIPELINE) run schema-sync --json

structure:
	$(PIPELINE) run structure --json

race:
	$(PIPELINE) run race --json

release-check:
	$(PIPELINE) run release-check --json

check:
	$(PIPELINE) run check --json

clean:
	rm -rf "$(BUILD_DIR)" dist coverage .pipeline-artifacts
