#!/usr/bin/env python3
"""Keep Registry release versions bound to immutable Git tags."""
from __future__ import annotations

from importlib.util import module_from_spec, spec_from_file_location
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
PIPELINE_REVISION = "1682c520bb5be073eeaa07db03a254fb2e247545"

_SPEC = spec_from_file_location("registry_checks", ROOT / "scripts" / "registry_checks.py")
if _SPEC is None or _SPEC.loader is None:
    raise RuntimeError("cannot load scripts/registry_checks.py for release-version tests")
registry_checks = module_from_spec(_SPEC)
_SPEC.loader.exec_module(registry_checks)


class ReleaseWorkflowPinTests(unittest.TestCase):
    def test_reusable_release_workflow_and_helper_input_use_the_same_full_sha(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        workflow = re.search(
            r"^\\s*uses:\\s*m-mdy-m/pipeline/\\.github/workflows/release\\.yml@([0-9a-f]{40})\\s*$",
            content,
            re.MULTILINE,
        )
        helper = re.search(
            r"^\\s*pipeline-ref:\\s*([0-9a-f]{40})\\s*$",
            content,
            re.MULTILINE,
        )
        self.assertIsNotNone(workflow, "release workflow must use a full immutable Pipeline SHA")
        self.assertIsNotNone(helper, "pipeline-ref must use a full immutable Pipeline SHA")
        self.assertEqual(workflow.group(1), helper.group(1))
        self.assertEqual(workflow.group(1), PIPELINE_REVISION)

    def test_release_version_is_derived_from_the_exact_git_tag(self) -> None:
        commit = "a" * 40
        self.assertEqual(
            registry_checks.release_version_from_tag(
                "v1.0.0",
                tag_commit=commit,
                head_commit=commit,
                pipeline_version="1.0.0",
            ),
            "1.0.0",
        )
        self.assertEqual(
            registry_checks.release_version_from_tag(
                "v0.2.0-dev.3",
                tag_commit=commit,
                head_commit=commit,
                pipeline_version="0.2.0-dev.3",
            ),
            "0.2.0-dev.3",
        )
        invalid = (
            ("1.0.0", commit, commit, ""),
            ("v1.0", commit, commit, ""),
            ("v1.0.0", "b" * 40, commit, ""),
            ("v1.0.0", commit, commit, "1.0.1"),
        )
        for tag, tag_commit, head_commit, pipeline_version in invalid:
            with self.subTest(tag=tag, pipeline_version=pipeline_version):
                with self.assertRaises(ValueError):
                    registry_checks.release_version_from_tag(
                        tag,
                        tag_commit=tag_commit,
                        head_commit=head_commit,
                        pipeline_version=pipeline_version,
                    )

    def test_release_workflow_publishes_only_from_a_validated_git_tag(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn('tags: ["v*"]', content)
        self.assertIn("workflow_dispatch:", content)
        self.assertNotIn("workflow_run:", content)
        self.assertNotIn("create-verified-development-tag", content)
        self.assertNotIn("TAG_NAME: v0.2.0-dev.3", content)

        validation = content.split("  validate-release-tag:", 1)[1].split("\\n  release:", 1)[0]
        for required in (
            'GITHUB_REF_TYPE: ${{ github.ref_type }}',
            'if [[ "$GITHUB_REF_TYPE" != "tag" ]]',
            'git rev-parse --verify "refs/tags/$TAG_NAME^{commit}"',
            'if [[ "$TAG_COMMIT" != "$HEAD_COMMIT" ]]',
            "CHANGELOG.md must contain exactly one heading",
            "source_ref=refs/tags/%s",
        ):
            with self.subTest(required=required):
                self.assertIn(required, validation)

        release = content.split("  release:", 1)[1]
        self.assertIn("needs: [validate-release-tag]", release)
        self.assertIn("needs.validate-release-tag.result == 'success'", release)
        self.assertIn("needs.validate-release-tag.outputs.source_ref", release)

    def test_build_scripts_do_not_contain_a_fixed_release_version_fallback(self) -> None:
        buildinfo = (ROOT / "internal" / "buildinfo" / "version.go").read_text(encoding="utf-8")
        checks = (ROOT / "scripts" / "registry_checks.py").read_text(encoding="utf-8")
        shell_build = (ROOT / "scripts" / "release" / "build.sh").read_text(encoding="utf-8")
        self.assertIn('Version   = "development"', buildinfo)
        self.assertIn('PIPELINE_RELEASE_TAG is required', checks)
        self.assertIn("release_version_from_tag(", checks)
        self.assertIn('release_tag="${PIPELINE_RELEASE_TAG:-}"', shell_build)
        self.assertNotIn('or "0.1.0-dev"', checks)
        self.assertNotIn(':-0.1.0-dev', shell_build)

    def test_changelog_has_one_dated_section_for_the_release_tag(self) -> None:
        changelog = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
        matches = re.findall(r"^## \\[1\\.0\\.0\\] - 2026-11-14$", changelog, re.MULTILINE)
        self.assertEqual(len(matches), 1)
        self.assertNotIn("## [1.0.0] - Unreleased", changelog)

    def test_ci_and_docs_use_the_same_immutable_pipeline_revision(self) -> None:
        for relative in (
            ".github/workflows/ci.yml",
            ".github/workflows/docs.yml",
        ):
            content = (ROOT / relative).read_text(encoding="utf-8")
            revisions = re.findall(
                r"^\\s*uses:\\s*m-mdy-m/pipeline/\\.github/workflows/[^\\s]+@([0-9a-f]{40})\\s*$",
                content,
                re.MULTILINE,
            )
            self.assertTrue(revisions, f"{relative} must pin reusable Pipeline workflows")
            self.assertEqual(set(revisions), {PIPELINE_REVISION}, relative)
        for relative in ("README.md", "CONTRIBUTING.md", "docs/getting-started.md", "docs/development.md"):
            content = (ROOT / relative).read_text(encoding="utf-8")
            self.assertIn(f"pipeline.git@{PIPELINE_REVISION}", content, relative)

    def test_release_readiness_checklist_is_linked_from_the_public_docs(self) -> None:
        self.assertTrue((ROOT / "docs" / "release-readiness-1.0.0.md").is_file())
        self.assertIn("docs/release-readiness-1.0.0.md", (ROOT / "README.md").read_text(encoding="utf-8"))
        self.assertIn("release-readiness-1.0.0.md", (ROOT / "docs" / "README.md").read_text(encoding="utf-8"))

    def test_release_still_prepares_the_catalog_from_the_tagged_source(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("asset-preparation-command:", content)
        self.assertIn("python scripts/prepare-release-catalog.py", content)
        self.assertIn("source-ref: ${{ needs.validate-release-tag.outputs.source_ref }}", content)


if __name__ == "__main__":
    unittest.main()
