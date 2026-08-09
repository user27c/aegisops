"""HTTP tracing wiring regressions."""

from app import main
from app.config import Settings


def test_diagnosis_api_initializes_provider_before_instrumenting(monkeypatch) -> None:
    """Instrumentor 必须取得有 exporter 的 provider，而非 no-op tracer。"""
    calls: list[str] = []
    monkeypatch.setattr(main, "init_tracing", lambda *_: calls.append("provider"))
    monkeypatch.setattr(
        main.FastAPIInstrumentor,
        "instrument_app",
        lambda *_args, **_kwargs: calls.append("middleware"),
    )

    main.create_app(
        Settings(
            database_url="postgresql+asyncpg://test:test@localhost:5432/test",
            llm_provider="fake",
            embedding_model="fake",
        )
    )

    assert calls == ["provider", "middleware"]


def test_otel_exporter_endpoint_env_alias(monkeypatch) -> None:
    """Helm 注入的标准 OTLP 环境变量必须进入 Python Settings。"""
    monkeypatch.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.test:4317")

    settings = Settings(
        database_url="postgresql+asyncpg://test:test@localhost:5432/test",
        llm_provider="fake",
        embedding_model="fake",
    )

    assert settings.otel_endpoint == "collector.test:4317"
