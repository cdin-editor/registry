# Product Definition

A Product Definition tells Registry what a product requires. It is declarative data: it names logical components, variants, acceptable package versions, and contracts already supplied by the target environment.

It does not identify source repositories. Registry obtains source locations and immutable revisions from the selected Component Release Metadata. A component ID is a stable logical identity and must never be inferred from a repository name.

## Example

The following is an illustrative product request. Its component IDs and version ranges are examples, not requirements imposed by Registry:

```yaml
schema_version: "1.0.0"
product:
  id: org.example.editor
  name: Example Editor
components:
  - component_id: document-engine
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: editor-runtime
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: application-host
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: terminal-frontend
    variant: default
    version_constraint: ">=1.0.0 <2.0.0"
environment_contracts: []
policy:
  allow_prerelease: false
```

The IDs and variants must match the values published in Release Metadata and referenced by a Catalog. Registry does not guess that a logical component corresponds to a similarly named repository.

## Fields

- `schema_version` identifies the Product Definition format. Use `1.0.0` for the current format.
- `product.id`, `product.name`, and `product.version` are optional provenance labels. They do not change how Registry selects releases.
- `components` is required and must contain at least one component.
- `component_id` identifies the requested logical component.
- `variant` selects a variant and defaults to `default`.
- `version_constraint` describes acceptable semantic versions.
- `environment_contracts` lists contracts already supplied by the target environment rather than by a Registry-managed component.
- `policy.allow_prerelease` controls whether prerelease versions may be selected. It defaults to `false`.

## Delivery policy

The optional top-level `resolution` object selects how Registry materializes
each already-resolved component. Omitting it preserves the default
source-collection behavior.

For a native package request, specify the exact target tuple:

```yaml
resolution:
  delivery_mode: artifact
  target:
    os: linux
    architecture: x86_64
    abi: gnu-glibc-2.28
```

Supported delivery modes are:

- `source`: collect exact source commits; this is the default.
- `artifact`: require one compatible prebuilt package for every selected
  component. A missing or ambiguous package fails resolution.
- `artifact-or-source`: prefer a compatible package and fall back to source
  only when no compatible package is published.

The target is not guessed from the machine running Registry. The caller must
provide the target ABI explicitly. Exact platform matches are preferred over a
universal runtime package. The only universal tuple is
`any / any / none`; native development and SDK packages require a concrete
target.

The delivery policy does not change version or dependency selection. It is
applied after the compatible release graph has been solved. See
[Immutable artifact delivery](artifact-delivery.md) for package metadata,
layouts, cache behavior and offline preparation.

Registry supports one exact version or a space-separated set of comparisons:

```yaml
components:
  - component_id: document-engine
    version_constraint: "1.2.1"
  - component_id: terminal-frontend
    variant: default
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: application-host
    version_constraint: ">=1.1.0"
```

Supported comparison operators are `=`, `>`, `>=`, `<`, and `<=`. Comparisons in one string are combined with **AND**. For example, `>=1.0.0 <2.0.0` means at least 1.0.0 and earlier than 2.0.0.

Caret ranges (`^1.2.0`), tilde ranges (`~1.2`), wildcards, partial versions, and OR expressions are not supported by the current contract. Write the comparison explicitly instead.

## Dependencies are added automatically

A Product Definition lists root requirements only. If a selected release declares dependencies, Registry includes them transitively and checks that all version constraints can be satisfied.

If two selected releases require incompatible versions of the same component, resolution fails and the Resolution Report explains the conflict. Registry does not silently select a version that violates a declared requirement.

## Validate your file

```sh
./build/registry validate product-definition ./product-definition.yaml
```

This checks the document against the Product Definition schema. To check whether the requested components and versions are available and compatible, run [resolve](using-registry.md#2-resolve-the-request).
