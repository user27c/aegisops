"""RRF（Reciprocal Rank Fusion）纯函数。"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class ScoredID:
    """融合后的结果。"""

    id: str
    score: float
    ranks: list[int] = field(default_factory=list)


def reciprocal_rank_fusion(
    rankings: list[list[str]], k: int = 60, limit: int = 5
) -> list[ScoredID]:
    """RRF 融合多路排序。

    score(id) = Σ 1/(k + rank(id))，rank 从 1 开始。
    tie 时按 id 字典序稳定排序；重复 ID 取第一次出现位置；空列表忽略。
    """
    if k <= 0:
        raise ValueError("k 必须为正")
    scores: dict[str, float] = {}
    first_rank: dict[str, int] = {}
    rank_lists: dict[str, list[int]] = {}

    for ranking in rankings:
        seen_in_ranking: set[str] = set()
        for idx, doc_id in enumerate(ranking):
            rank = idx + 1
            if doc_id in seen_in_ranking:
                # 同一路排名中的重复 ID 只计第一次出现；跨路必须累加。
                continue
            seen_in_ranking.add(doc_id)
            scores[doc_id] = scores.get(doc_id, 0.0) + 1.0 / (k + rank)
            first_rank.setdefault(doc_id, rank)
            rank_lists.setdefault(doc_id, []).append(rank)

    merged = [
        ScoredID(id=doc_id, score=score, ranks=rank_lists[doc_id])
        for doc_id, score in scores.items()
    ]
    # 按分数降序；同分按字典序（确定性）。
    merged.sort(key=lambda s: (-s.score, s.id))
    return merged[:limit]
