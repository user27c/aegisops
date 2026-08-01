"""0002_pgvector_runbooks: runbooks / runbook_chunks + pgvector

Revision ID: 0002_pgvector_runbooks
Revises: 0001_core_tables
Create Date: 2026-08-02
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from pgvector.sqlalchemy import Vector
from sqlalchemy.dialects import postgresql

revision = "0002_pgvector_runbooks"
down_revision = "0001_core_tables"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("CREATE EXTENSION IF NOT EXISTS vector")

    op.create_table(
        "runbooks",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("document_id", sa.Text(), nullable=False),
        sa.Column("path", sa.Text(), nullable=False),
        sa.Column("title", sa.Text(), nullable=False),
        sa.Column("version", sa.Text(), nullable=False),
        sa.Column("category", sa.Text(), nullable=False),
        sa.Column("metadata", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.Column("content_hash", sa.Text(), nullable=False),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.text("true")),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.UniqueConstraint("document_id", "version", name="uq_runbook_doc_version"),
    )

    op.create_table(
        "runbook_chunks",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("runbook_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("runbooks.id"), nullable=False),
        sa.Column("chunk_index", sa.Integer(), nullable=False),
        sa.Column("content", sa.Text(), nullable=False),
        sa.Column("metadata", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.Column("textsearch", sa.Text(), nullable=True),
        sa.Column("embedding", Vector(512), nullable=True),
        sa.Column("content_hash", sa.Text(), nullable=False),
        sa.UniqueConstraint("runbook_id", "chunk_index", "content_hash", name="uq_chunk_identity"),
    )
    # GIN 全文索引（simple 分词）+ HNSW 向量索引（cosine）。
    op.execute(
        "CREATE INDEX ix_chunks_textsearch ON runbook_chunks "
        "USING gin (to_tsvector('simple', textsearch))"
    )
    op.execute(
        "CREATE INDEX ix_chunks_embedding ON runbook_chunks "
        "USING hnsw (embedding vector_cosine_ops)"
    )


def downgrade() -> None:
    op.drop_index("ix_chunks_embedding", table_name="runbook_chunks")
    op.drop_index("ix_chunks_textsearch", table_name="runbook_chunks")
    op.drop_table("runbook_chunks")
    op.drop_table("runbooks")
    # vector extension 保留（其他表可能使用）；如需移除需显式确认。
    # op.execute("DROP EXTENSION IF EXISTS vector")
