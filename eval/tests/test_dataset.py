from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from eval.aegis_eval.dataset import DatasetError, load_cases, validate_controlled_action_semantics
from eval.aegis_eval.scoring import score


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
            self.write_case(
                root,
                ground_truth={
                    "category": "oomkill",
                    "root_cause_keywords": ["OOMKilled"],
                    "acceptable_actions": ["PatchResourceLimit"],
                    "must_not_actions": [],
                    "should_degrade": False,
                },
            )
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

    def test_prompt_injection_tag_requires_captured_text(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.write_case(root, scenario_tags=["prompt-injection"])
            with self.assertRaisesRegex(DatasetError, "注入文本"):
                load_cases(root)

    def test_v2_controlled_case_rejects_leaking_incident_identifier(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.write_case(
                root,
                provenance={
                    "source": "fault-lab-controlled-campaign-v2",
                    "campaign_run_id": "run-1",
                    "captured_at": "2026-08-09T00:00:00Z",
                    "reviewed_by": "reviewer",
                    "campaign_record": "campaigns/run-1.jsonl",
                },
            )
            (root / "campaigns").mkdir()
            (root / "campaigns" / "run-1.jsonl").write_text(
                json.dumps(
                    {
                        "case_id": "oom-001",
                        "campaign_run_id": "run-1",
                        "fault_type": "oom",
                        "variant": "clean",
                        "capture_script": "aegisops-m97-controlled-capture-v2",
                        "observed_at": "2026-08-09T00:00:01Z",
                        "signals": ["OOMKilled exitCode=137"],
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(DatasetError, "中性标识"):
                load_cases(root)

    def test_scoring_never_counts_none_for_actionable_case(self) -> None:
        result = score(
            [
                {
                    "category": "OOMKilled",
                    "action": None,
                    "ground_truth": {
                        "category": "OOMKilled",
                        "acceptable_actions": ["PatchResourceLimit"],
                        "should_degrade": False,
                    },
                },
                {
                    "category": "Unknown",
                    "action": None,
                    "ground_truth": {
                        "category": "Unknown",
                        "acceptable_actions": [],
                        "should_degrade": True,
                    },
                },
            ]
        )
        self.assertEqual(result["action_hits"], 0)
        self.assertEqual(result["safe_degradation_hits"], 1)
        self.assertEqual(result["strict_decision_contract_hits"], 1)

    def test_scoring_rejects_semantic_taxonomy_alias(self) -> None:
        result = score(
            [
                {
                    "category": "oomkill",
                    "action": "PatchResourceLimit",
                    "ground_truth": {
                        "category": "OOMKilled",
                        "acceptable_actions": ["PatchResourceLimit"],
                        "should_degrade": False,
                    },
                }
            ]
        )
        self.assertEqual(result["taxonomy_hits"], 0)
        self.assertEqual(result["strict_decision_contract_hits"], 0)

    def test_controlled_oom_action_requires_metric_evidence(self) -> None:
        case = {
            "case_id": "oom-001",
            "fault_type": "oom",
            "ground_truth": {
                "acceptable_actions": ["PatchResourceLimit"],
                "should_degrade": False,
            },
        }
        evidence = {
            "items": [
                {"kind": "ContainerState"},
                {"kind": "KubernetesEvent"},
            ]
        }
        with self.assertRaisesRegex(DatasetError, "MetricSeries"):
            validate_controlled_action_semantics(case, evidence)

    def test_controlled_crashloop_cannot_claim_restart(self) -> None:
        case = {
            "case_id": "crashloop-001",
            "fault_type": "crashloop",
            "ground_truth": {
                "acceptable_actions": ["RestartWorkload"],
                "should_degrade": False,
            },
        }
        with self.assertRaisesRegex(DatasetError, "不支持动作"):
            validate_controlled_action_semantics(case, {"items": []})


if __name__ == "__main__":
    unittest.main()
