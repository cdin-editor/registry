# Architecture overview

Registry is an independent build-time/delivery-plane engine. It discovers component releases, resolves declarative requirements, collects exact source revisions and prepares reproducible workspaces for independent consumers.

The governing invariant is:

> Registry must understand requirements, not consumer identity.

A Product Definition is data. Registry must not contain product-ID branches, product-specific plugins or consumer-owned workflow logic.

## System boundary

| Owner | Responsibility |
| --- | --- |
| Component repositories | Own source, package versions, release metadata, component tests and publication. |
| Product Definition | Declares root components, variants, constraints and target-environment contracts. |
| Catalog publisher | Publishes indexes that reference immutable release metadata. |
| Registry | Validates metadata, resolves versions/dependencies/contracts, collects immutable sources and produces a workspace, manifest and report. |
| Consumer | Owns product configuration, integration, build, product tests, packaging and launch. |
| Spec | Owns normative architecture and public data contracts. |

Registry is infrastructure, not a product component. No component links it as a runtime dependency.

## Implementation status

The initial Go CLI supports:

- Version/help output and schema listing/display.
- Embedded Registry v1 schemas mirrored from an immutable Spec commit.
- JSON and YAML Product Definition input validation.
- Duplicate-key and unsafe YAML/JSON structure rejection.
- A Pipeline task graph, PSX repository checks and GIX branch-flow configuration.

Catalog ingestion, resolution, source collection and workspace production are not implemented.
