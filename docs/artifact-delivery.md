# Immutable artifact delivery

Registry can prepare a component from either its exact Git source units or a
publisher-produced package archive. Artifact delivery is the install-time
path for components that publish reproducible, versioned packages: the
consumer downloads and verifies the package, but does not clone or build that
component's source.

This capability is additive. Existing Product Definitions keep source
delivery when they omit the `resolution` property.

## Delivery policy

A Product Definition may set:

```yaml
schema_version: "1.1.0"
product:
  id: org.example.cdin
  name: Example CDIN product
components:
  - component_id: backend
    version_constraint: "1.2.0"
  - component_id: frontend
    variant: gui-sdl
    version_constraint: "1.0.2"
resolution:
  delivery_mode: artifact
  target:
    os: linux
    architecture: x86_64
    abi: gnu-glibc-2.28
```

The `resolution` block applies after version and dependency solving. It does
not change which component versions are compatible; it determines how each
selected release is materialized.

| `delivery_mode` | Behavior |
| --- | --- |
| `source` | Collect the exact source units recorded by the release. This is the default and preserves existing behavior. |
| `artifact` | Require one compatible package for every selected component. Missing or ambiguous packages fail the operation. |
| `artifact-or-source` | Prefer a compatible package; use the declared source units only when no package matches. |

A native target is the exact tuple of operating system, architecture and ABI.
For example, `linux / x86_64 / gnu-glibc-2.28` is not interchangeable with
another libc or ABI. Registry prefers an exact target match over a universal
package. A universal package uses only `any / any / none`; the current
contract permits this for runtime-module archives. Native SDK and development
packages must name a concrete target.

A package selector never silently chooses between two artifacts for the same
target. Duplicate matches are an explicit resolution conflict.

## Publishing a package

Artifact descriptors are part of Component Release Metadata. A release that
contains artifacts uses schema version `1.1.0` or newer and retains its
source-unit records for reproducibility and build provenance.

Illustrative release fragment:

```json
{
  "schema_version": "1.1.0",
  "component_id": "runtime",
  "variant": "default",
  "version": "1.0.0",
  "status": "active",
  "source_units": [
    {
      "source_unit_id": "runtime",
      "repository_uri": "https://github.com/example/runtime",
      "revision": "0123456789abcdef0123456789abcdef01234567"
    }
  ],
  "artifacts": [
    {
      "artifact_id": "runtime-lua-modules",
      "kind": "runtime-module-archive",
      "target": { "os": "any", "architecture": "any", "abi": "none" },
      "format": "tar.gz",
      "file_name": "runtime-lua-modules.tar.gz",
      "uri": "https://releases.example/runtime/1.0.0/runtime-lua-modules.tar.gz",
      "sha256": "REPLACE_WITH_64_LOWERCASE_HEX_CHARACTERS",
      "size_bytes": 123456,
      "layout": "lua-module-tree-v1",
      "source_unit_ids": ["runtime"],
      "build": {
        "recipe": {
          "source_unit_id": "runtime",
          "path": "modules"
        },
        "toolchain": [
          { "name": "Lua", "version": "5.4.7" }
        ],
        "runner": "ubuntu-24.04"
      }
    }
  ],
  "dependencies": [],
  "contracts": { "provides": [], "requires": [] }
}
```

The example values are illustrative. Publishers must replace the URI, size,
digest, commit, toolchain and recipe details with values from their actual
release pipeline.

Every artifact descriptor carries:

- a stable artifact ID, package kind, target, archive format and layout ID;
- a basename, immutable HTTPS URI, exact byte size and SHA-256 digest;
- the source-unit IDs used to build the package;
- a recipe source-unit/path and non-empty toolchain identity;
- optional attestation references.

The supported archive formats are `zip` and `tar.gz`. The current layouts
are `cmake-prefix-v1` for native development/SDK packages and
`lua-module-tree-v1` for universal Lua module trees. A layout is a versioned
contract: a consumer must not guess a package's directory structure.

