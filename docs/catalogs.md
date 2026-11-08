# Catalogs and component releases

A Catalog makes published component releases discoverable to Registry. A Catalog Index is a small JSON data document; it does not contain component source code or define resolver behavior.

A Catalog has two document types:

1. A **Catalog Index** lists logical component releases and points to immutable metadata documents.
2. **Component Release Metadata** describes one exact release, including the source repositories and full Git commit IDs from which Registry can collect source.

The index stores the SHA-256 digest of each metadata document. Registry checks that digest against the exact fetched bytes before it parses or trusts the metadata.

## 1. Component Release Metadata

A release metadata document describes one component version. Here is an illustrative example:

```json
{
  "schema_version": "1.0.0",
  "component_id": "example-component",
  "variant": "default",
  "version": "1.2.3",
  "status": "active",
  "source_units": [
    {
      "source_unit_id": "source",
      "repository_uri": "https://github.com/example/component",
      "revision": "0123456789abcdef0123456789abcdef01234567"
    }
  ],
  "dependencies": [],
  "contracts": {
    "provides": [],
    "requires": []
  }
}
```

Component maintainers publish this document from their own release process. The revision must be a full Git commit ID: 40 hexadecimal characters for SHA-1 or 64 for SHA-256. Branch names, tag names, and shortened commit IDs are not valid source identities.

A release may declare dependencies, provided/required contracts, and multiple source units when one logical component uses more than one repository or subdirectory. Repository topology belongs in this metadata, not in a Product Definition or consumer build script.

A release with `status: "withdrawn"` is excluded from normal selection.

## 2. Immutable package artifacts

A Component Release may publish one or more prebuilt package descriptors in
its `artifacts` array. Each descriptor identifies an exact target, archive
format and layout, plus an immutable HTTPS URI, SHA-256 digest and byte size.
It also records the source-unit IDs and build toolchain used to produce the
package.

The release still records its source units even when consumers choose artifact
delivery. Those commits are provenance for the package; Registry does not
clone or build them on the artifact-delivery path.

Artifact kinds and layout IDs are closed contracts: the current runtime
package uses `runtime-module-archive` with the universal target and
`lua-module-tree-v1`; native development and SDK packages use a concrete
target, `native-development-package` or `sdk-package`, and
`cmake-prefix-v1`. Supported archive formats are `zip` and `tar.gz`.

Publishers must compute `size_bytes` and `sha256` from the exact final
archive bytes. Registry checks both before safe extraction and writes the
selected descriptor to the Manifest. It never executes archive content or
the recorded build recipe.

See [Immutable artifact delivery](artifact-delivery.md) for the full release
fragment and target-selection rules.


A Catalog Index points to Release Metadata and records its integrity digest:

```json
{
  "schema_version": "1.0.0",
  "catalog_id": "example-catalog",
  "entries": [
    {
      "component_id": "example-component",
      "variant": "default",
      "version": "1.2.3",
      "metadata_uri": "https://catalog.example/releases/example-component/1.2.3.json",
      "metadata_sha256": "REPLACE_WITH_64_HEX_SHA256_CHARACTERS"
    }
  ]
}
```

The digest must be the lowercase SHA-256 digest of the exact metadata bytes. If a published metadata document changes by even one byte, publish a new immutable metadata identity and update the Catalog entry. Never silently retarget a published metadata URI while retaining its old digest.

## 3. Version-controlled Catalog snapshots

Catalog Indexes are data, separate from Registry engine code. A Catalog publisher may maintain version-controlled snapshots in this repository under:

```text
catalogs/<catalog-id>/catalog-index.json
```

The current CDIN component Catalog is stored at:

```text
catalogs/cdin-components/catalog-index.json
```

That file is a generic Catalog input. It contains logical component identities, immutable metadata URIs, and metadata digests; it does not add CDIN-specific behavior to Registry. Consumers that pin a Registry source commit can read the Catalog snapshot from that exact checkout, making the engine and catalog revision reproducible together.

This location is a repository distribution convention, not a special resolver rule. The CLI still accepts any local Catalog Index path or remote Catalog URI through the same `--catalog` option.

## 4. Publish a release and update a Catalog

A typical publication sequence is:

1. Choose a stable `component_id` and variant. Keep them unchanged across releases of the same logical component.
2. Create a package release with a semantic version such as `1.2.3`.
3. Record the exact full Git commit ID for every source unit in the component-owned Release Metadata.
4. Validate and publish the metadata at an immutable HTTPS URI.
5. Calculate SHA-256 over the exact published metadata bytes.
6. Add the matching component ID, variant, version, metadata URI, and digest to the appropriate Catalog Index.
7. Validate the Catalog Index and commit the updated Catalog snapshot.
8. Consumers select the updated snapshot by pinning the Registry/catalog revision they build against.

The component maintainer remains authoritative for source and Release Metadata. The Catalog publisher maintains the index that makes immutable releases discoverable. Registry's generic engine reads the index, verifies metadata, and performs resolution/collection; it does not infer repositories by scanning source hosts or hardcode component names.

## 5. Validate and consume the documents

```sh
./build/registry validate component-release ./release.json
./build/registry validate catalog-index ./catalogs/cdin-components/catalog-index.json
```

A Catalog Index may be supplied to the CLI as a local file or remote URI. The production network fetcher supports public, unauthenticated HTTPS metadata. Every host used for remote Catalog, metadata, and source retrieval must be explicitly allowed with `--allow-host`. These host allow-lists are security policy and do not define component-to-repository mappings.
