# Immutable source Collector

## Responsibility and references

`internal/collector` consumes only the fully selected Resolver graph. It never selects versions, interprets product identity or runs component code.

- REG-017/018 — source-unit cardinality and full immutable Git commit IDs: [Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace)
- REG-020/026 — confined source paths and untrusted source content: [Registry security boundary](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary)

## Boundaries

- `GitRunner` injects process execution; `CommandGit` passes an argument vector directly to `exec.CommandContext`, never a shell string.
- `RepositoryPolicy` is mandatory. The production `HTTPSAllowList` allows HTTPS only, rejects embedded credentials, and requires exact host matching. Tests inject a deliberately local-only policy for temporary Git fixtures.
- Public, unauthenticated Git retrieval is the only supported production mode. `CommandGit` inherits only the operating-system variables needed to start Git, forces non-interactive/config-isolated operation, and does not inherit ambient askpass helpers, SSH-agent state, credential helpers or proxy variables.
- Private-repository authentication is not currently supported. Future authentication must use an explicit injected credential-provider boundary; credentials must never be placed in repository URIs, Git command arguments, diagnostics or logs.
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
9. Clean temporary repository and hook directory on every exit path.

The collector writes only into a caller-supplied staging root; Workspace owns final publication.

## Security qualification

The HTTPS host allow-list validates the URI but cannot by itself enforce a deployment's DNS/IP egress policy. Git's underlying network behavior must be constrained by an egress firewall or sandbox. The production deployment must block private/link-local destinations and DNS rebinding. Since private-repository credentials are unsupported, no token or ambient credential helper is supplied to Git.

## Tests

Temporary Git repositories prove exact commit retrieval, multiple source units from one repository, subdirectory collection, no `.git` copy, hook disabling, branch/tag rejection, traversal rejection and symlink rejection.
