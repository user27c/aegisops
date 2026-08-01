"""RRF 纯函数测试。"""

from app.rag.rrf import reciprocal_rank_fusion


def test_basic_fusion():
    rankings = [["a", "b", "c"], ["c", "a"]]
    merged = reciprocal_rank_fusion(rankings, k=60, limit=5)
    assert len(merged) == 3
    # a: rank1(1/61)+rank2(1/62) 最高；c: rank3(1/63)+rank1(1/61) 次之。
    assert merged[0].id == "a"
    assert merged[0].score > merged[1].score
    assert merged[1].id == "c"


def test_tie_deterministic():
    rankings = [["x", "y"], ["y", "x"]]
    m1 = reciprocal_rank_fusion(rankings, limit=2)
    m2 = reciprocal_rank_fusion(rankings, limit=2)
    assert [x.id for x in m1] == [x.id for x in m2]


def test_duplicate_ids_ignored():
    # 同一路排名中重复 ID 只计第一次。
    merged = reciprocal_rank_fusion([["a", "a", "a", "b"]], k=60, limit=5)
    assert len(merged) == 2
    assert merged[0].id == "a"


def test_empty_lists():
    assert reciprocal_rank_fusion([[], []], limit=5) == []


def test_limit():
    merged = reciprocal_rank_fusion([["a", "b", "c", "d"]], limit=2)
    assert len(merged) == 2


def test_invalid_k():
    import pytest

    with pytest.raises(ValueError):
        reciprocal_rank_fusion([["a"]], k=0)
