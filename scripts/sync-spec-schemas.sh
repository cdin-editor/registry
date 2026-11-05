#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="$ROOT/schemas/registry/v1/SOURCE.toml"
DEST="$ROOT/schemas/registry/v1"
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

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

schemas=(
  product-definition.schema.json
  catalog-index.schema.json
  component-release.schema.json
  resolved-manifest.schema.json
  resolution-report.schema.json
)

for name in "${schemas[@]}"; do
  url="https://raw.githubusercontent.com/$repository/$revision/schemas/registry/v1/$name"
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 "$url" -o "$tmp/$name"
  python3 -m json.tool "$tmp/$name" >/dev/null

  if [[ "$MODE" == "--update" ]]; then
    install -m 0644 "$tmp/$name" "$DEST/$name"
  elif ! cmp -s "$tmp/$name" "$DEST/$name"; then
    echo "Schema drift detected: $name" >&2
    echo "Update the Spec first, change SOURCE.toml to the accepted Spec commit, then run --update." >&2
    diff -u "$DEST/$name" "$tmp/$name" || true
    exit 1
  fi
done

echo "Registry v1 schemas match $repository@$revision"
