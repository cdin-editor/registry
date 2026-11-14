# Changelog

Notable changes to CDIN Registry are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [1.0.0] - 2026-11-14

### Added

- Add a release-readiness checklist for the stable 1.0.0 CLI/package release,
  covering the public CLI and data-contract compatibility surface,
  reproducibility, Workspace/cache safety, the five-target release bundle,
  clean-consumer verification and documented limitations.

### Changed

- Derive the CLI version, release descriptor, and release asset metadata from the exact immutable Git tag; untagged developer builds identify as `development`.
- Publish only from a validated `vMAJOR.MINOR.PATCH` Git tag whose target equals the checked-out source commit; verify that the changelog contains the matching version section.
- Replace the fixed CDIN component version list with six GitHub Release sources; Backend, Kernel, Runtime, and Frontend Core/TUI/GUI-SDL versions are discovered from their own immutable Git tags and verified against the matching release metadata asset and tag commit.
- Skip historical GitHub Releases that predate canonical Registry metadata assets; keep strict failure for present-but-ambiguous, malformed, digest-mismatched or tag-mismatched Registry metadata.
- Align CI, documentation and contributor guidance with the exact immutable Pipeline revision used by the repository's release workflow.
- Repair contributor-guide links to retired architecture, status and tooling pages.

### Fixed

- Make release publication idempotent: skip an already complete immutable
  Release and fail closed with the missing asset inventory when an existing
  Release is incomplete.
- Distinguish Windows Git-object lock contention from transient lock
  disappearance; retry the latter during both object-lock and maintenance-lock
  acquisition while preserving fail-closed handling for unsafe paths and
  unrelated permission errors.

## [0.2.0-dev.3] - 2026-11-13

### Added

- Pin canonical ADR-0028 Release Metadata assets for Backend 1.2.2
  (tag commit `41681388d8294efeb371b0d337b426a72c19aeae`), Kernel 1.2.3
  (tag commit `2fb09f52dc1a9963cf358ec54d584c520e174e67`), and Runtime 1.0.0
  (tag commit `eb48a511ea6d5085db38e501784290d80cb03947`) in the static CDIN
  Catalog.
- Refresh the Frontend Core, GUI-SDL and TUI 1.2.2 metadata digests from the
  GitHub Release Asset API. The immutable `v1.2.2` tag resolves to
  `728406c1e677d601c55b82b4fbf1a77eecfd8581`; do not use the earlier
  pre-release-candidate commit recorded in the original Catalog preparation.
- Keep the exact Catalog snapshot, five verified Registry executables and
  `registry-release-0.2.0-dev.3.json` descriptor together in the same
  Registry Release, with the final Pipeline SHA256SUMS covering all assets.

### Compatibility

- Retain the backward-compatible static Catalog 1.0.0 entries model for this
  release. New metadata entries are pinned only when their canonical GitHub
  Release Asset names, tag-resolved commits and API-reported SHA-256 digests
  are available. Dynamic `release_sources` remain a separate Catalog migration
  and are not mixed into this static document.

## [0.2.0-dev.2] - 2026-11-12

### Added

- Discover immutable Component Release Metadata from GitHub Releases using
  Catalog `release_sources`, full pagination, exact tag/commit resolution,
  canonical asset names and digest-verified metadata identities.
- Generate the release-time Catalog snapshot and
  `registry-release-0.2.0-dev.2.json` descriptor from the exact tag commit,
  including the verified five-target executable inventory and final asset
  identities.

### Fixed

- Verify each cross-compiled executable before upload, checking embedded Go
  GOOS/GOARCH/CGO settings and actual ELF, Mach-O or PE machine identity.
- Reject mismatched release/tag/source identities and malformed or ambiguous
  Catalog metadata instead of publishing an unverifiable target inventory.
- Keep the Registry CLI/package version independent from Registry Data
  Contract and Spec schema versions.

## [0.2.0-dev.1] - 2026-11-10

### Added

- Add optional Registry Data Contracts 1.3.0 target-specific artifact delivery (defined by Spec release 1.4.0), with explicit delivery policy/target selection, digest-verified package materialization, secure archive extraction, artifact provenance in Resolved Manifests, and exact selected-artifact replay. Source delivery remains the default for existing Product Definitions.
- Versioning boundary: `0.2.0-dev.1` is the Registry CLI/package version; Registry Data Contract schema versions and the overall Spec release version are independent compatibility tracks.

