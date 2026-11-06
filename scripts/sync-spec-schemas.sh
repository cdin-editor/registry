#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="$ROOT/schemas/registry/v1/SOURCE.toml"
DEST="$ROOT/schemas/registry/v1"
EMBEDDED_DEST="$ROOT/internal/schemas/registry/v1"
MODE="${1:---check}"

if [[ ! -f "$SOURCE" ]]; then
  echo "Missing schema source lock: $SOURCE" >&2
  exit 2
fi

repository="$(sed -n 's/^repository = "\(.*\)"/\1/p' "$SOURCE")"
revision="$(sed -n 's/^revision = "\(.*\)"/\1/p' "$SOURCE")"
if [[ "$repository" != "cdin-editor/spec" || ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Invalid schema source lock; expected cdin-editor/spec and a full commit SHA." >&2
  exit 2
fi

case "$MODE" in
  --check|--update) ;;
  *) echo "Usage: $0 [--check|--update]" >&2; exit 2 ;;
esac

python_bin="${PYTHON:-python3}"
if ! command -v "$python_bin" >/dev/null 2>&1; then
  python_bin=python
fi
if ! command -v "$python_bin" >/dev/null 2>&1; then
  echo "Python 3 is required to validate the downloaded JSON schemas." >&2
  exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$EMBEDDED_DEST"

schemas=(
  product-definition.schema.json
  catalog-index.schema.json
  component-release.schema.json
  resolved-manifest.schema.json
  resolution-report.schema.json
  update-discovery-report.schema.json
)

for name in "${schemas[@]}"; do
  url="https://raw.githubusercontent.com/$repository/$revision/schemas/registry/v1/$name"
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 "$url" -o "$tmp/$name"
  "$python_bin" -m json.tool "$tmp/$name" >/dev/null

  if [[ "$MODE" == "--update" ]]; then
    install -m 0644 "$tmp/$name" "$DEST/$name"
    install -m 0644 "$tmp/$name" "$EMBEDDED_DEST/$name"
  else
    for local_copy in "$DEST/$name" "$EMBEDDED_DEST/$name"; do
      if [[ ! -f "$local_copy" ]] || ! cmp -s "$tmp/$name" "$local_copy"; then
        echo "Schema drift detected: $local_copy" >&2
        echo "Update the Spec first, advance SOURCE.toml to the accepted Spec commit, then run --update." >&2
        [[ ! -f "$local_copy" ]] || diff -u "$local_copy" "$tmp/$name" || true
        exit 1
      fi
    done
  fi
done

echo "Registry v1 schemas and embedded copies match $repository@$revision"
