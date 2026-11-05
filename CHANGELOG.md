# Changelog

All notable changes to CDIN Registry are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Add Registry Resolver 1.1.0 report evidence: sourced applied constraints, immutable candidate provenance, typed conflict/rejection codes and structured contract-provider diagnostics.
- Add adversarial Resolver coverage for diamond graphs, duplicated constraints/providers, transitive conflicts, fallback branch isolation, permutation invariance and bounded search.
- Add SemVer 2.0.0 parser vectors alongside precedence and unbounded-identifier tests.
- Add the provider-neutral Pipeline task graph and pin reusable CI, documentation, security and release workflows to a reviewed Pipeline commit.
- Add PSX repository-structure validation and executable governance tests for PR/Issue templates and repository policy.
- Add GIX branch-flow validation using GIX itself, plus a main/develop Git Flow matching the CDIN repositories.
- Add pull request and issue templates for bugs, feature requests and Spec/contract changes.
- Pin the Registry schema mirror to an immutable CDIN Spec commit and add a drift-check/synchronization script.
- Add cross-platform release target compilation through Pipeline's release runner.
- Add a transport-injected Catalog Index loader with digest-before-parse verification, release identity checks, default-variant normalization, duplicate deduplication and conflict rejection.
- Preserve typed dependency and contract declarations in Catalog releases for resolver consumption.
- Add SemVer 2.0.0 parser/precedence and Registry 1.0.0 constraint parsing/evaluation.
- Add deterministic dependency/version/contract resolver with backtracking, cycle diagnostics, branch-scoped evidence and structured in-memory results.

### Changed

- Make Pipeline the source of truth for local task execution and CI/release orchestration; keep GitHub workflows as thin provider adapters.
- Require governance, GIX configuration, PSX structure and schema-mirror checks in the release verification graph.
- Strengthen JSON/YAML validation by rejecting duplicate JSON keys, unsupported YAML forms, recursive aliases and trailing documents/values.
- Update contributor, development and repository documentation to describe the shared engineering tools and the implemented verification boundary.

### Not yet implemented

- Full Catalog HTTP transport policy, SSRF/redirect controls, cache and local fixture integration.
- Immutable Git source collection and transactional workspace publication.
- Serialized Resolved Manifest and Resolution Report production, RFC 8785 canonical JSON and replay.
- CLI composition, update discovery and release hardening.
