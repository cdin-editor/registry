# Getting started

## What is Registry?

CDIN Registry is a command-line tool used when preparing a CDIN build. It
figures out which published component releases satisfy a product's
requirements, retrieves the exact source revisions for those releases, and
records the result.

Think of it as the step between **choosing component versions** and **building
the product**.

Registry does not replace the component repositories. Kernel, Runtime, Backend,
and Frontend remain responsible for their own source and releases. Registry
uses the release information they publish; CDIN uses the prepared result to
build and test the complete editor.

## When do I need it?

Use Registry when you need to:

- prepare a product from independently released components;
- select compatible component versions instead of manually checking each one;
- collect the exact source commits used by a build;
- keep a record of what was selected;
- verify or recreate a previously prepared set of sources.

You do not need Registry to edit a file in an already built CDIN application.
It is a build-time tool.

## The five things to know

- **Component:** a product part, such as Kernel or Frontend.
- **Release:** one published version of a component, with information about
  where its source can be found.
- **Catalog:** a list of component releases that Registry is allowed to consider.
- **Product Definition:** a file that says which components and versions a
  product needs.
- **Workspace:** the directory Registry prepares after it finds a compatible
  selection.

The [Concepts](concepts.md) page explains these terms in more detail.

## Requirements

To build Registry from source, install:

- Go 1.27.3;
- Python 3.11 or newer;
- GNU Make;
- Git;
- curl.

Install the Pipeline task runner used by this repository:

```sh
python -m pip install "git+https://github.com/m-mdy-m/pipeline.git@1682c520bb5be073eeaa07db03a254fb2e247545"
```

Clone and build:

```sh
git clone https://github.com/cdin-editor/registry.git
cd registry
make build
```

Check that the executable runs:

```sh
./build/registry --version
./build/registry help
./build/registry schema list
```

A release build reports the version encoded by its exact Git tag: for
example, tag `v1.0.0` produces CLI version `1.0.0`. Untagged developer builds
report `development`. The release workflow checks that the tag resolves to
the checked-out commit and that this changelog contains the matching version.

## Your first validation

The repository contains a small example Product Definition:

```sh
./build/registry validate product-definition examples/product-definition.yaml
```

A successful validation prints an `OK` message. This checks the file's format
and required fields. It does not prove that the referenced component releases
exist in a Catalog.

To continue, read [Using Registry](using-registry.md). It shows how to supply a
Catalog, resolve versions, and prepare a Workspace.
