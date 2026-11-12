#!/usr/bin/env python3
"""Keep Registry's tag-release helper bound to an immutable Pipeline revision."""
from __future__ import annotations

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
PIPELINE_REVISION = "eae4979972be66358646c0a94c99e02127ae0563"


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

    def test_release_still_prepares_the_adr_0028_catalog_from_the_tagged_source(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("asset-preparation-command:", content)
        self.assertIn("python scripts/prepare-release-catalog.py", content)
        self.assertIn("pipeline-ref:", content)


if __name__ == "__main__":
    unittest.main()
