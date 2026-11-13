"""Anonymous, read-only public MCP server.

This module deliberately has no imports from the admin MCP adapter.  It talks to
the documented public API over GET only, selects a small public response shape,
and is safe to expose at ``https://opennavo.com/mcp``.
"""

from __future__ import annotations

import asyncio
import hashlib
import ipaddress
import json
import logging
import re
import secrets
import sys
import time
from collections import OrderedDict
from collections.abc import AsyncGenerator, Mapping, Sequence
from contextlib import asynccontextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from typing import Annotated, Any, Literal, cast
from urllib.parse import urlsplit

import httpx2
import uvicorn
from fastmcp import FastMCP
from fastmcp.exceptions import ToolError
from mcp_types import ToolAnnotations
from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Receive, Scope, Send

DEFAULT_API_BASE = "https://api.opennavo.com/api/v1"
PUBLIC_SITE = "https://opennavo.com"
MAX_UPSTREAM_BYTES = 2 * 1024 * 1024
MAX_OUTPUT_BYTES = 24 * 1024
MAX_TEXT = 900
MAX_LIST = 12
PUBLIC_GCRA_MAX_ENTRIES = 10_000
PUBLIC_GCRA_OVERFLOW_SHARDS = 64
_PUBLIC_GCRA_HASH_KEY = secrets.token_bytes(32)
_TOKEN = re.compile(r"^[a-z0-9][a-z0-9+_.@-]{0,127}$")
_SLUG = re.compile(r"^[a-z0-9][a-z0-9-]{0,127}$")
_KIND = re.compile(r"^[a-z][a-z0-9_-]{0,31}$")

logger = logging.getLogger(__name__)
PUBLIC_PEER: ContextVar[str | None] = ContextVar("public_mcp_peer", default=None)


class PublicMCPError(ToolError):
    """A bounded, model-friendly public MCP failure."""


class PublicSettings(BaseSettings):
    """Configuration for the isolated public process; no credentials are accepted."""

    model_config = SettingsConfigDict(extra="ignore", env_file=None)

    api_base: str = Field(DEFAULT_API_BASE, alias="OPENNAVO_PUBLIC_API_BASE")
    host: str = Field("127.0.0.1", alias="OPENNAVO_PUBLIC_MCP_HOST")
    port: int = Field(8790, alias="OPENNAVO_PUBLIC_MCP_PORT", ge=1024, le=65535)
    path: Literal["/mcp"] = Field("/mcp", alias="OPENNAVO_PUBLIC_MCP_PATH")
    trusted_proxies: list[str] = Field(
        default_factory=list, alias="OPENNAVO_PUBLIC_MCP_TRUSTED_PROXIES"
    )
    rate_limit: int = Field(60, alias="OPENNAVO_PUBLIC_MCP_RATE_LIMIT", ge=1, le=300)
    rate_window_seconds: int = Field(
        60, alias="OPENNAVO_PUBLIC_MCP_RATE_WINDOW_SECONDS", ge=1, le=3600
    )
    timeout_seconds: float = Field(15.0, alias="OPENNAVO_PUBLIC_MCP_TIMEOUT_SECONDS", gt=0, le=60)

    @field_validator("api_base")
    @classmethod
    def _validate_origin_path(cls, value: str) -> str:
        parsed = urlsplit(value)
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
            or parsed.path.rstrip("/") != "/api/v1"
        ):
            raise ValueError("Public API base must end in /api/v1, without credentials or query")
        return value.rstrip("/")


def _check_base(value: str) -> str:
    parsed = urlsplit(value)
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or parsed.path.rstrip("/") != "/api/v1"
    ):
        raise ValueError("Public API base must end in /api/v1, without credentials or query")
    return value.rstrip("/")


def _safe_segment(value: str, pattern: re.Pattern[str], label: str) -> str:
    if not pattern.fullmatch(value):
        raise PublicMCPError(f"Invalid {label}")
    return value


