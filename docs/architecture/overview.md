# Architecture overview

## Purpose

Registry is an independent build-time/delivery-plane engine. It discovers component releases, resolves declarative requirements, collects exact source revisions and prepares reproducible workspaces for independent consumers.

The governing invariant is:

> Registry must understand requirements, not consumer identity.

A Product Definition is data. Registry must not contain product-ID branches, product-specific plug-ins or consumer-owned workflow logic.

## System boundary

| Owner | Responsibility |
|---|---|
| Component repositories | Own source, package versions, release metadata, component tests and publication. |
| Product Definition | Declares root components, variants, constraints and target-environment contracts. |
| Catalog publisher | Publishes indexes that reference immutable release metadata. |
| Registry | Validates metadata, resolves versions/dependencies/contracts, collects immutable sources and produces a workspace, manifest and report. |
| Consumer | Owns product configuration, integration, build, product tests, packaging and launch. |
| Spec | Owns normative architecture and public data contracts. |

Registry is infrastructure, not a product component. No component links it as a runtime dependency.

## Current implementation

The initial vertical slice supports:

- CLI version and help output.
- Listing and displaying the embedded Registry v1 schemas.
- Validating JSON documents against the five Spec schemas.
- Validating YAML Product Definitions after converting the YAML 1.2 data model to JSON-compatible values.
- Rejection of duplicate mapping keys, unsupported YAML tags, non-string mapping keys and multiple YAML documents.

Catalog ingestion, resolution, source collection and workspace production are not implemented yet.
