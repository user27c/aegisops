"""Runbook 解析与分块。

按标题和步骤边界切分，不把"禁止条件"与对应动作分开。
"""

from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

# frontmatter 分隔符。
_FRONTMATTER_RE = re.compile(r"^---\s*\n(.*?)\n---\s*\n", re.DOTALL)

# 章节标题（固定正文结构）。
_SECTION_RE = re.compile(r"^##\s+(.+)$", re.MULTILINE)

# 禁止条件章节：必须与动作保持同块。
_FORBIDDEN_SECTION = "Forbidden Conditions"


@dataclass
class RunbookDocument:
    """解析后的 Runbook 文档。"""

    document_id: str
    version: str
    title: str
    category: str
    path: str
    metadata: dict[str, Any]
    content: str
    content_hash: str = ""

    def __post_init__(self) -> None:
        self.content_hash = hashlib.sha256(self.content.encode("utf-8")).hexdigest()


@dataclass
class RunbookChunk:
    """Runbook 分块。"""

    content: str
    metadata: dict[str, Any] = field(default_factory=dict)
    content_hash: str = ""


def parse_frontmatter(path: Path) -> RunbookDocument:
    """解析 Markdown frontmatter 与正文。"""
    raw = path.read_text(encoding="utf-8")
    m = _FRONTMATTER_RE.match(raw)
    if not m:
        raise ValueError(f"{path}: 缺少 frontmatter")
    meta = yaml.safe_load(m.group(1))
    if not isinstance(meta, dict):
        raise ValueError(f"{path}: frontmatter 必须是 YAML map")
    body = raw[m.end() :]

    required = ("id", "version", "title", "category")
    missing = [k for k in required if k not in meta]
    if missing:
        raise ValueError(f"{path}: frontmatter 缺少字段 {missing}")

    return RunbookDocument(
        document_id=str(meta["id"]),
        version=str(meta["version"]),
        title=str(meta["title"]),
        category=str(meta["category"]),
        path=str(path),
        metadata=meta,
        content=body,
    )


def chunk_markdown(doc: RunbookDocument, target_chars: int = 700, overlap: int = 100) -> list[RunbookChunk]:
    """按章节切分；禁止条件章节与相邻动作合并。

    target_chars 是目标块大小，overlap 是相邻块重叠字符数。
    """
    if target_chars <= 0 or overlap < 0 or overlap >= target_chars:
        raise ValueError("非法分块参数")

    sections = _split_sections(doc.content)
    chunks: list[RunbookChunk] = []
    buffer = ""
    buffer_section = ""

    def flush() -> None:
        nonlocal buffer, buffer_section
        if buffer.strip():
            chunks.append(_make_chunk(doc, buffer_section, buffer))
        buffer = ""

    for title, body in sections:
        # 禁止条件章节合并到上一个块（不单独切分）。
        if _FORBIDDEN_SECTION in title and chunks:
            last = chunks.pop()
            merged = last.content + "\n\n## " + title + "\n" + body
            chunks.append(_make_chunk(doc, last.metadata.get("section", ""), merged))
            continue
        if len(buffer) + len(body) > target_chars and buffer:
            flush()
        buffer += f"## {title}\n{body}\n\n"
        buffer_section = title

        # 长章节内部按段落二次切分。
        while len(buffer) > target_chars * 2:
            split_at = _find_split(buffer, target_chars)
            chunks.append(_make_chunk(doc, buffer_section, buffer[:split_at]))
            buffer = buffer[split_at - overlap :]
    flush()
    return chunks


def _split_sections(content: str) -> list[tuple[str, str]]:
    """把正文拆成 (章节标题, 内容) 列表。"""
    matches = list(_SECTION_RE.finditer(content))
    if not matches:
        return [("Overview", content)]
    sections: list[tuple[str, str]] = []
    for idx, m in enumerate(matches):
        title = m.group(1).strip()
        start = m.end()
        end = matches[idx + 1].start() if idx + 1 < len(matches) else len(content)
        sections.append((title, content[start:end].strip()))
    return sections


def _find_split(text: str, target: int) -> int:
    """在目标位置附近找段落边界。"""
    if len(text) <= target:
        return len(text)
    window = text[target : target + 200]
    newline = window.find("\n\n")
    if newline >= 0:
        return target + newline
    return target


def _make_chunk(doc: RunbookDocument, section: str, content: str) -> RunbookChunk:
    metadata = {
        "document_id": doc.document_id,
        "version": doc.version,
        "category": doc.category,
        "section": section,
    }
    return RunbookChunk(
        content=content.strip(),
        metadata=metadata,
        content_hash=hashlib.sha256(content.strip().encode("utf-8")).hexdigest(),
    )
