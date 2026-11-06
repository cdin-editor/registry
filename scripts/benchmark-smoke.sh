#!/usr/bin/env bash
set -euo pipefail

run_benchmarks() {
  local package="$1"
  local pattern="$2"
  echo "==> benchmark smoke: $package/$pattern"
  GOMAXPROCS=2 go test "$package" -run '^$' -bench "$pattern" -benchtime=1x -benchmem
}

run_benchmarks ./internal/catalog '^(BenchmarkLoadLargeCatalog|BenchmarkMetadataCacheGarbageCollectEmpty)$'
run_benchmarks ./internal/resolver '^BenchmarkResolve'
run_benchmarks ./internal/collector '^(BenchmarkCopyLargeSourceTree|BenchmarkGitObjectCacheGarbageCollectEmpty)$'
