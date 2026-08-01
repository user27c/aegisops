"""API 路由汇总。"""

from fastapi import APIRouter

from app.api import analyses, audit, health

api_router = APIRouter()
api_router.include_router(health.router)
api_router.include_router(analyses.router)
api_router.include_router(audit.router)
