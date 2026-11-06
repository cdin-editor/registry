# Security and trust boundaries

This document states what Registry verifies, what remains an operator responsibility,
and which supply-chain guarantees Registry deliberately does not claim.

## Inputs and trust roots

- Product Definitions, Catalog Indexes, Release Metadata and retrieved Git content
  are untrusted data. JSON/YAML parsing is strict; unknown metadata is never
  executed as code, templates, plugins or configuration scripts.
- Catalog metadata retrieval uses HTTPS, the platform's normal TLS certificate
  trust store, exact-host allow-lists, redirect revalidation, DNS/IP checks and
  bounded response reads. The default TLS minimum is 1.2. The allow-list is a
  destination policy, not a publisher identity or signing trust root.
- Catalog Index SHA-256 values bind Release Metadata bytes to the declared
  Catalog entry. A matching digest proves byte equality with that declaration;
  it does not prove who published the Catalog or whether the publisher is
  trustworthy. HTTPS certificate validation does not replace publisher
  signatures.
- Catalog metadata fetching is public and unauthenticated. It does not infer
  credentials from URLs, local configuration, cookies, proxies or ambient
  environment. Private Catalog authentication is not implemented.
- Git source retrieval requires an explicit repository policy. The optional
  `GitCredentialProvider` is injected by the caller only after URI policy
  accepts the repository. Provider values are restricted to the credential
  environment boundary; secrets must not enter URLs, Git arguments or logs.
  Ambient credential helpers, askpass settings, SSH agents and proxies are not
  inherited.

## Source and execution boundary

- Registry accepts only full Git commit object IDs, checks out detached and
  re-verifies the commit. This makes the selected revision immutable; it does
  not establish publisher authenticity, provenance attestation or code safety.
- Git hooks, filters, prompts, LFS smudge/process filters and automatic
  submodule recursion are disabled. Submodule contents and Git LFS objects are
  not fetched or materialized. Symlinks and special files in collected trees
  are rejected.
- Source materialization is bounded to 100,000 files / 1 GiB per Source Unit
  and 500,000 files / 8 GiB across one Workspace. Copying remains sequential
  and checks cancellation while streaming regular files.
- Registry copies source into a prepared Workspace but never executes source
  code, build scripts, tests, package hooks or component binaries. CDIN's
  composition/build pipeline owns execution and its sandbox policy.
- A hostname allow-list and Registry's DNS checks do not enforce deployment
  egress. Production deployments must apply firewall, container, VM or
  equivalent network policy that blocks private/link-local destinations and
  constrains Git traffic.

## Manifest, replay, update discovery and caches

- A Resolved Manifest is a deterministic record of selected versions,
  metadata identities and full source commits. Canonical-byte and SHA-256
  verification detect changes relative to the bytes/digest being checked;
  SHA-256 alone is not a signature or attestation.
- Replay verifies the saved Manifest and reconstructs only the recorded full
  commits. It does not rerun version selection and does not execute source.
- Update discovery verifies the historical Manifest before loading current
  Catalog state. Withdrawal and identity-change evidence is recorded in a
  separate deterministic report; it never rewrites or invalidates the old
  Manifest, prepared Workspace or retained cache pin.
- Metadata cache entries are re-hashed against their declared SHA-256 before
  use. Git-object cache entries are checked against the full commit object ID
  and object-graph integrity before checkout. Caches are optimizations, not
  authorities: corrupt entries are rejected/refetched, and cache contents do
  not establish publisher trust.
- Cache pins and active leases protect referenced identities from GC. GC
  validates the complete reference snapshot and fails closed when it is
  incomplete or unsafe. It does not guess that stale locks/leases are safe to
  remove after abnormal termination.

## Cache filesystem permissions

- Metadata and Git-object cache trees are secured with platform-native
  permissions before use. Unix directories are restricted to mode `0700`
  and regular files to `0600`.
- On Windows, cache DACLs are protected from inherited parent grants and grant
  full access only to the current process user, Local System and the built-in
  Administrators group. Child files/directories inherit the restricted DACL.
  Native Windows tests inspect the resulting security descriptors and reject
  World/Authenticated Users grants.
- Registry does not rewrite permissions on the caller-selected parent directory
  above the cache root. Operators must choose a cache parent that is itself
  controlled by the intended user/service account and cannot be replaced by an
  untrusted principal. The protected cache root and descendants do not make an
  unsafe parent directory safe.

## Guarantees not provided

Registry does not currently provide:

- Publisher signing, signature verification, signed release catalogs or
  cryptographic attestations.
- SBOM generation, dependency/license analysis, vulnerability scanning of
  selected components or a vulnerability acceptance policy.
- Deployment firewall enforcement, sandboxing of CDIN's later build/runtime
  execution, or isolation of arbitrary component code.
- Git submodule retrieval or Git LFS object retrieval.
- Uniform parent-directory fsync or crash/power-loss durability guarantees on
  every supported filesystem and operating system.

The repository's CI security scan evaluates Registry's own source and
dependencies; it is not a vulnerability scan of the components Registry
downloads. Operators must not interpret a green Registry CI run as a clean
bill of health for a selected component.
