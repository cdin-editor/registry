# Catalogs and component releases

A Catalog makes published component releases visible to Registry. It is a
small JSON index; it does not contain the component source code.

A Catalog has two kinds of documents:

1. A **Catalog Index** lists releases and points to their metadata documents.
2. **Component Release Metadata** describes one exact release, including the
   Git commit or commits from which Registry can collect source.

The index stores the SHA-256 digest of each metadata document. Registry checks
that digest before it trusts or parses the metadata.

## 1. Component Release Metadata

A release metadata document describes one component version. Here is an
illustrative example:

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

Replace the example component, repository, version, and commit with the values
for a real release. The revision must be a full Git commit ID: 40 hexadecimal
characters for SHA-1 or 64 for SHA-256. A branch name, tag name, or shortened
commit ID is not sufficient.

A release may declare:

- `dependencies`: other components and the version constraints they require;
- `contracts.provides`: versioned contracts the release provides;
- `contracts.requires`: versioned contracts that must be provided by another
  selected component or by the target environment;
- multiple `source_units`: when one release needs source from more than one
  repository or subdirectory.

A release with `status: "withdrawn"` is excluded from normal selection.

## 2. Catalog Index

A Catalog Index points to release metadata. For example:

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

The digest placeholder must be replaced with the lowercase SHA-256 digest of
the exact metadata file bytes. The example is not valid for resolution until
you replace it with a real digest and publish the metadata at the stated URI.

Calculate the digest after the metadata file is final:

```sh
sha256sum example-component-1.2.3.json
```

On macOS, the equivalent command is:

```sh
shasum -a 256 example-component-1.2.3.json
```

If you change even one byte in a published metadata document, publish a new
immutable metadata document and update the Catalog entry's digest. Do not
silently replace metadata while keeping the old digest.

## 3. Make a release discoverable

A typical publication sequence is:

1. Choose a stable `component_id` and variant. Keep them unchanged across
   releases of the same component.
2. Create a release with a semantic version such as `1.2.3`.
3. Record the exact full Git commit ID for every source unit.
4. Write and validate the Component Release Metadata.
5. Publish the metadata at an immutable HTTPS URL.
6. Calculate its SHA-256 digest.
7. Add or update the matching entry in a Catalog Index.
8. Publish the updated Catalog Index.

Registry reads Catalogs; it does not publish component releases, upload
metadata, or host a Catalog for you. Those publication steps belong to the
component release process or Catalog publisher.

## 4. Validate the documents

```sh
./build/registry validate component-release ./release.json
./build/registry validate catalog-index ./catalog-index.json
```

The current production network fetcher supports public, unauthenticated HTTPS
metadata. The Catalog Index may also be supplied to the CLI as a local file.
All hosts used for remote Catalog, metadata, and source retrieval must be
explicitly allowed with `--allow-host`.
