"""数据库引擎与会话工厂。"""

from __future__ import annotations

from app.config import Settings
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine, AsyncSession, async_sessionmaker, create_async_engine


def create_engine(settings: Settings) -> AsyncEngine:
    """创建异步引擎。设置连接池、语句超时与应用名。"""
    return create_async_engine(
        settings.database_url.get_secret_value(),
        pool_size=10,
        max_overflow=5,
        pool_pre_ping=True,
        connect_args={
            "timeout": 5,
            "command_timeout": 10,
            "server_settings": {"application_name": "aegisops-diagnosis"},
        },
    )


def create_session_factory(engine: AsyncEngine) -> async_sessionmaker[AsyncSession]:
    """创建会话工厂。"""
    return async_sessionmaker(engine, expire_on_commit=False, class_=AsyncSession)


async def check_database(engine: AsyncEngine) -> None:
    """数据库连通性检查。失败抛出异常（映射为 503）。"""
    async with engine.connect() as conn:
        await conn.execute(text("SELECT 1"))


async def check_migration_head(engine: AsyncEngine) -> bool:
    """检查 migration 是否已到 head（版本表存在且无待执行迁移）。"""
    async with engine.connect() as conn:
        try:
            row = await conn.execute(text("SELECT version_num FROM alembic_version"))
            return row.scalar() is not None
        except Exception:
            return False
