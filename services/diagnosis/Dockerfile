# aegisops-diagnosis 镜像：Python 3.12 + uv，多阶段。
# Stage 1: 依赖构建
FROM python:3.11-slim AS builder
ENV UV_LINK_MODE=copy
COPY --from=ghcr.io/astral-sh/uv:0.11.10 /uv /uvx /bin/
WORKDIR /app
COPY services/diagnosis/pyproject.toml services/diagnosis/uv.lock ./
ENV UV_PYTHON_INSTALL_DIR=/opt/uv-python
RUN uv python install 3.12 && uv venv --python 3.12 && uv sync --frozen --no-dev --no-install-project --python 3.12
COPY services/diagnosis/app/ app/
COPY services/diagnosis/alembic.ini alembic.ini
COPY services/diagnosis/alembic/ alembic/

# Stage 2: 运行（不装编译器；embedding 权重不 COPY，由 initContainer/PVC 提供）
FROM python:3.11-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libpq5 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 65532 --create-home app
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    EMBEDDING_CACHE_DIR=/data/models
WORKDIR /app
COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /opt/uv-python /opt/uv-python
COPY --from=builder /app/app /app/app
COPY --from=builder /app/alembic /app/alembic
COPY --from=builder /app/alembic.ini /app/alembic.ini
USER 65532
EXPOSE 8000
CMD ["python", "-m", "uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8000"]
