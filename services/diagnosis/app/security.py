"""Diagnosis 服务间鉴权：Bearer Token(SHA256 + constant-time 比较)。

- /healthz、/readyz 不要求认证;
- /v1/** 全部要求认证(401 统一响应,不区分失败原因);
- 日志不得记录完整 Authorization Header 或 Token;
- Token 文件最大 4KiB,读取后 strip();空值在非开发环境 fail-closed。
"""

from __future__ import annotations

import hashlib
import hmac
from pathlib import Path
from typing import Annotated

from fastapi import Header, HTTPException, Request, status

from app.config import Settings

# Token 文件大小上限(4KiB)。
MAX_TOKEN_FILE_BYTES = 4 * 1024

# 统一 401 响应体(不泄露"未配置"还是"Token 错误")。
_UNAUTHORIZED = HTTPException(
    status_code=status.HTTP_401_UNAUTHORIZED,
    detail={"code": "UNAUTHORIZED", "message": "未授权"},
    headers={"WWW-Authenticate": "Bearer"},
)


class AuthenticationError(Exception):
    """认证失败(内部使用,不直接暴露)。"""


def load_api_token(settings: Settings) -> bytes | None:
    """优先读取 api_token_file;其次显式 api_token。空值返回 None。"""
    if settings.api_token_file:
        path = Path(settings.api_token_file)
        if path.exists():
            try:
                raw = path.read_bytes()
            except OSError:
                raise AuthenticationError("无法读取 API Token 文件") from None
            if len(raw) > MAX_TOKEN_FILE_BYTES:
                raise AuthenticationError("API Token 文件超过 4KiB")
            token = raw.decode("utf-8", errors="strict").strip()
            if token:
                return token.encode("utf-8")
    if settings.api_token:
        token = settings.api_token.get_secret_value().strip()
        if token:
            return token.encode("utf-8")
    return None


def parse_bearer_header(value: str | None) -> str:
    """只接受 Bearer scheme;缺失、空 Token、其他 scheme 均拒绝。"""
    if not value:
        raise AuthenticationError("缺少 Authorization Header")
    scheme, _, token = value.partition(" ")
    if scheme.lower() != "bearer" or not token.strip():
        raise AuthenticationError("非 Bearer scheme 或 Token 为空")
    return token.strip()


def verify_token(candidate: str, expected: bytes) -> bool:
    """SHA256 后使用 hmac.compare_digest,避免直接字符串比较。"""
    candidate_hash = hashlib.sha256(candidate.encode("utf-8")).digest()
    expected_hash = hashlib.sha256(expected).digest()
    return hmac.compare_digest(candidate_hash, expected_hash)


async def require_service_token(
    request: Request,
    authorization: Annotated[str | None, Header()] = None,
) -> None:
    settings: Settings = request.app.state.settings
    """验证服务间 Token。失败统一 401。"""
    # 未配置 Token:仅允许显式开启的开发模式;否则 fail-closed。
    expected = load_api_token(settings)
    if expected is None:
        if settings.allow_insecure_no_auth and settings.environment == "development":
            return
        raise _UNAUTHORIZED
    try:
        candidate = parse_bearer_header(authorization)
    except AuthenticationError:
        raise _UNAUTHORIZED from None
    if not verify_token(candidate, expected):
        raise _UNAUTHORIZED
