# ADR-0001: Go for the Registry engine and CLI

- **ID:** ADR-0001
- **Status:** PROPOSED
- **Decision scope:** Implementation language and executable toolchain for the Registry repository only

## Context

Registry is a standalone delivery-plane engine and CLI. It is not a low-level C ABI library and it is not a product runtime component. Its initial responsibilities are schema validation, catalog access, deterministic metadata resolution, Git source collection and local workspace preparation.

The other CDIN repositories demonstrate useful shared engineering conventions (Makefile entry points, tests, docs, scripts, PSX configuration and GitHub Actions). They do not require every repository to use the same implementation language or duplicate the Kernel's public C ABI layout.

## Proposal

Use Go for the Registry engine and CLI, with a versioned Go module and a single executable under cmd/registry. Keep implementation packages under internal/ until a separately justified public Go API exists.

The proposed toolchain baseline is Go 1.27.3. JSON Schema Draft 2020-12 validation uses github.com/santhosh-tekuri/jsonschema/v6; YAML 1.2 Product Definition decoding uses go.yaml.in/yaml/v3. Both are pinned in go.mod.

## Alternatives

- **C11:** consistent with Kernel/Backend/Frontend native implementations, but adds platform/toolchain work without improving the delivery-plane CLI's core boundary.
- **Lua:** consistent with Runtime, but unsuitable as the only distribution format for a standalone dependency/workspace tool.
- **Go (proposal):** produces a self-contained cross-platform executable and has a standard build/test toolchain. It is an implementation choice, not a Registry runtime dependency.

## Consequences

- Registry's binary and module versioning are owned by this repository.
- No component links Registry at runtime.
- The initial public surface is the CLI; a public Go API is not created prematurely.
- The implementation-language decision remains PROPOSED until explicitly reviewed and accepted by the project owner.
