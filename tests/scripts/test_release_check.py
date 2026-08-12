#!/usr/bin/env python3
"""Release gate must fail before invoking tools unless its explicit evidence inputs exist."""

from __future__ import annotations

import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "release-check.sh"


class ReleaseCheckTest(unittest.TestCase):
    def test_help_and_bash_syntax_are_offline(self) -> None:
        syntax = subprocess.run(["bash", "-n", str(SCRIPT)], check=False, capture_output=True, text=True)
        self.assertEqual(syntax.returncode, 0, syntax.stderr)
        help_result = subprocess.run(["bash", str(SCRIPT), "--help"], check=False, capture_output=True, text=True)
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertIn("用法", help_result.stdout)

    def test_missing_explicit_e2e_confirmation_fails_before_tools(self) -> None:
        result = subprocess.run(["bash", str(SCRIPT)], check=False, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--with-integration-e2e", result.stderr)


if __name__ == "__main__":
    unittest.main()
