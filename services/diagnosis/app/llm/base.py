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


class LLMClient(Protocol):
    """LLM 客户端协议。"""

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse: ...
    async def review(self, prompt: dict[str, Any]) -> LLMResponse: ...
