from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from eval.aegis_eval.dataset import DatasetError, load_cases


class DatasetTest(unittest.TestCase):
    def write_case(self, root: Path, **overrides: object) -> None:
        (root / "evidence").mkdir()
        (root / "evidence" / "oom-001.json").write_text(
            json.dumps({"items": [{"id": "k8s-1", "summary": "reason=OOMKilled exitCode=137"}]}),
            encoding="utf-8",
        )
        case: dict[str, object] = {
            "case_id": "oom-001",
            "dataset_version": "v1",
            "fault_type": "oom",
            "variant": "clean",
            "incident": {"namespace": "eval", "name": "faultlab"},
            "evidence_path": "evidence/oom-001.json",
            "ground_truth": {
                "category": "OOMKilled",
                "root_cause_keywords": ["OOMKilled", "exit code 137"],
                "acceptable_actions": ["PatchResourceLimit"],
                "must_not_actions": ["RestoreConfigMap"],
                "should_degrade": False,
            },
            "provenance": {
                "source": "fault-lab",
                "campaign_run_id": "run-1",
                "captured_at": "2026-08-09T00:00:00Z",
                "reviewed_by": "automation-pending-human-review",
            },
        }
        case.update(overrides)
        (root / "incidents.jsonl").write_text(json.dumps(case) + "\n", encoding="utf-8")

    def test_loads_strict_safe_case(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.write_case(root)
            self.assertEqual(load_cases(root)[0]["case_id"], "oom-001")

    def test_rejects_alias_category(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.write_case(root, ground_truth={
                "category": "oomkill",
                "root_cause_keywords": ["OOMKilled"],
                "acceptable_actions": ["PatchResourceLimit"],
                "must_not_actions": [],
                "should_degrade": False,
            })
            with self.assertRaisesRegex(DatasetError, "taxonomy"):
                load_cases(root)

    def test_rejects_private_ip_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.write_case(root)
            (root / "evidence" / "oom-001.json").write_text(
                json.dumps({"items": [{"id": "k8s-1", "summary": "peer 10.0.0.4 failed"}]}), encoding="utf-8"
            )
            with self.assertRaisesRegex(DatasetError, "敏感数据"):
                load_cases(root)


if __name__ == "__main__":
    unittest.main()
