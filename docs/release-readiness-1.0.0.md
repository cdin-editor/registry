# Registry 1.0.0 release readiness

**Status: NOT READY — no stable `v1.0.0` release has been published.**

This checklist defines the acceptance boundary for the Registry CLI/package release. It does not claim that CDIN itself is product-ready. Registry is a generic build-time delivery engine; a downstream product remains responsible for its own integration, build, tests, installers and launch.

## Version boundaries

Keep these version tracks separate:

- **Registry CLI/package:** the executable version shown by `registry --version`. Release builds derive it from the exact immutable Git tag (`v1.0.0` becomes `1.0.0`); untagged developer builds report `development`.
- **Spec release and Registry Data Contracts:** schema and contract versions pinned in `schemas/registry/v1/SOURCE.toml`. They are not changed merely because the CLI reaches 1.0.0.
- **Document schema versions:** Product Definition, Catalog Index, Component Release Metadata, Manifest, Resolution Report and Update Discovery Report have their own compatibility rules.
- **Catalog model:** static Catalog 1.0.x (`entries`) and dynamic Catalog 1.1.x+ (`release_sources`) are separate supported shapes. A document must not mix them.

A Registry 1.0.0 tag must not be interpreted as Spec 1.0.0, Catalog 1.0.0, or a CDIN product release.

## Required release gates

### 1. Public CLI and data-contract behavior

- [ ] The final public command list, flags, input formats, exit behavior and machine-readable output are reviewed and documented as the 1.x compatibility surface.
- [ ] All six public document types validate against the exact embedded schemas pinned to the Spec commit/tree/blob IDs in `SOURCE.toml`.
- [ ] Static Catalog 1.0.x and dynamic Catalog 1.1.x+ are both covered by valid and invalid fixtures.
- [ ] Mixed `entries` + `release_sources`, duplicate identities, invalid provider/source definitions, malformed metadata and ambiguous candidates fail closed.
- [ ] SemVer selection, dependency/contract resolution, deterministic ordering, conflict reporting and update discovery have executable regression coverage.
- [ ] Existing source-delivery Product Definitions remain backward-compatible when the optional artifact-delivery policy is absent.

### 2. Reproducibility and Workspace safety

- [ ] Resolve and Prepare select immutable component metadata and full Git commit identities; source collection must never infer a mutable branch or tag as a revision.
- [ ] Manifest verification checks canonical bytes, schema and semantic provenance; replay reproduces the exact saved source/artifact selection without resolving newer versions.
- [ ] Workspace publication remains all-or-nothing under failures, cancellation and concurrent operations on Linux, macOS and Windows.
- [ ] Metadata/Git-object caches, Manifest pinning, concurrent population, cache garbage collection and offline misses have failure-injection and race coverage.
- [ ] Artifact delivery checks declared size and SHA-256 before extraction, rejects unsafe archive members/path overrides and preserves exact descriptor provenance.
- [ ] Offline artifact Prepare/Replay uses only a local Catalog and already verified cache entries; source fallback must remain rejected in offline mode.

### 3. Release bundle and publisher

- [ ] The release is built from the exact immutable `v1.0.0` tag commit. The tag must never be moved to a later main commit.
- [ ] Five native executable targets are built and inspected: Linux x86_64/ARM64, macOS x86_64/ARM64 and Windows x86_64 UCRT64.
- [ ] Each executable's embedded Go build information and actual ELF/Mach-O/PE header match its declared target.
- [ ] The same tag commit produces `catalog-index.json` and `registry-release-1.0.0.json`; the descriptor records the full source commit, Catalog identity, exact Catalog SHA-256/size and all five executable identities/digests/sizes.
- [ ] The final `SHA256SUMS` covers all five executables, the Catalog and the release descriptor. Any additional signatures/attestations are supplementary and must not replace digest verification.
- [ ] Publisher preflight rejects a tag/source mismatch, malformed or mixed Catalog models, duplicate release-source identities, missing targets and incomplete artifact inventories.
- [ ] Pipeline and helper workflow references are full immutable commit SHAs. Release publication uses authenticated, least-privilege GitHub API access.
- [ ] Main-CI automation is idempotent: an already complete immutable prerelease is skipped; an existing but incomplete Release fails with an actionable error rather than being silently treated as complete.

### 4. Clean-consumer acceptance

- [ ] A clean runner downloads the Registry executable, Catalog and descriptor from the same immutable Release without cloning the Registry source or installing Go.
- [ ] The consumer verifies descriptor identity, Catalog byte count/digest, executable target and `SHA256SUMS` before invoking Registry.
- [ ] The published binary validates a Product Definition, resolves a fixture Catalog, prepares a Workspace, verifies the resulting Manifest and replays it to a second fresh output directory.
- [ ] The test runs on all supported native targets or a documented split of native CI plus cross-platform artifact verification; no target may be inferred from the runner filename alone.
- [ ] The published Release asset inventory and exact tag-resolved commit are recorded in the release evidence.

The current open downstream publication requirement is tracked in [Registry #58](https://github.com/cdin-editor/registry/issues/58). That issue closes only after the real immutable Release assets exist and a consumer verifies them; publisher code merging is not sufficient.

### 5. Documentation and repository quality

- [ ] README, Getting Started, command reference, examples, development guide and changelog agree on the same current CLI version and Pipeline revision.
- [ ] All repository-local Markdown links resolve; documentation/YAML workflows pass on the exact release candidate commit.
- [ ] `make check`, release preflight, native Workspace integration, security scans and the five-target release matrix pass on the release candidate.
- [ ] Release notes describe actual delivered behavior and known limitations; no capability is described as supported solely because a schema field exists.

## Explicitly documented limitations

The following are not silently implied by a 1.0.0 release. Keep them visible in the security/limitations documentation and decide separately before promising them as product capabilities:

- Private Catalog and private Git authentication are not supported by the current production CLI flow.
- Git submodule contents and Git LFS objects are not collected.
- SHA-256 and Git commit IDs establish content identity, not publisher authenticity. Registry does not cryptographically verify publisher signatures/attestations, generate an SBOM, or scan component vulnerabilities.
- Registry does not sandbox downstream builds or execute component code.
- A host allow-list is not a firewall; deployments own outbound network restrictions.
- Uniform crash/power-loss durability guarantees are not provided across all filesystems.
- Longer sustained fuzzing, production-scale performance baselines and broader long-running concurrent replay/update/GC stress remain hardening work unless explicitly promoted into the 1.0.0 acceptance scope.

## Release procedure

1. Complete the gates above and link the exact evidence from the release issue/PRs.
2. Update `internal/buildinfo/version.go` to `1.0.0`, add a dated `[1.0.0]` changelog section and review the release notes.
3. Run the full CI and release preflight against the exact release candidate commit.
4. Create and push the immutable `v1.0.0` tag at that commit. The tag-push release workflow builds and publishes; do not manually upload or retarget assets.
5. Verify the published Release contains the exact five executables, Catalog, descriptor and final checksum manifest. Re-download and verify every asset from a clean consumer.
6. Only then mark Registry 1.0.0 released and close the corresponding release-tracking requirements.

Until these gates pass, describe Registry as a development prerelease and do not claim stable 1.0.0 availability.
