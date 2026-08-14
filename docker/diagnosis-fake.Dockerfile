# AegisOps 受控演练诊断镜像：只允许与 LLM_PROVIDER=fake / EMBEDDING_MODEL=fake 配套。
# 它保留 API、Worker、LangGraph、RAG/DB 与审计合同，但不携带未使用的
# sentence-transformers / torch 运行时，避免把 8+ GiB 模型依赖拉进 gate-down 演练。
FROM python:3.12-slim AS builder
ENV UV_LINK_MODE=copy
COPY --from=ghcr.io/astral-sh/uv:0.11.10 /uv /uvx /bin/
WORKDIR /app
COPY services/diagnosis/pyproject.toml services/diagnosis/uv.lock ./
ENV UV_PYTHON_INSTALL_DIR=/opt/uv-python
RUN uv python install 3.12 \
    && uv venv --python 3.12 \
    && uv sync --frozen --no-dev --no-install-project --python 3.12
COPY services/diagnosis/app/ app/
COPY services/diagnosis/alembic.ini alembic.ini
COPY services/diagnosis/alembic/ alembic/

FROM python:3.12-slim
RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates libpq5 \
    && apt-get autoremove --purge -y \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 65532 --create-home app
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    LLM_PROVIDER=fake \
    EMBEDDING_MODEL=fake
WORKDIR /app
COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /opt/uv-python /opt/uv-python
COPY --from=builder /app/app /app/app
COPY --from=builder /app/alembic /app/alembic
COPY --from=builder /app/alembic.ini /app/alembic.ini
USER 65532
EXPOSE 8000
CMD ["python", "-m", "uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8000"]
