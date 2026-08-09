#!/usr/bin/env python3
"""Regression tests for the E2E artifact upload boundary."""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SANITIZER = ROOT / "scripts" / "sanitize-e2e-artifacts.py"


class E2EArtifactSanitizerTest(unittest.TestCase):
    def run_tool(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SANITIZER), *args],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_plain_secret_words_and_references_are_uploadable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "raw"
            upload = Path(temporary) / "upload"
            source.mkdir()
            content = (
                "message: secret references are expected Kubernetes metadata\n"
                "secretKeyRef:\n  name: aegisops-gateway-token\n  key: token\n"
                "envName: DEEPSEEK_API_KEY\n"
            )
            (source / "k8s-all.yaml").write_text(content, encoding="utf-8")

            scan = self.run_tool("--source", str(source), "--scan-only", "--report", str(source / "SCAN-HITS.txt"))
            self.assertEqual(scan.returncode, 0, scan.stderr)
            prepared = self.run_tool("--source", str(source), "--destination", str(upload))
            self.assertEqual(prepared.returncode, 0, prepared.stderr)
            self.assertEqual((upload / "k8s-all.yaml").read_text(encoding="utf-8"), content)

    def test_canary_and_secret_values_are_blocked_from_raw_and_redacted_for_upload(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "raw"
            upload = Path(temporary) / "upload"
            source.mkdir()
            canary = "aegisops-e2e-canary-secret-6d2c8829"
            (source / "sensitive.yaml").write_text(
                "apiVersion: v1\nkind: Secret\nstringData:\n"
                f"  password: {canary}\n"
                "---\nAuthorization: Bearer abcdefghijklmnop\n"
                "api_key: abcdefghijklmnop\n"
                "AEGISOPS_E2E_CANARY_TOKEN=canary-value-6d2c8829\n"
                "owner: test@example.com\n",
                encoding="utf-8",
            )

            report = source / "SCAN-HITS.txt"
            scan = self.run_tool("--source", str(source), "--scan-only", "--report", str(report))
            self.assertEqual(scan.returncode, 1)
            report_text = report.read_text(encoding="utf-8")
            self.assertIn("kubernetes-secret-data", report_text)
            self.assertIn("test-canary", report_text)

            prepared = self.run_tool("--source", str(source), "--destination", str(upload))
            self.assertEqual(prepared.returncode, 0, prepared.stderr)
            uploaded = (upload / "sensitive.yaml").read_text(encoding="utf-8")
            self.assertNotIn(canary, uploaded)
            self.assertNotIn("canary-value-6d2c8829", uploaded)
            self.assertNotIn("abcdefghijklmnop", uploaded)
            self.assertNotIn("test@example.com", uploaded)
            self.assertIn("[REDACTED]", uploaded)
            verified = self.run_tool("--source", str(upload), "--scan-only")
            self.assertEqual(verified.returncode, 0, verified.stderr)

    def test_inline_and_block_secret_data_are_redacted(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "raw"
            upload = Path(temporary) / "upload"
            source.mkdir()
            inline_secret = "plaintext-secret-6d2c8829"
            block_secret = "multiline-secret-6d2c8829"
            token_secret = "token-secret-6d2c8829"
            (source / "secret.yaml").write_text(
                "apiVersion: v1\nkind: Secret\n"
                f"stringData: {{opaque: {inline_secret}}}\n"
                "data:\n  private.pem: |\n"
                f"    {block_secret}\n"
                f"token: {token_secret}\n",
                encoding="utf-8",
            )

            scan = self.run_tool("--source", str(source), "--scan-only")
            self.assertEqual(scan.returncode, 1)
            prepared = self.run_tool("--source", str(source), "--destination", str(upload))
            self.assertEqual(prepared.returncode, 0, prepared.stderr)
            uploaded = (upload / "secret.yaml").read_text(encoding="utf-8")
            self.assertNotIn(inline_secret, uploaded)
            self.assertNotIn(block_secret, uploaded)
            self.assertNotIn(token_secret, uploaded)
            self.assertIn("[REDACTED]", uploaded)


if __name__ == "__main__":
    unittest.main()
