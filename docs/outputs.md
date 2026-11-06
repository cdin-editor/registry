# Registry outputs

A successful `prepare` operation creates a new Workspace and writes two
important JSON documents into it.

## Workspace

The Workspace contains the source units for the selected releases. Each source
unit is placed under a stable path derived from its component, variant, and
source-unit IDs.

Registry copies the declared source files. It does not compile them, run their
build scripts, install their dependencies, or launch the product.

Use the Workspace as input to the next step of the CDIN build process.

## Resolved Manifest

The file `registry-manifest.json` is the durable record of the preparation.
It records:

- the selected component, variant, and version;
- the Catalog and release metadata locations and SHA-256 digests;
- the exact Git repository and full commit ID for every source unit;
- the Workspace-relative path for every collected source unit;
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
