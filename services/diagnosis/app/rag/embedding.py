"""Embedding 客户端：本地 sentence-transformers 与 Fake 实现。"""

from __future__ import annotations

import logging
from typing import Any, Protocol

logger = logging.getLogger(__name__)


class Embedder(Protocol):
    """向量化协议。"""

    async def embed_documents(self, texts: list[str]) -> list[list[float]]: ...
    async def embed_query(self, text: str) -> list[float]: ...


class SentenceTransformerEmbedder:
    """本地 sentence-transformers 实现（BAAI/bge-small-zh-v1.5，512 维）。"""

    def __init__(self, model_name: str, cache_dir: str) -> None:
        self.model_name = model_name
        self.cache_dir = cache_dir
        self._model = None

    def _get_model(self) -> Any:
        if self._model is None:
            from sentence_transformers import SentenceTransformer

            logger.info("加载 embedding 模型 %s（首次下载可能较慢）", self.model_name)
            self._model = SentenceTransformer(self.model_name, cache_folder=self.cache_dir)
        return self._model

    async def embed_documents(self, texts: list[str]) -> list[list[float]]:
        import asyncio

        model = await asyncio.to_thread(self._get_model)
        vectors = await asyncio.to_thread(model.encode, texts, normalize_embeddings=True)
        return [[float(x) for x in v] for v in vectors]

    async def embed_query(self, text: str) -> list[float]:
        import asyncio

        model = await asyncio.to_thread(self._get_model)
        vector = await asyncio.to_thread(model.encode, [text], normalize_embeddings=True)
        return [float(x) for x in vector[0]]


class FakeEmbedder:
    """确定性伪向量（CI/无模型环境）：512 维，基于哈希填充。

    绝不能在生产配置启用（Settings.validate_production 会拒绝 fake）。
    """

    DIM = 512

    async def embed_documents(self, texts: list[str]) -> list[list[float]]:
        return [self._embed(t) for t in texts]

    async def embed_query(self, text: str) -> list[float]:
        return self._embed(text)

    @staticmethod
    def _embed(text: str) -> list[float]:
        import hashlib

        digest = hashlib.sha256(text.encode("utf-8")).digest()
        vec = [0.0] * FakeEmbedder.DIM
        for i in range(FakeEmbedder.DIM):
            vec[i] = (digest[i % 32] / 255.0) - 0.5
        norm = sum(v * v for v in vec) ** 0.5 or 1.0
        return [v / norm for v in vec]
