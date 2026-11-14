# Development

This page is for people changing Registry itself. For normal use, start with
[Getting started](getting-started.md).

## Requirements

- Go 1.27.3;
- Python 3.11 or newer;
- GNU Make;
- Git;
- curl.

Install the Pipeline revision used by the current build workflow:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@1682c520bb5be073eeaa07db03a254fb2e247545"
```

## Build

```sh
make deps
make build
./build/registry --version
```

The executable is written to `build/registry`.

The current source default is `0.2.0-dev.3`. The planned stable release is governed by the [Registry 1.0.0 release-readiness checklist](release-readiness-1.0.0.md); do not change the version to `1.0.0` until its gates are satisfied.

## Run tests and checks

Run the complete quality gate:

```sh
make check
```

Useful focused commands:

```sh
make test
make vet
make format
make boundary
make schema-sync
make structure
make race
```

The full check includes unit and CLI tests, Go vet, formatting, race detection,
schema synchronization against the pinned CDIN Spec commit, Git tree and
per-file Git blob IDs, repository checks, bounded fuzz campaigns, and
performance smoke tests. The tree/blob pins make schema retrieval independent
of mutable branch names and raw-content ref resolution.

## Before opening a change

- Run the checks affected by your change; run `make check` before asking for
  review.
- Update the relevant user guide when CLI behavior or public document formats
  change.
- Update `CHANGELOG.md` for user-visible behavior.
- Keep examples consistent with the embedded schemas.
- Do not describe a proposed feature as available until it is implemented and
  covered by tests.

For the public input and output formats, see
[CDIN Spec — Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
