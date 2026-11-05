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
pipeline run governance --json
pipeline run gix-config --json
pipeline run structure --json
pipeline run schema-sync --json
pipeline run release-check --json
pipeline run race --json
```

The quality graph verifies Go modules, formatting, static analysis, unit/CLI/governance tests, build, package boundaries, the pinned Spec schema mirror, PSX structure and GIX's own configuration parser. `check` also runs the race detector.

Do not add a second copy of Pipeline's task runner or release orchestration to this repository. A project-specific build command belongs in a small script; generic task planning, stage reporting and CI provider behavior belong in Pipeline.

## PSX — repository structure

[m-mdy-m/psx](https://github.com/m-mdy-m/psx) validates repository shape and governance files. Its rules are configured in the repository-owned `psx.yml`.

The task graph pins PSX `v0.3.2` and runs it with Go:

```sh
go run github.com/m-mdy-m/psx/cmd/psx@v0.3.2 check
```

The PSX rules make README, license, Go module/lockfile, tests, Makefile, docs, contribution/security policy, PR template, issue templates, CI and release workflows mandatory. Generated tool/build directories are excluded. PSX checks structure; it does not validate Go behavior, JSON Schema semantics, issue-form field correctness, resolver correctness or security properties. Dedicated Go tests and Pipeline tasks cover those separate contracts.

## GIX — branch workflow

[m-mdy-m/gix](https://github.com/m-mdy-m/gix) manages the Git branch flow. Its reviewed configuration is `.gix/config`; Registry uses `main` and `develop`, with feature/bugfix branches from `develop` and hotfix/release branches merging into both base branches.

Install GIX `v1.2.0` using its [official instructions](https://github.com/m-mdy-m/gix#installation), then inspect the flow:

```sh
gix doctor
gix status
gix list --tree
gix config list
```

The `gix-config` Pipeline task runs the same non-mutating `gix config list` command using the pinned GIX release. The Go governance tests independently assert the exact branch names, parents, prefixes and merge targets required by this repository. This avoids relying on a hand-written YAML check alone.

Start feature work from `develop`:

```sh
git switch develop
gix feature start catalog-loader
# Make changes and commit normally.
gix feature finish catalog-loader
```

Open a PR targeting `develop` before finishing/merging the topic branch. Do not run `gix flow init`: the committed configuration already defines the flow.
