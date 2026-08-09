"""公开评估数据集的敏感数据拒绝规则。"""

from __future__ import annotations

import ipaddress
import re
from collections.abc import Iterator
from typing import Any

_EMAIL = re.compile(r"(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b")
_AUTHORIZATION = re.compile(r"(?i)\bauthorization\s*[:=]")
_BEARER = re.compile(r"(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}")
_PRIVATE_KEY = re.compile(r"-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----")
_TOKEN_FIELD = re.compile(r"(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password)\s*[:=]")


def sensitive_findings(value: Any) -> list[str]:
    """Return paths that contain data unsafe for a committed dataset.

    The production collector already redacts data.  The exporter intentionally
    fails closed when that boundary regresses instead of silently publishing a
    partial or incorrectly transformed evidence object.
    """

    return [path for path, text in _strings(value) if _unsafe(text)]


def assert_safe(value: Any) -> None:
    findings = sensitive_findings(value)
    if findings:
        raise ValueError("评估导出包含未脱敏敏感数据: " + ", ".join(findings[:8]))


def _strings(value: Any, path: str = "$") -> Iterator[tuple[str, str]]:
    if isinstance(value, str):
        yield path, value
    elif isinstance(value, dict):
        for key, child in value.items():
            yield from _strings(child, f"{path}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from _strings(child, f"{path}[{index}]")


def _unsafe(text: str) -> bool:
    if any(pattern.search(text) for pattern in (_EMAIL, _AUTHORIZATION, _BEARER, _PRIVATE_KEY, _TOKEN_FIELD)):
        return True
    for candidate in re.findall(r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])", text):
        try:
            if ipaddress.ip_address(candidate).is_private:
                return True
        except ValueError:
            continue
    return False
