# Development tooling

Registry uses three independent tools, each with one bounded responsibility.

## Pipeline — task graph and CI

[m-mdy-m/pipeline](https://github.com/m-mdy-m/pipeline) is the source of truth for task planning/execution and reusable CI/release workflows. The project-owned `pipeline.toml` defines the task graph once; GitHub Actions only calls Pipeline's reusable workflows.

The CI workflows pin Pipeline to reviewed commit `c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16`. This prevents a change to Pipeline's `main` branch from silently changing Registry's checks.

Install the same Pipeline revision for local work:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@c5cb4a661b9c15b679e422f3f4584c6f7ebe9e16"
pipeline validate
pipeline list
pipeline run check --json
```

Useful tasks:

```sh
pipeline run test --json
pipeline run structure --json
pipeline run schema-sync --json
pipeline run release-check --json
pipeline run race --json
```

Do not add a second copy of Pipeline's task runner or release orchestration to this repository. A project-specific build command belongs in a small script; generic task planning, stage reporting and CI provider behavior belong in Pipeline.

## PSX — repository structure

[m-mdy-m/psx](https://github.com/m-mdy-m/psx) validates repository shape and governance files. Its rules are configured in the repository-owned `psx.yml`.

The task graph pins PSX `v0.3.2` and runs it with Go:

```sh
go run github.com/m-mdy-m/psx/cmd/psx@v0.3.2 check
```

PSX checks project structure; it does not validate Go behavior, JSON Schema semantics, resolver correctness or security properties. Those checks remain explicit Pipeline tasks and Go tests.

## GIX — branch workflow

[m-mdy-m/gix](https://github.com/m-mdy-m/gix) manages the Git branch flow. Its checked-in configuration is `.gix/config`.

Registry currently uses a main-based flow because the repository has no long-lived `develop` branch:

- `feature/*` and `bugfix/*` start from `main` and finish into `main`.
- `hotfix/*` and `release/*` start from and merge into `main`.
- GIX never runs automatically in CI; the developer invokes it deliberately.

Install GIX `v1.2.0` using its [official instructions](https://github.com/m-mdy-m/gix#installation), then inspect the configuration without changing the repository:

```sh
gix doctor
gix status
gix list --tree
```

Start and finish a feature:

```sh
gix feature start catalog-loader
# Make changes and commit normally.
gix feature finish catalog-loader
```

Do not run `gix flow init` in this repository: the committed `.gix/config` already defines the flow, and `flow init` would initialize its default branch topology.
