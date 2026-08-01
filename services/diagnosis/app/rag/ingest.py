"""Runbook 索引：读取 → 校验 → 分块 → 向量化 → upsert。"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from app.db.repositories import PostgresRunbookRepository, RunbookRepository
from app.rag.chunker import RunbookDocument, chunk_markdown, parse_frontmatter
from app.rag.embedding import Embedder

logger = logging.getLogger(__name__)

# Runbook 正文固定章节（蓝图 23.6）。
REQUIRED_SECTIONS = [
    "Symptoms",
    "Required Evidence",
    "Decision Tree",
    "Allowed Remediation",
    "Forbidden Conditions",
    "Verification",
    "Rollback",
    "Escalation",
    "References",
]


@dataclass
class IndexReport:
    """索引报告。"""

    indexed: int = 0
    updated: int = 0
    unchanged: int = 0
    skipped: int = 0
    errors: list[str] = field(default_factory=list)


def validate_document(doc: RunbookDocument, schema: dict[str, Any]) -> list[str]:
    """校验 frontmatter 与正文结构（CI dry-run 使用）。"""
    errors: list[str] = []
    # 用 JSON Schema 校验元数据。
    try:
        import jsonschema  # type: ignore[import-untyped]

        jsonschema.validate(doc.metadata, schema)
    except Exception as exc:  # noqa: BLE001
        errors.append(f"{doc.path}: schema 校验失败: {exc}")

    # 正文固定章节。
    for section in REQUIRED_SECTIONS:
        if f"## {section}" not in doc.content:
            errors.append(f"{doc.path}: 缺少章节 {section}")
    return errors


async def index_runbooks(
    root: Path,
    repo: RunbookRepository,
    embedder: Embedder,
    schema: dict[str, Any] | None = None,
    prune: bool = False,
) -> IndexReport:
    """索引目录下全部 Runbook。内容哈希未变则跳过。"""
    report = IndexReport()
    active_ids: set[str] = set()
    files = sorted(await asyncio.to_thread(lambda: list(root.glob("*.md"))))

    for path in files:
        try:
            doc = await asyncio.to_thread(parse_frontmatter, path)
        except ValueError as exc:
            report.errors.append(str(exc))
            report.skipped += 1
            continue

        if schema:
            errors = validate_document(doc, schema)
            if errors:
                report.errors.extend(errors)
                report.skipped += 1
                continue

        chunks = chunk_markdown(doc)
        texts = [c.content for c in chunks]
        vectors = await embedder.embed_documents(texts)
        for chunk, vec in zip(chunks, vectors, strict=True):
            chunk.metadata["embedding"] = vec

        result = await repo.upsert_document(
            doc={
                "document_id": doc.document_id,
                "version": doc.version,
                "path": doc.path,
                "title": doc.title,
                "category": doc.category,
                "metadata": doc.metadata,
                "content_hash": doc.content_hash,
            },
            chunks=[
                {
                    "content": c.content,
                    "metadata": {k: v for k, v in c.metadata.items() if k != "embedding"},
                    "textsearch": c.content,  # tsvector 由 DB 触发器/迁移生成时使用原文
                    "embedding": c.metadata["embedding"],
                    "content_hash": c.content_hash,
                }
                for c in chunks
            ],
        )
        active_ids.add(doc.document_id)
        if result == "unchanged":
            report.unchanged += 1
        else:
            report.updated += 1
        report.indexed += 1

    if prune:
        # 只把缺失文档标 inactive，不硬删除历史引用。
        existing = await repo.list_active()
        for rb in existing:
            if rb.document_id not in active_ids:
                await repo.deactivate(rb.document_id)
                logger.info("标记 inactive: %s", rb.document_id)
    return report


def cli() -> None:
    """命令行入口：aegis-runbooks index/validate。"""
    parser = argparse.ArgumentParser(prog="aegis-runbooks", description="Runbook 索引管理")
    parser.add_argument("command", choices=["index", "validate"], help="子命令")
    parser.add_argument("--root", default="runbooks", help="Runbook 目录")
    parser.add_argument("--dry-run", action="store_true", help="只校验不写入")
    parser.add_argument("--prune", action="store_true", help="标记缺失文档为 inactive")
    parser.add_argument("--schema", default="runbooks/schema.json", help="frontmatter JSON Schema")
    args = parser.parse_args()

    root = Path(args.root)
    schema: dict[str, Any] | None = None
    if Path(args.schema).exists():
        schema = json.loads(Path(args.schema).read_text(encoding="utf-8"))

    if args.dry_run:
        # 纯校验模式：不需要数据库连接。
        report = IndexReport()
        for path in sorted(root.glob("*.md")):
            try:
                doc = parse_frontmatter(path)
                errors = validate_document(doc, schema or {})
                if errors:
                    report.errors.extend(errors)
                else:
                    report.indexed += 1
            except ValueError as exc:
                report.errors.append(str(exc))
        print(json.dumps(report.__dict__, ensure_ascii=False, indent=2))
        return

    async def run() -> None:
        from app.config import get_settings
        from app.db.engine import create_engine, create_session_factory
        from app.rag.embedding import FakeEmbedder

        settings = get_settings()
        engine = create_engine(settings)
        factory = create_session_factory(engine)

        async with factory() as session:
            repo: RunbookRepository = PostgresRunbookRepository(session)
            embedder: Embedder = FakeEmbedder()  # CLI 默认 fake；生产用真实模型由服务配置
            report = await index_runbooks(root, repo, embedder, schema, prune=args.prune)
            await session.commit()
            print(json.dumps(report.__dict__, ensure_ascii=False, indent=2))
        await engine.dispose()

    asyncio.run(run())


if __name__ == "__main__":
    cli()
