#!/usr/bin/env python3
"""Cross-platform Registry maintenance and release commands.

These commands deliberately use Python's subprocess API instead of spawning
nested Bash processes. On Windows runners, the name "bash" can resolve to the
WSL launcher (C:\\Windows\\System32\\bash.exe) rather than Git Bash.
"""
from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def run(command: list[str], *, env: dict[str, str] | None = None) -> None:
    print("+", subprocess.list2cmdline(command), flush=True)
    subprocess.run(command, cwd=ROOT, env=env, check=True)


def go_files() -> list[Path]:
    roots = (ROOT / "cmd", ROOT / "internal", ROOT / "tests")
    return sorted(
        path
        for root in roots
        if root.exists()
        for path in root.rglob("*.go")
        if path.is_file()
    )


def check_format() -> None:
    files = go_files()
    if not files:
        raise SystemExit("No Go source files found.")

    unformatted: list[str] = []
    parse_errors: list[str] = []
    for path in files:
        name = str(path.relative_to(ROOT))
        result = subprocess.run(
            ["gofmt", "-l", name],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = (result.stderr or result.stdout).strip()
            parse_errors.append(f"{name}: {detail or 'gofmt failed'}")
        elif result.stdout.strip():
            unformatted.append(name)

    if parse_errors:
        print("gofmt could not parse the following Go files:", file=sys.stderr)
        for detail in parse_errors:
            print(detail, file=sys.stderr)
        raise SystemExit(1)

    if unformatted:
        print("The following Go files need gofmt:", file=sys.stderr)
        for name in unformatted:
            print(name, file=sys.stderr)
            subprocess.run(["gofmt", "-d", name], cwd=ROOT, check=False)
        raise SystemExit(1)
    print("Go formatting: OK")


def run_fuzz_smoke() -> None:
    targets = [
        ("./internal/validation", "FuzzDecodeJSONStrict"),
        ("./internal/validation", "FuzzDecodeYAMLStrict"),
        ("./internal/semver", "FuzzParseVersion"),
        ("./internal/semver", "FuzzParseConstraint"),
        ("./internal/catalog", "FuzzLoadReleaseMetadata"),
        ("./internal/resolver", "FuzzResolveCatalogGraph"),
        ("./internal/collector", "FuzzValidateSourceSubdirectory"),
        ("./internal/collector", "FuzzValidateCredentialEnvironment"),
        ("./internal/collector", "FuzzValidateCredentialEnvironmentDoesNotEchoSecret"),
        ("./internal/catalog", "FuzzMetadataKeysFromManifest"),
        ("./internal/collector", "FuzzGitObjectIDsFromManifest"),
    ]
    env = os.environ.copy()
    env["GOMAXPROCS"] = "2"
    for package, target in targets:
        print(f"==> fuzz smoke: {package}/{target}", flush=True)
        run(
            ["go", "test", package, "-run", "^$", "-fuzz", f"^{target}$",
             "-fuzztime=1s", "-parallel=2"],
            env=env,
        )


def run_benchmark_smoke() -> None:
    targets = [
        ("./internal/catalog", "^(BenchmarkLoadLargeCatalog|BenchmarkMetadataCacheGarbageCollectEmpty)$"),
        ("./internal/resolver", "^BenchmarkResolve"),
        ("./internal/collector", "^(BenchmarkCopyLargeSourceTree|BenchmarkGitObjectCacheGarbageCollectEmpty)$"),
    ]
    env = os.environ.copy()
    env["GOMAXPROCS"] = "2"
    for package, pattern in targets:
        print(f"==> benchmark smoke: {package}/{pattern}", flush=True)
        run(
            ["go", "test", package, "-run", "^$", "-bench", pattern,
             "-benchtime=1x", "-benchmem"],
            env=env,
        )


def check_boundaries() -> None:
    checks = [
        (
            "internal",
            re.compile(r'"github\.com/cdin-editor/registry/internal/cli"'),
            "boundary violation: internal packages must not import the CLI adapter",
        ),
        (
            "cmd internal tests",
            re.compile(r'"[^"]*cdin-editor/(kernel|runtime|backend|frontend)'),
            "boundary violation: Registry must not import product implementation packages",
        ),
        (
            "cmd internal",
            re.compile(r'com\.cdin\.editor|cdin-editor/(cdin|cdin-x)'),
            "boundary violation: product identity or product-specific code leaked into Registry",
        ),
    ]
    found = False
    for roots_text, pattern, message in checks:
        for root_name in roots_text.split():
            root = ROOT / root_name
            if not root.exists():
                continue
            for path in sorted(root.rglob("*.go")):
                try:
                    content = path.read_text(encoding="utf-8")
                except UnicodeDecodeError:
                    continue
                for number, line in enumerate(content.splitlines(), 1):
                    if pattern.search(line):
                        print(f"{path.relative_to(ROOT)}:{number}:{line}")
                        found = True
                        print(message, file=sys.stderr)
    if found:
        raise SystemExit(1)
    print("Package and consumer-independence boundaries: OK")


def fetch_schema(url: str, destination: Path) -> None:
    request = urllib.request.Request(
        url, headers={"User-Agent": "cdin-registry-schema-check"}
    )
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                destination.write_bytes(response.read())
            return
        except (OSError, urllib.error.URLError) as exc:
            last_error = exc
            if attempt < 2:
                time.sleep(attempt + 1)
    assert last_error is not None
    raise last_error


def sync_schemas(mode: str) -> None:
    source = ROOT / "schemas/registry/v1/SOURCE.toml"
    destination = ROOT / "schemas/registry/v1"
    embedded = ROOT / "internal/schemas/registry/v1"
    if not source.is_file():
        raise SystemExit(f"Missing schema source lock: {source}")
    lock = source.read_text(encoding="utf-8")
    repository_match = re.search(r'^repository = "([^"]+)"$', lock, re.M)
    revision_match = re.search(r'^revision = "([^"]+)"$', lock, re.M)
    repository = repository_match.group(1) if repository_match else ""
    revision = revision_match.group(1) if revision_match else ""
    if repository != "cdin-editor/spec" or not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise SystemExit(
            "Invalid schema source lock; expected cdin-editor/spec and a full commit SHA."
        )
    names = (
        "product-definition.schema.json",
        "catalog-index.schema.json",
        "component-release.schema.json",
        "resolved-manifest.schema.json",
        "resolution-report.schema.json",
        "update-discovery-report.schema.json",
    )
    embedded.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="registry-schemas-") as temporary:
        temp = Path(temporary)
        for name in names:
            url = (
                f"https://raw.githubusercontent.com/{repository}/{revision}/"
                f"schemas/registry/v1/{name}"
            )
            downloaded = temp / name
            fetch_schema(url, downloaded)
            subprocess.run(
                [sys.executable, "-m", "json.tool", str(downloaded)],
                cwd=ROOT, check=True, stdout=subprocess.DEVNULL,
            )
            if mode == "--update":
                shutil.copyfile(downloaded, destination / name)
                shutil.copyfile(downloaded, embedded / name)
            else:
                for local_copy in (destination / name, embedded / name):
                    if not local_copy.is_file() or local_copy.read_bytes() != downloaded.read_bytes():
                        print(f"Schema drift detected: {local_copy}", file=sys.stderr)
                        print(
                            "Update the Spec first, advance SOURCE.toml to the accepted Spec commit, "
                            "then run --update.",
                            file=sys.stderr,
                        )
                        if local_copy.is_file():
                            subprocess.run(
                                ["git", "diff", "--no-index", str(local_copy), str(downloaded)],
                                cwd=ROOT,
                            )
                        raise SystemExit(1)
    print(f"Registry v1 schemas and embedded copies match {repository}@{revision}")


