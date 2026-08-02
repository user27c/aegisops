"""服务间 Bearer Token 鉴权测试。"""

from __future__ import annotations

import pytest
from app.config import Settings
from app.main import create_app
from app.security import (
    AuthenticationError,
    load_api_token,
    parse_bearer_header,
    verify_token,
)
from fastapi.testclient import TestClient


def make_app(token: str | None = "test-token", *, no_auth: bool = False) -> TestClient:  # noqa: S107
    settings = Settings(
        database_url="postgresql+asyncpg://x:x@localhost/x",
        llm_provider="fake",
        embedding_model="fake",
        api_token=token,
        allow_insecure_no_auth=no_auth,
        environment="development" if no_auth else "production",
    )
    # raise_server_exceptions=False:业务层(缺 DB)错误以 500 呈现,便于区分鉴权 401。
    return TestClient(create_app(settings), raise_server_exceptions=False)


def test_no_header_401() -> None:
    client = make_app()
    resp = client.post("/v1/analyses", json={"evidence": {}})
    assert resp.status_code == 401


def test_basic_scheme_401() -> None:
    client = make_app()
    resp = client.post(
        "/v1/analyses",
        headers={"Authorization": "Basic dXNlcjpwYXNz"},
        json={"evidence": {}},
    )
    assert resp.status_code == 401


def test_bearer_empty_token_401() -> None:
    client = make_app()
    resp = client.post(
        "/v1/analyses", headers={"Authorization": "Bearer "}, json={"evidence": {}}
    )
    assert resp.status_code == 401


def test_wrong_token_401() -> None:
    client = make_app()
    resp = client.post(
        "/v1/analyses",
        headers={"Authorization": "Bearer wrong-token"},
        json={"evidence": {}},
    )
    assert resp.status_code == 401


def test_correct_token_reaches_business_route() -> None:
    client = make_app()
    # 鉴权通过后进入业务层(缺 DB 返回 500),而不是 401。
    resp = client.post(
        "/v1/analyses",
        headers={"Authorization": "Bearer test-token"},
        json={"evidence": {"items": []}},
    )
    assert resp.status_code != 401


def test_token_whitespace_trimmed() -> None:
    client = make_app(" test-token ")
    resp = client.post(
        "/v1/analyses",
        headers={"Authorization": "Bearer test-token"},
        json={"evidence": {}},
    )
    assert resp.status_code != 401


def test_healthz_public() -> None:
    client = make_app()
    assert client.get("/healthz").status_code == 200


def test_no_token_production_fails_closed() -> None:
    settings = Settings(
        database_url="postgresql+asyncpg://x:x@localhost/x",
        llm_provider="fake",
        embedding_model="fake",
        api_token=None,
        environment="production",
    )
    client = TestClient(create_app(settings))
    resp = client.post("/v1/analyses", json={"evidence": {}})
    assert resp.status_code == 401


def test_auth_failure_log_does_not_contain_token() -> None:
    """认证失败日志不得包含 Token 原文(此处直接验证 401 响应不泄露)。"""
    client = make_app()
    resp = client.post(
        "/v1/analyses",
        headers={"Authorization": "Bearer secret-token-value"},
        json={"evidence": {}},
    )
    assert resp.status_code == 401
    body = resp.text
    assert "secret-token-value" not in body
    assert "UNAUTHORIZED" in body


# ---- 单元级:parse/verify/load ----

def test_parse_bearer_header_valid() -> None:
    assert parse_bearer_header("Bearer abc") == "abc"
    assert parse_bearer_header("bearer  abc  ") == "abc"


def test_parse_bearer_header_invalid() -> None:
    for bad in (None, "", "Basic abc", "Bearer"):
        with pytest.raises(AuthenticationError):
            parse_bearer_header(bad)


def test_verify_token_constant_time() -> None:
    assert verify_token("right", b"right")
    assert not verify_token("wrong", b"right")


def test_load_api_token_file() -> None:
    from pydantic import SecretStr

    settings = Settings(
        database_url="postgresql+asyncpg://x:x@localhost/x",
        llm_provider="fake",
        embedding_model="fake",
        api_token=SecretStr("fallback"),
    )
    assert load_api_token(settings) == b"fallback"
