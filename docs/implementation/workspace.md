# Transactional workspace publication

## Responsibility and references

`internal/workspace` composes Resolver, Collector and Manifest output into one complete prepared workspace. It does not decide component versions and never executes source code.

- REG-020 — path confinement.
- REG-021 — canonical workspace layout.
- REG-022 — all-or-nothing publication.
- REG-023–025 — reproducibility documents and failure semantics.
- [Registry contract §6](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace)
- [Registry architecture §5](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#5-source-collection-and-handoff)

## Prepare sequence

1. Require a successful Resolver result with no conflicts.
2. Resolve and validate the destination parent; reject symlink ancestors.
3. Acquire an exclusive sibling lock for this destination and refuse an existing destination.
4. Create a private sibling staging directory on the same filesystem as the target.
5. Collect all declared source units into staging.
6. Build the Manifest from the resolver graph and Collector records; serialize Manifest and Report canonically and validate both against the embedded v1 schemas.
7. Write output documents with exclusive create, restrictive permissions and fsync.
8. Recheck context and the staging path, then publish with a native atomic
   no-replace directory rename.
9. Return the published paths and detached digests only after publication succeeds.

A deferred cleanup removes the private staging directory and the lock owned by
this operation on every failure path. A pre-existing destination is never
overwritten. The final directory rename stays on the same filesystem. The
destination is checked before publication for an early diagnostic, then the
native no-replace operation closes the check/rename race, including when an
unrelated process creates an empty destination immediately before publication.

The supported native implementations are:

- Linux: `renameat2(..., RENAME_NOREPLACE)`.
- macOS: `renamex_np(..., RENAME_EXCL)`.
- Windows: `MoveFileEx(..., 0)`, without replace or cross-volume-copy flags.

On other operating systems the publication helper fails closed rather than
falling back to a racy check followed by `os.Rename`.

An existing lock is treated as busy whether it belongs to a live operation or
was left by abnormal termination. Registry does not guess that a lock is stale:
the lock format has no portable, race-free owner-liveness proof, especially
across hosts. Recovery is an operator action: establish that no Registry
process still owns the destination, then remove the stale sibling lock. Registry
never removes a lock it did not create.

## Failure and cancellation boundaries

The package-private `prepareHooks` seam injects failures without widening the
public API. Tests cover failure after a source unit was staged, after the final
source collection, Manifest/Report write and fsync failures, cancellation
between material stages, and failure immediately before publication. Every
such case must leave no destination or staging directory and must remove the
lock owned by the failing operation.

Competing-process tests inject both empty and non-empty destinations at the
final rename boundary. The destination must survive unchanged; Registry must
return an error and clean its own stage and lock. The no-replace helper also
runs two concurrent publishers against one absent destination and proves that
exactly one complete directory wins. Existing destination symlinks are not
followed. A stage path replaced by a symlink before publication is rejected.

## Limits and durability

The sibling lock serializes only Registry publishers that follow this
protocol. It does not exclude unrelated processes. Native no-replace
publication prevents an existing destination from being replaced on Linux,
macOS and Windows, and those implementations are exercised by the native
Workspace workflow. File contents are synced before publication, but the
parent directory is not fsynced and the package does not claim uniform crash
durability. A successful atomic namespace rename is not by itself a guarantee
that the directory entry survives sudden power loss on every filesystem.

## Tests

Integration tests use real temporary Git repositories and the production Collector, embedded schemas and Manifest/Report serializers. They verify the canonical source path, output documents, no Git internals, SHA-256 return values, failure cleanup, pre-existing destination preservation and rejection of unsuccessful resolution.

The Phase 4 matrix additionally injects:

- failure after the first of multiple source units and after all sources are staged;
- Manifest and Report write and fsync failures;
- cancellation after collection, after Manifest validation, before Report write, after Report write and before publication;
- pre-rename failure, lock contention, stale-lock fail-closed handling and owned-lock cleanup;
- unrelated empty and non-empty destinations appearing inside the final rename boundary;
- two concurrent native publishers competing for one destination, with exactly one complete winner;
- destination symlink rejection and staging-path symlink replacement rejection;
- cleanup assertions for temporary Git clones and disabled-hook directories.

`TestREG021WorkspaceSharedRepositoryPreservesComponentAndVariantPaths`
covers the complete Workspace path layout for multiple source units that share
one repository and commit across component/variant identities.

The native matrix runs `go test ./internal/workspace/...` through the pinned
Pipeline task on Ubuntu, macOS and Windows. The native tests establish the
no-replace syscall behavior on the corresponding runner images and toolchain.
They do not establish parent-directory crash durability or power-loss
persistence guarantees.

## Windows workspace ACLs

On Windows, the destination lock and private staging directory receive protected DACLs before collection or document writes begin. The DACL grants full control only to the object owner (Owner Rights), LocalSystem and the local Administrators group. The staging directory's ACEs are inheritable by child files and directories, so collected sources and the published Manifest/Report retain the workspace boundary after the final same-volume rename. The lock file uses non-inheritable ACEs.

If the native ACL cannot be applied, preparation fails closed and removes the Registry-owned lock/staging artifacts. This policy is intentionally tested with Windows security descriptors and ACEs; POSIX mode-bit assertions are not treated as Windows ACL evidence.

## Persisted Manifest replay

`workspace.Replay` is also exposed by `registry manifest replay`. The CLI
reads a saved Manifest from a file or stdin, verifies it before metadata/Git
access, requires an explicit allow-list for every recorded source host, and
publishes only to a new destination. It does not load a Product Definition or
invoke Resolver. Offline mode does not contact Catalog metadata; strict mode
re-fetches the exact recorded Catalog Index and selected Release Metadata
before source collection. Cancellation, source failures, metadata mismatch
and destination collisions all use the normal stage/lock cleanup path.
