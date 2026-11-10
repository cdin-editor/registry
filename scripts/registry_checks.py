#!/usr/bin/env python3
"""Cross-platform Registry maintenance and release commands.

These commands deliberately use Python's subprocess API instead of spawning
nested Bash processes. On Windows runners, the name "bash" can resolve to the
WSL launcher (C:\\Windows\\System32\\bash.exe) rather than Git Bash.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import tomllib
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
        ("./internal/artifact", "FuzzExtractArchive"),
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


def verify_schema_tree(repository: str, tree_sha: str, pinned: dict[str, str]) -> None:
    if not re.fullmatch(r"[0-9a-f]{40}", tree_sha):
        raise SystemExit(f"Invalid pinned Spec schema tree SHA: {tree_sha!r}")
    url = f"https://api.github.com/repos/{repository}/git/trees/{tree_sha}?recursive=1"
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "cdin-registry-schema-check",
        },
    )
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                tree = json.loads(response.read().decode("utf-8"))
            if tree.get("sha") != tree_sha or tree.get("truncated") is True:
                raise ValueError("GitHub returned a different or truncated Spec schema tree")
            expected = {
                f"schemas/registry/v1/{name}": blob_sha
                for name, blob_sha in pinned.items()
            }
            actual = {
                entry.get("path"): entry
                for entry in tree.get("tree", [])
                if entry.get("path") in expected
            }
            if set(actual) != set(expected):
                raise ValueError("Pinned Spec schema tree is missing one or more schema paths")
            for path, blob_sha in expected.items():
                entry = actual[path]
                if entry.get("type") != "blob" or entry.get("sha") != blob_sha:
                    raise ValueError(f"Spec schema tree identity mismatch for {path}")
            return
        except (OSError, urllib.error.URLError, UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
            last_error = exc
            if attempt < 2:
                time.sleep(attempt + 1)
    assert last_error is not None
    raise last_error


def fetch_schema(repository: str, blob_sha: str, destination: Path) -> None:
    if not re.fullmatch(r"[0-9a-f]{40}", blob_sha):
        raise SystemExit(f"Invalid pinned Spec schema Git blob SHA: {blob_sha!r}")
    url = f"https://api.github.com/repos/{repository}/git/blobs/{blob_sha}"
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "cdin-registry-schema-check",
        },
    )
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                blob = json.loads(response.read().decode("utf-8"))
            if blob.get("sha") != blob_sha or blob.get("encoding") != "base64":
                raise ValueError("GitHub returned a different or unsupported schema blob")
            encoded = "".join(str(blob.get("content", "")).split())
            payload = base64.b64decode(encoded, validate=True)
            git_object = b"blob " + str(len(payload)).encode("ascii") + b"\0" + payload
            actual_sha = hashlib.sha1(git_object).hexdigest()
            if actual_sha != blob_sha:
                raise ValueError(
                    f"Downloaded schema content does not match its pinned Git blob SHA: "
                    f"expected {blob_sha}, got {actual_sha}"
                )
            destination.write_bytes(payload)
            return
        except (OSError, urllib.error.URLError, UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
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
    try:
        lock = tomllib.loads(source.read_text(encoding="utf-8"))
    except tomllib.TOMLDecodeError as exc:
        raise SystemExit(f"Invalid schema source lock: {exc}") from exc

    repository = lock.get("repository", "")
    revision = lock.get("revision", "")
    tree_sha = lock.get("tree_sha", "")
    if repository != "cdin-editor/spec" or not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise SystemExit(
            "Invalid schema source lock; expected cdin-editor/spec and a full commit SHA."
        )
    if not re.fullmatch(r"[0-9a-f]{40}", tree_sha):
        raise SystemExit("Invalid schema source lock; expected a full immutable Git tree SHA.")
    names = (
        "product-definition.schema.json",
        "catalog-index.schema.json",
        "component-release.schema.json",
        "resolved-manifest.schema.json",
        "resolution-report.schema.json",
        "update-discovery-report.schema.json",
    )
    schema_blobs = lock.get("schema_blobs", [])
    if not isinstance(schema_blobs, list):
        raise SystemExit("Invalid schema source lock; schema_blobs must be an array.")
    pinned = {
        item.get("name"): item.get("blob_sha")
        for item in schema_blobs
        if isinstance(item, dict)
    }
    if len(pinned) != len(schema_blobs) or set(pinned) != set(names):
        raise SystemExit("Invalid schema source lock; every Registry schema must have one pinned Git blob SHA.")
    verify_schema_tree(repository, tree_sha, pinned)
    embedded.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="registry-schemas-") as temporary:
        temp = Path(temporary)
        for name in names:
            downloaded = temp / name
            fetch_schema(repository, pinned[name], downloaded)
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
                            "Update the Spec first, advance SOURCE.toml and the blob pins, "
                            "then run --update.",
                            file=sys.stderr,
                        )
                        if local_copy.is_file():
                            subprocess.run(
                                ["git", "diff", "--no-index", str(local_copy), str(downloaded)],
                                cwd=ROOT,
                            )
                        raise SystemExit(1)
    print(f"Registry v1 schemas and embedded copies match {repository}@{revision} (tree and Git blob-pinned)")


_GO_BUILD_SETTING = re.compile(r"^\s*build\s+(GOOS|GOARCH|CGO_ENABLED)=(\S+)\s*$")

def parse_go_build_info(output: str) -> dict[str, str]:
    """Extract target settings from `go version -m` without trusting filenames."""
    settings: dict[str, str] = {}
    for line in output.splitlines():
        match = _GO_BUILD_SETTING.match(line)
        if not match:
            continue
        key, value = match.groups()
        previous = settings.get(key)
        if previous is not None and previous != value:
            raise ValueError(f"conflicting Go build setting {key}")
        settings[key] = value
    return settings


def validate_go_build_info(settings: dict[str, str], goos: str, goarch: str) -> None:
    expected = {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}
    missing = [key for key in expected if key not in settings]
    if missing:
        raise ValueError(f"Go executable build info is missing {', '.join(missing)}")
    mismatched = {
        key: (settings[key], value)
        for key, value in expected.items()
        if settings[key] != value
    }
    if mismatched:
        details = ", ".join(
            f"{key}={actual!r} (expected {wanted!r})"
            for key, (actual, wanted) in sorted(mismatched.items())
        )
        raise ValueError(f"Go executable target mismatch: {details}")


def validate_executable_header(data: bytes, goos: str, goarch: str) -> None:
    """Verify the executable container and machine field from the built bytes."""
    if goos == "linux":
        if len(data) < 20 or data[:4] != b"\x7fELF":
            raise ValueError("Linux Registry artifact is not an ELF executable")
        if data[4] != 2 or data[5] != 1:
            raise ValueError("Linux Registry artifact must be 64-bit little-endian ELF")
        machine = int.from_bytes(data[18:20], "little")
        expected = {"amd64": 62, "arm64": 183}.get(goarch)
        if expected is None or machine != expected:
            raise ValueError(f"ELF machine {machine} does not match {goarch}")
        return

    if goos == "darwin":
        if len(data) < 8 or int.from_bytes(data[:4], "little") != 0xFEEDFACF:
            raise ValueError("macOS Registry artifact is not a 64-bit little-endian Mach-O executable")
        cpu_type = int.from_bytes(data[4:8], "little")
        expected = {"amd64": 0x01000007, "arm64": 0x0100000C}.get(goarch)
        if expected is None or cpu_type != expected:
            raise ValueError(f"Mach-O CPU type {cpu_type:#x} does not match {goarch}")
        return

    if goos == "windows":
        if goarch != "amd64" or len(data) < 0x40 or data[:2] != b"MZ":
            raise ValueError("Windows Registry artifact must be an x86_64 PE executable")
        pe_offset = int.from_bytes(data[0x3C:0x40], "little")
        if pe_offset < 0x40 or pe_offset > len(data) - 6:
            raise ValueError("Windows Registry artifact has an invalid PE header offset")
        if data[pe_offset:pe_offset + 4] != b"PE\x00\x00":
            raise ValueError("Windows Registry artifact has an invalid PE signature")
        machine = int.from_bytes(data[pe_offset + 4:pe_offset + 6], "little")
        if machine != 0x8664:
            raise ValueError(f"PE machine {machine:#x} does not match x86_64")
        return

    raise ValueError(f"unsupported executable target OS: {goos}")


def verify_release_binary_target(binary: Path, goos: str, goarch: str) -> None:
    """Inspect the built executable itself before Pipeline uploads the asset."""
    result = subprocess.run(
        ["go", "version", "-m", str(binary)],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        raise SystemExit("Unable to inspect Registry executable build metadata")
    try:
        settings = parse_go_build_info(result.stdout)
        validate_go_build_info(settings, goos, goarch)
        validate_executable_header(binary.read_bytes(), goos, goarch)
    except (OSError, ValueError) as error:
        raise SystemExit(f"Registry release target verification failed: {error}") from error


def run_release_binary_self_test() -> None:
    """Exercise Go build-info and executable-header checks with synthetic fixtures."""
    sample = (
        "registry: go1.27.3\n"
        " path\tgithub.com/cdin-editor/registry/cmd/registry\n"
        " build\tCGO_ENABLED=0\n"
        " build\tGOARCH=amd64\n"
        " build\tGOOS=linux\n"
    )
    settings = parse_go_build_info(sample)
    validate_go_build_info(settings, "linux", "amd64")
    validate_go_build_info(
        {"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"}, "linux", "arm64"
    )

    checks = [
        ("missing build identity", lambda: validate_go_build_info({"GOOS": "linux"}, "linux", "amd64")),
        ("wrong architecture", lambda: validate_go_build_info(
            {"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"}, "linux", "amd64"
        )),
        ("conflicting metadata", lambda: parse_go_build_info(
            " build\tGOOS=linux\n build\tGOOS=darwin\n"
        )),
    ]
    for label, check in checks:
        try:
            check()
        except ValueError:
            continue
        raise SystemExit(f"Registry target self-test failed: {label} was accepted")

    elf = bytearray(64)
    elf[:4] = b"\x7fELF"
    elf[4:6] = b"\x02\x01"
    elf[18:20] = (62).to_bytes(2, "little")
    validate_executable_header(bytes(elf), "linux", "amd64")
    try:
        validate_executable_header(bytes(elf), "linux", "arm64")
    except ValueError:
        pass
    else:
        raise SystemExit("Registry target self-test failed: wrong ELF machine was accepted")
    elf_arm64 = bytearray(64)
    elf_arm64[:4] = b"\x7fELF"
    elf_arm64[4:6] = b"\x02\x01"
    elf_arm64[18:20] = (183).to_bytes(2, "little")
    validate_executable_header(bytes(elf_arm64), "linux", "arm64")

    mach_o = bytearray(32)
    mach_o[:4] = (0xFEEDFACF).to_bytes(4, "little")
    mach_o[4:8] = (0x0100000C).to_bytes(4, "little")
    validate_executable_header(bytes(mach_o), "darwin", "arm64")
    mach_o_x86 = bytearray(32)
    mach_o_x86[:4] = (0xFEEDFACF).to_bytes(4, "little")
    mach_o_x86[4:8] = (0x01000007).to_bytes(4, "little")
    validate_executable_header(bytes(mach_o_x86), "darwin", "amd64")

    pe = bytearray(0x90)
    pe[:2] = b"MZ"
    pe[0x3C:0x40] = (0x80).to_bytes(4, "little")
    pe[0x80:0x84] = b"PE\x00\x00"
    pe[0x84:0x86] = (0x8664).to_bytes(2, "little")
    validate_executable_header(bytes(pe), "windows", "amd64")
    print("Registry executable target self-tests: OK")


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
    verify_release_binary_target(binary, goos, goarch)
    print(f"Verified executable target: {goos}/{goarch} (CGO_ENABLED=0)")
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
    sub.add_parser("release-binary-self-test")
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
    elif args.command == "release-binary-self-test":
        run_release_binary_self_test()


if __name__ == "__main__":
    main()
