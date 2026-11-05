# Changelog

All notable changes to CDIN Registry are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Bootstrap a standalone Go CLI using the CDIN repository engineering layout.
- Embed the five Registry v1 JSON Schemas mirrored from the accepted Spec contract.
- Add JSON Schema validation for Registry documents and YAML 1.2 authoring support for Product Definitions.
- Add schema inspection commands, example documents, unit tests, CI and contributor/security documentation.

### Not yet implemented

- Catalog ingestion and metadata digest verification.
- Deterministic version, dependency and contract resolution.
- Immutable Git source collection and prepared workspace publication.
- Resolved Manifest and Resolution Report production.
