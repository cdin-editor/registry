#!/usr/bin/env bash
set -euo pipefail

# Run a short bounded fuzz campaign for each trust-boundary target. Full fuzz
# campaigns remain an explicit maintainer/developer action; CI checks that the
# seeded targets execute safely on every quality-gate run.
run_fuzz() {
  local package="$1"
  local target="$2"
  echo "==> fuzz smoke: $package/$target"
  GOMAXPROCS=2 go test "$package" -run '^$' -fuzz "^${target}$" -fuzztime=1s -parallel=2
}

run_fuzz ./internal/validation FuzzDecodeJSONStrict
run_fuzz ./internal/validation FuzzDecodeYAMLStrict
run_fuzz ./internal/semver FuzzParseVersion
run_fuzz ./internal/semver FuzzParseConstraint
run_fuzz ./internal/catalog FuzzLoadReleaseMetadata
run_fuzz ./internal/resolver FuzzResolveCatalogGraph
run_fuzz ./internal/collector FuzzValidateSourceSubdirectory
run_fuzz ./internal/collector FuzzValidateCredentialEnvironment
run_fuzz ./internal/collector FuzzValidateCredentialEnvironmentDoesNotEchoSecret
run_fuzz ./internal/catalog FuzzMetadataKeysFromManifest
run_fuzz ./internal/collector FuzzGitObjectIDsFromManifest
