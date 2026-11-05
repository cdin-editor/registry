# Branching workflow

Registry follows the CDIN Git Flow with [GIX](https://github.com/m-mdy-m/gix). The authoritative, reviewable branch configuration is committed in `.gix/config`.

```text
main
└── develop
    ├── feature/*
    └── bugfix/*

main ── hotfix/* ──> main + develop
develop ── release/* ──> main + develop
```

| Branch kind | Prefix | Parent | Finish targets |
| --- | --- | --- | --- |
| Main | — | — | — |
| Develop | — | main | — |
| Feature | `feature/` | develop | develop |
| Bugfix | `bugfix/` | develop | develop |
| Hotfix | `hotfix/` | main | main, develop |
| Release | `release/` | develop | main, develop |

## Before starting

Install GIX v1.2.0 using its [official instructions](https://github.com/m-mdy-m/gix#installation), then run its read-only diagnostics:

```sh
gix doctor
gix status
gix list --tree
gix config list
```

The Pipeline quality graph also executes GIX's own config reader against the committed file. This checks GIX's real configuration semantics; the Go governance tests additionally assert Registry's intended branch topology.

## Feature work

The integration branch is `develop`. Create a topic branch from it:

```sh
git switch develop
git pull --ff-only
gix feature start catalog-loader
```

Commit normally with Git. Push the branch and open a pull request targeting `develop`. Once review and required checks pass, finish the branch according to the configured GIX flow:

```sh
gix feature finish catalog-loader
```

Bug fixes use `gix bugfix start <name>` and finish into `develop`.

## Releases and hotfixes

A release branch starts from `develop` and merges into both long-lived branches:

```sh
gix release start v0.1.0
# Stabilize, test, and update release notes.
gix release finish v0.1.0 --tag v0.1.0
```

A hotfix starts from `main` and merges into both `main` and `develop`:

```sh
git switch main
gix hotfix start critical-fix
# Fix, test, and commit.
gix hotfix finish critical-fix --tag v0.1.1
```

GIX performs only the branch operations declared in `.gix/config`; it does not replace pull-request review. It is not run automatically in CI and never resolves merge conflicts on a developer's behalf.

Do not run `gix flow init`: the reviewed configuration is already version-controlled. The `gix-config` Pipeline task runs `gix config list` to parse and validate it without changing branches or files.