### Build and release pipeline

The component maintainer builds packages in CI from the exact source commits
named in the release metadata, then publishes the package archive and metadata
as immutable release assets. The release process records the actual output
size and SHA-256 of the exact archive bytes. The Catalog Index then pins the
release-metadata bytes by their own SHA-256 digest.

Registry does not run the build recipe. Recipe and toolchain fields are
provenance records, not executable instructions. Do not put scripts or
credentials in an artifact descriptor.

## Prepare and workspace layout

In artifact mode, Registry:

1. resolves versions, dependencies and contracts;
2. selects one compatible artifact for every selected component;
3. fetches the package through the explicit host allow-list;
4. checks the exact byte count and SHA-256 before extraction;
5. extracts only regular files and directories into a private staging tree;
6. writes the artifact descriptor and provenance into the canonical Manifest;
7. publishes the complete Workspace only after all checks succeed.

The package is placed at:

```text
components/<component-id>/<variant>/package/
```

Source-delivered components retain their source layout under
`components/<component-id>/<variant>/sources/<source-unit-id>/`. A Workspace
may contain both forms when the Product Definition uses
`artifact-or-source`.

Registry never executes package files, install hooks, build scripts or tests.
Extraction rejects path traversal, links and special files, unsafe Windows
names, file/directory conflicts, path collisions and divergent archive path
overrides (including TAR PAX/GNU effective paths and ZIP Unicode Path extra
fields). TAR.GZ trailer checksums are verified before acceptance. Configured
limits cap compressed bytes, expanded bytes, entry count and path depth. A failed
materialization removes its private staging directory and does not publish a
partial Workspace.

## Manifest and replay

The Manifest records the selected artifact descriptor, its workspace-relative
package path, source provenance and the selected delivery mode. Verification
checks the canonical bytes, public schema and internal provenance. Replay uses
the already-selected descriptor; it does not rerun version or artifact
selection.

Artifact replay fetches the exact URI and SHA-256 in the saved Manifest. Strict
remote metadata verification is a separate opt-in operation; omitting it does
not permit artifact bytes to be substituted or fetched without digest
verification.

## Cache and offline operation

An online `prepare` with `--cache-dir` warms the integrity-checked cache with
pinned release metadata and selected package bytes:

```sh
registry prepare \
  --product ./product-definition.yaml \
  --catalog ./catalog-index.json \
  --allow-host catalog.example \
  --allow-host releases.example \
  --cache-dir ./registry-cache \
  --output ./workspace
```

Pin the Manifest to protect its referenced cache entries from garbage
collection:

```sh
registry cache pin \
  --manifest ./workspace/registry-manifest.json \
  --cache-dir ./registry-cache
```

Offline prepare uses a local Catalog Index file and requires every selected
component to resolve to a cached artifact. It will not fetch a remote,
unpinned Catalog Index, and it refuses source fallback because Git retrieval
is not part of the offline artifact path:

```sh
registry prepare \
  --product ./product-definition.yaml \
  --catalog ./catalog-index.json \
  --allow-host catalog.example \
  --allow-host releases.example \
  --cache-dir ./registry-cache \
  --offline \
  --output ./workspace-offline
```

Offline artifact replay has the same all-artifact requirement:

```sh
registry manifest replay \
  --input ./workspace/registry-manifest.json \
  --output ./workspace-replayed \
  --allow-host releases.example \
  --cache-dir ./registry-cache \
  --offline
```

Offline mode accepts only digest-pinned cache entries. Missing or corrupt
entries fail closed with an `ARTIFACT_OFFLINE_MISS` or integrity error. A
source-delivered component requires Git retrieval and is therefore rejected
by the offline artifact-only path.

## Security and guarantees

A matching SHA-256 establishes byte identity relative to the trusted digest
in release metadata; it does not authenticate the publisher. Optional
attestation fields are recorded as metadata but are not cryptographically
verified by this Registry implementation. Use trusted publishers, protected
release workflows and an isolated downstream build environment.
