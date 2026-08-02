"""Fake LLM：CI 与本地无 API Key 模式。

按 fixture 的 ground_truth 或明确 fault markers 返回固定合法结果；
支持环境变量模拟 timeout/empty/invalid_json/unsafe_action。
绝不能在生产配置启用（Settings.validate_production 拒绝 fake）。
"""

from __future__ import annotations

import re

import json
import os
from typing import Any

from app.llm.base import LLMResponse, TokenUsage

# 模拟故障模式（仅测试）。
MODE_TIMEOUT = "timeout"
MODE_EMPTY = "empty"
MODE_INVALID_JSON = "invalid_json"
MODE_UNSAFE_ACTION = "unsafe_action"


class FakeClient:
    """确定性假模型。"""

    def __init__(self, mode: str = "") -> None:
        self.mode = mode or os.environ.get("FAKE_LLM_MODE", "")

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse:
        return self._respond(prompt, "diagnosis")

    async def review(self, prompt: dict[str, Any]) -> LLMResponse:
        return self._respond(prompt, "review")

    def _respond(self, prompt: dict[str, Any], kind: str) -> LLMResponse:
        if self.mode == MODE_TIMEOUT:
            raise TimeoutError("模拟超时")
        if self.mode == MODE_EMPTY:
            return LLMResponse(content={}, model="fake", finish_reason="stop")
        if self.mode == MODE_INVALID_JSON:
            raise json.JSONDecodeError("模拟非法 JSON", "", 0)

        # 从 evidence markers 推导结果。
        markers = self._collect_markers(prompt)

        if kind == "review":
            return LLMResponse(
                content={"verdict": "pass", "issues": [], "pass": True},
                model="fake",
                usage=TokenUsage(input_tokens=10, output_tokens=5),
                finish_reason="stop",
            )

        action = self._pick_action(markers)
        if self.mode == MODE_UNSAFE_ACTION:
            action = {"action": "DeleteNamespace", "parameters": {"name": "fault-lab"}}

        return LLMResponse(
            content={
                "category": markers.get("category", "Unknown"),
                "root_cause": markers.get("root_cause", "fake 模型: 无法从证据确定根因"),
                "confidence": 0.9 if markers.get("category") else 0.3,
                "evidence_ids": markers.get("evidence_ids", []),
                "runbook_refs": markers.get("runbook_refs", []),
                "reviewer": {"verdict": "ok", "issues": [], "pass": True},
                "proposal": action,
            },
            model="fake",
            usage=TokenUsage(input_tokens=10, output_tokens=5),
            finish_reason="stop",
        )

    @staticmethod
    def _collect_markers(prompt: dict[str, Any]) -> dict[str, Any]:
        """从 prompt 的 evidence/incident 字段提取 fault markers（与 eval fixture 一致）。"""
        markers: dict[str, Any] = {}
        incident = prompt.get("incident", {})
        if incident.get("category_hint"):
            markers["category"] = incident["category_hint"]

        evidence = prompt.get("evidence", {})
        items = evidence.get("items", [])
        for item in items:
            summary = item.get("summary", "")
            kind = item.get("kind", "")
            if "OOMKilled" in summary or "exit code 137" in summary or "OOMKilling" in summary:
                markers.setdefault("category", "OOMKilled")
                markers.setdefault("root_cause", "内存 limit 低于工作集")
                markers.setdefault("evidence_ids", [item.get("id", "")])
                markers.setdefault("runbook_refs", ["runbook://k8s-oomkilled/v1.0.0"])
                m = re.search(r"container=([\w-]+)", summary)
                if m:
                    markers.setdefault("container", m.group(1))
            elif "CrashLoopBackOff" in summary or (
                kind == "ContainerState" and "terminated:" in summary and "exitCode=1" in summary
            ) or (kind == "KubernetesEvent" and "reason=BackOff" in summary):
                markers.setdefault("category", "CrashLoop")
                markers.setdefault("root_cause", "容器启动失败（配置或依赖）")
                markers.setdefault("evidence_ids", [item.get("id", "")])
                markers.setdefault("runbook_refs", ["runbook://k8s-crashloop-config/v1.0.0"])
            elif "ImagePullBackOff" in summary or "FailedToPullImage" in summary:
                markers.setdefault("category", "ImagePullBackOff")
                markers.setdefault("root_cause", "镜像不存在或不可拉取")
                markers.setdefault("evidence_ids", [item.get("id", "")])
                markers.setdefault("runbook_refs", ["runbook://k8s-imagepullbackoff/v1.0.0"])
            elif "checkout request failed" in summary and "connection refused" in summary:
                markers.setdefault("category", "CheckoutFailure")
                markers.setdefault("root_cause", "checkout 接口返回 500（配置/进程状态异常）")
                markers.setdefault("evidence_ids", [item.get("id", "")])
                markers.setdefault("runbook_refs", ["runbook://k8s-probe-failure/v1.0.0"])
        return markers

    @staticmethod
    def _pick_action(markers: dict[str, Any]) -> dict[str, Any]:
        category = markers.get("category", "")
        if category == "OOMKilled":
            container = markers.get("container", "app")
            return {
                "action": "PatchResourceLimit",
                "parameters": {"container": container, "memoryLimit": "384Mi"},
            }
        if category == "CrashLoop":
            return {
                "action": "RestoreConfigMap",
                "parameters": {"targetConfigMap": "checkout-config", "backupConfigMap": "checkout-config-backup"},
            }
        if category == "ImagePullBackOff":
            return {"action": "RollbackDeployment", "parameters": {"targetRevision": 1}}
        if category == "CheckoutFailure":
            return {"action": "RestartWorkload", "parameters": {"reason": "checkout 500，滚动重启恢复"}}
        return {"action": "RestartWorkload", "parameters": {"reason": "fake: 无法确定根因"}}
