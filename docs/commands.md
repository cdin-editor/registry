# Command reference

Run `registry help` to print the command summary. Run
`registry --version` to print the binary version and build information.

In the examples below, `./build/registry` is the executable produced by
`make build`.

## General commands

### Help and version

```sh
./build/registry help
./build/registry --version
```

### List and inspect document schemas

```sh
./build/registry schema list
./build/registry schema show product-definition
```

Available document kinds:

- `product-definition`
- `catalog-index`
- `component-release`
- `resolved-manifest`
- `resolution-report`
- `update-discovery-report`

### Validate a document

```sh
./build/registry validate product-definition ./product-definition.yaml
./build/registry validate catalog-index ./catalog-index.json
./build/registry validate component-release ./release.json
./build/registry validate resolved-manifest ./registry-manifest.json
```

Only Product Definitions accept YAML. Other document types are JSON. Use
`-` as the input path to read JSON from standard input.

## Resolve

```sh
./build/registry resolve \
  --product <file> \
  --catalog <file-or-https-url>... \
  --allow-host <host>...
```

Required options:

- `--product`: Product Definition file.
- `--catalog`: Catalog Index file or HTTPS URL. Repeat this option to use
  multiple Catalogs.
- `--allow-host`: allowed network host. Repeat it for each host, or provide
  comma-separated host names.

- `--cache-dir`: directory for integrity-checked metadata, package and
  Git-object caches.
- `--offline`: refuse network access for Catalog/release metadata and
  digest-pinned package downloads. The Catalog Index itself must be a local
  file; release metadata must already be cached by URI and SHA-256.

Resolve prints a JSON Resolution Report. It does not collect Git source.

## Prepare

```sh
./build/registry prepare \
  --product <file> \
  --catalog <file-or-https-url>... \
  --allow-host <host>... \
  --output <new-directory> \
  [--cache-dir <directory>] \
  [--offline]
```

The options are the same as `resolve`, with two differences:

- `--output` is required and must name a destination that does not already
  exist.
- A successful command publishes a complete Workspace. It may contain
  collected source units, verified package archives, or both.

When `--offline` is used, every selected component must have a compatible,
already-cached immutable artifact. Source fallback is rejected; use a local
Catalog Index and warm the cache during an earlier online prepare.

It prints a JSON receipt with paths and digests for the Workspace, Manifest,
and Report.

## Manifest commands

### Verify a saved Manifest

```sh
./build/registry manifest verify \
  --input ./registry-manifest.json \
  [--sha256 <lowercase-sha256>]
```

Verification is local and read-only. The optional digest is a detached receipt
that must match the exact Manifest bytes. Omitting it does not skip schema,
canonical-byte, or provenance checks.

### Replay a saved Manifest

```sh
./build/registry manifest replay \
  --input ./registry-manifest.json \
  --output ./replayed-workspace \
  --allow-host github.com \
  [--sha256 <lowercase-sha256>] \
  [--verify-remote-metadata] \
  [--cache-dir <directory>] \
  [--offline]
```

Replay uses the exact full Git commit IDs and immutable artifact descriptors
recorded in the Manifest. It does not select current versions or artifacts
again. The output directory must not exist.

By default, replay does not contact Catalog metadata. Add
`--verify-remote-metadata` to re-fetch and verify the exact Catalog and release
metadata bytes recorded in the Manifest. Artifact bytes are always checked
against the saved URI, size and SHA-256 before extraction.

`--cache-dir` enables package/metadata and Git-object caches. `--offline`
requires `--cache-dir` and an artifact-only Manifest whose exact packages are
already cached; source-delivered components require Git retrieval and are
rejected in this mode.

### Discover available updates

```sh
./build/registry manifest discover \
  --input ./registry-manifest.json \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com \
  [--sha256 <lowercase-sha256>] \
  [--cache-dir <directory>] \
  [--report ./update-discovery-report.json]
```

Discovery compares a verified historical Manifest with a new Product
Definition and current Catalog state. It prints a JSON Update Discovery
Report. It does not fetch component source, create a Workspace, or modify the
historical Manifest. If `--report` is used, the file must not already exist.

## Cache commands

### Keep a Manifest's cache entries

```sh
./build/registry cache pin \
  --manifest ./registry-manifest.json \
  --cache-dir ./registry-cache
```

A pin retains the Catalog/release metadata and package assets,
plus Git objects referenced by a verified Manifest.

### Release a pin

```sh
./build/registry cache unpin \
  --manifest-sha256 <manifest-digest> \
  --cache-dir ./registry-cache
```

Use the digest returned when the Manifest was pinned.

### Remove unreferenced cache entries

```sh
./build/registry cache gc --cache-dir ./registry-cache
```

Garbage collection protects entries referenced by retained Manifests and
active operations. It prints a JSON receipt describing removals and remaining
cache sizes. See [Limitations and security](limitations.md) before operating a
shared build cache.
