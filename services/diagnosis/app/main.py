"""诊断服务 FastAPI 入口。Lifespan 创建 DB pool，只检查 migration 不自动迁移。"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor

from app.api import api_router
from app.config import Settings, get_settings
from app.db.engine import check_database, create_engine, create_session_factory
from app.tracing import init_tracing


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """应用生命周期：创建 DB pool 与 repositories。"""
    settings: Settings = app.state.settings
    logging.getLogger("uvicorn").setLevel(settings.log_level.upper())

    engine = create_engine(settings)
    session_factory = create_session_factory(engine)
    app.state.engine = engine
    app.state.session_factory = session_factory

    # 只检查连通性，不自动迁移（migration 由 Helm hook/手动执行）。
    try:
        await check_database(engine)
    except Exception:
        logging.getLogger("uvicorn").warning("数据库暂不可用，readyz 将返回 503")
    yield
    await engine.dispose()


def create_app(settings: Settings | None = None) -> FastAPI:
    """创建 FastAPI 应用。测试可注入自定义 settings。"""
    settings = settings or get_settings()
    # Instrumentor 在挂载时获取 tracer；必须先初始化 provider，避免它绑定
    # 到无 exporter 的 no-op tracer。
    init_tracing(settings.otel_endpoint, "aegisops-diagnosis")
    app = FastAPI(
        title="AegisOps Diagnosis API",
        version="0.1.0",
        lifespan=lifespan,
    )
    app.state.settings = settings
    app.state.engine = None
    # 在 provider 初始化后挂载中间件；排除探针，避免高频健康检查淹没
    # 业务 trace。
    FastAPIInstrumentor.instrument_app(app, excluded_urls="healthz,readyz,metrics")
    app.include_router(api_router)
    return app


def run() -> None:
    """命令行入口：uvicorn 启动。"""
    import uvicorn

    settings = get_settings()
    uvicorn.run(
        "app.main:create_app",
        factory=True,
        host="0.0.0.0",  # noqa: S104 - 容器内服务默认监听全部接口
        port=8000,
        log_level=settings.log_level,
    )
