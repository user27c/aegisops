#!/usr/bin/env python3
"""Safety regressions for cloud scripts: help and missing confirmation are offline-only."""

from __future__ import annotations

import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = (
    ROOT / "scripts" / "cloud-deploy.sh",
    ROOT / "scripts" / "cloud-smoke.sh",
    ROOT / "scripts" / "cloud-destroy-checklist.sh",
)


class CloudScriptTest(unittest.TestCase):
    def test_scripts_are_valid_bash_and_help_is_offline(self) -> None:
        for script in SCRIPTS:
            with self.subTest(script=script.name):
                syntax = subprocess.run(["bash", "-n", str(script)], check=False, capture_output=True, text=True)
                self.assertEqual(syntax.returncode, 0, syntax.stderr)
                help_result = subprocess.run(["bash", str(script), "--help"], check=False, capture_output=True, text=True)
                self.assertEqual(help_result.returncode, 0, help_result.stderr)
                self.assertIn("用法", help_result.stdout)

    def test_operational_paths_require_confirmation_before_tools(self) -> None:
        expected = {
            "cloud-deploy.sh": "deploy aliyun-demo",
            "cloud-smoke.sh": "smoke aliyun-demo",
            "cloud-destroy-checklist.sh": "review destroy aliyun-demo",
        }
        for script in SCRIPTS:
            with self.subTest(script=script.name):
                result = subprocess.run(["bash", str(script)], check=False, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(expected[script.name], result.stderr)


if __name__ == "__main__":
    unittest.main()