class PublicAPIClient:
    """Allowlisted, anonymous GET client for the public OpenAPI base."""

    def __init__(self, settings: PublicSettings, http: httpx2.AsyncClient | None = None):
        self.settings = settings
        self.base = _check_base(settings.api_base)
        self.http = http or httpx2.AsyncClient(
            timeout=settings.timeout_seconds, follow_redirects=False, trust_env=False
        )

    async def close(self) -> None:
        await self.http.aclose()

    def _url(self, endpoint: str) -> str:
        if not endpoint.startswith("/") or "//" in endpoint or ".." in endpoint:
            raise ValueError("invalid public API endpoint")
        allowed = (
            "/categories",
            "/search",
            "/packages/",
            "/rankings",
            "/collections",
            "/articles",
            "/developer-map",
        )
        if not any(
            endpoint.startswith(prefix)
            if prefix.endswith("/")
            else endpoint == prefix or endpoint.startswith(prefix + "/")
            for prefix in allowed
        ):
            raise ValueError("endpoint is not in the public API allowlist")
        url = self.base + endpoint
        parsed = urlsplit(url)
        base_parsed = urlsplit(self.base)
        if (
            parsed.scheme != base_parsed.scheme
            or parsed.hostname != base_parsed.hostname
            or parsed.port != base_parsed.port
            or not parsed.path.startswith(base_parsed.path.rstrip("/") + "/")
        ):
            raise ValueError("public API endpoint escaped the configured base")
        return url

    async def get(
        self,
        endpoint: str,
        params: Mapping[str, Any] | None = None,
        *,
        text: bool = False,
    ) -> Any:
        """Fetch one public endpoint and return its compact JSON data or plain text body."""

        url = self._url(endpoint)
        query = {
            key: value for key, value in (params or {}).items() if value is not None and value != ""
        }
        headers = {"Accept": "application/json", "User-Agent": "OpenNavo-public-mcp/0.1"}
        # Resolve the peer in PublicPeerMiddleware and never copy an inbound X-Forwarded-For.
        if (peer := PUBLIC_PEER.get()) is not None:
            headers["X-Forwarded-For"] = peer
        try:
            response = await self.http.get(
                url,
                params=query,
                headers=headers,
            )
        except httpx2.TimeoutException:
            raise PublicMCPError(
                "public_api_timeout: the public API did not respond in time"
            ) from None
        except httpx2.HTTPError:
            logger.warning("public API request failed")
            raise PublicMCPError(
                "public_api_unavailable: the public API could not be reached"
            ) from None
        if len(response.content) > MAX_UPSTREAM_BYTES:
            raise PublicMCPError("public_api_response_too_large")
        if response.status_code == 404:
            raise PublicMCPError("public_api_not_found: the public object was not found")
        if response.status_code == 429:
            raise PublicMCPError("public_api_rate_limited: try again later")
        if response.status_code >= 500:
            raise PublicMCPError(
                "public_api_unavailable: the public API is temporarily unavailable"
            )
        if response.status_code >= 400:
            raise PublicMCPError("public_api_request_rejected")
        if text:
            return response.text[:8000]
        try:
            payload = response.json()
        except (ValueError, TypeError):
            raise PublicMCPError("public_api_invalid_response") from None
        if not isinstance(payload, dict):
            raise PublicMCPError("public_api_invalid_response")
        payload = _map(payload)
        code = payload.get("code")
        if code != "0000":
            if code == "1002":
                raise PublicMCPError("public_api_not_found: the public object was not found")
            if code == "1005":
                raise PublicMCPError("public_api_rate_limited: try again later")
            raise PublicMCPError("public_api_error: the public API rejected the request")
        return payload.get("data")


def _text(value: Any, limit: int = MAX_TEXT) -> str | None:
    if value is None:
        return None
    if not isinstance(value, str):
        value = str(value)
    value = " ".join(value.split())
    return value if len(value) <= limit else value[: limit - 1].rstrip() + "…"


def _map(value: Any) -> dict[str, Any]:
    return cast(dict[str, Any], value) if isinstance(value, dict) else {}


def _list(value: Any, limit: int = MAX_LIST) -> list[Any]:
    return list(cast(list[Any], value))[:limit] if isinstance(value, list) else []


def _pick(source: Mapping[str, Any], *keys: str) -> dict[str, Any]:
    return {key: source[key] for key in keys if key in source and source[key] is not None}


def _package_status(source: Mapping[str, Any]) -> dict[str, bool]:
    return {key: bool(source.get(key, False)) for key in ("deprecated", "disabled")}


def app_link(kind: str, token: str) -> str:
    return f"{PUBLIC_SITE}/apps/{token}"


