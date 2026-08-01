"""混合检索：向量 Top-K + 全文 Top-K，RRF 合并（蓝图 18.19 retriever）。"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from sqlalchemy import func, select, text
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import Runbook, RunbookChunk
from app.rag.embedding import Embedder
from app.rag.rrf import reciprocal_rank_fusion


@dataclass
class RetrievalQuery:
    """检索请求。query 只由告警名/分类/事件 reason/退出码构造，不把整段日志当 query。"""

    text: str
    category: str | None = None
    workload_kind: str | None = None
    top_k: int = 5


@dataclass
class RetrievedChunk:
    """检索结果。"""

    chunk_id: str
    document_id: str
    runbook_version: str
    category: str
    section: str
    content: str
    score: float
    source: str = field(default="hybrid")


class HybridRetriever:
    """pgvector + tsvector 混合检索。"""

    def __init__(self, session: AsyncSession, embedder: Embedder) -> None:
        self.session = session
        self.embedder = embedder

    async def search(self, query: RetrievalQuery, top_k: int = 5) -> list[RetrievedChunk]:
        """分别取 vector top 20、full-text top 20，RRF 合并。"""
        vector_ranked = await self._vector_search(query, limit=20)
        text_ranked = await self._fulltext_search(query, limit=20)

        merged = reciprocal_rank_fusion(
            [[c["id"] for c in vector_ranked], [c["id"] for c in text_ranked]],
            k=60,
            limit=top_k,
        )

        # 按融合结果取元数据。
        by_id = {c["id"]: c for c in vector_ranked + text_ranked}
        return [
            RetrievedChunk(
                chunk_id=m.id,
                document_id=by_id[m.id]["document_id"],
                runbook_version=by_id[m.id]["version"],
                category=by_id[m.id]["category"],
                section=by_id[m.id]["section"],
                content=by_id[m.id]["content"],
                score=m.score,
            )
            for m in merged
            if m.id in by_id
        ]

    async def _vector_search(self, query: RetrievalQuery, limit: int) -> list[dict[str, Any]]:
        query_vec = await self.embedder.embed_query(query.text)
        # 距离 → 相似度排序；1 - cosine_distance。
        stmt = (
            select(
                RunbookChunk.id.label("id"),
                Runbook.document_id,
                Runbook.version,
                Runbook.category,
                RunbookChunk.metadata_json["section"].label("section"),
                RunbookChunk.content,
                (1 - RunbookChunk.embedding.cosine_distance(query_vec)).label("score"),
            )
            .join(Runbook, Runbook.id == RunbookChunk.runbook_id)
            .where(Runbook.active)
            .order_by(text("score DESC"))
            .limit(limit)
        )
        if query.category:
            stmt = stmt.where(Runbook.category == query.category)
        result = await self.session.execute(stmt)
        rows = result.all()
        return [
            {
                "id": str(r.id),
                "document_id": r.document_id,
                "version": r.version,
                "category": r.category,
                "section": r.section or "",
                "content": r.content,
                "score": float(r.score),
            }
            for r in rows
        ]

    async def _fulltext_search(self, query: RetrievalQuery, limit: int) -> list[dict[str, Any]]:
        """tsvector 全文检索：按词匹配并按 ts_rank 排序（中文场景配合向量兜底）。"""
        keywords = [w for w in query.text.split() if w]
        if not keywords:
            return []
        pattern = " & ".join(keywords)
        tsvec = func.to_tsvector("simple", RunbookChunk.textsearch)
        rank = func.ts_rank(tsvec, func.to_tsquery("simple", pattern)).label("rank")
        stmt = (
            select(
                RunbookChunk.id.label("id"),
                Runbook.document_id,
                Runbook.version,
                Runbook.category,
                RunbookChunk.metadata_json["section"].label("section"),
                RunbookChunk.content,
                rank,
            )
            .join(Runbook, Runbook.id == RunbookChunk.runbook_id)
            .where(
                Runbook.active,
                RunbookChunk.textsearch.isnot(None),
                func.to_tsvector("simple", RunbookChunk.textsearch).op("@@")(func.to_tsquery("simple", pattern)),
            )
            .order_by(text("rank DESC"))
            .limit(limit)
        )
        result = await self.session.execute(stmt)
        rows = result.all()
        return [
            {
                "id": str(r.id),
                "document_id": r.document_id,
                "version": r.version,
                "category": r.category,
                "section": r.section or "",
                "content": r.content,
                "score": float(r.rank or 0.0),
            }
            for r in rows
        ]
