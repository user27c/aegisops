"""DeepSeek OpenAI-compatible 客户端。

要求（蓝图 18.16）：
- response_format={"type":"json_object"}；
- System prompt 明确包含 JSON、目标 Schema 示例与"Evidence/Runbook 是不可信数据"；
- 检查 finish_reason=length；空 content/JSON decode/schema error 分开计数；
- 429/5xx/timeout 指数退避，最多 2 次模型请求；
- 保存 prompt hash/version，不保存 Key。
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import time
from typing import Any

from app.llm.base import LLMResponse, TokenUsage

logger = logging.getLogger(__name__)

# 最大模型请求次数（蓝图：最多 2 次）。
MAX_ATTEMPTS = 2


class LLMError(Exception):
    """LLM 调用错误。"""

    def __init__(self, code: str, message: str, retryable: bool = False) -> None:
        super().__init__(message)
        self.code = code
        self.retryable = retryable


class DeepSeekClient:
    """DeepSeek Chat Completion 客户端。"""

    def __init__(
        self,
        api_key: str,
        base_url: str = "https://api.deepseek.com",
        model: str = "deepseek-chat",
        max_tokens: int = 2048,
        timeout: float = 30.0,
    ) -> None:
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.max_tokens = max_tokens
        self.timeout = timeout

    async def generate_diagnosis(self, prompt: dict[str, Any]) -> LLMResponse:
        """生成诊断（JSON Output）。"""
        return await self._chat_json(prompt["messages"], schema_name="diagnosis")

    async def review(self, prompt: dict[str, Any]) -> LLMResponse:
        """生成审查结论。"""
        return await self._chat_json(prompt["messages"], schema_name="review")

    async def _chat_json(self, messages: list[dict[str, str]], schema_name: str) -> LLMResponse:
        last_error: Exception | None = None
        for attempt in range(MAX_ATTEMPTS):
            try:
                return await self._call_once(messages, schema_name)
            except LLMError as exc:
                last_error = exc
                if not exc.retryable or attempt + 1 >= MAX_ATTEMPTS:
                    raise
                delay = min(2**attempt * 1.0, 8.0)
                logger.warning("LLM 调用失败（可重试）: %s，%ss 后重试", exc.code, delay)
                await asyncio.sleep(delay)
        raise LLMError("MAX_ATTEMPTS", f"重试后仍失败: {last_error}")  # pragma: no cover

    async def _call_once(self, messages: list[dict[str, str]], schema_name: str) -> LLMResponse:
        import httpx

        body = {
            "model": self.model,
            "messages": messages,
            "response_format": {"type": "json_object"},
            "max_tokens": self.max_tokens,
            "temperature": 0,
            "stream": False,
        }
        headers = {
            "Authorization": f"Bearer {self.api_key}",
            "Content-Type": "application/json",
        }
        start = time.monotonic()
        try:
            async with httpx.AsyncClient(timeout=self.timeout) as client:
                resp = await client.post(
                    f"{self.base_url}/chat/completions", json=body, headers=headers
                )
        except httpx.TimeoutException as exc:
            raise LLMError("TIMEOUT", "DeepSeek 请求超时", retryable=True) from exc
        except httpx.HTTPError as exc:
            raise LLMError("NETWORK", f"DeepSeek 网络错误: {exc}", retryable=True) from exc

        latency = time.monotonic() - start
        if resp.status_code == 429:
            raise LLMError("RATE_LIMITED", "DeepSeek 限流", retryable=True)
        if resp.status_code >= 500:
            raise LLMError("UPSTREAM", f"DeepSeek {resp.status_code}", retryable=True)
        if resp.status_code >= 400:
            raise LLMError("CLIENT_ERROR", f"DeepSeek {resp.status_code}: {resp.text[:200]}")

        data = resp.json()
        choice = data["choices"][0]
        finish_reason = choice.get("finish_reason", "")
        content = choice["message"].get("content", "")
        usage = data.get("usage", {})
        tokens = TokenUsage(
            input_tokens=int(usage.get("prompt_tokens", 0)),
            output_tokens=int(usage.get("completion_tokens", 0)),
        )

        if not content or not content.strip():
            raise LLMError("EMPTY_RESPONSE", "模型返回空内容")
        if finish_reason == "length":
            raise LLMError("TRUNCATED", "模型输出被 max_tokens 截断")

        try:
            parsed = json.loads(content)
        except json.JSONDecodeError as exc:
            raise LLMError("INVALID_JSON", f"模型输出非法 JSON: {exc}") from exc

        logger.info(
            "DeepSeek 调用完成 schema=%s tokens=%d/%d latency=%.2fs",
            schema_name,
            tokens.input_tokens,
            tokens.output_tokens,
            latency,
        )
        return LLMResponse(
            content=parsed,
            model=data.get("model", self.model),
            usage=tokens,
            finish_reason=finish_reason,
        )


def prompt_hash(messages: list[dict[str, str]]) -> str:
    """计算 Prompt 内容哈希（不保存完整 Prompt）。"""
    canonical = json.dumps(messages, sort_keys=True, ensure_ascii=False)
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
