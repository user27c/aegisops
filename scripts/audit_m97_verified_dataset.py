#!/usr/bin/env python3
"""Fail-closed reviewer for locally captured, controlled M9.7 cases.

The reviewer intentionally never calls a model, Kubernetes, or a cloud API.
It validates the immutable local export, then (and only with an explicit
user-authorized reviewer marker) signs the cases and regenerates SHA256SUMS.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

from eval.aegis_eval.dataset import CONTROLLED_CAMPAIGN_SOURCE, load_cases
from eval.aegis_eval.experiment import dataset_readiness

REVIEWER = "user-authorized-codex"
CONTROLLED_EVENT_FAULTS = {"config", "oom", "crashloop", "cpu", "dependency"}
FAULT_SIGNATURES = {
    "config": ("checkout returned http 500",),
    "oom": ("oomkilled", "exitcode=137"),
    "crashloop": ("crashloop", "exitcode="),
    "cpu": ("cpu injector active",),
    "dependency": ("dependency timeout",),
    "imagepullbackoff": ("imagepullbackoff",),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Audit and optionally sign a local M9.7 verified dataset")
    parser.add_argument("--dataset", type=Path, default=ROOT / "eval/datasets/v1-verified")
    parser.add_argument("--sign", action="store_true", help="write the user-authorized review marker after a passed audit")
    parser.add_argument(
        "--confirm",
        help=f"required with --sign; exact value: sign m97 dataset as {REVIEWER}",
    )
    return parser.parse_args()


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_checksum_manifest(dataset: Path) -> None:
    manifest = dataset / "SHA256SUMS"
    if not manifest.is_file():
        raise ValueError("SHA256SUMS missing")
    expected: dict[str, str] = {}
    for lineno, line in enumerate(manifest.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            digest, relative = line.split("  ", 1)
        except ValueError as exc:
            raise ValueError(f"SHA256SUMS:{lineno} malformed") from exc
        expected[relative] = digest
    actual_paths = sorted((dataset / "evidence").glob("*.json"))
    actual_paths += sorted((dataset / "campaigns").glob("*.jsonl"))
    actual_paths += [dataset / "incidents.jsonl"]
    actual = {str(path.relative_to(dataset)): sha256(path) for path in actual_paths}
    if expected != actual:
        raise ValueError("SHA256SUMS does not exactly match controlled dataset files")


def _evidence_text(evidence: dict[str, Any]) -> str:
    return json.dumps(evidence, ensure_ascii=False, sort_keys=True).lower()


def audit_cases(
    dataset: Path, *, allow_pending_authorized_review: bool = False
) -> tuple[list[dict[str, Any]], list[dict[str, str]]]:
    verify_checksum_manifest(dataset)
    cases = load_cases(dataset)
    manifest_evidence = {str(case["evidence_path"]) for case in cases}
    actual_evidence = {
        str(path.relative_to(dataset)) for path in (dataset / "evidence").glob("*.json")
    }
    if manifest_evidence != actual_evidence:
        raise ValueError("evidence directory contains missing or unreferenced payloads")
    manifest_campaign_cases = {(str(case["provenance"]["campaign_record"]), str(case["case_id"])) for case in cases}
    actual_campaign_cases: set[tuple[str, str]] = set()
    for path in (dataset / "campaigns").glob("*.jsonl"):
        for line in path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                actual_campaign_cases.add((str(path.relative_to(dataset)), str(json.loads(line)["case_id"])))
    if manifest_campaign_cases != actual_campaign_cases:
        raise ValueError("campaign records contain missing or unreferenced cases")
    readiness = dataset_readiness(cases)
    unexpected = [
        item
        for item in readiness
        if not (allow_pending_authorized_review and "审核未完成" in item)
    ]
    if unexpected:
        raise ValueError("dataset structural gate failed: " + "；".join(unexpected))

    seen_uids: set[str] = set()
    seen_evidence_hashes: set[str] = set()
    seen_campaign_cases: set[tuple[str, str]] = set()
    summaries: list[dict[str, str]] = []
    for case in cases:
        case_id = str(case["case_id"])
        provenance = case["provenance"]
        incident = case["incident"]
        if provenance["source"] != CONTROLLED_CAMPAIGN_SOURCE:
            raise ValueError(f"{case_id}: non-v2 provenance")
        if incident.get("name") != "controlled-evaluation" or incident.get("alert", {}).get("name") != "controlled-evaluation":
            raise ValueError(f"{case_id}: operational taxonomy identifier leaked into incident input")
        uid = str(incident.get("uid", ""))
        if not uid or uid in seen_uids:
            raise ValueError(f"{case_id}: incident UID missing or reused")
        seen_uids.add(uid)

        evidence = json.loads((dataset / case["evidence_path"]).read_text(encoding="utf-8"))
        if str(evidence.get("incidentUID", "")) != uid:
            raise ValueError(f"{case_id}: evidence incidentUID does not match case")
        evidence_hash = str(evidence.get("hash", ""))
        if not evidence_hash or evidence_hash in seen_evidence_hashes:
            raise ValueError(f"{case_id}: evidence hash missing or reused")
        seen_evidence_hashes.add(evidence_hash)

        campaign_key = (str(provenance["campaign_run_id"]), case_id)
        if campaign_key in seen_campaign_cases:
            raise ValueError(f"{case_id}: duplicate campaign record key")
        seen_campaign_cases.add(campaign_key)

        text = _evidence_text(evidence)
        if case["fault_type"] in CONTROLLED_EVENT_FAULTS and "controlledevalevidence" not in text:
            raise ValueError(f"{case_id}: controlled observation event missing from evidence")
        if case["fault_type"] == "imagepullbackoff" and not (
            "imagepullbackoff" in text or "errimagepull" in text
        ):
            raise ValueError(f"{case_id}: ImagePullBackOff signal missing from evidence")
        observed_faults = {
            fault for fault, signature in FAULT_SIGNATURES.items() if all(marker in text for marker in signature)
        }
        allowed_faults = {str(case["fault_type"])}
        if "multi-fault" in case.get("scenario_tags", []):
            allowed_faults.add("cpu")
        if unexpected := sorted(observed_faults - allowed_faults):
            raise ValueError(f"{case_id}: undeclared cross-case fault signal: {', '.join(unexpected)}")
        summaries.append(
            {
                "case_id": case_id,
                "fault_type": str(case["fault_type"]),
                "variant": str(case["variant"]),
                "evidence_hash": evidence_hash,
                "campaign_record": str(provenance["campaign_record"]),
            }
        )
    return cases, summaries


def regenerate_checksums(dataset: Path) -> None:
    entries: list[str] = []
    for path in sorted((dataset / "evidence").glob("*.json")):
        entries.append(f"{sha256(path)}  {path.relative_to(dataset)}")
    for path in sorted((dataset / "campaigns").glob("*.jsonl")):
        entries.append(f"{sha256(path)}  {path.relative_to(dataset)}")
    incidents = dataset / "incidents.jsonl"
    entries.append(f"{sha256(incidents)}  incidents.jsonl")
    temporary = dataset / "SHA256SUMS.tmp"
    temporary.write_text("\n".join(entries) + "\n", encoding="utf-8")
    os.replace(temporary, dataset / "SHA256SUMS")


def sign_cases(dataset: Path, cases: list[dict[str, Any]]) -> None:
    reviewed_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    signed: list[str] = []
    for case in cases:
        case["provenance"]["reviewed_by"] = REVIEWER
        case["provenance"]["reviewed_at"] = reviewed_at
        signed.append(json.dumps(case, ensure_ascii=False, sort_keys=True))
    temporary = dataset / "incidents.jsonl.tmp"
    temporary.write_text("\n".join(signed) + "\n", encoding="utf-8")
    os.replace(temporary, dataset / "incidents.jsonl")
    regenerate_checksums(dataset)


def write_report(dataset: Path, summaries: list[dict[str, str]], cases: list[dict[str, Any]]) -> Path:
    """Write the current dataset state, not merely this invocation's mode."""
    reviewers = {str(case["provenance"].get("reviewed_by", "")) for case in cases}
    authorized_signed = reviewers == {REVIEWER}
    review_times = {
        str(case["provenance"].get("reviewed_at", ""))
        for case in cases
        if str(case["provenance"].get("reviewed_at", ""))
    }
    report = {
        "schema_version": 1,
        "status": "passed",
        "reviewed_by": REVIEWER if authorized_signed else None,
        "reviewer_authority": (
            "explicit user authorization; not represented as a human review"
            if authorized_signed
            else None
        ),
        "reviewed_at": next(iter(review_times)) if authorized_signed and len(review_times) == 1 else None,
        "audited_at": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "case_count": len(summaries),
        "by_fault_type": dict(sorted(Counter(row["fault_type"] for row in summaries).items())),
        "by_variant": dict(sorted(Counter(row["variant"] for row in summaries).items())),
        "checks": [
            "SHA256SUMS exact match",
            "schema, safe-data, campaign and time-window checks",
            "unique incident UID, campaign key and evidence hash",
            "neutral incident/alert input identifiers",
            "controlled fault observations and ImagePull signals",
        ],
        "cases": summaries,
    }
    destination = dataset / "audit-report.json"
    destination.write_text(json.dumps(report, ensure_ascii=False, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    return destination


def main() -> int:
    args = parse_args()
    dataset = args.dataset.resolve()
    if args.sign and args.confirm != f"sign m97 dataset as {REVIEWER}":
        raise SystemExit(f"--sign requires --confirm 'sign m97 dataset as {REVIEWER}'")
    cases, summaries = audit_cases(dataset, allow_pending_authorized_review=args.sign)
    if args.sign:
        sign_cases(dataset, cases)
        # Re-load after the mutation; a signed dataset must have no remaining
        # M9.7 gate failures before it can be used by a real provider.
        cases = load_cases(dataset)
        remaining = dataset_readiness(cases)
        if remaining:
            raise SystemExit("signed dataset is not ready: " + "；".join(remaining))
    report = write_report(dataset, summaries, cases)
    print(f"M9.7 audit passed: cases={len(summaries)} signed={args.sign} report={report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
