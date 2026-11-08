# Limitations and security

Registry handles untrusted release information and source repositories. It
validates the data it reads, verifies declared digests, and collects exact Git
revisions. These checks make a prepared result identifiable and repeatable;
they do not make every downloaded component safe to execute.

## Network access

- Remote Catalog Indexes and release metadata are retrieved over HTTPS.
- Remote source repositories must use HTTPS and pass the explicit host
  allow-list.
- Every host needed for the Catalog, metadata, and source repositories must be
  supplied with `--allow-host`. Registry does not infer trust from a URL
  written inside a release document.
- The production Catalog fetcher supports public, unauthenticated HTTPS
  metadata. Private Catalog authentication is not supported by the CLI.
- Git credentials are not read from ambient credential helpers, SSH agents,
  or machine configuration. Private Git repositories are not supported by
  the production CLI flow.
- A host allow-list is not a firewall. Deployments must enforce their own
  outbound network policy, including restrictions on private and link-local
  addresses.

## Artifact package behavior

Registry verifies the declared package size and SHA-256 before extracting an
artifact. It extracts only regular files and directories; links, special files,
privileged modes, unsafe paths, and case/normalization collisions are rejected.
Compressed size, expanded size, entry count and path depth are bounded.

Offline artifact preparation requires a local Catalog Index and already
verified cache entries for all selected release metadata and package assets.
Every selected component must use artifact delivery. The CLI rejects source
fallback because a source checkout requires Git retrieval. Offline artifact
replay has the same all-artifact requirement.

An artifact digest proves byte identity relative to the trusted Catalog and
release metadata. It does not identify or authenticate the publisher.
Attestation references are recorded but are not cryptographically verified by
this implementation.


Registry validates documents against the embedded public schemas, checks the
SHA-256 digest of release metadata before parsing it, verifies release
identities, and requires full Git commit IDs.

It validates the collected source tree and rejects unsafe paths, symlinks, and
special files. Git hooks, filters, submodule recursion, and Git LFS retrieval
are not run as part of source collection.

## What Registry does not guarantee

- Registry never executes component code, build scripts, hooks, or tests.
- Registry does not compile components, install their dependencies, build
  CDIN, create installers, or publish application releases.
- A SHA-256 digest proves that bytes match a digest value. It does not prove
  who published those bytes.
- A Git commit ID identifies a Git object. It is not a publisher signature.
- Registry does not sign releases, verify publisher signatures, create
  attestations or SBOMs, or scan components for vulnerabilities.
- Registry does not sandbox the later CDIN build. Build isolation is the
  responsibility of the build environment.
- Git submodule contents and Git LFS objects are not collected.
- Parent-directory fsync and uniform crash or power-loss durability guarantees
  are not provided across all filesystems and operating systems.

## Practical advice

Use trusted Catalog publishers, immutable release-metadata URLs, protected
source repositories, reviewed release commits, and a restricted build runner.
Keep the Product Definition, Catalog Indexes, Manifest, Report, and CLI receipt
with the build record.

For the exact supported document fields, see
[CDIN Spec — Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
