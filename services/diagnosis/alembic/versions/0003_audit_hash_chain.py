"""0003_audit_hash_chain: audit_events + 应用权限约束

Revision ID: 0003_audit_hash_chain
Revises: 0002_pgvector_runbooks
Create Date: 2026-08-02
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0003_audit_hash_chain"
down_revision = "0002_pgvector_runbooks"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "audit_events",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("incident_uid", sa.Text(), nullable=False),
        sa.Column("sequence", sa.BigInteger(), nullable=False),
        sa.Column("idempotency_key", sa.Text(), nullable=False, unique=True),
        sa.Column("component", sa.Text(), nullable=False),
        sa.Column("event_type", sa.Text(), nullable=False),
        sa.Column("actor", sa.Text(), nullable=True),
        sa.Column("payload", postgresql.JSONB(), nullable=False, server_default="{}"),
        sa.Column("previous_hash", sa.Text(), nullable=False),
        sa.Column("event_hash", sa.Text(), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.UniqueConstraint("incident_uid", "sequence", name="uq_audit_incident_seq"),
    )
    op.create_index("ix_audit_incident", "audit_events", ["incident_uid", "created_at"])


def downgrade() -> None:
    op.drop_index("ix_audit_incident", table_name="audit_events")
    op.drop_table("audit_events")
