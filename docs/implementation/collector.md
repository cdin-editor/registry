# Immutable source Collector

## Responsibility and references

`internal/collector` consumes only the fully selected Resolver graph. It never selects versions, interprets product identity or runs component code.

- REG-017/018 — source-unit cardinality and full immutable Git commit IDs: [Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace)
- REG-020/026 — confined source paths and untrusted source content: [Registry security boundary](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary)

## Boundaries

- `GitRunner` injects process execution; `CommandGit` passes an argument vector directly to `exec.CommandContext`, never a shell string. Each Unix invocation runs in its own process group, and cancellation terminates that group so helper processes cannot hold output pipes open. Windows uses a dedicated process group and a bounded wait after terminating the Git process.
- `RepositoryPolicy` is mandatory. The production `HTTPSAllowList` allows HTTPS only, rejects embedded credentials, and requires exact host matching. Tests inject a deliberately local-only policy for temporary Git fixtures.
- Public, unauthenticated Git retrieval is the default. An optional `GitCredentialProvider` is invoked only after `RepositoryPolicy` accepts a repository URI. It may inject an absolute `GIT_ASKPASS`, `GIT_ASKPASS_REQUIRE=force`, and narrowly named `REGISTRY_GIT_CREDENTIAL_*` values for that operation only.
- `CommandGit` inherits only operating-system variables needed to start Git. It excludes ambient HOME, all ambient `GIT_*` settings, askpass helpers, SSH-agent state, credential helpers and proxy variables. Provider-supplied environment is separately validated; arbitrary Git config, proxy, PATH, SSH and terminal settings are rejected.
- Credentials must never be placed in repository URIs or Git arguments. Provider errors and Git stderr are not copied into diagnostics. The injected askpass executable is trusted code supplied by the caller; Registry does not create or store credentials.
- Failed Git commands expose a safe exit classification rather than raw arguments or remote stderr, which may contain secret-bearing URLs.
- The collector does not own version resolution, auth UI, credential storage or catalog ingestion.

## Collection algorithm

1. Validate component, variant and source-unit identities.
2. Require a full 40- or 64-character hexadecimal object ID. A branch, tag or abbreviated commit is not accepted.
3. Apply repository URI policy before Git receives the value.
4. Derive the destination solely from stable IDs: `components/<component>/<variant>/sources/<source-unit>/`.
5. Create an isolated temporary repository; disable prompts, system/global Git config, hooks, LFS smudge/process filters and automatic submodule recursion.
6. Fetch the declared revision without tags, verify it resolves to a commit whose full object ID equals the declared revision, then checkout detached and verify HEAD again.
7. Validate the declared source subdirectory one path component at a time. Reject parent traversal, absolute forms, symlink components and missing/non-directory paths.
8. Copy regular files/directories only. Exclude the temporary `.git` directory and reject symlinks and special files. Do not run hooks, filters, scripts, submodules or LFS downloads.
9. Copy in bounded chunks while checking context cancellation between reads/writes. A cancelled or failed copy removes the partial source target.
10. Clean temporary repository, hook directory and any source targets created by a failed `Collect` operation on every exit path.

The collector writes only into a caller-supplied staging root; Workspace owns final publication.

## Security qualification

The HTTPS host allow-list validates the URI but cannot by itself enforce a deployment's DNS/IP egress policy. Git's underlying network behavior must be constrained by an egress firewall or sandbox. The production deployment must block private/link-local destinations and DNS rebinding. Since private-repository credentials are unsupported, no token or ambient credential helper is supplied to Git.

## Tests

Temporary Git repositories prove exact commit retrieval, multiple source units from one repository, branch/tag/abbreviated-ID rejection, missing and malformed repository failures, subdirectory collection, no `.git` copy, hook/filter/submodule isolation, traversal/symlink/special-file rejection, source-tree rollback, and reuse of one repository across logical components/variants without path collisions. Process tests prove an actual Git daemon is terminated by context cancellation and Unix helper descendants do not survive process-group cancellation; Collector tests prove cancellation during fetch and source copy cleans temporary directories and partial targets. Environment tests prove only an explicit, validated askpass credential provider can inject credential values and Git failures do not echo stderr or arguments.
