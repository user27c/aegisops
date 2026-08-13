#!/usr/bin/env python3
"""Release gate must fail before invoking tools unless its explicit evidence inputs exist."""

from __future__ import annotations

import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "release-check.sh"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
GITLEAKS_WORKFLOW = ROOT / ".github" / "workflows" / "gitleaks.yml"
SECURITY_WORKFLOW = ROOT / ".github" / "workflows" / "security.yml"


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

    def test_release_checksums_use_downloadable_asset_basenames(self) -> None:
        workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("find . -maxdepth 1 -type f -name '*.spdx.json' -printf '%f\\0'", workflow)
        self.assertIn('sha256sum -- "aegisops-$tag.tgz" "${sboms[@]}"', workflow)
        self.assertIn("sha256sum --check checksums.txt", workflow)
        self.assertIn("dist/*.spdx.json", workflow)
        self.assertNotIn("dist/sbom", workflow)
        self.assertNotIn("sbom/*.spdx.json", workflow)

    def test_gitleaks_is_an_unfiltered_full_history_gate(self) -> None:
        workflow = GITLEAKS_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("pull_request:", workflow)
        self.assertIn("branches: [main]", workflow)
        self.assertIn("workflow_call:", workflow)
        self.assertIn("fetch-depth: 0", workflow)
        self.assertIn("gitleaks/gitleaks-action@v3", workflow)
        self.assertIn("GITLEAKS_CONFIG: .gitleaks.toml", workflow)
        self.assertIn("GITLEAKS_VERSION: 8.24.3", workflow)
        self.assertNotIn("paths:", workflow)

        release = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("uses: ./.github/workflows/gitleaks.yml", release)
        self.assertIn("needs: [gitleaks-gate, security-gate]", release)

        security = SECURITY_WORKFLOW.read_text(encoding="utf-8")
        self.assertNotIn("gitleaks/gitleaks-action", security)


if __name__ == "__main__":
    unittest.main()
