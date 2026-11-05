# Architecture overview

Registry is an independent build-time/delivery-plane engine. It discovers
component releases, resolves declarative requirements, collects exact source
revisions and prepares reproducible workspaces for independent consumers.

The governing invariant is:

> Registry must understand requirements, not consumer identity.

A Product Definition is data. Registry must not contain product-ID branches,
product-specific plugins or consumer-owned workflow logic.

## System boundary

| Owner | Responsibility |
| --- | --- |
| Component repositories | Own source, package versions, release metadata, tags, component tests and publication. |
| Product Definition | Declares root component requirements, variants, constraints and target-environment contracts. |
| Catalog publisher | Publishes indexes that reference immutable release metadata. |
| Registry | Validates metadata, resolves versions/dependencies/contracts, collects immutable sources and produces workspace, manifest and report outputs. |
| Consumer | Owns product configuration, integration, build, product tests, packaging and launch. |
| Spec | Owns normative architecture and public data contracts. |

Registry is infrastructure, not a product component. No component links it as a
runtime dependency.

## Implemented vertical slices

- Go CLI, version/help and schema listing/display.
- Embedded Registry v1 schemas mirrored from an immutable Spec commit.
- Strict JSON and safe Product Definition YAML parsing.
- Catalog Index loading with injected fetch boundary, exact-byte SHA-256
  verification before release parsing, default-variant normalization and
  conflicting identity rejection.
- Typed dependency and contract declaration preservation in Catalog releases.
- SemVer 2.0.0 parsing/precedence and Registry 1.0.0 constraint parsing.
- Deterministic in-memory resolver: transitive dependency closure, constraint
  intersection, backtracking, prerelease/withdrawal policy, contract providers,
  cycle paths and dependency-first ordering.

## Deliberately separate future stages

The following are not implemented by the Resolver and must remain separate
packages/adapters:

- Production Catalog transport policy, host allow-list, redirect controls and cache.
- Git Collector with actual immutable commit verification.
- Source subdirectory and symlink containment.
- Transactional workspace staging and all-or-nothing publication.
- Schema-conformant Manifest/Report serialization, RFC 8785 canonical JSON and replay.
- CLI composition for prepare/resolve/replay and update discovery.

The Resolver returns an in-memory graph and structured report only. It does not
fetch sources, write files or execute component code.
