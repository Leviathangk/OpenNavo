"""Read secrets only from the environment; never load .env files automatically."""

from typing import Literal
from urllib.parse import urlsplit

from pydantic import Field, SecretStr, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(extra="ignore", env_file=None)
    agent_api_base: str = Field("http://127.0.0.1:8080/agent-api", alias="OPENNAVO_AGENT_API_BASE")
    gateway_secret: SecretStr = Field(alias="AGENT_GATEWAY_SECRET", min_length=32)
    host: str = Field("127.0.0.1", alias="OPENNAVO_MCP_HOST")
    port: int = Field(8790, alias="OPENNAVO_MCP_PORT", ge=1024, le=65535)
    path: str = Field("/mcp", alias="OPENNAVO_MCP_PATH")
    oauth_issuer: str | None = Field(None, alias="OPENNAVO_OAUTH_ISSUER")
    transport: Literal["http", "stdio"] = Field("http", alias="OPENNAVO_MCP_TRANSPORT")
    token: SecretStr | None = Field(None, alias="OPENNAVO_TOKEN")
    trusted_proxies: list[str] = Field(default_factory=list, alias="OPENNAVO_MCP_TRUSTED_PROXIES")

    @field_validator("agent_api_base")
    @classmethod
    def internal_base(cls, value: str) -> str:
        parsed = urlsplit(value)
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
            or parsed.path.rstrip("/") != "/agent-api"
        ):
            raise ValueError("Agent API base must end in /agent-api, without credentials or query")
        return value.rstrip("/")

    @field_validator("path")
    @classmethod
    def mcp_path(cls, value: str) -> str:
        if value != "/mcp":
            raise ValueError("MCP path must be /mcp")
        return value

    @field_validator("oauth_issuer")
    @classmethod
    def public_issuer(cls, value: str | None) -> str | None:
        if value is None:
            return None
        parsed = urlsplit(value)
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.path not in {"", "/"}
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError("OAuth issuer must be an HTTPS origin")
        return value.rstrip("/")
