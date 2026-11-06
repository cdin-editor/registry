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
8. Recheck context and destination, then rename the completed staging directory to the destination.
9. Return the published paths and detached digests only after rename succeeds.

A deferred cleanup removes the private staging directory and the lock owned by
this operation on every failure path. A pre-existing destination is never
intentionally overwritten. The final directory rename stays on the same
filesystem. The destination is checked again immediately before rename to
reject a competing creator.

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

A competing process is also injected at the final rename boundary. Its
destination data must survive; Registry must return an error and clean its own
stage and lock. The destination recheck is not a substitute for the OS-specific
atomic no-replace rename primitive against an empty-directory race; callers
should reserve output paths for Registry until that platform guarantee is
implemented and verified.

## Limits and durability

The sibling lock serializes only Registry publishers that follow this
protocol. It does not exclude unrelated processes. File contents are synced
before publication, but the parent directory is not fsynced and the package
does not claim uniform crash durability. Same-filesystem directory rename
provides the namespace publication boundary on the tested platform; the exact
rename, locking, cleanup and durability guarantees require native Linux,
macOS and Windows CI evidence before a cross-platform guarantee is claimed.

## Tests

Integration tests use real temporary Git repositories and the production Collector, embedded schemas and Manifest/Report serializers. They verify the canonical source path, output documents, no Git internals, SHA-256 return values, failure cleanup, pre-existing destination preservation and rejection of unsuccessful resolution. The Phase 4 matrix additionally injects staging, persistence, cancellation, lock and competing-destination failures.