def release_build() -> None:
    target_os = os.environ.get("PIPELINE_TARGET_OS") or subprocess.check_output(
        ["go", "env", "GOOS"], cwd=ROOT, text=True
    ).strip()
    target_arch = os.environ.get("PIPELINE_TARGET_ARCH") or subprocess.check_output(
        ["go", "env", "GOARCH"], cwd=ROOT, text=True
    ).strip()
    os_map = {"linux": "linux", "macos": "darwin", "darwin": "darwin", "windows": "windows"}
    arch_map = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64",
                "arm64": "arm64", "x86": "386", "386": "386"}
    if target_os not in os_map:
        raise SystemExit(f"Unsupported release target OS: {target_os}")
    if target_arch not in arch_map:
        raise SystemExit(f"Unsupported release target architecture: {target_arch}")
    goos, goarch = os_map[target_os], arch_map[target_arch]
    version = (os.environ.get("PIPELINE_RELEASE_TAG") or "0.1.0-dev").removeprefix("v")
    commit = os.environ.get("PIPELINE_SHA")
    if not commit:
        commit = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip()
    output_dir = ROOT / "dist"
    output_dir.mkdir(exist_ok=True)
    binary_name = f"registry-{goos}-{goarch}" + (".exe" if goos == "windows" else "")
    binary = output_dir / binary_name
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch})
    print(f"Building Registry {version} for {goos}/{goarch}", flush=True)
    run(
        ["go", "build", "-trimpath", "-buildvcs=false",
         "-ldflags", f"-s -w -X github.com/cdin-editor/registry/internal/buildinfo.Version={version} "
                     f"-X github.com/cdin-editor/registry/internal/buildinfo.Commit={commit}",
         "-o", str(binary), "./cmd/registry"],
        env=env,
    )
    if not binary.is_file() or binary.stat().st_size == 0:
        raise SystemExit(f"Release artifact missing or empty: {binary}")
    print(f"Artifact ready: {binary.relative_to(ROOT)}")


def main() -> None:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("format")
    sub.add_parser("fuzz-smoke")
    sub.add_parser("benchmark-smoke")
    sub.add_parser("boundaries")
    schema = sub.add_parser("schema-sync")
    schema.add_argument("mode", choices=("check", "update"), nargs="?", default="check")
    sub.add_parser("release-build")
    args = parser.parse_args()
    if args.command == "format":
        check_format()
    elif args.command == "fuzz-smoke":
        run_fuzz_smoke()
    elif args.command == "benchmark-smoke":
        run_benchmark_smoke()
    elif args.command == "boundaries":
        check_boundaries()
    elif args.command == "schema-sync":
        sync_schemas("--update" if args.mode == "update" else "--check")
    elif args.command == "release-build":
        release_build()


if __name__ == "__main__":
    main()
