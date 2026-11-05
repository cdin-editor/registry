#!/usr/bin/env bash
set -euo pipefail

# Avoid Bash 4-only mapfile so the same task works with macOS's system Bash.
if ! find cmd internal tests -type f -name '*.go' -print -quit | grep -q .; then
  echo "No Go source files found." >&2
  exit 1
fi

unformatted="$(find cmd internal tests -type f -name '*.go' -print0 | xargs -0 gofmt -l)"
if [[ -n "$unformatted" ]]; then
  printf 'The following Go files need gofmt:\n%s\n' "$unformatted" >&2
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    gofmt -d "$file" >&2
  done <<< "$unformatted"
  exit 1
fi

echo "Go formatting: OK"
