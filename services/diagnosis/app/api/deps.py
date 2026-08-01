"""FastAPI 依赖：数据库会话与引擎。"""

from __future__ import annotations

from collections.abc import AsyncIterator

from fastapi import Request
from sqlalchemy.ext.asyncio import AsyncEngine, AsyncSession


async def get_session(request: Request) -> AsyncIterator[AsyncSession]:
    """每请求一个会话，自动提交/回滚。"""
    factory = request.app.state.session_factory
    if factory is None:
        raise RuntimeError("数据库未初始化")
    async with factory() as session:
        try:
            yield session
            await session.commit()
        except Exception:
            await session.rollback()
            raise


def get_engine(request: Request) -> AsyncEngine | None:
    """返回应用引擎（可能为 None，就绪检查使用）。"""
    engine: AsyncEngine | None = request.app.state.engine
    return engine
