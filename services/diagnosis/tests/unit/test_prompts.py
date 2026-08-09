"""Prompt 渲染与安全约束测试。"""

from app.llm.prompts import PromptRegistry, render_prompt


def test_diagnosis_prompt_security_rule():
    registry = PromptRegistry()
    tpl = registry.get_diagnosis()
    assert "不可信数据" in tpl.system
    assert "不允许生成或执行任何 Shell" in tpl.system
    assert "OOMKilled" in tpl.system
    assert "snake_case" in tpl.system


def test_reviewer_prompt_defines_pass_contract() -> None:
    tpl = PromptRegistry().get_reviewer()

    assert "verdict 必须为 pass" in tpl.system
    assert "pass 字段必须与 verdict" in tpl.system


def test_render_prompt_compacts():
    registry = PromptRegistry()
    tpl = registry.get_diagnosis()
    big_evidence = {"items": [{"summary": "x" * 50000}]}
    messages = render_prompt(tpl, {"uid": "u1"}, big_evidence, [])
    assert messages[0]["role"] == "system"
    assert messages[1]["role"] == "user"
    # 压缩后不应携带 50KB 原文。
    assert len(messages[1]["content"]) < 30000


def test_unknown_prompt_version():
    registry = PromptRegistry()
    import pytest

    with pytest.raises(ValueError):
        registry.get_diagnosis("v999")
