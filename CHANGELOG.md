# Changelog

All notable changes to CDIN Registry are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Add the provider-neutral Pipeline task graph and pin reusable CI, documentation, security and release workflows to a reviewed Pipeline commit.
- Add PSX repository-structure validation and executable governance tests for PR/Issue templates and repository policy.
- Add GIX branch-flow validation using GIX itself, plus a main/develop Git Flow matching the CDIN repositories.
- Add pull request and issue templates for bugs, feature requests and Spec/contract changes.
- Pin the Registry schema mirror to an immutable CDIN Spec commit and add a drift-check/synchronization script.
- Add cross-platform release target compilation through Pipeline's release runner.

### Changed

- Make Pipeline the source of truth for local task execution and CI/release orchestration; keep GitHub workflows as thin provider adapters.
- Require governance, GIX configuration, PSX structure and schema-mirror checks in the release verification graph.
- Strengthen JSON/YAML validation by rejecting duplicate JSON keys, unsupported YAML forms, recursive aliases and trailing documents/values.
- Update contributor, development and repository documentation to describe the shared engineering tools and the implemented verification boundary.

### Not yet implemented

- Catalog ingestion and metadata digest verification.
- Deterministic version, dependency and contract resolution.
- Immutable Git source collection and transactional workspace publication.
- Resolved Manifest and Resolution Report production.
