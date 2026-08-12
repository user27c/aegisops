from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from eval.aegis_eval.experiment import (
    CONFIGS,
    ExperimentOptions,
    _safe_incident,
    dataset_readiness,
    planned_logical_calls,
    run,
)
from eval.aegis_eval.report import FAKE_WATERMARK, write_report


class ExperimentTest(unittest.TestCase):
    def _dataset(self, root: Path) -> Path:
        data = root / "dataset"
        (data / "evidence").mkdir(parents=True)
        (data / "evidence" / "oom-001.json").write_text(
            json.dumps({"items": [{"id": "event-1", "kind": "KubernetesEvent", "summary": "OOMKilled exit code 137"}]}),
            encoding="utf-8",
        )
        case = {
            "case_id": "oom-001",
            "dataset_version": "v1",
            "fault_type": "oom",
            "variant": "clean",
            "incident": {
                "uid": "test-uid",
                "namespace": "eval",
                "name": "faultlab",
                "severity": "critical",
                "target": {"kind": "Deployment", "namespace": "eval", "name": "faultlab"},
                "alert": {"name": "ContainerOOMKilled"},
            },
            "evidence_path": "evidence/oom-001.json",
            "ground_truth": {
                "category": "OOMKilled",
                "root_cause_keywords": ["OOMKilled"],
                "acceptable_actions": ["PatchResourceLimit"],
                "must_not_actions": ["RestoreConfigMap"],
                "should_degrade": False,
            },
            "provenance": {
                "source": "fault-lab",
                "campaign_run_id": "test",
                "captured_at": "2026-08-09T00:00:00Z",
                "reviewed_by": "automation-pending-human-review",
            },
        }
        (data / "incidents.jsonl").write_text(json.dumps(case) + "\n", encoding="utf-8")
        return data

    def test_fake_run_is_auditable_and_resumable(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            dataset = self._dataset(root)
            options = ExperimentOptions(
                provider="fake",
                dataset_dir=dataset,
                output_root=root / "runs",
                config_names=("a-alert-only", "b-evidence", "d-evidence-rag-review"),
                max_calls=4,
                allow_incomplete_dataset=True,
                confirm_budget=True,
            )
            run_dir = run(options)
            rows = [json.loads(line) for line in (run_dir / "raw.jsonl").read_text(encoding="utf-8").splitlines()]
            self.assertEqual(len(rows), 3)
            self.assertEqual({row["config"] for row in rows}, set(options.config_names))
            self.assertTrue(all("input_summary" in row and "calls" in row for row in rows))
            self.assertTrue(all("fault_type" in row and "citation_summary" in row for row in rows))
            self.assertTrue(all("normalized_input" not in row for row in rows))
            self.assertTrue(all("messages" not in call and "response" not in call for row in rows for call in row["calls"]))
            self.assertTrue((run_dir / "manifest.json").is_file())
            manifest = json.loads((run_dir / "manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest["planned_logical_calls"], 4)
            self.assertEqual(manifest["report_watermark"], FAKE_WATERMARK)
            self.assertEqual(manifest["data_governance"]["status"], "incomplete")
            report_path = write_report(run_dir)
            report = report_path.read_text(encoding="utf-8")
            self.assertIn(FAKE_WATERMARK, report)
            self.assertNotIn("真实评估", report)
            self.assertIn("数据治理：**未完成**", report)
            summary = json.loads((run_dir / "summary.json").read_text(encoding="utf-8"))
            arm = summary["configs"]["d-evidence-rag-review"]
            self.assertEqual(arm["metrics"]["strict_decision_contract"]["denominator"], 1)
            self.assertIn("ci95", arm["metrics"]["strict_decision_contract"])
            self.assertIn("p50", arm["latency_ms"])
            self.assertIn("p95", arm["latency_ms"])
            self.assertIn("dangerous_action", arm["safety"])
            self.assertIn("validated_evidence_citation", arm["reference_metrics"])
            self.assertEqual(arm["reference_metrics"]["runbook_ranking"]["status"], "unavailable")
            self.assertIn("oom", arm["by_fault_type"])
            self.assertEqual(arm["token_totals"]["input"], 20)
            self.assertEqual(arm["token_totals"]["output"], 10)
            self.assertEqual(summary["manifest"]["dataset_manifest_sha256"], manifest["dataset_manifest_sha256"])
            self.assertEqual(summary["provenance"][0]["reviewed_by"], "automation-pending-human-review")
            per_case = (run_dir / "per_case.jsonl").read_text(encoding="utf-8")
            self.assertNotIn("messages", per_case)
            self.assertNotIn("normalized_input", per_case)

            resumed = run(ExperimentOptions(**{**options.__dict__, "resume_dir": run_dir}))
            self.assertEqual(resumed, run_dir)
            self.assertEqual(len((run_dir / "raw.jsonl").read_text(encoding="utf-8").splitlines()), 3)

    def test_alert_only_input_does_not_leak_operational_taxonomy_labels(self) -> None:
        case = {
            "incident": {
                "uid": "uid-1",
                "namespace": "eval",
                "name": "containeroomkilled-12345",
                "severity": "critical",
                "target": {"kind": "Deployment", "namespace": "eval", "name": "faultlab"},
                "alert": {"name": "ContainerOOMKilled"},
            }
        }
        safe = _safe_incident(case)
        self.assertEqual(safe["name"], "controlled-evaluation")
        self.assertEqual(safe["alert"], "controlled-evaluation")
        self.assertNotIn("oom", json.dumps(safe).lower())

    def test_budget_is_explicit_and_reports_full_abcd_plan(self) -> None:
        self.assertEqual(planned_logical_calls(37, tuple(CONFIGS)), 185)
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            dataset = self._dataset(root)
            with self.assertRaisesRegex(ValueError, "confirm-budget"):
                run(
                    ExperimentOptions(
                        provider="fake",
                        dataset_dir=dataset,
                        output_root=root / "runs",
                        config_names=("a-alert-only",),
                        max_calls=1,
                    )
                )
            with self.assertRaisesRegex(ValueError, "计划需要 5 次逻辑调用"):
                run(
                    ExperimentOptions(
                        provider="fake",
                        dataset_dir=dataset,
                        output_root=root / "runs",
                        config_names=tuple(CONFIGS),
                        max_calls=4,
                        confirm_budget=True,
                    )
                )

    def test_real_provider_refuses_incomplete_dataset_before_calling_api(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            options = ExperimentOptions(
                provider="deepseek",
                dataset_dir=self._dataset(root),
                output_root=root / "runs",
            )
            with self.assertRaisesRegex(ValueError, "审核未完成"):
                run(options)

    def test_readiness_names_every_missing_gate(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            case = json.loads((self._dataset(root) / "incidents.jsonl").read_text())
            failures = dataset_readiness([case])
            self.assertTrue(any("样本数" in item for item in failures))
            self.assertTrue(any("三种证据变体" in item for item in failures))
            self.assertTrue(any("注入样本" in item for item in failures))


if __name__ == "__main__":
    unittest.main()
