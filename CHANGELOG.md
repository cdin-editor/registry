# Changelog

Notable changes to CDIN Registry are recorded here. This file follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Add a complete Registry CLI for schema inspection, document validation,
  release resolution, Workspace preparation, Manifest verification, replay,
  update discovery, and cache maintenance.
- Add the six Registry document formats: Product Definition, Catalog Index,
  Component Release Metadata, Resolved Manifest, Resolution Report, and
  Update Discovery Report.
- Add strict JSON and Product Definition YAML validation against the schemas
  maintained by CDIN Spec.
- Add Catalog loading with SHA-256 verification before release metadata is
  parsed, release identity checks, duplicate handling, and all-or-nothing
  failure.
- Add deterministic SemVer-based selection with transitive dependency and
  contract checks, conflict evidence, and bounded search.
- Add collection of exact Git commit revisions, safe source-tree validation,
  cancellation, and transactional Workspace preparation.
- Add canonical Manifest and Report output with detached SHA-256 digests.
- Add Manifest verification, exact-source replay, and read-only update
  discovery.
- Add integrity-checked metadata and Git-object caches with Manifest pins,
  active-operation leases, and explicit garbage collection.
- Add native Workspace publication and platform-specific private-directory
  protections for Linux, macOS, and Windows.
- Add fuzz smoke tests, race detection, resource limits, performance smoke
  tests, and native Workspace tests to the quality checks.

### Improved

- Require explicit host allow-lists for Catalog metadata and Git source
  retrieval.
- Keep resolution deterministic and fail closed on invalid metadata,
  conflicting releases, unsafe source paths, and incomplete preparation.
- Preserve the selected source revisions and release metadata identities in
  the Resolved Manifest so a prepared result can be verified and replayed.

### Documentation

- Replace implementation-oriented notes with a beginner-focused guide that
  explains Registry's role in CDIN, its inputs and outputs, and how to use the
  CLI with practical examples.
- Document current behavior and operational limitations without presenting
  development goals as completed guarantees.

### Known limitations

- Registry prepares source; it does not build, test, package, or launch CDIN.
- SHA-256 digests and Git commit IDs establish content identity, not publisher
  authenticity. Signing, attestations, SBOM generation, and component
  vulnerability scanning are not provided.
- Git submodule and Git LFS object retrieval are unsupported.
- A host allow-list is not a replacement for deployment network-egress rules.
- Parent-directory fsync and uniform crash or power-loss durability guarantees
  are not provided.
- Longer sustained fuzz campaigns, workload-sized performance baselines, and
  broader concurrent update/replay/garbage-collection stress remain future
  hardening work.
