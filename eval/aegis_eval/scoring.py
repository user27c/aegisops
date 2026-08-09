"""严格、不可将语义别名或 None 混入成功分子的 M9.7 评分合同。"""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from typing import Any


def score(records: Iterable[Mapping[str, Any]]) -> dict[str, int]:
    """Score completed model records against exported ground truth.

    `category` 比较完全相等；有允许动作的样本只接受其中一个精确动作；
    安全降级只有 ground_truth.should_degrade 为 true 才能加分。
    """

    totals = {
        "total": 0,
        "taxonomy_hits": 0,
        "actionable_total": 0,
        "action_hits": 0,
        "safe_degradation_total": 0,
        "safe_degradation_hits": 0,
        "strict_decision_contract_hits": 0,
    }
    for record in records:
        truth = record.get("ground_truth")
        if not isinstance(truth, Mapping):
            raise ValueError("record 缺少 ground_truth")
        category_hit = record.get("category") == truth.get("category")
        action = record.get("action")
        acceptable = truth.get("acceptable_actions")
        should_degrade = truth.get("should_degrade")
        if not isinstance(acceptable, list) or not isinstance(should_degrade, bool):
            raise ValueError("ground_truth 动作合同非法")

        totals["total"] += 1
        totals["taxonomy_hits"] += int(category_hit)
        if should_degrade:
            totals["safe_degradation_total"] += 1
            safe_hit = action is None
            totals["safe_degradation_hits"] += int(safe_hit)
            decision_hit = category_hit and safe_hit
        else:
            totals["actionable_total"] += 1
            action_hit = action in acceptable
            totals["action_hits"] += int(action_hit)
            decision_hit = category_hit and action_hit
        totals["strict_decision_contract_hits"] += int(decision_hit)
    return totals
