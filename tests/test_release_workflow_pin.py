#!/usr/bin/env python3
"""Keep Registry's tag-release helper bound to an immutable Pipeline revision."""
from __future__ import annotations

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
PIPELINE_REVISION = "1682c520bb5be073eeaa07db03a254fb2e247545"


class ReleaseWorkflowPinTests(unittest.TestCase):
    def test_reusable_release_workflow_and_helper_input_use_the_same_full_sha(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        workflow = re.search(
            r"^\s*uses:\s*m-mdy-m/pipeline/\.github/workflows/release\.yml@([0-9a-f]{40})\s*$",
            content,
            re.MULTILINE,
        )
        helper = re.search(
            r"^\s*pipeline-ref:\s*([0-9a-f]{40})\s*$",
            content,
            re.MULTILINE,
        )
        self.assertIsNotNone(workflow, "release workflow must use a full immutable Pipeline SHA")
        self.assertIsNotNone(helper, "pipeline-ref must use a full immutable Pipeline SHA")
        self.assertEqual(workflow.group(1), helper.group(1))
        self.assertEqual(workflow.group(1), PIPELINE_REVISION)

    def test_existing_catalog_tag_is_reused_without_moving_its_target(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        tag_step = content.split("      - name: Create or verify the immutable Catalog release tag", 1)[1]
        tag_step = tag_step.split("  release:", 1)[0]
        self.assertIn('status="$(curl --silent --show-error --output "$tag_ref_json"', tag_step)
        self.assertIn('200)', tag_step)
        self.assertIn('404)', tag_step)
        self.assertIn('git fetch origin "refs/tags/$TAG_NAME:refs/tags/$TAG_NAME"', tag_step)
        self.assertIn('git merge-base --is-ancestor "$release_sha" "$SOURCE_SHA"', tag_step)
        self.assertIn('if [[ "$tag_commit" != "$release_sha" ]]', tag_step)
        self.assertIn("source_ref=refs/tags/%s", tag_step)
        self.assertIn("Using the existing immutable tag target", tag_step)
        self.assertNotIn('git tag -f', tag_step)

    def test_ci_and_docs_use_the_same_immutable_pipeline_revision(self) -> None:
        for relative in (
            ".github/workflows/ci.yml",
            ".github/workflows/docs.yml",
        ):
            content = (ROOT / relative).read_text(encoding="utf-8")
            revisions = re.findall(
                r"^\s*uses:\s*m-mdy-m/pipeline/\.github/workflows/[^\s]+@([0-9a-f]{40})\s*$",
                content,
                re.MULTILINE,
            )
            self.assertTrue(revisions, f"{relative} must pin reusable Pipeline workflows")
            self.assertEqual(set(revisions), {PIPELINE_REVISION}, relative)
        for relative in ("README.md", "CONTRIBUTING.md", "docs/getting-started.md", "docs/development.md"):
            content = (ROOT / relative).read_text(encoding="utf-8")
            self.assertIn(f"pipeline.git@{PIPELINE_REVISION}", content, relative)

    def test_main_ci_release_job_skips_only_a_complete_existing_release(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        tag_step = content.split("      - name: Create or verify the immutable Catalog release tag", 1)[1]
        tag_step = tag_step.split("  release:", 1)[0]
        for asset in (
            "registry-linux-amd64",
            "registry-linux-arm64",
            "registry-darwin-amd64",
            "registry-darwin-arm64",
            "registry-windows-amd64.exe",
            "catalog-index.json",
            "registry-release-$VERSION.json",
            "SHA256SUMS",
        ):
            with self.subTest(asset=asset):
                self.assertIn(asset, tag_step)
        self.assertIn('echo "release_required=false" >> "$GITHUB_OUTPUT"', tag_step)
        self.assertIn('echo "release_required=true" >> "$GITHUB_OUTPUT"', tag_step)
        self.assertIn("outputs.release_required == 'true'", content)
        self.assertIn("Existing immutable Release", tag_step)
        self.assertIn("repair its asset inventory explicitly", tag_step)

    def test_release_readiness_checklist_is_linked_from_the_public_docs(self) -> None:
        self.assertTrue((ROOT / "docs" / "release-readiness-1.0.0.md").is_file())
        self.assertIn("docs/release-readiness-1.0.0.md", (ROOT / "README.md").read_text(encoding="utf-8"))
        self.assertIn("release-readiness-1.0.0.md", (ROOT / "docs" / "README.md").read_text(encoding="utf-8"))

    def test_release_still_prepares_the_adr_0028_catalog_from_the_tagged_source(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("asset-preparation-command:", content)
        self.assertIn("python scripts/prepare-release-catalog.py", content)
        self.assertIn("pipeline-ref:", content)


if __name__ == "__main__":
    unittest.main()
