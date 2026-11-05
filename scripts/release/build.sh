#!/usr/bin/env bash
set -euo pipefail

target_os="${PIPELINE_TARGET_OS:-$(go env GOOS)}"
target_arch="${PIPELINE_TARGET_ARCH:-$(go env GOARCH)}"

case "$target_os" in
  linux) goos=linux ;;
  macos|darwin) goos=darwin ;;
  windows) goos=windows ;;
  *) echo "Unsupported release target OS: $target_os" >&2; exit 2 ;;
esac

case "$target_arch" in
  x86_64|amd64) goarch=amd64 ;;
  aarch64|arm64) goarch=arm64 ;;
  x86|386) goarch=386 ;;
  *) echo "Unsupported release target architecture: $target_arch" >&2; exit 2 ;;
esac

version="${PIPELINE_RELEASE_TAG:-0.1.0-dev}"
version="${version#v}"
commit="${PIPELINE_SHA:-$(git rev-parse HEAD)}"

mkdir -p dist
binary="dist/registry-$goos-$goarch"
if [[ "$goos" == windows ]]; then
  binary+=".exe"
fi

echo "Building Registry $version for $goos/$goarch"
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
  -trimpath \
  -buildvcs=false \
  -ldflags "-s -w -X github.com/cdin-editor/registry/internal/buildinfo.Version=$version -X github.com/cdin-editor/registry/internal/buildinfo.Commit=$commit" \
  -o "$binary" ./cmd/registry

test -s "$binary"
echo "Artifact ready: $binary"
