#!/usr/bin/env bash
set -euo pipefail

mapfile -d '' go_files < <(find cmd internal tests -type f -name '*.go' -print0)
if ((${#go_files[@]} == 0)); then
  echo "No Go source files found." >&2
  exit 1
fi

unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
  printf 'The following Go files need gofmt:\n%s\n' "$unformatted" >&2
  exit 1
fi

echo "Go formatting: OK"
