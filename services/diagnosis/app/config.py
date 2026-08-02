"""诊断服务配置。所有密钥只通过环境变量注入，不进日志与 repr。"""

from __future__ import annotations

from functools import lru_cache
from typing import Literal

from pydantic import AnyHttpUrl, SecretStr
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """服务配置。生产环境禁止 fake provider、空 Key 与 HTTP DeepSeek 地址。"""

    model_config = SettingsConfigDict(env_file=".env", env_prefix="", extra="ignore")

    # 数据库
    database_url: SecretStr
    # LLM
    deepseek_api_key: SecretStr | None = None
    deepseek_base_url: AnyHttpUrl = AnyHttpUrl("https://api.deepseek.com")
    deepseek_model: str = "deepseek-chat"
    llm_provider: Literal["deepseek", "fake"] = "fake"
    # Embedding
    embedding_model: str = "BAAI/bge-small-zh-v1.5"
    embedding_cache_dir: str = "/data/models"
    # Worker
    worker_concurrency: int = 2
    max_evidence_bytes: int = 524288
    # Prompt
    prompt_version: str = "diagnosis-v1"
    # 鉴权（由 aegisops-operator / incident-api 调用）
    api_token: SecretStr | None = None
    api_token_file: str = "/run/secrets/diagnosis-token"  # noqa: S105
    allow_insecure_no_auth: bool = False
    environment: Literal["development", "production"] = "development"
    # 可观测性
    otel_endpoint: str = ""
    log_level: str = "info"

    def validate_production(self) -> None:
        """生产模式校验：fake provider 与空 Key 直接拒绝。"""
        if self.llm_provider == "fake":
            raise ValueError("llm_provider=fake 禁止在生产环境启用")
        if self.deepseek_api_key is None or not self.deepseek_api_key.get_secret_value():
            raise ValueError("生产环境必须配置 DEEPSEEK_API_KEY")
        if self.deepseek_base_url.scheme != "https":
            raise ValueError("生产环境 DeepSeek 地址必须使用 HTTPS")


@lru_cache
def get_settings() -> Settings:
    """读取并缓存设置。"""
    return Settings()
