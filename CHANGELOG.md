# Changelog

Notable changes to CDIN Registry are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
