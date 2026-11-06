# Product Definition

A Product Definition tells Registry what a product needs. It lists the root
components and the acceptable version range for each one.

For CDIN, this is where the product build declares whether it needs Kernel,
Runtime, Backend, and which Frontend variant it wants. The exact constraints
should be chosen by the CDIN maintainers after compatible component releases
are published.

## A CDIN-shaped example

The following is an illustrative example of the intended product request. The
version ranges are examples only; they do not claim that these exact releases
are currently published or mutually compatible.

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

The IDs and variants in this file must match the IDs used by the component
publishers. Registry does not guess that `kernel` means a repository with a
similar name.

## Fields

- `schema_version` identifies the Product Definition format. Use `1.0.0`
  for the current format.
- `product.id`, `product.name`, and `product.version` are optional
  identifying information. They are recorded as provenance; they do not change
  how Registry selects releases.
- `components` is required and must contain at least one component.
- `component_id` identifies the requested component.
- `variant` selects a particular variant. It defaults to `default`.
- `version_constraint` describes acceptable semantic versions.
- `environment_contracts` lists contracts already supplied by the machine or
  product environment rather than by a Registry-managed component.
- `policy.allow_prerelease` controls whether prerelease versions may be
  selected. It defaults to `false`.

## Version constraints

Registry supports one exact version or a space-separated set of comparisons:

```yaml
components:
  - component_id: kernel
    version_constraint: "1.2.1"
  - component_id: frontend
    variant: tui
    version_constraint: ">=1.0.0 <2.0.0"
  - component_id: backend
    version_constraint: ">=1.1.0"
```

Supported comparison operators are `=`, `>`, `>=`, `<`, and `<=`.
The comparisons in one string are combined with **AND**. For example,
`>=1.0.0 <2.0.0` means at least 1.0.0 and earlier than 2.0.0.

Caret ranges (`^1.2.0`), tilde ranges (`~1.2`), wildcards, partial
versions, and OR expressions are not supported by the current contract.
Write the comparison explicitly instead.

## Dependencies are added automatically

You only need to list the product's root requirements. If a selected Backend
release declares that it depends on Kernel, Registry includes that dependency
and checks that its version constraint can be satisfied.

If two selected releases require incompatible versions of the same component,
resolution fails and the Resolution Report explains the conflict. Registry
does not silently pick a version that violates a declared requirement.

## Validate your file

```sh
./build/registry validate product-definition ./product-definition.yaml
```

This checks the document against the Product Definition schema. To check
whether the requested components and versions actually exist and are
compatible, run [resolve](using-registry.md#2-resolve-the-request).