### Fixed

- Pin Registry v1 schemas to an immutable Spec commit/tree and per-file Git blob identities; retrieve
  them through the Git Database API, verifying each downloaded Git object
  before schema comparison. This avoids raw-content ref lookup failures while
  preserving a reproducible Spec revision.
- Validate the gzip checksum/trailer after TAR end markers and keep archive
  path-collision checks bounded by path depth rather than total entry count.
- Reject ZIP Unicode Path extra fields that override the stored entry name or carry an invalid filename checksum; cover GNU long-name traversal in archive tests.
- Pass an explicit target OS and architecture to every Pipeline release-matrix
  build. The previous matrix could inherit the runner's native GOARCH,
  producing mislabeled or duplicate ARM64/AMD64 assets instead of the requested
  cross-compiled binaries.

### Tests

- Cover PAX path traversal overrides and corrupted gzip trailers in TAR.GZ
  package extraction.

## [0.1.0-dev.1] - 2026-11-08

> Corrected development prerelease. This release rebuilds the Registry CLI
> with an explicit target OS and architecture for every published asset.
> It supersedes the incorrectly mapped platform binaries in `v0.1.0-dev`.

### Fixed

- Cross-compile Linux AMD64/ARM64, macOS AMD64/ARM64 and Windows AMD64 assets for their declared target instead of inheriting the runner's native architecture.
- Publish architecture-specific executable assets that can be consumed by a platform-aware CDIN bootstrap after exact tag and SHA-256 verification.

## [0.1.0-dev] - 2026-11-07

> Development prerelease. This version matches
> `internal/buildinfo/version.go`. It is not a stable `1.0.0` release.

### Added

- Store the CDIN Catalog Index as version-controlled data under `catalogs/cdin-components/`, so consumers can pin the Registry commit and obtain the matching catalog snapshot without carrying component repository metadata in the Composition Root.

- Add the Registry command-line application for validating documents,
  resolving product requirements, and preparing component Workspaces.
- Define and embed the six public document formats: Product Definition,
  Catalog Index, Component Release Metadata, Resolved Manifest,
  Resolution Report, and Update Discovery Report.
- Validate JSON documents and Product Definition YAML against the
  Spec-maintained schemas.
- Load Catalog Indexes and release metadata with SHA-256 verification before
  parsing, release identity checks, duplicate handling, and all-or-nothing
  failure.
- Resolve semantic-version constraints, transitive dependencies, and
  versioned contracts deterministically; report conflicts and candidate
  decisions as structured data.
- Collect source from exact full Git commit IDs, validate the resulting source
  tree, and prepare a new Workspace without running component code.
- Write canonical Resolved Manifests and Resolution Reports with detached
  SHA-256 digests.
- Verify saved Manifests, replay the exact source revisions recorded in them,
  and discover possible updates without changing an existing Workspace.
- Cache verified Catalog metadata and Git objects. Retain cache entries used by
  a Manifest and remove eligible unreferenced entries through explicit
  garbage collection.
- Add native Workspace publication and private-directory handling for Linux,
  macOS, and Windows.
- Add bounded resource limits, fuzz smoke checks, race detection,
  performance smoke checks, and native-platform tests to the quality gates.

### Improved

- Classify persisted Manifest verification failures by canonical-byte, public-schema or semantic-provenance stage without echoing untrusted Manifest content in CLI diagnostics.

- Reject Git mode-120000 entries from the selected immutable source subtree before checkout, independent of host-specific symlink materialization. Apply the same check to Git-object cache hits.

- Verify fetched and checked-out Git commits with `cat-file` type/content and canonical object hashes instead of platform-sensitive revision-peeling expressions, including the shared Git-object cache path.

- Make Registry user documentation consumer-independent and clarify the separation between component-owned Release Metadata, Catalog Index publication, and generic Registry resolution.

- Require explicit host allow-lists for remote Catalog, metadata, and Git
  source retrieval.
- Reject invalid or conflicting release metadata, unsafe source paths,
  incomplete source collection, and invalid Manifest provenance.
- Preserve exact release and source identities so a prepared result can be
  checked and reproduced without selecting newer versions.
