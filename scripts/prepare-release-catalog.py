#!/usr/bin/env python3
"""Prepare the exact Registry Catalog snapshot for an immutable GitHub Release."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
CATALOG_PATH = ROOT / "catalogs" / "cdin-components" / "catalog-index.json"
REPOSITORY_URI = "https://github.com/cdin-editor/registry"
VERSION_PATTERN = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-(?:dev(?:\.[0-9]+)?|(?:alpha|beta|rc)\.[0-9]+))?$"
)
GIT_SHA_PATTERN = re.compile(r"^[0-9a-f]{40}$")
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
COMPONENT_PATTERN = re.compile(r"^[a-z0-9]+(?:[.-][a-z0-9]+)*$")
REPOSITORY_PATTERN = re.compile(
    r"^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/?$"
)


class CatalogReleaseError(ValueError):
    """Raised when the pinned Catalog or its release identity is invalid."""


def _unique_object_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise CatalogReleaseError(f"duplicate JSON object key: {key!r}")
        result[key] = value
    return result


def decode_catalog(data: bytes) -> dict[str, Any]:
    try:
        document = json.loads(
            data.decode("utf-8"),
            object_pairs_hook=_unique_object_pairs,
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise CatalogReleaseError(f"Catalog is not valid UTF-8 JSON: {exc}") from exc
    if not isinstance(document, dict):
        raise CatalogReleaseError("Catalog root must be a JSON object")
    validate_catalog(document)
    return document


def validate_catalog(document: dict[str, Any]) -> None:
    schema_version = document.get("schema_version")
    catalog_id = document.get("catalog_id")
    if not isinstance(catalog_id, str) or not COMPONENT_PATTERN.fullmatch(catalog_id):
        raise CatalogReleaseError("Catalog catalog_id is missing or invalid")
    if not isinstance(schema_version, str):
        raise CatalogReleaseError("Catalog schema_version is missing or invalid")
    match = re.fullmatch(r"1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", schema_version)
    if match is None:
        raise CatalogReleaseError(f"unsupported Catalog schema version: {schema_version!r}")

    minor = int(match.group(1))
    has_entries = "entries" in document
    has_sources = "release_sources" in document
    if has_entries == has_sources:
        raise CatalogReleaseError(
            "Catalog must use exactly one model: entries or release_sources"
        )

    if minor == 0:
        entries = document.get("entries")
        if not isinstance(entries, list) or not entries:
            raise CatalogReleaseError("static Catalog 1.0.x requires a non-empty entries array")
        seen: set[tuple[str, str, str]] = set()
        for index, entry in enumerate(entries):
            if not isinstance(entry, dict):
                raise CatalogReleaseError(f"Catalog entry {index} must be an object")
            component = entry.get("component_id")
            variant = entry.get("variant", "default")
            version = entry.get("version")
            uri = entry.get("metadata_uri")
            digest = entry.get("metadata_sha256")
            if not isinstance(component, str) or not COMPONENT_PATTERN.fullmatch(component):
                raise CatalogReleaseError(f"Catalog entry {index} has an invalid component_id")
            if not isinstance(variant, str) or not COMPONENT_PATTERN.fullmatch(variant):
                raise CatalogReleaseError(f"Catalog entry {index} has an invalid variant")
            if not isinstance(version, str) or VERSION_PATTERN.fullmatch(version) is None:
                raise CatalogReleaseError(f"Catalog entry {index} has an invalid SemVer version")
            if not isinstance(uri, str) or not uri.startswith("https://"):
                raise CatalogReleaseError(f"Catalog entry {index} has an invalid metadata_uri")
            if not isinstance(digest, str) or not SHA256_PATTERN.fullmatch(digest):
                raise CatalogReleaseError(f"Catalog entry {index} has an invalid metadata_sha256")
            identity = (component, variant, version)
            if identity in seen:
                raise CatalogReleaseError(f"duplicate static Catalog entry identity: {identity}")
            seen.add(identity)
        return

    sources = document.get("release_sources")
    if not isinstance(sources, list) or not sources:
        raise CatalogReleaseError("dynamic Catalog 1.1.x+ requires a non-empty release_sources array")
    source_ids: set[str] = set()
    source_identities: set[tuple[str, str, str]] = set()
    for index, source in enumerate(sources):
        if not isinstance(source, dict):
            raise CatalogReleaseError(f"Catalog release_source {index} must be an object")
        source_id = source.get("source_id")
        provider = source.get("provider")
        repository_uri = source.get("repository_uri")
        component = source.get("component_id")
        variant = source.get("variant", "default")
        tag_prefix = source.get("tag_prefix", "v")
        if not isinstance(source_id, str) or not COMPONENT_PATTERN.fullmatch(source_id):
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid source_id")
        if source_id in source_ids:
            raise CatalogReleaseError(f"duplicate Catalog release source_id: {source_id}")
        if provider != "github-releases-v1":
            raise CatalogReleaseError(f"Catalog release_source {index} has an unsupported provider")
        if not isinstance(repository_uri, str) or not REPOSITORY_PATTERN.fullmatch(repository_uri):
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid repository_uri")
        if not isinstance(component, str) or not COMPONENT_PATTERN.fullmatch(component):
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid component_id")
        if not isinstance(variant, str) or not COMPONENT_PATTERN.fullmatch(variant):
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid variant")
        if not isinstance(tag_prefix, str) or len(tag_prefix) > 32:
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid tag_prefix")
        if not re.fullmatch(r"[A-Za-z0-9._+-]*", tag_prefix):
            raise CatalogReleaseError(f"Catalog release_source {index} has an invalid tag_prefix")
        identity = (component, variant, repository_uri.rstrip("/"))
        if identity in source_identities:
            raise CatalogReleaseError(
                f"duplicate Catalog release source for component/variant/repository: {identity}"
            )
        source_ids.add(source_id)
        source_identities.add(identity)


EXPECTED_EXECUTABLES = {
    "linux-amd64": ("registry-linux-amd64", "linux/amd64"),
    "linux-arm64": ("registry-linux-arm64", "linux/arm64"),
    "darwin-amd64": ("registry-darwin-amd64", "darwin/amd64"),
    "darwin-arm64": ("registry-darwin-arm64", "darwin/arm64"),
    "windows-amd64": ("registry-windows-amd64.exe", "windows/amd64"),
}


def collect_executables(root: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    seen: set[str] = set()
    for target_id, (expected_name, target) in EXPECTED_EXECUTABLES.items():
        target_directory = root / f"release-build-{target_id}"
        if target_directory.is_symlink() or not target_directory.is_dir():
            raise CatalogReleaseError(f"missing or unsafe Registry target artifact: {target_id}")
        files = sorted(target_directory.iterdir(), key=lambda item: item.name)
        if len(files) != 1 or files[0].is_symlink() or not files[0].is_file():
            raise CatalogReleaseError(
                f"Registry target {target_id} must contain exactly one regular executable"
            )
        binary = files[0]
        if binary.name != expected_name:
            raise CatalogReleaseError(
                f"Registry target {target_id} produced {binary.name!r}, expected {expected_name!r}"
            )
        if binary.name in seen:
            raise CatalogReleaseError(f"duplicate Registry executable asset name: {binary.name}")
        seen.add(binary.name)
        records.append({
            "asset_name": binary.name,
            "target_id": target_id,
            "target": target,
            "size": binary.stat().st_size,
            "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        })
    unexpected = sorted(
        item.name for item in root.iterdir()
        if item.name.startswith("release-build-")
        and item.name.removeprefix("release-build-") not in EXPECTED_EXECUTABLES
    )
    if unexpected:
        raise CatalogReleaseError(
            "unexpected Registry release target artifact directories: " + ", ".join(unexpected)
        )
    return records


def release_descriptor(
    *,
    version: str,
    tag: str,
    source_sha: str,
    catalog_bytes: bytes,
    catalog: dict[str, Any],
    executables: list[dict[str, Any]],
) -> dict[str, Any]:
    if VERSION_PATTERN.fullmatch(version) is None:
        raise CatalogReleaseError(f"invalid Pipeline release version: {version!r}")
    if tag != f"v{version}":
        raise CatalogReleaseError("release tag must equal v plus Pipeline release version")
    if not GIT_SHA_PATTERN.fullmatch(source_sha):
        raise CatalogReleaseError("Pipeline source SHA must be a full lowercase Git commit SHA")
    expected_targets = set(EXPECTED_EXECUTABLES)
    actual_targets = {item.get("target_id") for item in executables if isinstance(item, dict)}
    if len(executables) != len(expected_targets) or actual_targets != expected_targets:
        raise CatalogReleaseError(
            f"Registry executable target inventory mismatch: expected {sorted(expected_targets)}, "
            f"got {sorted(str(item) for item in actual_targets)}"
        )
    asset_names: set[str] = set()
    for item in executables:
        name = item.get("asset_name")
        expected_name, expected_target = EXPECTED_EXECUTABLES[item["target_id"]]
        if name != expected_name or item.get("target") != expected_target:
            raise CatalogReleaseError(f"Registry executable identity mismatch: {item!r}")
        if name in asset_names:
            raise CatalogReleaseError(f"duplicate Registry executable asset: {name}")
        if not isinstance(item.get("size"), int) or item["size"] <= 0:
            raise CatalogReleaseError(f"invalid Registry executable size for {name}")
        if not isinstance(item.get("sha256"), str) or not SHA256_PATTERN.fullmatch(item["sha256"]):
            raise CatalogReleaseError(f"invalid Registry executable SHA-256 for {name}")
        asset_names.add(name)
    return {
        "schema_version": "1.0.0",
        "registry": {
            "repository_uri": REPOSITORY_URI,
            "version": version,
            "tag": tag,
            "source_commit": source_sha,
        },
        "catalog": {
            "asset_name": "catalog-index.json",
            "catalog_id": catalog["catalog_id"],
            "schema_version": catalog["schema_version"],
            "sha256": hashlib.sha256(catalog_bytes).hexdigest(),
            "size": len(catalog_bytes),
        },
        "executables": sorted(executables, key=lambda item: item["target_id"]),
    }


def _git_output(*arguments: str) -> str:
    result = subprocess.run(
        ["git", *arguments],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise CatalogReleaseError(f"git {' '.join(arguments)} failed: {detail}")
    return result.stdout.strip()


def prepare_release_catalog() -> tuple[Path, Path]:
    tag = os.environ.get("PIPELINE_RELEASE_TAG", "")
    version = os.environ.get("PIPELINE_RELEASE_VERSION", "")
    source_sha = os.environ.get("PIPELINE_SOURCE_SHA", "")
    output_directory = os.environ.get("RELEASE_ASSETS_DIRECTORY", "")
    if not tag or not version or not source_sha or not output_directory:
        raise CatalogReleaseError(
            "PIPELINE_RELEASE_TAG, PIPELINE_RELEASE_VERSION, PIPELINE_SOURCE_SHA and "
            "RELEASE_ASSETS_DIRECTORY are required"
        )
    if tag != f"v{version}":
        raise CatalogReleaseError("Pipeline tag and version do not identify the same release")

    head_sha = _git_output("rev-parse", "HEAD")
    tag_sha = _git_output("rev-parse", f"{tag}^{{commit}}")
    if not GIT_SHA_PATTERN.fullmatch(source_sha):
        raise CatalogReleaseError("Pipeline source SHA must be a full lowercase Git commit SHA")
    if head_sha != source_sha or tag_sha != source_sha:
        raise CatalogReleaseError(
            f"release source identity mismatch: Pipeline={source_sha}, HEAD={head_sha}, "
            f"tag={tag_sha}"
        )

    try:
        catalog_bytes = CATALOG_PATH.read_bytes()
    except OSError as exc:
        raise CatalogReleaseError(f"cannot read Catalog snapshot {CATALOG_PATH}: {exc}") from exc
    catalog = decode_catalog(catalog_bytes)
    descriptor = release_descriptor(
        version=version,
        tag=tag,
        source_sha=source_sha,
        catalog_bytes=catalog_bytes,
        catalog=catalog,
        executables=collect_executables(Path(output_directory).expanduser()),
    )

    root = Path(output_directory).expanduser()
    if root.is_symlink():
        raise CatalogReleaseError("release assets directory must not be a symlink")
    root.mkdir(parents=True, exist_ok=True)
    target_directory = root / "release-build-catalog"
    if target_directory.exists() or target_directory.is_symlink():
        raise CatalogReleaseError("release-build-catalog already exists in the asset directory")
    target_directory.mkdir()
    catalog_asset = target_directory / "catalog-index.json"
    descriptor_asset = target_directory / f"registry-release-{version}.json"
    try:
        catalog_asset.write_bytes(catalog_bytes)
        descriptor_asset.write_text(
            json.dumps(descriptor, indent=2, sort_keys=True) + "\n",
            encoding="utf-8",
            newline="\n",
        )
    except OSError as exc:
        raise CatalogReleaseError(f"cannot prepare Registry Catalog release assets: {exc}") from exc

    print(f"Catalog release asset: {catalog_asset.name} ({len(catalog_bytes)} bytes)")
    print(f"Catalog SHA-256: {descriptor['catalog']['sha256']}")
    print(f"Registry source: {tag} @ {source_sha}")
    print(f"Catalog descriptor: {descriptor_asset.name}")
    return catalog_asset, descriptor_asset


def _expect_failure(label: str, callback: Any) -> None:
    try:
        callback()
    except CatalogReleaseError:
        return
    raise AssertionError(f"self-test accepted invalid input: {label}")


def run_self_test() -> None:
    static = {
        "schema_version": "1.0.0",
        "catalog_id": "sample",
        "entries": [{
            "component_id": "runtime",
            "variant": "default",
            "version": "1.0.0-rc.2",
            "metadata_uri": "https://example.test/runtime.json",
            "metadata_sha256": "a" * 64,
        }],
    }
    dynamic = {
        "schema_version": "1.1.0",
        "catalog_id": "sample",
        "release_sources": [{
            "source_id": "runtime-default",
            "provider": "github-releases-v1",
            "repository_uri": "https://github.com/example/runtime",
            "component_id": "runtime",
            "variant": "default",
            "tag_prefix": "v",
        }],
    }
    validate_catalog(static)
    validate_catalog(dynamic)
    if CATALOG_PATH.is_file():
        decode_catalog(CATALOG_PATH.read_bytes())
    _expect_failure(
        "mixed static and dynamic models",
        lambda: validate_catalog({**static, "release_sources": dynamic["release_sources"]}),
    )
    _expect_failure(
        "duplicate dynamic component source",
        lambda: validate_catalog({
            **dynamic,
            "release_sources": [dynamic["release_sources"][0], {
                **dynamic["release_sources"][0],
                "source_id": "runtime-default-duplicate",
            }],
        }),
    )
    _expect_failure(
        "invalid static metadata digest",
        lambda: validate_catalog({
            **static,
            "entries": [{**static["entries"][0], "metadata_sha256": "bad"}],
        }),
    )
    with tempfile.TemporaryDirectory(prefix="registry-release-fixtures-") as temporary:
        artifact_root = Path(temporary)
        for target_id, (asset_name, _target) in EXPECTED_EXECUTABLES.items():
            target_directory = artifact_root / f"release-build-{target_id}"
            target_directory.mkdir()
            (target_directory / asset_name).write_bytes(f"fixture:{target_id}".encode("ascii"))
        executable_fixture = collect_executables(artifact_root)
        if len(executable_fixture) != len(EXPECTED_EXECUTABLES):
            raise AssertionError("executable collection self-test did not find five targets")
        wrong_target = artifact_root / "release-build-linux-arm64" / "registry-linux-amd64"
        (artifact_root / "release-build-linux-arm64" / "registry-linux-arm64").unlink()
        wrong_target.write_bytes(b"wrong target")
        _expect_failure(
            "wrong executable target filename",
            lambda: collect_executables(artifact_root),
        )
    raw = json.dumps(static, sort_keys=True).encode("utf-8")
    descriptor = release_descriptor(
        version="0.2.0-dev.1",
        tag="v0.2.0-dev.1",
        source_sha="a" * 40,
        catalog_bytes=raw,
        catalog=static,
        executables=executable_fixture,
    )
    if descriptor["catalog"]["sha256"] != hashlib.sha256(raw).hexdigest():
        raise AssertionError("descriptor SHA-256 self-test failed")
    if descriptor["catalog"]["size"] != len(raw):
        raise AssertionError("descriptor size self-test failed")
    _expect_failure(
        "tag/version mismatch",
        lambda: release_descriptor(
            version="0.2.0-dev.1",
            tag="v0.2.0-dev.2",
            source_sha="a" * 40,
            catalog_bytes=raw,
            catalog=static,
            executables=executable_fixture,
        ),
    )
    _expect_failure(
        "incomplete executable target inventory",
        lambda: release_descriptor(
            version="0.2.0-dev.1",
            tag="v0.2.0-dev.1",
            source_sha="a" * 40,
            catalog_bytes=raw,
            catalog=static,
            executables=executable_fixture[:-1],
        ),
    )
    duplicate_key = b'{"schema_version":"1.0.0","schema_version":"1.0.0"}'
    _expect_failure("duplicate JSON keys", lambda: decode_catalog(duplicate_key))
    print("Registry Catalog release preparation self-tests: OK")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    arguments = parser.parse_args()
    try:
        if arguments.self_test:
            run_self_test()
        else:
            prepare_release_catalog()
    except (CatalogReleaseError, OSError) as exc:
        print(f"Registry Catalog release preparation failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
