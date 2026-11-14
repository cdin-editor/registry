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

release_tag="${PIPELINE_RELEASE_TAG:-}"
if [[ ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-((dev|alpha|beta|rc)\.[0-9]+))?$ ]]; then
  echo "PIPELINE_RELEASE_TAG must be a valid versioned Git tag (vMAJOR.MINOR.PATCH[-channel.N])." >&2
  exit 2
fi

head_commit="$(git rev-parse --verify HEAD)"
tag_commit="$(git rev-parse --verify "refs/tags/${release_tag}^{commit}")"
if [[ "$tag_commit" != "$head_commit" ]]; then
  echo "Release tag $release_tag resolves to $tag_commit, not checked-out commit $head_commit." >&2
  exit 1
fi

version="${release_tag#v}"
pipeline_version="${PIPELINE_RELEASE_VERSION:-}"
if [[ -n "$pipeline_version" && "${pipeline_version#v}" != "$version" ]]; then
  echo "Pipeline release version $pipeline_version does not match Git tag $release_tag." >&2
  exit 1
fi

commit="${PIPELINE_SHA:-$head_commit}"
if [[ "$commit" != "$head_commit" ]]; then
  echo "Pipeline source SHA $commit does not match tagged build commit $head_commit." >&2
  exit 1
fi

mkdir -p dist
binary="dist/registry-$goos-$goarch"
if [[ "$goos" == windows ]]; then
  binary+=".exe"
fi

echo "Building Registry $version from Git tag $release_tag for $goos/$goarch"
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
  -trimpath \
  -buildvcs=false \
  -ldflags "-s -w -X github.com/cdin-editor/registry/internal/buildinfo.Version=$version -X github.com/cdin-editor/registry/internal/buildinfo.Commit=$commit" \
  -o "$binary" ./cmd/registry

test -s "$binary"
echo "Artifact ready: $binary"
