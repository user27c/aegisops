#!/usr/bin/env python3
"""Regression: every AegisOps workload must honor global imagePullSecrets."""

from __future__ import annotations

import subprocess
import unittest
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]
CHART = ROOT / "deploy" / "helm" / "aegisops"


class HelmPrivateRegistryTest(unittest.TestCase):
    def test_all_deployments_render_image_pull_secret(self) -> None:
        rendered = subprocess.run(
            [
                "helm",
                "template",
                "aegisops",
                str(CHART),
                "--set",
                "imagePullSecrets[0]=private-registry",
                "--set",
                "observability.otelCollector.enabled=true",
            ],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
        workloads = [
            item
            for item in yaml.safe_load_all(rendered)
            if item and item.get("kind") in {"Deployment", "StatefulSet", "Job"}
        ]
        self.assertEqual(len(workloads), 8)
        for workload in workloads:
            with self.subTest(name=workload["metadata"]["name"]):
                pod_spec = workload["spec"]["template"]["spec"]
                self.assertEqual(
                    pod_spec.get("imagePullSecrets"),
                    [{"name": "private-registry"}],
                )


if __name__ == "__main__":
    unittest.main()
