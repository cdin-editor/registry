# Registry 1.0.0 release readiness

**Status: RELEASE PUBLISHED — CLEAN-CONSUMER ACCEPTANCE INCOMPLETE.** The immutable `v1.0.0` GitHub Release was published on 2026-11-14 from tag commit `c5b6900319cebe22f2d035a6c3b23a32f06a48b5` (GitHub Release ID `445222506`). Its inventory contains five target executables, the same-tag dynamic Catalog, `registry-release-1.0.0.json` and `SHA256SUMS`. Release build/test, assembly/checksum verification and publication jobs succeeded. The published binary/Catalog have not yet passed the clean-consumer, offline-cache, Manifest verification and exact replay gates in section 4. Keep Registry #71 OPEN until those are verified; publication alone is not the entire acceptance.

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
- [ ] The production CDIN Catalog uses one GitHub Release source per component/variant; each candidate version is derived from its repository tag and the tag-resolved commit is verified against canonical ADR-0028 Release Metadata. Historical releases without that metadata asset are skipped; malformed or mismatched metadata fails closed.
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

- [x] The release is built from the exact immutable `v1.0.0` tag commit `c5b6900319cebe22f2d035a6c3b23a32f06a48b5`. The tag resolves to that same commit and remains immutable.
- [x] Five native executable targets were built and inspected: Linux x86_64/ARM64, macOS x86_64/ARM64 and Windows x86_64 UCRT64. Release run [43586740253](https://github.com/cdin-editor/registry/actions/runs/43586740253) verified each binary's actual OS/architecture before upload.
- [x] Each executable's embedded build target and actual ELF/Mach-O/PE target were checked by the release build validator; all five target jobs passed in run [43586740253](https://github.com/cdin-editor/registry/actions/runs/43586740253).
- [x] The same tag commit produced `catalog-index.json` and `registry-release-1.0.0.json`. The Catalog is 1,538 bytes (SHA-256 `92d6d837d2dfa94c516036e406e2b7b12c3398a94acb8bfc4d26726bc47f60e0`); the descriptor is 1,633 bytes (SHA-256 `b47030a2f6ebba6a3ccfe88ca28437111f1e71b0c0ae5e8fbcb6a6eeaf78e495`) and records the tag/source identity and all five executable targets.
- [x] The final `SHA256SUMS` covers all five executables, the Catalog and the release descriptor. The Pipeline assemble and independent checksum-verification stages passed in run [43586740253](https://github.com/cdin-editor/registry/actions/runs/43586740253).
- [x] Publisher preflight validates the exact tag/source match, static/dynamic Catalog shape, duplicate release-source identities, required target inventory and descriptor identities before the Release is assembled.
- [x] The release workflow pins Pipeline to immutable commit `1682c520bb5be073eeaa07db03a254fb2e247545`; publication uses the workflow's scoped GitHub token and the `contents: write` permission required for the Release.
- [ ] Main-CI automation is idempotent: an already complete immutable prerelease is skipped; an existing but incomplete Release fails with an actionable error rather than being silently treated as complete.

### 4. Clean-consumer acceptance

- [ ] A clean runner downloads the Registry executable, Catalog and descriptor from the same immutable Release without cloning the Registry source or installing Go.
- [ ] The consumer verifies descriptor identity, Catalog byte count/digest, executable target and `SHA256SUMS` before invoking Registry.
- [ ] The published binary validates a Product Definition, resolves a fixture Catalog, prepares a Workspace, verifies the resulting Manifest and replays it to a second fresh output directory.
- [ ] The test runs on all supported native targets or a documented split of native CI plus cross-platform artifact verification; no target may be inferred from the runner filename alone.
- [x] The published Release asset inventory and tag-resolved commit are recorded in [GitHub Release `v1.0.0`](https://github.com/cdin-editor/registry/releases/tag/v1.0.0), release descriptor and tag commit.

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
2. Keep `internal/buildinfo/version.go` at the untagged `development` default; add the dated changelog section matching the exact release tag and review the release notes. Never write the release version into source defaults.
3. Run the full CI and release preflight against the exact release candidate commit.
4. Create and push the immutable `v1.0.0` tag at that commit. The tag-push release workflow builds and publishes; do not manually upload or retarget assets.
5. Verify the published Release contains the exact five executables, Catalog, descriptor and final checksum manifest. Re-download and verify every asset from a clean consumer.
6. Only then mark Registry 1.0.0 released and close the corresponding release-tracking requirements.

The GitHub Release is now published as stable `v1.0.0`; until the remaining clean-consumer, data-contract compatibility, offline/replay, documentation and quality gates pass, describe the release as **published but not fully accepted**. Do not equate the published tag with completion of Registry #71 or downstream CDIN #121/#122.
