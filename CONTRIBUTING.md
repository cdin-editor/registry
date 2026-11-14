# Contributing to CDIN Registry

Thank you for helping improve CDIN Registry. The [CDIN Spec](https://github.com/cdin-editor/spec)
is the authority for cross-project Registry behavior; this repository implements those contracts.

## Before changing code

1. Read the [Registry responsibilities and consumer boundary](README.md#responsibilities).
2. Read [Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
3. Keep consumer identity out of Registry behavior. A product supplies data; Registry remains generic.
4. Do not fetch or execute undeclared component sources during validation or resolution.
5. Check the [1.0.0 release-readiness checklist](docs/release-readiness-1.0.0.md) before claiming stable-release readiness.

## Toolchain

- Go 1.27.3
- Python 3.11+ and the pinned [Pipeline task runner](docs/development.md)
- GNU Make and Git
- PSX v0.3.2 for repository-structure checks
- GIX v1.2.0 for the documented branch workflow

Install Pipeline and run the authoritative task graph:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@1682c520bb5be073eeaa07db03a254fb2e247545"
pipeline validate
pipeline run check --json
```

For a focused review of repository governance:

```sh
pipeline run governance --json
pipeline run gix-config --json
pipeline run structure --json
pipeline run schema-sync --json
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

The repository governance tests validate the GitHub Issue Forms and PR review checklist, including required fields and unique field IDs. PSX enforces the presence of the governance files, while GIX's own parser validates `.gix/config`. Keep both layers current when changing repository policy.

For changes to a public contract, propose and merge the Spec change first, then update the pinned schema source revision.

## Branching

The committed `.gix/config` defines the branch flow. Feature and bugfix branches start from `develop`; open PRs targeting `develop`. Use `gix doctor` before starting work and follow the committed [GIX branch configuration](.gix/config). Do not run `gix flow init`; this repository already has a checked-in configuration.

## Pull requests

Keep one pull request focused. Explain behavior, link the exact REG requirement(s), list commands and actual results, identify compatibility implications and disclose untested platforms. The [pull request template](.github/PULL_REQUEST_TEMPLATE.md) is the review checklist. GitHub issue forms are available for [bugs](.github/ISSUE_TEMPLATE/bug_report.yml), [feature proposals](.github/ISSUE_TEMPLATE/feature_request.yml) and [Spec/contract changes](.github/ISSUE_TEMPLATE/spec_contract.yml).

Do not force-push over another contributor's work or merge without review.
