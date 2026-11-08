# Registry documentation

CDIN Registry helps prepare a known set of component releases for a product
build. These pages explain it from the point of view of someone who has never
used Registry before.

## Start here

1. [Getting started](getting-started.md) — what Registry is and how to run it.
2. [Concepts](concepts.md) — the words used by Registry and what each one means.
3. [Using Registry](using-registry.md) — validate, resolve, and prepare a product.
4. [Product Definition](product-definition.md) — describe the components and
   version ranges your product needs.
5. [Catalogs and releases](catalogs.md) — make component releases available to
   Registry.
6. [Outputs](outputs.md) — read the prepared Workspace and its records.
7. [Immutable artifact delivery](artifact-delivery.md) — publish and consume
   target-specific prebuilt packages, including digest-pinned offline replay.
## Look up a task

- [Command reference](commands.md) — every supported CLI command.
- [Examples](examples.md) — example Product Definitions and command sequences.
- [Limitations and security](limitations.md) — retrieval rules, trust boundaries,
  and unsupported behavior.
- [Development](development.md) — build, test, and contribute to Registry.

## What these documents cover

The focus is using Registry to prepare CDIN components. They do not describe
Registry's internal source layout or implementation packages. For the formal
data-format contract, see [CDIN Spec — Registry data contracts](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md).
