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

    def test_release_still_prepares_the_adr_0028_catalog_from_the_tagged_source(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("asset-preparation-command:", content)
        self.assertIn("python scripts/prepare-release-catalog.py", content)
        self.assertIn("pipeline-ref:", content)


if __name__ == "__main__":
    unittest.main()
