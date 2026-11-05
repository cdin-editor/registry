# Contributing to CDIN Registry

Thank you for helping improve CDIN Registry. The [CDIN Spec](https://github.com/cdin-editor/spec)
is the authority for cross-project Registry behavior; this repository implements those contracts.

## Before changing code

1. Read [Registry architecture](docs/architecture/overview.md) and [Module boundaries](docs/architecture/module-boundaries.md).
2. Read [Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
3. Keep consumer identity out of Registry behavior. A product supplies data; Registry remains generic.
4. Do not fetch or execute undeclared component sources during validation or resolution.
5. Check [implementation status](docs/status.md) before claiming a capability is complete.

## Toolchain

- Go 1.27.3
- Python 3.11+ and the pinned [Pipeline](docs/development/tooling.md#pipeline--task-graph-and-ci)
- GNU Make and Git
- PSX v0.3.2 for repository-structure checks
- GIX v1.2.0 for the documented branch workflow

Install Pipeline and run the authoritative task graph:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"
pipeline validate
pipeline run check --json
```

Do not add a second CI engine or duplicate Pipeline's provider/release orchestration in this repository.

## Code organization

- Keep executable entry points in `cmd/`.
- Keep implementation packages under `internal/` until a public Go API is explicitly designed and accepted.
- Keep schema copies under `schemas/registry/v1/`; update them only from the pinned Spec source revision.
- Keep filesystem, HTTP and Git operations behind narrow interfaces so failure paths can be tested without network access.
- Validate external data at the boundary and return actionable errors. Do not panic on user input.
- Keep output deterministic. Do not put wall-clock timestamps, random identifiers or absolute host paths in resolved manifests.

## Validation expectations

Add tests for both valid and invalid inputs. For parsers and resolvers, include malformed data, duplicate keys, ambiguous candidates, missing dependencies, cycles, invalid source revisions, path traversal and cleanup failures as applicable.

A green schema-validation test does not prove resolver correctness, source reproducibility or product integration. Update the conformance and traceability documents only when executable evidence exists.

Run:

```sh
pipeline run check --json
pipeline run structure --json
pipeline run schema-sync --json
```

For changes to a public contract, propose and merge the Spec change first, then update the pinned schema source revision.

## Branching

The committed `.gix/config` defines the branch flow. Use `gix doctor` before starting work and the commands in [Branching](docs/development/branching.md). Do not run `gix flow init`; this repository already has a checked-in configuration.

## Pull requests

Keep one pull request focused. Explain behavior, link the exact REG requirement(s), list commands and actual results, identify compatibility implications and disclose untested platforms. The [pull request template](.github/PULL_REQUEST_TEMPLATE.md) is the review checklist.

Do not force-push over another contributor's work or merge without review.
