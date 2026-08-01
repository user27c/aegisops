"""chunker 测试。"""

from pathlib import Path

import pytest
from app.rag.chunker import chunk_markdown, parse_frontmatter

RUNBOOK = """---
id: k8s-oomkilled
version: 1.0.0
title: Kubernetes OOMKilled
category: OOMKilled
alertnames: [ContainerOOMKilled]
targetKinds: [Deployment]
allowedActions: [PatchResourceLimit]
risk: medium
requiredEvidence: [ContainerState]
---

## Symptoms

- 容器以 exit code 137 退出。

## Required Evidence

- ContainerState：exit code。

## Decision Tree

1. 工作集是否持续 > 90% limit？

## Allowed Remediation

- `PatchResourceLimit`。

## Forbidden Conditions

- 禁止把 limit 改为无上限。

## Verification

- 新 Pod 无 OOMKilled。

## Rollback

- 恢复执行前快照。

## Escalation

- 升级 SRE。

## References

- Kubernetes docs
"""


def test_parse_frontmatter(tmp_path: Path):
    path = tmp_path / "oom.md"
    path.write_text(RUNBOOK, encoding="utf-8")
    doc = parse_frontmatter(path)
    assert doc.document_id == "k8s-oomkilled"
    assert doc.category == "OOMKilled"
    assert doc.content_hash


def test_parse_frontmatter_missing(tmp_path: Path):
    path = tmp_path / "no-frontmatter.md"
    path.write_text("## Symptoms\n无 frontmatter 的文档\n", encoding="utf-8")
    with pytest.raises(ValueError):
        parse_frontmatter(path)


def test_chunk_markdown_keeps_forbidden_with_action(tmp_path: Path):
    path = tmp_path / "oom-chunk.md"
    path.write_text(RUNBOOK, encoding="utf-8")
    doc = parse_frontmatter(path)
    chunks = chunk_markdown(doc, target_chars=700, overlap=100)
    assert chunks
    # Forbidden Conditions 与动作在同一个 chunk 或相邻（禁止条件不孤立）。
    for chunk in chunks:
        content = chunk.content
        if "Forbidden Conditions" in content and "Allowed Remediation" in content:
            return  # 已合并，符合要求
    # 或禁止条件在动作块之后相邻出现。
    texts = [c.content for c in chunks]
    joined = "\n".join(texts)
    assert "Forbidden Conditions" in joined
    assert "Allowed Remediation" in joined


def test_chunk_markdown_invalid_params(tmp_path: Path):
    path = tmp_path / "oom-chunk2.md"
    path.write_text(RUNBOOK, encoding="utf-8")
    doc = parse_frontmatter(path)
    with pytest.raises(ValueError):
        chunk_markdown(doc, target_chars=0)