def _category(value: Any) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        return None
    source = _map(value)
    out = _pick(source, "slug", "name", "icon", "appliesTo", "packageCount")
    if "description" in source:
        out["description"] = _text(source["description"], 300)
    return out or None


def app_summary(value: Any, *, include_status_defaults: bool = True) -> dict[str, Any]:
    source = _map(value)
    kind = str(source.get("kind", "cask"))
    token = str(source.get("token", ""))
    out = _pick(
        source,
        "kind",
        "token",
        "displayName",
        "name",
        "version",
        "installs30d",
        "rank30d",
        "autoUpdates",
        "editorChoice",
        "isFont",
        "isLibrary",
        "iconUrl",
        "accentColor",
    )
    out.update(
        {
            key: status
            for key, status in _package_status(source).items()
            if include_status_defaults or key in source
        }
    )
    if "summary" in source:
        out["summary"] = _text(source["summary"], 450)
    category = _category(source.get("primaryCategory"))
    if category:
        out["category"] = category
    if token:
        out["link"] = app_link(kind, token)
    return out


def _release(value: Any, link: str) -> dict[str, Any]:
    source = _map(value)
    out = _pick(
        source,
        "id",
        "version",
        "versionBase",
        "title",
        "publishedAt",
        "brewCommittedAt",
        "source",
        "isLatest",
        "isPrerelease",
        "hasNotes",
    )
    for key in ("summary", "bodyMarkdown"):
        if key in source:
            out["notes"] = _text(source[key], 1000)
            break
    out["link"] = link
    return out


def _dependency(value: Any) -> dict[str, Any]:
    source = _map(value)
    out = _pick(source, "kind", "token", "name", "displayName", "version", "optional")
    # DependencyNode does not guarantee lifecycle flags; absence means unknown.
    out.update({key: bool(source[key]) for key in ("deprecated", "disabled") if key in source})
    token, kind = source.get("token"), source.get("kind", "cask")
    if isinstance(token, str) and token:
        out["link"] = app_link(str(kind), token)
    for key in ("dependencies", "children", "requires"):
        if isinstance(source.get(key), list):
            out[key] = [_dependency(item) for item in _list(source[key], 8)]
    return out


