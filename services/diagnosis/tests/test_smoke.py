"""冒烟测试：应用可创建、健康检查可用。"""

from app.config import Settings
from app.main import create_app
from fastapi.testclient import TestClient


def make_settings() -> Settings:
    """测试专用 Settings，避免读取真实环境变量。"""
    return Settings(
        database_url="postgresql+asyncpg://test:test@localhost:5432/test",
        llm_provider="fake",
    )


def test_healthz() -> None:
    client = TestClient(create_app(make_settings()))
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_readyz() -> None:
    client = TestClient(create_app(make_settings()))
    resp = client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ready"
