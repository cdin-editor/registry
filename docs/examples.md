# Examples

All URLs, repository names, commit IDs, versions, and digests in this page are
illustrative. Replace them with values from releases that have actually been
published to your Catalog.

## 1. Ask for a small product

A Product Definition can request one component:

```yaml
schema_version: "1.0.0"
product:
  id: com.example.notes
  name: Example Notes
components:
  - component_id: editor
    version_constraint: ">=1.0.0 <2.0.0"
environment_contracts: []
policy:
  allow_prerelease: false
```

Save this as `product-definition.yaml`.

## 2. Describe a component release

```json
{
  "schema_version": "1.0.0",
  "component_id": "editor",
  "variant": "default",
  "version": "1.2.0",
  "status": "active",
  "source_units": [
    {
      "source_unit_id": "source",
      "repository_uri": "https://github.com/example/editor",
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

Save the exact bytes as `editor-1.2.0.json`. Publish that file at an immutable
HTTPS URL and calculate its SHA-256 digest.

## 3. Add it to a Catalog

```json
{
  "schema_version": "1.0.0",
  "catalog_id": "example",
  "entries": [
    {
      "component_id": "editor",
      "variant": "default",
      "version": "1.2.0",
      "metadata_uri": "https://catalog.example/releases/editor-1.2.0.json",
      "metadata_sha256": "REPLACE_WITH_THE_REAL_64_CHARACTER_SHA256"
    }
  ]
}
```

Save this as `catalog-index.json`. The digest must be the SHA-256 of the
exact `editor-1.2.0.json` bytes, not a digest of a reformatted copy.

## 4. Validate, resolve, and prepare

```sh
./build/registry validate product-definition product-definition.yaml
./build/registry validate component-release editor-1.2.0.json
./build/registry validate catalog-index catalog-index.json

./build/registry resolve \
  --product product-definition.yaml \
  --catalog catalog-index.json \
  --allow-host catalog.example \
  --allow-host github.com

./build/registry prepare \
  --product product-definition.yaml \
  --catalog catalog-index.json \
  --allow-host catalog.example \
  --allow-host github.com \
  --output ./prepared-editor
```

The Catalog Index is read from a local file. The metadata document and Git
source are fetched from their HTTPS hosts, so those hosts must be listed with
`--allow-host`.

## 5. Verify and replay

```sh
./build/registry manifest verify --input ./prepared-editor/registry-manifest.json

./build/registry manifest replay \
  --input ./prepared-editor/registry-manifest.json \
  --output ./prepared-editor-replay \
  --allow-host github.com
```

Replay should recreate the source recorded by the Manifest, even if a newer
release has since been added to the Catalog.

## 6. CDIN product example

For CDIN, the Product Definition will normally request the Kernel, Runtime,
Backend, and one chosen Frontend variant. The following is only a starting
point for that file:

```yaml
schema_version: "1.0.0"
product:
  id: com.cdin.editor
  name: CDIN
components:
  - component_id: kernel
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: runtime
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: backend
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: frontend
    variant: tui
    version_constraint: ">=1.0.0 <2.0.0"
environment_contracts: []
policy:
  allow_prerelease: false
```

Do not use these ranges as a release policy without checking the actual
published component metadata and the compatibility requirements for CDIN.