- Keep update discovery read-only: it reports differences between a saved
  Manifest and current product/catalog inputs without rewriting historical
  build records.
- Bound Catalog metadata concurrency and keep deterministic result ordering.
- Document security limits and avoid claiming publisher authenticity,
  component safety, build isolation, or crash-durability guarantees that
  Registry does not provide.

### Documentation

- Rewrite the user documentation around the questions a first-time user has:
  what Registry does for CDIN, when to use it, what inputs it needs, how to
  resolve and prepare a product, and how to use the resulting files.
- Add practical examples for Product Definitions, Component Release Metadata,
  Catalog Indexes, resolve, prepare, verify, replay, and update discovery.
- Keep implementation details out of the user guides and identify example
  versions, repositories, URLs, commit IDs, and digests as illustrative
  placeholders.

### Known limitations

- Registry prepares source and records a selection. It does not compile
  components, run their tests or build scripts, package CDIN, create installers,
  or launch the editor.
- SHA-256 digests and Git commit IDs identify content; they do not prove who
  published it. Release signing, signature verification, attestations, SBOM
  generation, and component vulnerability scanning are not provided.
- Private Catalog and private Git authentication are not supported by the
  current production CLI flow.
- Git submodule contents and Git LFS objects are not collected.
- A host allow-list is not a firewall. Deployments must enforce their own
  outbound network policy.
- Parent-directory fsync and uniform crash or power-loss durability guarantees
  are not provided across all filesystems and operating systems.
- Longer sustained fuzz campaigns, production-sized performance baselines,
  and broader concurrent update/replay/GC stress remain future hardening work.

### Closed tracking issue

