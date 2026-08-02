"""API 路由汇总。

- /healthz、/readyz 公开;
- /v1/** 全部要求服务间 Bearer Token(见 app.security)。
"""

from fastapi import APIRouter, Depends

from app.api import analyses, audit, health
from app.security import require_service_token

api_router = APIRouter()
api_router.include_router(health.router)

# 受保护路由:/v1/** 统一鉴权。
protected = APIRouter(dependencies=[Depends(require_service_token)])
protected.include_router(analyses.router)
protected.include_router(audit.router)
api_router.include_router(protected)
