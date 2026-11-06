# Using Registry

This guide takes a Product Definition and a Catalog and turns them into a
prepared source Workspace.

Before starting, you need:

- a valid Product Definition;
- at least one Catalog Index;
- release metadata referenced by that index;
- Git repositories and full commit IDs declared by the selected releases;
- the host names used by the Catalog, metadata documents, and Git repositories.

The examples use placeholder URLs. Use the actual immutable Catalog and
repository URLs for your product.

## 1. Validate the Product Definition

```sh
./build/registry validate product-definition ./product-definition.yaml
```

Registry accepts YAML or JSON for a Product Definition. Other document types
are JSON.

## 2. Resolve the request

```sh
./build/registry resolve \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com
```

You can pass more than one Catalog by repeating `--catalog`. You can also
pass a local Catalog Index path:

```sh
./build/registry resolve \
  --product ./product-definition.yaml \
  --catalog ./catalog/index.json \
  --allow-host catalog.example \
  --allow-host github.com
```

The host list must still include every remote host used by release metadata
and source repositories. The Catalog Index itself is read locally in the
second example.

A successful resolve prints a JSON Resolution Report to standard output. It
does not download component source or create a Workspace. If the request
cannot be satisfied, Registry prints the report with the conflict information
and exits unsuccessfully.

Review the report before preparing a build. Check the selected version of each
component, any included dependencies, and any warnings.

## 3. Prepare the Workspace

Once the selected versions are acceptable, run:

```sh
./build/registry prepare \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com \
  --output ./build/cdin-workspace
```

The destination must be a new path. Registry refuses to replace an existing
directory. Preparation validates the inputs, resolves the releases, collects
the declared full Git commits, validates the outputs, and publishes the
complete Workspace only after all steps succeed.

A successful command prints a JSON receipt containing the Workspace path,
Manifest path, Report path, and their SHA-256 digests.

## 4. Reuse a cache

For repeated preparations, give Registry a cache directory:

```sh
./build/registry prepare \
  --product ./product-definition.yaml \
  --catalog https://catalog.example/index.json \
  --allow-host catalog.example \
  --allow-host github.com \
  --output ./build/cdin-workspace \
  --cache-dir ./build/registry-cache
```

The cache stores verified Catalog metadata and Git objects. It does not change
the selected versions or the Manifest contents.

## 5. Keep the result

Keep these together for each prepared build:

- the prepared Workspace;
- `registry-manifest.json`;
- `resolution-report.json`;
- the Product Definition used for the preparation;
- the Catalog Indexes used for the preparation, or their immutable URLs.

The Manifest records the exact releases and source revisions. It is the key to
verifying or recreating the same selection later.

## 6. What happens next in CDIN?

Registry's job ends when the prepared Workspace and its records are ready.
The CDIN build process then consumes that Workspace, configures and builds
the selected components, runs integration and end-to-end tests, and produces
the application packages.

Registry does not run component build scripts or decide whether the complete
editor passes its product tests.

See [Outputs](outputs.md) for the files Registry writes and
[Commands](commands.md) for verification, replay, and update discovery.
