# Registry concepts

This page explains the terms that appear in Registry commands and output files.

## Component

A component is a named part of a product. In CDIN, examples include Kernel,
Runtime, Backend, and Frontend.

Registry does not identify a component by its Git repository name. It uses a
stable `component_id` published in release metadata. The same component can
have multiple released versions and, when needed, multiple variants.

## Release

A release is a published version of one component and one variant. Its release
metadata tells Registry:

- the component ID, variant, and semantic version;
- whether the release is active or withdrawn;
- which source repositories and exact Git commits make up the release;
- which other components it depends on;
- which versioned contracts it provides or requires.

A release is descriptive data. Registry reads and validates it; it does not run
scripts from the release.

## Variant

A variant is a named choice of a component. For example, a Frontend publisher
could offer `tui`, `gui-sdl`, and `headless` variants. A Product Definition
must request the variant it needs. When no variant is written, Registry uses
`default`.

A variant is not automatically available just because its name appears in an
example. It must exist in a published Catalog.

## Source unit

A source unit is one exact piece of source needed by a release. It identifies a
Git repository, a full commit ID, and optionally a subdirectory within that
repository. A release may contain more than one source unit.

Registry collects the declared revision, not the latest branch or tag. This is
what lets a Manifest identify the source used by a prepared build.

## Catalog

A Catalog Index is a JSON document that lists available component releases.
Each entry points to a separate Component Release Metadata document and
contains the SHA-256 digest of its exact bytes.

A Catalog is a source of release information. It is not a source archive and
does not contain the component code itself.

See [Catalogs and releases](catalogs.md).

## Product Definition

A Product Definition is the request sent to Registry. It names the root
components a product needs and the acceptable version range for each one. It
can also declare contracts supplied by the target environment.

The Product Definition does not contain the Resolver algorithm or executable
build instructions. See [Product Definition](product-definition.md).

## Resolve

Resolve means checking the Product Definition against the supplied Catalogs and
choosing a compatible set of releases. Dependencies declared by selected
releases are included automatically.

Resolve does not download component source. It returns a Resolution Report
describing the selection or the reasons it could not be made.

## Artifact package

An artifact package is a publisher-built archive attached to Component Release
Metadata. Its descriptor identifies an exact target tuple, archive format,
layout version, URI, byte size, SHA-256 digest, source-unit provenance and
build toolchain. Registry retrieves and verifies the archive but does not run
its build recipe. Supported package layouts are versioned; consumers must not
guess paths inside an archive.

Runtime module archives use a universal target. Native development and SDK
packages identify a concrete OS, architecture and ABI. See
[Immutable artifact delivery](artifact-delivery.md).

## Delivery mode

The Product Definition may request `source`, `artifact`, or
`artifact-or-source` delivery. The default remains source. Artifact selection
takes place after version/dependency resolution and cannot alter the selected
component release. Exact target matches take priority over universal runtime
packages; ambiguity and missing strict-mode packages are errors.

## Workspace

A Workspace is a new directory containing the source units and/or verified
package directories selected by Registry, together with the records that
explain the selection. CDIN can use that directory as input for its own build
and integration steps.

Registry never runs the component code it collects or extracts.

## Resolved Manifest

The Resolved Manifest is the record of a successful preparation. It includes
the selected component releases, the Catalog and release-metadata identities,
the exact source commit IDs, and the paths where the source was placed.

Keep the Manifest with the build output. It is the input for later verification
and replay.

## Resolution Report

The Resolution Report explains what Registry selected, rejected, or could not
resolve. It is useful both when a request succeeds and when it fails.

## Update Discovery Report

An Update Discovery Report compares a previously prepared Manifest with the
current Product Definition and Catalog state. It shows what would change if
the product were resolved again. It does not change the old Workspace or
Manifest, and it does not download new source.
