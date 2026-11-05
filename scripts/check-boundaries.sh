#!/usr/bin/env bash
set -euo pipefail

# The CLI is the outer adapter. Internal packages must not import it.
if grep -R --include='*.go' -n '"github.com/cdin-editor/registry/internal/cli"' internal; then
  echo "boundary violation: internal packages must not import internal/cli" >&2
  exit 1
fi

# Registry must not couple itself to product implementation repositories.
if grep -R -E --include='*.go' -n 'cdin-editor/(kernel|runtime|backend|frontend)' cmd internal tests; then
  echo "boundary violation: Registry must not import product components" >&2
  exit 1
fi