def fit_public_output(value: Any, link: str) -> dict[str, Any]:
    """Bound a selected result without ever forwarding raw upstream payloads."""

    text_limit, list_limit = MAX_TEXT, MAX_LIST
    current = value
    for _ in range(7):
        current = _trim(current, text_limit, list_limit)
        encoded = json.dumps(current, ensure_ascii=False, separators=(",", ":")).encode()
        if len(encoded) <= MAX_OUTPUT_BYTES:
            return cast(dict[str, Any], current)
        text_limit = max(80, text_limit // 2)
        list_limit = max(1, list_limit // 2)
    return {"link": link, "truncated": True, "message": "The public result was truncated."}


def _trim(value: Any, text_limit: int, list_limit: int) -> Any:
    if isinstance(value, str):
        return _text(value, text_limit) or ""
    if isinstance(value, list):
        items = cast(list[Any], value)
        return [_trim(item, text_limit, list_limit) for item in items[:list_limit]]
    if isinstance(value, dict):
        source = _map(value)
        return {str(key): _trim(item, text_limit, list_limit) for key, item in source.items()}
    return value


def _locale_params(locale: str | None) -> dict[str, str]:
    return {"locale": locale} if locale else {}


def _tool_annotations() -> ToolAnnotations:
    return ToolAnnotations(
        read_only_hint=True,
        destructive_hint=False,
        idempotent_hint=True,
        open_world_hint=True,
    )


def register_public_tools(server: FastMCP[Any], api: PublicAPIClient) -> None:
    """Register only the ten anonymous public tools."""

    annotations = _tool_annotations()

    @server.tool(
        name="search_apps",
        description=(
            "Search the public OpenNavo app catalog. Results are compact and link to "
            "public app pages."
        ),
        annotations=annotations,
    )
    async def search_apps(
        q: Annotated[str, Field(min_length=1, max_length=64)],
        kind: Literal["cask"] = "cask",
        category: Annotated[str | None, Field(max_length=128)] = None,
        limit: Annotated[int, Field(ge=1, le=12)] = 8,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        data = await api.get(
            "/search",
            {"q": q, "kind": kind, "category": category, "size": limit, **_locale_params(locale)},
        )
        source = _map(data)
        apps = [app_summary(item.get("package", item)) for item in _list(source.get("records"))]
        return fit_public_output(
            {"link": f"{PUBLIC_SITE}/apps", "query": q, "total": source.get("total"), "apps": apps},
            f"{PUBLIC_SITE}/apps",
        )

    @server.tool(
        name="get_app",
        description=(
            "Read one public app with its Homebrew install command, OpenNavo page, "
            "open-source details, and safety status."
        ),
        annotations=annotations,
    )
    async def get_app(
        token: Annotated[str, Field(min_length=1, max_length=128)],
        kind: Literal["cask"] = "cask",
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        token = _safe_segment(token, _TOKEN, "token")
        data = await api.get(f"/packages/{kind}/{token}", _locale_params(locale))
        source = _map(data)
        link = app_link(kind, token)
        out: dict[str, Any] = {"link": link}
        out.update(
            _pick(
                source,
                "kind",
                "token",
                "displayName",
                "name",
                "version",
                "developer",
                "license",
                "installCommand",
                "downloadSize",
                "minMacos",
            )
        )
        for key in ("summary", "description", "descriptionEn", "caveats"):
            if key in source:
                out[key] = _text(source[key], 1200 if key == "description" else 700)
        if source.get("artifacts") and isinstance(source["artifacts"], dict):
            out["artifacts"] = _pick(
                _map(source["artifacts"]), "apps", "binaries", "pkgs", "uninstallNotes"
            )
        if source.get("supports") and isinstance(source["supports"], dict):
            out["supports"] = _pick(
                _map(source["supports"]), "arm64", "x86_64", "status", "requiresRosetta"
            )
        out["openSource"] = _pick(source, "repoUrl", "license")
        safety = _pick(source, "autoUpdates", "kegOnly", "minMacos")
        safety.update(_package_status(source))
        for key in ("deprecation", "disable"):
            if isinstance(source.get(key), dict):
                notice = _map(source[key])
                safety[key] = _pick(notice, "date", "reason")
                if isinstance(notice.get("replacement"), dict):
                    safety[key]["replacement"] = _pick(_map(notice["replacement"]), "kind", "token")
        out["safety"] = safety
        links = {"detail": link}
        for key, alias in (
            ("homepage", "homepage"),
            ("repoUrl", "repository"),
            ("sourceUrl", "source"),
            ("formulaeUrl", "formula"),
        ):
            if source.get(key):
                links[alias] = source[key]
        out["links"] = links
        if isinstance(source.get("latestRelease"), dict):
            out["latestRelease"] = _release(source["latestRelease"], link)
        return fit_public_output(out, link)

    @server.tool(
        name="get_app_releases",
        description="Read a compact public release history for an app.",
        annotations=annotations,
    )
    async def get_app_releases(
        token: Annotated[str, Field(min_length=1, max_length=128)],
        kind: Literal["cask"] = "cask",
        limit: Annotated[int, Field(ge=1, le=12)] = 8,
        only_with_notes: bool = False,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        token = _safe_segment(token, _TOKEN, "token")
        link = app_link(kind, token)
        data = await api.get(
            f"/packages/{kind}/{token}/releases",
            {"size": limit, "onlyWithNotes": only_with_notes, **_locale_params(locale)},
        )
        source = _map(data)
        return fit_public_output(
            {
                "link": link,
                "app": token,
                "total": source.get("total"),
                "releases": [_release(item, link) for item in _list(source.get("records"))],
            },
            link,
        )

    @server.tool(
        name="get_app_dependencies",
        description=(
            "Read public dependencies, conflicts, platform requirements, and a bounded "
            "dependency graph for an app."
        ),
        annotations=annotations,
    )
    async def get_app_dependencies(
        token: Annotated[str, Field(min_length=1, max_length=128)],
        kind: Literal["cask"] = "cask",
        depth: Annotated[int, Field(ge=1, le=3)] = 2,
        platform: Annotated[str | None, Field(max_length=80)] = None,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        token = _safe_segment(token, _TOKEN, "token")
        link = app_link(kind, token)
        data = await api.get(
            f"/packages/{kind}/{token}/dependencies",
            {"depth": depth, "platform": platform, **_locale_params(locale)},
        )
        source = _map(data)
        out: dict[str, Any] = {"link": link, "app": token}
        for key in ("dependsOn", "conflictsWith"):
            if isinstance(source.get(key), dict):
                value = _map(source[key])
                out[key] = _pick(value, "casks", "formulae", "requirements", "reasons")
        out["dependencies"] = [_dependency(item) for item in _list(source.get("dependencies"), 10)]
        if isinstance(source.get("dependents"), dict):
            dependents = _map(source["dependents"])
            out["dependents"] = {
                "count": dependents.get("count"),
                "top": [_dependency(item) for item in _list(dependents.get("top"), 8)],
            }
        platforms: list[dict[str, Any]] = []
        for item in _list(source.get("platforms"), 8):
            if isinstance(item, dict):
                platforms.append(
                    _pick(
                        _map(item),
                        "arch",
                        "version",
                        "macos",
                        "requiresRosetta",
                        "downloadUrl",
                        "downloadSha256",
                        "tag",
                    )
                )
        if platforms:
            out["platforms"] = platforms
        return fit_public_output(out, link)

    @server.tool(
        name="similar_apps",
        description="Find public apps related by content type and category.",
        annotations=annotations,
    )
    async def similar_apps(
        token: Annotated[str, Field(min_length=1, max_length=128)],
        kind: Literal["cask"] = "cask",
        limit: Annotated[int, Field(ge=1, le=12)] = 8,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        token = _safe_segment(token, _TOKEN, "token")
        link = app_link(kind, token)
        data = await api.get(
            f"/packages/{kind}/{token}/related", {"limit": limit, **_locale_params(locale)}
        )
        return fit_public_output(
            {"link": link, "app": token, "apps": [app_summary(item) for item in _list(data)]}, link
        )

    @server.tool(
        name="list_categories",
        description="List the public OpenNavo category tree with counts and short descriptions.",
        annotations=annotations,
    )
    async def list_categories(
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        data = await api.get("/categories", _locale_params(locale))

        def category_tree(item: Any) -> dict[str, Any]:
            source = _map(item)
            out = _category(source) or {}
            slug = source.get("slug")
            if isinstance(slug, str):
                out["link"] = f"{PUBLIC_SITE}/apps?category={slug}"
            children = [_category(child) for child in _list(source.get("children"), 8)]
            out["children"] = [child for child in children if child]
            return out

        return fit_public_output(
            {
                "link": f"{PUBLIC_SITE}/apps",
                "categories": [category_tree(item) for item in _list(data, 100)],
            },
            f"{PUBLIC_SITE}/apps",
        )

    @server.tool(
        name="rankings",
        description="Read public app installation rankings for a period and optional category.",
        annotations=annotations,
    )
    async def rankings(
        kind: Literal["cask"] = "cask",
        period: Literal["30d", "90d", "365d"] = "30d",
        category: Annotated[str | None, Field(max_length=128)] = None,
        limit: Annotated[int, Field(ge=1, le=12)] = 10,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        data = await api.get(
            "/rankings",
            {
                "kind": kind,
                "period": period,
                "category": category,
                "size": limit,
                **_locale_params(locale),
            },
        )
        source = _map(data)
        rows: list[dict[str, Any]] = []
        for item in _list(source.get("records")):
            if isinstance(item, dict):
                row_source = _map(item)
                row = {
                    "rank": row_source.get("rank"),
                    "installs": row_source.get("installs"),
                    "app": app_summary(row_source.get("package")),
                }
                rows.append(row)
        return fit_public_output(
            {
                "link": f"{PUBLIC_SITE}/rankings",
                "period": period,
                "total": source.get("total"),
                "rankings": rows,
            },
            f"{PUBLIC_SITE}/rankings",
        )

    @server.tool(
        name="collections",
        description=(
            "List public app collections, read one collection, and optionally include its Brewfile."
        ),
        annotations=annotations,
    )
    async def collections(
        slug: Annotated[str | None, Field(max_length=128)] = None,
        include_brewfile: bool = False,
        limit: Annotated[int, Field(ge=1, le=12)] = 10,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        if slug:
            slug = _safe_segment(slug, _SLUG, "collection slug")
            link = f"{PUBLIC_SITE}/collections/{slug}"
            data = await api.get(f"/collections/{slug}", _locale_params(locale))
            source = _map(data)
            out: dict[str, Any] = {"link": link, "slug": slug}
            out.update(_pick(source, "title", "subtitle", "itemCount", "updatedAt", "sourceLocale"))
            for key in ("body",):
                if key in source:
                    out[key] = _text(source[key], 1200)
            out["apps"] = [
                app_summary(_map(item).get("package", item))
                for item in _list(source.get("items"), 12)
                if isinstance(item, dict)
            ]
            if include_brewfile:
                brewfile = await api.get(f"/collections/{slug}/brewfile", text=True)
                out["brewfile"] = _text(brewfile, 8000) or ""
            return fit_public_output(out, link)
        data = await api.get(
            "/collections", {"current": 1, "size": limit, **_locale_params(locale)}
        )
        source = _map(data)
        records: list[dict[str, Any]] = []
        for item in _list(source.get("records")):
            if isinstance(item, dict):
                item_source = _map(item)
                collection = _pick(
                    item_source,
                    "slug",
                    "title",
                    "subtitle",
                    "itemCount",
                    "updatedAt",
                    "sourceLocale",
                )
                if isinstance(item_source.get("slug"), str):
                    collection["link"] = f"{PUBLIC_SITE}/collections/{item_source['slug']}"
                collection["apps"] = [
                    app_summary(app, include_status_defaults=False)
                    for app in _list(item_source.get("previewItems"), 5)
                ]
                records.append(collection)
        return fit_public_output(
            {
                "link": f"{PUBLIC_SITE}/collections",
                "total": source.get("total"),
                "collections": records,
            },
            f"{PUBLIC_SITE}/collections",
        )

    @server.tool(
        name="developer_map",
        description=(
            "Read the public OpenNavo developer map: developer and app totals, "
            "cities with their top developers, and country counts."
        ),
        annotations=annotations,
    )
    async def developer_map(
        limit: Annotated[int, Field(ge=1, le=12)] = 8,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        data = await api.get("/developer-map", _locale_params(locale))
        source = _map(data)
        totals = _map(source.get("totals"))
        places = [
            _pick(
                _map(item),
                "name",
                "countryCode",
                "region",
                "developers",
                "apps",
                "installs30d",
            )
            for item in _list(source.get("places"))[:limit]
        ]
        countries = [
            _pick(_map(item), "code", "developers", "apps", "installs30d")
            for item in _list(source.get("countries"))[:limit]
        ]
        return fit_public_output(
            {
                "link": f"{PUBLIC_SITE}/developer-map",
                "totals": _pick(
                    totals,
                    "developers",
                    "located",
                    "apps",
                    "locatedApps",
                    "places",
                    "countries",
                ),
                "places": places,
                "countries": countries,
            },
            f"{PUBLIC_SITE}/developer-map",
        )

    @server.tool(
        name="articles",
        description="List published public articles or read one published article by slug.",
        annotations=annotations,
    )
    async def articles(
        slug: Annotated[str | None, Field(max_length=128)] = None,
        limit: Annotated[int, Field(ge=1, le=12)] = 10,
        article_type: Annotated[str | None, Field(max_length=32)] = None,
        locale: Annotated[str | None, Field(max_length=16)] = None,
    ) -> dict[str, Any]:
        def card(item: Any) -> dict[str, Any]:
            source = _map(item)
            out = _pick(
                source,
                "slug",
                "title",
                "type",
                "publishedAt",
                "updatedAt",
                "readingMinutes",
                "locale",
            )
            if "summary" in source:
                out["summary"] = _text(source["summary"], 500)
            if isinstance(source.get("slug"), str):
                out["link"] = f"{PUBLIC_SITE}/articles/{source['slug']}"
            return out

        if slug:
            slug = _safe_segment(slug, _SLUG, "article slug")
            link = f"{PUBLIC_SITE}/articles/{slug}"
            data = await api.get(f"/articles/{slug}", _locale_params(locale))
            source = _map(data)
            if source.get("state") not in {None, "published"}:
                raise PublicMCPError("public_api_not_found: the article is not public")
            article = _map(source.get("article")) or source
            out: dict[str, Any] = {"link": link}
            out.update(card(article))
            for key in ("seoTitle", "seoDescription", "bodyMarkdown", "sourceLocale"):
                if key in article:
                    out[key] = _text(article[key], 3200 if key == "bodyMarkdown" else 700)
            if isinstance(article.get("sources"), list):
                out["sources"] = [
                    _pick(_map(item), "title", "url")
                    for item in _list(article["sources"], 8)
                    if isinstance(item, dict)
                ]
            if isinstance(article.get("related"), list):
                out["related"] = [card(item) for item in _list(article["related"], 6)]
            return fit_public_output(out, link)
        data = await api.get(
            "/articles",
            {"current": 1, "size": limit, "type": article_type, **_locale_params(locale)},
        )
        source = _map(data)
        return fit_public_output(
            {
                "link": f"{PUBLIC_SITE}/articles",
                "total": source.get("total"),
                "articles": [card(item) for item in _list(source.get("records"), 12)],
            },
            f"{PUBLIC_SITE}/articles",
        )


@asynccontextmanager
async def _lifespan(client: PublicAPIClient) -> AsyncGenerator[dict[str, Any]]:
    try:
        yield {}
    finally:
        await client.close()


def create_public_server(
    settings: PublicSettings, client: PublicAPIClient | None = None
) -> FastMCP[Any]:
    api = client or PublicAPIClient(settings)
    server: FastMCP[Any] = FastMCP(
        "OpenNavo Public",
        version="0.1.0",
        instructions="Anonymous, read-only access to the public OpenNavo catalog.",
        lifespan=lambda _: _lifespan(api),
        mask_error_details=True,
        strict_input_validation=True,
    )
    register_public_tools(server, api)

    @server.custom_route("/healthz", methods=["GET"])
    async def health(_: Request) -> JSONResponse:
        return JSONResponse({"status": "ok"})

    return server


def _peer(scope: Scope, trusted: Sequence[str]) -> str:
    peer = scope.get("client", ("", 0))[0]
    address = ipaddress.ip_address(peer)
    networks = [ipaddress.ip_network(value) for value in trusted]
    if any(address in network for network in networks):
        forwarded = dict(scope.get("headers", [])).get(b"x-forwarded-for", b"").decode("ascii")
        for candidate in reversed(forwarded.split(",")):
            if not candidate.strip():
                continue
            address = ipaddress.ip_address(candidate.strip())
            if not any(address in network for network in networks):
                break
    return str(address)


class PublicPeerMiddleware:
    """Resolve X-Forwarded-For only when the direct peer is trusted."""

    def __init__(self, app: ASGIApp, trusted: Sequence[str]):
        self.app = app
        self.trusted = list(trusted)

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        try:
            address = _peer(scope, self.trusted)
        except (ValueError, UnicodeError):
            await JSONResponse({"error": "invalid peer"}, status_code=400)(scope, receive, send)
            return
        state = scope.setdefault("state", {})
        state["public_peer"] = address
        marker = PUBLIC_PEER.set(address)
        try:
            await self.app(scope, receive, send)
        finally:
            PUBLIC_PEER.reset(marker)


@dataclass
class _Bucket:
    tat: float
    expires: float


def _rate_limit_identity(key: str) -> str:
    try:
        address = ipaddress.ip_address(key)
    except ValueError:
        return key
    if isinstance(address, ipaddress.IPv6Address):
        if address.ipv4_mapped is not None:
            return str(address.ipv4_mapped)
        return str(ipaddress.ip_network(f"{address}/64", strict=False))
    return str(address)


class PublicGCRALimiter:
    """In-process GCRA matching the API's count/window/burst semantics.

    Client keys are untrusted, so the LRU has a hard identity cap. Only expired
    buckets are removed, never active quota state. When all slots are active,
    new identities hash into a fixed set of overflow buckets, each with the same
    count/window/burst quota. A process-random key prevents predictable shard
    selection. IPv4 addresses (including mapped IPv6) and IPv6 /64s are identities.
    Memory is bounded by max_entries plus PUBLIC_GCRA_OVERFLOW_SHARDS buckets.
    """

    def __init__(
        self,
        count: int = 60,
        window_seconds: float = 60.0,
        max_entries: int = PUBLIC_GCRA_MAX_ENTRIES,
    ):
        if count <= 0 or window_seconds <= 0 or not 1 <= max_entries <= PUBLIC_GCRA_MAX_ENTRIES:
            raise ValueError("invalid rate limit")
        self.count = count
        self.window_seconds = window_seconds
        self.interval = window_seconds / count
        self.max_entries = max_entries
        self._buckets: OrderedDict[str, _Bucket] = OrderedDict()
        self._overflow: list[_Bucket | None] = [None] * PUBLIC_GCRA_OVERFLOW_SHARDS
        self._lock = asyncio.Lock()

    def _overflow_index(self, key: str) -> int:
        digest = hashlib.blake2b(key.encode(), key=_PUBLIC_GCRA_HASH_KEY, digest_size=8).digest()
        return int.from_bytes(digest, "big") % PUBLIC_GCRA_OVERFLOW_SHARDS

    @property
    def description(self) -> str:
        return f"per IPv4 address or IPv6 /64 {self.count} requests / {self.window_seconds:g} s"

    async def check(
        self, key: str, cost: int = 1, now: float | None = None
    ) -> tuple[bool, int, int]:
        if cost <= 0 or cost > self.count:
            raise ValueError("invalid rate limit cost")
        key = _rate_limit_identity(key)
        current = time.monotonic() if now is None else now
        async with self._lock:
            for bucket_key, bucket in list(self._buckets.items()):
                if bucket.expires <= current:
                    del self._buckets[bucket_key]
            old = self._buckets.get(key)
            if old is not None:
                self._buckets.move_to_end(key)
            overflow_index = (
                self._overflow_index(key)
                if old is None and len(self._buckets) >= self.max_entries
                else None
            )
            if overflow_index is not None:
                old = self._overflow[overflow_index]
            tat = old.tat if old and old.expires > current else current
            allow_at = tat - (self.count - cost) * self.interval
            if current < allow_at:
                return False, max(1, int(allow_at - current + 0.999)), 0
            next_tat = max(tat, current) + cost * self.interval
            if overflow_index is not None:
                self._overflow[overflow_index] = _Bucket(next_tat, next_tat)
            else:
                self._buckets[key] = _Bucket(next_tat, next_tat)
                self._buckets.move_to_end(key)
            remaining = max(0, self.count - int((next_tat - current) / self.interval + 0.999))
            return True, 0, remaining


class PublicRateLimitMiddleware:
    """Apply a separate MCP quota before FastMCP handles a request."""

    def __init__(self, app: ASGIApp, limiter: PublicGCRALimiter, path: str = "/mcp"):
        self.app, self.limiter, self.path = app, limiter, path

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http" or scope.get("path") != self.path:
            await self.app(scope, receive, send)
            return
        state = scope.get("state", {})
        peer = str(state.get("public_peer", "unknown"))
        allowed, retry_after, remaining = await self.limiter.check(peer)
        if not allowed:
            message = (
                f"Public MCP rate limit exceeded: {self.limiter.description}; "
                f"retry after {retry_after} s"
            )
            await JSONResponse(
                {"error": "rate_limited", "message": message},
                status_code=429,
                headers={
                    "Retry-After": str(retry_after),
                    "X-RateLimit-Limit": str(self.limiter.count),
                    "X-RateLimit-Remaining": str(remaining),
                },
            )(scope, receive, send)
            return
        scope.setdefault("state", {})["public_rate_remaining"] = remaining
        await self.app(scope, receive, send)


def public_app(settings: PublicSettings | None = None) -> ASGIApp:
    settings = settings or PublicSettings.model_validate({})
    server = create_public_server(settings)
    app = server.http_app(path=settings.path, stateless_http=True, transport="streamable-http")
    limiter = PublicGCRALimiter(settings.rate_limit, settings.rate_window_seconds)
    return PublicPeerMiddleware(
        PublicRateLimitMiddleware(app, limiter, settings.path), settings.trusted_proxies
    )


def main() -> None:
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    for name in ("httpx2", "httpcore", "fastmcp", "mcp", "uvicorn.access"):
        logging.getLogger(name).setLevel(logging.CRITICAL)
    try:
        settings = PublicSettings.model_validate({})
    except ValueError:
        raise SystemExit("Invalid public MCP environment configuration") from None
    uvicorn.run(
        public_app(settings),
        host=settings.host,
        port=settings.port,
        proxy_headers=False,
        access_log=False,
        log_level="warning",
    )


if __name__ == "__main__":
    main()
