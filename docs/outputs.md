# Registry outputs

A successful `prepare` operation creates a new Workspace and writes two
important JSON documents into it.

## Workspace

The Workspace contains source units, verified artifact package directories, or
both. Source units are placed under
`components/<component>/<variant>/sources/<source-unit-id>/`. An artifact
package is placed under
`components/<component>/<variant>/package/`.

Registry copies declared source files and extracts regular files/directories
from digest-verified package archives. It does not compile components, run
build scripts or install component dependencies.


Use the Workspace as input to the next step of the CDIN build process.

## Resolved Manifest

The file `registry-manifest.json` is the durable record of the preparation.
It records:

- the selected component, variant, and version;
- the Catalog and release metadata locations and SHA-256 digests;
- the exact Git repository and full commit ID for every source unit;
- the Workspace-relative path for every collected source unit;
- selected artifact identity, target, kind, format, layout, URI, size and SHA-256;
- the Workspace-relative path and source provenance for every artifact package;
- the resolved dependency and contract information.


The Manifest lets a later operation verify the recorded selection without
asking the Resolver to choose versions again.

## Resolution Report

The file `resolution-report.json` explains the selection. It contains the
successful resolution result, diagnostics, warnings, and the evidence used
when considering candidates.

When `resolve` is run without `prepare`, the Resolution Report is written
to standard output instead of a Workspace.

## Prepare receipt

After preparation, the CLI also writes a JSON receipt to standard output. It
contains:

- `workspace_path`;
- `manifest_path`;
- `report_path`;
- `manifest_sha256`;
- `report_sha256`.

Save the receipt with the build logs. The digests can be used to identify the
exact output documents.

## Verify the result

```sh
./build/registry manifest verify --input ./build/cdin-workspace/registry-manifest.json
```

This checks the Manifest locally. It does not download source or prove that a
release publisher is authentic.

## Recreate the source later

```sh
./build/registry manifest replay \
  --input ./build/cdin-workspace/registry-manifest.json \
  --output ./build/cdin-workspace-replayed \
  --allow-host github.com
```

Replay collects the exact commits already recorded in the Manifest. It does
not re-resolve versions from the latest Catalog. The replayed Manifest is
checked against the original before the new Workspace is published.
