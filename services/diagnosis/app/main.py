"""诊断服务 FastAPI 入口。Lifespan 只检查 migration 版本，不自动迁移。"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.responses import JSONResponse

from app.config import Settings, get_settings


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """应用生命周期：M3 阶段在此创建 DB pool 与 repositories。"""
    settings: Settings = app.state.settings
    logging.getLogger("uvicorn").setLevel(settings.log_level.upper())
    yield


def create_app(settings: Settings | None = None) -> FastAPI:
    """创建 FastAPI 应用。测试可注入自定义 settings。"""
    settings = settings or get_settings()
    app = FastAPI(
        title="AegisOps Diagnosis API",
        version="0.1.0",
        lifespan=lifespan,
    )
    app.state.settings = settings

    @app.get("/healthz", tags=["health"])
    async def healthz() -> dict[str, str]:
        """进程存活检查。"""
        return {"status": "ok"}

    @app.get("/readyz", tags=["health"])
    async def readyz() -> JSONResponse:
        """就绪检查：M3 阶段增加数据库连通性校验。"""
        return JSONResponse({"status": "ready"})

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
