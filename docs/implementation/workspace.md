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

A deferred cleanup removes the stage and lock on failure. A pre-existing destination is never intentionally overwritten. The final directory rename is same-filesystem to preserve atomic publication semantics.

## Limits

This package's lock file coordinates Registry publishers that follow the same protocol; an unrelated process that ignores the lock can still race to create the destination. The final existence recheck prevents deliberate overwrite, but callers should use an output directory exclusively owned by Registry. Platform-specific rename and directory-fsync guarantees must be covered by Linux/macOS/Windows integration tests before claiming uniform crash-durability.

## Tests

Integration tests use real temporary Git repositories and the production Collector, embedded schemas and Manifest/Report serializers. They verify the canonical source path, output documents, no Git internals, SHA-256 return values, failure cleanup, pre-existing destination preservation and rejection of unsuccessful resolution.
