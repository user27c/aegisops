"""0004_langgraph_checkpoints: LangGraph checkpoint 表

Revision ID: 0004_langgraph_checkpoints
Revises: 0003_audit_hash_chain
Create Date: 2026-08-02

说明：LangGraph checkpoint 使用官方 langgraph-checkpoint-postgres 的
表结构。为保证 CI 从空库可初始化，这里调用官方 setup 生成表；
若官方结构变化，请同步升级本迁移并记录 package version。
"""
from __future__ import annotations

from alembic import op
from sqlalchemy import text

revision = "0004_langgraph_checkpoints"
down_revision = "0003_audit_hash_chain"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # 当前 langgraph-checkpoint-postgres 版本（>=2.0）无公开 setup API；
    # 这里按官方 DDL 手工创建 schema（表名/列名与 PostgresSaver 一致）。
    # 升级依赖时请核对官方表结构是否变化。
    _create_checkpoint_tables(op.get_bind())


def _create_checkpoint_tables(conn) -> None:  # type: ignore[no-untyped-def]
    """手工创建 LangGraph checkpoint 表（官方 DDL 的 SQLAlchemy 映射）。"""
    conn.execute(text(
        """
        CREATE TABLE IF NOT EXISTS checkpoints (
            thread_id TEXT NOT NULL,
            checkpoint_ns TEXT NOT NULL DEFAULT '',
            checkpoint_id TEXT NOT NULL,
            parent_checkpoint_id TEXT,
            type TEXT,
            checkpoint JSONB NOT NULL,
            metadata JSONB NOT NULL DEFAULT '{}',
            PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)
        )
        """
    ))
    conn.execute(text(
        """
        CREATE TABLE IF NOT EXISTS checkpoint_blobs (
            thread_id TEXT NOT NULL,
            checkpoint_ns TEXT NOT NULL DEFAULT '',
            checkpoint_id TEXT NOT NULL,
            blob_type TEXT NOT NULL,
            blob BYTEA NOT NULL,
            PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id, blob_type)
        )
        """
    ))
    conn.execute(text(
        """
        CREATE TABLE IF NOT EXISTS checkpoint_writes (
            thread_id TEXT NOT NULL,
            checkpoint_ns TEXT NOT NULL DEFAULT '',
            checkpoint_id TEXT NOT NULL,
            task_id TEXT NOT NULL,
            idx INTEGER NOT NULL,
            channel TEXT NOT NULL,
            type TEXT,
            blob BYTEA NOT NULL,
            PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id, task_id, idx)
        )
        """
    ))
    conn.execute(text(
        """
        CREATE INDEX IF NOT EXISTS checkpoint_writes_idx
        ON checkpoint_writes (thread_id, checkpoint_ns, checkpoint_id)
        """
    ))


def downgrade() -> None:
    # 生产 downgrade 会丢 checkpoint 数据，显式拒绝。
    raise RuntimeError(
        "0004_langgraph_checkpoints downgrade 会丢失 LangGraph checkpoint 数据，请人工确认后手动删除表"
    )