- [Issue #5 — Complete Core 1.0 conformance and reproducible replay](https://github.com/cdin-editor/registry/issues/5) — **closed as completed** on 2026-11-06.

### Merged pull requests and merge commits

The commit links below are the exact merge commit SHAs returned by GitHub
for the listed pull requests. They are traceability references, not release
tags.

- [PR #1 — Bootstrap Registry Go engine](https://github.com/cdin-editor/registry/pull/1) — merge commit [`09bf8ee436f84c6bd5bd201fd7f72edbb1dd4e24`](https://github.com/cdin-editor/registry/commit/09bf8ee436f84c6bd5bd201fd7f72edbb1dd4e24).
- [PR #3 — Verified Catalog ingestion](https://github.com/cdin-editor/registry/pull/3) — merge commit [`709e2921818e34f4fc06024dd22e755a4aed12bd`](https://github.com/cdin-editor/registry/commit/709e2921818e34f4fc06024dd22e755a4aed12bd).
- [PR #6 — Deterministic SemVer and Resolver core](https://github.com/cdin-editor/registry/pull/6) — merge commit [`0a885211abf0d1804b699e12e370d5b45fcf8cea`](https://github.com/cdin-editor/registry/commit/0a885211abf0d1804b699e12e370d5b45fcf8cea).
- [PR #10 — Catalog fixtures, transport failures, and credentials](https://github.com/cdin-editor/registry/pull/10) — merge commit [`e4f50a173dd4751620157edf63b6105c331aa1a1`](https://github.com/cdin-editor/registry/commit/e4f50a173dd4751620157edf63b6105c331aa1a1).
- [PR #11 — Structured Resolver evidence](https://github.com/cdin-editor/registry/pull/11) — merge commit [`34ee56d89d71a45b535445df764c2d7fed509cc2`](https://github.com/cdin-editor/registry/commit/34ee56d89d71a45b535445df764c2d7fed509cc2).
- [PR #12 — Collector credential and cancellation hardening](https://github.com/cdin-editor/registry/pull/12) — merge commit [`563cd474a5899279880d40d14047b311414fe8ae`](https://github.com/cdin-editor/registry/commit/563cd474a5899279880d40d14047b311414fe8ae).
- [PR #13 — Workspace transaction failure coverage](https://github.com/cdin-editor/registry/pull/13) — merge commit [`53031f1539046d88fd7f45dd656f359d35af7314`](https://github.com/cdin-editor/registry/commit/53031f1539046d88fd7f45dd656f359d35af7314).
- [PR #14 — Atomic Workspace publication](https://github.com/cdin-editor/registry/pull/14) — merge commit [`3b968b270f2e6939ea983cf626754bbc556f2c0f`](https://github.com/cdin-editor/registry/commit/3b968b270f2e6939ea983cf626754bbc556f2c0f).
- [PR #15 — Deterministic Manifest bytes and RFC 8785 vectors](https://github.com/cdin-editor/registry/pull/15) — merge commit [`2f591440fe418f8a32d36c25d48cba16c91f2702`](https://github.com/cdin-editor/registry/commit/2f591440fe418f8a32d36c25d48cba16c91f2702).
- [PR #16 — Private Windows Workspace permissions](https://github.com/cdin-editor/registry/pull/16) — merge commit [`06034d43320ae8a52ee6452ff98e9ad4b3983b8f`](https://github.com/cdin-editor/registry/commit/06034d43320ae8a52ee6452ff98e9ad4b3983b8f).
- [PR #18 — Read-only Manifest verification](https://github.com/cdin-editor/registry/pull/18) — merge commit [`ed770b713cf77552c0968746214c1166b2327129`](https://github.com/cdin-editor/registry/commit/ed770b713cf77552c0968746214c1166b2327129).
- [PR #19 — Manifest replay from pinned commits](https://github.com/cdin-editor/registry/pull/19) — merge commit [`7dfd890f78e8f88000e3c364e8a190e709facb95`](https://github.com/cdin-editor/registry/commit/7dfd890f78e8f88000e3c364e8a190e709facb95).
- [PR #20 — Catalog updates preserve prepared Workspaces](https://github.com/cdin-editor/registry/pull/20) — merge commit [`7a8b8a87eac3a885810a3ab5173dbacb92766407`](https://github.com/cdin-editor/registry/commit/7a8b8a87eac3a885810a3ab5173dbacb92766407).
- [PR #21 — Manifest verify and replay CLI commands](https://github.com/cdin-editor/registry/pull/21) — merge commit [`f59334c90bcf805bdf96c49412a2cc2a56c0c887`](https://github.com/cdin-editor/registry/commit/f59334c90bcf805bdf96c49412a2cc2a56c0c887).
- [PR #22 — Metadata cache retention and garbage collection](https://github.com/cdin-editor/registry/pull/22) — merge commit [`cdeae8ec28a414bd25fb3f416251269013422e57`](https://github.com/cdin-editor/registry/commit/cdeae8ec28a414bd25fb3f416251269013422e57).
- [PR #23 — Content-addressed Git-object cache](https://github.com/cdin-editor/registry/pull/23) — merge commit [`46562550d6a1b5e8ed9675c1a30b947e25013a16`](https://github.com/cdin-editor/registry/commit/46562550d6a1b5e8ed9675c1a30b947e25013a16).
- [PR #24 — Immutable Manifest update-discovery report](https://github.com/cdin-editor/registry/pull/24) — merge commit [`e1cdcd208cd7deef5de2d4f038705fbbdd9b3b9f`](https://github.com/cdin-editor/registry/commit/e1cdcd208cd7deef5de2d4f038705fbbdd9b3b9f).
- [PR #25 — Bounded concurrency and Phase 8 hardening](https://github.com/cdin-editor/registry/pull/25) — merge commit [`f12c0eb48290b356c0ca373507874ac8b84b7954`](https://github.com/cdin-editor/registry/commit/f12c0eb48290b356c0ca373507874ac8b84b7954).
- [PR #26 — Phase 9 conformance and clean-room replay verification](https://github.com/cdin-editor/registry/pull/26) — merge commit [`a919a53cc5f22904edfca094b4d42be497aaa640`](https://github.com/cdin-editor/registry/commit/a919a53cc5f22904edfca094b4d42be497aaa640).
- [PR #27 — Promote the reconciled develop history to main](https://github.com/cdin-editor/registry/pull/27) — merge commit [`0909d92753701095be96b2c2a45650abfd0999d4`](https://github.com/cdin-editor/registry/commit/0909d92753701095be96b2c2a45650abfd0999d4).

[PR #17](https://github.com/cdin-editor/registry/pull/17) was closed without being merged; its follow-up was included in [PR #19](https://github.com/cdin-editor/registry/pull/19), so PR #17 is not listed as delivered work.
