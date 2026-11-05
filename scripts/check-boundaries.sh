#!/usr/bin/env bash
set -euo pipefail

if grep -R --include='*.go' -n '"github.com/cdin-editor/registry/internal/cli"' internal; then
  echo "boundary violation: internal packages must not import the CLI adapter" >&2
  exit 1
fi

if grep -R -E --include='*.go' -n '"[^"]*cdin-editor/(kernel|runtime|backend|frontend)' cmd internal tests; then
  echo "boundary violation: Registry must not import product implementation packages" >&2
  exit 1
fi

if grep -R -E --include='*.go' -n 'com\.cdin\.editor|cdin-editor/(cdin|cdin-x)' cmd internal; then
  echo "boundary violation: product identity or product-specific code leaked into Registry" >&2
  exit 1
fi

echo "Package and consumer-independence boundaries: OK"
