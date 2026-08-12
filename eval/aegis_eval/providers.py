"""真实评估的 provider 边界。

此模块只从进程环境取 DeepSeek key；不会记录、回显或把 key 传给 artifact。
"""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Protocol

_ROOT = Path(__file__).resolve().parents[2]
_SERVICE_ROOT = _ROOT / "services" / "diagnosis"
if str(_SERVICE_ROOT) not in sys.path:
    sys.path.insert(0, str(_SERVICE_ROOT))

from app.llm.base import LLMClient
from app.llm.deepseek import DeepSeekClient
from app.llm.fake import FakeClient


class ProviderFactory(Protocol):
    def __call__(self, name: str, *, max_output_tokens: int) -> LLMClient: ...


def create_provider(name: str, *, max_output_tokens: int) -> LLMClient:
    """Construct the explicitly requested provider without a fake fallback."""
    if name == "fake":
        return FakeClient()
    if name == "deepseek":
        key = os.environ.get("DEEPSEEK_API_KEY", "").strip()
        if not key:
            raise ValueError("provider=deepseek 需要非空 DEEPSEEK_API_KEY；拒绝回退 fake")
        return DeepSeekClient(api_key=key, max_tokens=max_output_tokens)
    raise ValueError(f"未知 provider: {name}")
