# Contributing to CDIN Registry

Thank you for helping improve CDIN Registry. The [CDIN Spec](https://github.com/cdin-editor/spec)
is the source of truth for cross-project Registry behavior. This repository implements
those contracts; it must not redefine them.

## Before changing code

1. Read [Registry architecture](docs/architecture/overview.md) and [Module boundaries](docs/architecture/module-boundaries.md).
2. Read the [Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
3. Check the implementation status and roadmap before adding a new subsystem.
4. Keep consumer identity out of Registry behavior. A product supplies data; Registry code remains generic.
5. Do not fetch or execute undeclared component sources during validation or resolution.

## Code organization

- Keep executable entry points in cmd/.
- Keep implementation packages under internal/ until a public Go API is explicitly designed and accepted.
- Keep schema copies under schemas/registry/v1; update them only from the matching Spec revision.
- Keep filesystem and Git operations behind narrow package interfaces so they can be tested without network access.
- Validate external data at the boundary and return actionable errors. Do not panic on user input.
- Keep output deterministic. Never place timestamps, random identifiers or absolute local paths in reproducibility records.

## Verification

Use Go 1.27.3:

```sh
make deps
make check
```

For a focused change:

```sh
go test ./internal/validation
go test ./tests/...
make boundary
```

Add negative tests for malformed input, conflicts, invalid paths and failed cleanup. Do not claim a feature is implemented because its package or interface exists; its behavior must be covered by tests.

Document commands that could not run locally and explain why. A passing schema test does not prove resolver correctness or product integration.

## Pull requests

Keep one pull request focused. Explain the behavior changed, list the commands run and identify any compatibility implications. Changes to Registry v1 public data contracts must be proposed in the Spec repository first.

Do not force-push over another contributor's work or merge a pull request without review.
