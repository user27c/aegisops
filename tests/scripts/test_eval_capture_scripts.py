"""Cheap regressions for M9.7 capture scripts before they touch a cluster."""

from __future__ import annotations

import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = (
    ROOT / "scripts" / "export-eval-case.sh",
    ROOT / "scripts" / "capture-controlled-eval-case.sh",
    ROOT / "scripts" / "capture-imagepull-eval-case.sh",
    ROOT / "scripts" / "capture-m97-verified-dataset.sh",
    ROOT / "scripts" / "rotate-e2e-viewer-token.sh",
)


class EvalCaptureScriptTest(unittest.TestCase):
    def test_scripts_are_valid_bash(self) -> None:
        for script in SCRIPTS:
            with self.subTest(script=script.name):
                result = subprocess.run(["bash", "-n", str(script)], check=False, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_help_requires_no_cluster_or_credentials(self) -> None:
        for script in SCRIPTS:
            with self.subTest(script=script.name):
                result = subprocess.run(["bash", str(script), "--help"], check=False, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("用法", result.stdout)
                self.assertNotIn("webhook-token-123", result.stdout)
                self.assertNotIn("console-token-xyz", result.stdout)


if __name__ == "__main__":
    unittest.main()
