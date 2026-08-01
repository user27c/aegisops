# aegisops-diagnosis 镜像：Python 3.12 + uv，多阶段。
# Stage 1: 依赖构建
FROM python:3.12-slim AS builder
ENV UV_LINK_MODE=copy
COPY --from=ghcr.io/astral-sh/uv:0.11.10 /uv /uvx /bin/
WORKDIR /app
COPY services/diagnosis/pyproject.toml services/diagnosis/uv.lock ./
COPY services/diagnosis/app/ app/
COPY services/diagnosis/alembic.ini alembic.ini
COPY services/diagnosis/alembic/ alembic/
RUN uv sync --frozen --no-dev --no-install-project

# Stage 2: 运行（不装编译器；embedding 权重不 COPY，由 initContainer/PVC 提供）
FROM python:3.12-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libpq5 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 65532 --create-home app
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    EMBEDDING_CACHE_DIR=/data/models
WORKDIR /app
COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /app/app /app/app
COPY --from=builder /app/alembic /app/alembic
COPY --from=builder /app/alembic.ini /app/alembic.ini
USER 65532
EXPOSE 8000
CMD ["python", "-m", "uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8000"]
