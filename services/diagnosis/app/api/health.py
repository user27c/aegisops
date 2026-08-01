"""健康检查。"""

from __future__ import annotations

from typing import Annotated

from fastapi import APIRouter, Depends, Response, status
from sqlalchemy.ext.asyncio import AsyncEngine

from app.api.deps import get_engine

router = APIRouter(tags=["health"])


@router.get("/healthz")
async def healthz() -> dict[str, str]:
    """进程存活检查。"""
    return {"status": "ok"}


EngineDep = Annotated[AsyncEngine | None, Depends(get_engine)]


@router.get("/readyz")
async def readyz(engine: EngineDep) -> Response:
    """就绪检查：数据库可连接且 migration 已到 head。"""
    if engine is None:
        return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content="database not configured")
    from app.db.engine import check_database, check_migration_head

    try:
        await check_database(engine)
        migrated = await check_migration_head(engine)
    except Exception:
        return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content="database unavailable")
    if not migrated:
        return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content="migration pending")
    return Response(status_code=status.HTTP_200_OK, content="ready")
