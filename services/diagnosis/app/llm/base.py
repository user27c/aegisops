"""LLM 客户端协议与响应模型。"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol


@dataclass
class TokenUsage:
    """Token 用量。"""

    input_tokens: int = 0
    output_tokens: int = 0


@dataclass
class LLMResponse:
    """模型响应。"""

    content: dict[str, Any]
    model: str = ""
    usage: TokenUsage = field(default_factory=TokenUsage)
    finish_reason: str = ""
    # 运行时元数据仅用于审计；不得包含 Authorization 或 API key。
    request_id: str = ""
    attempts: int = 1
    latency_seconds: float = 0.0


class LLMClient(Protocol):
    """LLM 客户端协议。"""

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse: ...
    async def review(self, prompt: dict[str, Any]) -> LLMResponse: ...
