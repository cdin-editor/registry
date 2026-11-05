# Branching workflow

The repository uses [GIX](https://github.com/m-mdy-m/gix) with its flow declared in `.gix/config`. The configuration deliberately uses `main` as the only long-lived base branch; this repository has no permanent `develop` branch.

| Branch kind | Prefix | Parent | Finish target |
|---|---|---|---|
| Feature | `feature/` | `main` | `main` |
| Bugfix | `bugfix/` | `main` | `main` |
| Hotfix | `hotfix/` | `main` | `main` |
| Release | `release/` | `main` | `main` |

Install GIX and run its read-only diagnostics:

```sh
gix doctor
gix status
gix list --tree
```

Start a feature branch:

```sh
gix feature start catalog-loader
```

Commit normally with Git. Once the work is ready and the working tree is clean:

```sh
gix feature finish catalog-loader
```

GIX merges according to `.gix/config`; it does not replace code review. Changes still go through a GitHub pull request and the Pipeline quality gate. GIX is not run automatically in CI and never resolves merge conflicts on the developer's behalf.

Do not run `gix flow init`: the configuration is already version-controlled.
