import json
from typing import Any, cast

import httpx2
import pytest
from fastmcp import Client
from mcp_types import TextContent
from starlette.responses import JSONResponse
from starlette.types import Receive, Scope, Send

from opennavo_mcp.public_server import (
    MAX_OUTPUT_BYTES,
    PUBLIC_GCRA_OVERFLOW_SHARDS,
    PublicAPIClient,
    PublicGCRALimiter,
    PublicMCPError,
    PublicPeerMiddleware,
    PublicRateLimitMiddleware,
    PublicSettings,
    app_summary,
    create_public_server,
    fit_public_output,
)


def settings(base: str = "https://api.example.test/api/v1") -> PublicSettings:
    return PublicSettings.model_validate({"OPENNAVO_PUBLIC_API_BASE": base})


async def close_client(client: PublicAPIClient) -> None:
    await client.close()


async def test_public_client_uses_only_configured_public_base() -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        if request.url.path.endswith("/collections/demo/brewfile"):
            return httpx2.Response(200, text='cask "demo"\n')
        return httpx2.Response(200, json={"code": "0000", "data": {"records": []}})

    client = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    try:
        assert await client.get("/search", {"q": "code"}) == {"records": []}
        assert calls[0].url.host == "api.example.test"
        assert calls[0].url.path == "/api/v1/search"
        assert await client.get("/collections/demo/brewfile", text=True) == 'cask "demo"\n'
        assert await client.post("/developer/fetch", {"url": "https://example.com"}) == {
            "records": []
        }
        assert calls[2].method == "POST"
        assert calls[2].url.path == "/api/v1/developer/fetch"
        with pytest.raises(ValueError):
            await client.get("/admin-api/users")
        with pytest.raises(ValueError):
            await client.get("//evil.example/api/v1/search")
        with pytest.raises(ValueError):
            await client.post("/admin-api/users", {})
        with pytest.raises(ValueError):
            await client.post("/developer/submissions/abc", {})
    finally:
        await close_client(client)


async def test_public_client_forwards_only_resolved_peer() -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        return httpx2.Response(200, json={"code": "0000", "data": {"records": []}})

    client = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))

    async def endpoint(scope: Scope, receive: Receive, send: Send) -> None:
        await client.get("/categories")
        await JSONResponse({"ok": True})(scope, receive, send)

    app = PublicPeerMiddleware(endpoint, ["172.30.80.2/32"])
    transport = httpx2.ASGITransport(app=app, client=("172.30.80.2", 1234))
    try:
        async with httpx2.AsyncClient(transport=transport, base_url="http://test") as caller:
            response = await caller.get(
                "/", headers={"X-Forwarded-For": "198.51.100.9, 203.0.113.10"}
            )
        assert response.status_code == 200
        assert calls[0].headers["X-Forwarded-For"] == "203.0.113.10"
    finally:
        await close_client(client)


@pytest.mark.parametrize(
    ("status", "message"),
    [
        (404, "public_api_not_found"),
        (429, "public_api_rate_limited"),
        (500, "public_api_unavailable"),
    ],
)
async def test_public_client_maps_upstream_errors(status: int, message: str) -> None:
    def handle(_: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(status, text="internal details must not escape")

    client = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    try:
        with pytest.raises(PublicMCPError, match=message) as error:
            await client.get("/categories")
        assert "internal details" not in str(error.value)
    finally:
        await close_client(client)


async def test_public_client_maps_timeout() -> None:
    def handle(request: httpx2.Request) -> httpx2.Response:
        raise httpx2.ReadTimeout("upstream timeout", request=request)

    client = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    try:
        with pytest.raises(PublicMCPError, match="public_api_timeout"):
            await client.get("/categories")
    finally:
        await close_client(client)


async def test_public_tools_are_compact_and_mark_the_developer_write() -> None:
    def handle(request: httpx2.Request) -> httpx2.Response:
        assert request.headers.get("X-OpenNavo-Caps") is None
        data: dict[str, Any]
        if request.url.path.endswith("/packages/cask/demo"):
            data = {
                "token": "demo",
                "displayName": "Demo",
                "summary": "S" * 10_000,
                "hidden": True,
                "draft": {"secret": "must not pass"},
                "installCommand": "brew install --cask demo",
                "repoUrl": "https://github.com/example/demo",
                "license": "MIT",
                "supports": {
                    "arm64": False,
                    "x86_64": False,
                    "status": "unknown",
                    "requiresRosetta": False,
                },
            }
        else:
            data = {"records": []}
        return httpx2.Response(200, json={"code": "0000", "msg": "ok", "data": data})

    http = httpx2.AsyncClient(transport=httpx2.MockTransport(handle))
    api = PublicAPIClient(settings(), http)
    server = create_public_server(settings(), api)
    async with Client(server) as client:
        tools = await client.list_tools()
        assert len(tools) == 14
        assert {tool.name for tool in tools} == {
            "search_apps",
            "get_app",
            "get_app_releases",
            "get_app_dependencies",
            "similar_apps",
            "list_categories",
            "rankings",
            "collections",
            "articles",
            "developer_map",
            "developer_picks",
            "developer_metadata_fetch",
            "developer_submission_create",
            "developer_submission_status",
        }
        for tool in tools:
            assert tool.annotations is not None
            assert tool.annotations.destructive_hint is False
            if tool.name == "developer_submission_create":
                assert tool.annotations.read_only_hint is False
                assert tool.annotations.idempotent_hint is False
            else:
                assert tool.annotations.read_only_hint is True
                assert tool.annotations.idempotent_hint is True
        result = await client.call_tool("get_app", {"token": "demo"})
        assert result.is_error is False
        data = cast(dict[str, Any], result.data)
        assert data["link"] == "https://opennavo.com/apps/demo"
        assert data["installCommand"] == "brew install --cask demo"
        assert data["supports"] == {
            "arm64": False,
            "x86_64": False,
            "status": "unknown",
            "requiresRosetta": False,
        }
        assert data["safety"] == {"deprecated": False, "disabled": False}
        assert "hidden" not in json.dumps(data)
        assert "must not pass" not in json.dumps(data)
        assert len(json.dumps(data, ensure_ascii=False).encode()) <= MAX_OUTPUT_BYTES


async def test_developer_map_returns_country_coordinates_and_top_developers() -> None:
    places: list[dict[str, Any]] = [
        {
            "key": "US",
            "kind": "country",
            "name": "US",
            "countryCode": "US",
            "region": "americas",
            "lat": 39.8,
            "lng": -98.6,
            "developers": 4,
            "apps": 6,
            "installs30d": 120000,
            "topDevelopers": [
                {"name": "Example Studio", "apps": 2, "installs30d": 80000, "internal": "x"}
            ],
        },
        {
            "key": "JP",
            "kind": "country",
            "name": "JP",
            "countryCode": "JP",
            "region": "apac",
            "lat": 36.2,
            "lng": 138.3,
            "developers": 2,
            "apps": 2,
            "installs30d": 30000,
            "topDevelopers": [],
        },
    ]

    def handle(request: httpx2.Request) -> httpx2.Response:
        assert request.headers.get("X-OpenNavo-Caps") == "link"
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "generatedAt": "2026-11-11T10:00:00Z",
                    "totals": {
                        "developers": 12,
                        "located": 9,
                        "apps": 4210,
                        "locatedApps": 14,
                        "places": 2,
                        "countries": 2,
                    },
                    "places": places,
                    "countries": [
                        {"code": "US", "developers": 7, "apps": 9},
                        {"code": "JP", "developers": 2, "apps": 2},
                    ],
                    "recentDevelopers": [
                        {
                            "name": "Example Studio",
                            "countryCode": "US",
                            "apps": 2,
                            "updatedAt": "2026-11-11T09:00:00Z",
                        }
                    ],
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool("developer_map", {"limit": 1})
        data = cast(dict[str, Any], result.data)
        assert data["generatedAt"] == "2026-11-11T10:00:00Z"
        assert data["totals"]["places"] == 2
        assert data["places"][0]["lat"] == 39.8
        assert data["places"][0]["lng"] == -98.6
        assert data["places"][0]["topDevelopers"] == [
            {"name": "Example Studio", "apps": 2, "installs30d": 80000}
        ]
        assert data["paging"] == {"offset": 0, "limit": 1, "total": 2, "hasMore": True}
        assert data["countryPaging"] == {
            "offset": 0,
            "limit": 12,
            "total": 2,
            "hasMore": False,
        }
        assert data["recentDevelopers"][0]["countryCode"] == "US"
        assert "internal" not in json.dumps(data)


async def test_developer_map_pages_the_complete_upstream_lists() -> None:
    places: list[dict[str, Any]] = [
        {
            "key": f"C{index:02}",
            "kind": "country",
            "name": f"C{index:02}",
            "countryCode": "US",
            "region": "americas",
            "lat": 1.0,
            "lng": 2.0,
            "developers": 1,
            "apps": 1,
            "installs30d": 1,
            "topDevelopers": [],
        }
        for index in range(205)
    ]
    countries = [
        {
            "code": f"{chr(65 + index // 26)}{chr(65 + index % 26)}",
            "developers": 1,
            "apps": 1,
            "installs30d": 1,
        }
        for index in range(205)
    ]

    def handle(request: httpx2.Request) -> httpx2.Response:
        assert request.headers.get("X-OpenNavo-Caps") == "link"
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "totals": {"places": 205, "countries": 205},
                    "places": places,
                    "countries": countries,
                    "recentDevelopers": [],
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_map",
            {
                "limit": 12,
                "offset": 204,
                "country_limit": 12,
                "country_offset": 204,
            },
        )
        data = cast(dict[str, Any], result.data)
        assert [place["key"] for place in data["places"]] == ["C204"]
        assert data["paging"] == {
            "offset": 204,
            "limit": 12,
            "total": 205,
            "hasMore": False,
        }
        assert data["countryPaging"] == {
            "offset": 204,
            "limit": 12,
            "total": 205,
            "hasMore": False,
        }
        assert data["countries"][0]["code"] == "HW"


async def test_developer_picks_return_compact_packages() -> None:
    def handle(request: httpx2.Request) -> httpx2.Response:
        assert request.headers.get("X-OpenNavo-Caps") == "link"
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": [
                    {
                        "package": {
                            "kind": "cask",
                            "token": "ghostty",
                            "displayName": "Ghostty",
                            "hidden": True,
                            "draft": {"secret": "must not pass"},
                        },
                        "quote": "A calm terminal that stays out of the way.",
                        "publishedAt": "2026-11-11T09:30:00Z",
                    }
                ],
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool("developer_picks", {})
        data = cast(dict[str, Any], result.data)
        pick = data["picks"][0]
        assert pick["package"]["link"] == "https://opennavo.com/apps/ghostty"
        assert pick["package"]["displayName"] == "Ghostty"
        assert "hidden" not in json.dumps(pick)
        assert "must not pass" not in json.dumps(pick)
        assert pick["quote"].startswith("A calm terminal")


async def test_developer_metadata_fetch_posts_only_public_fields() -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "source": "github",
                    "name": "Ghostty",
                    "summary": "Fast terminal.",
                    "description": "A terminal emulator.",
                    "developer": "Ghostty",
                    "homepage": "https://ghostty.org",
                    "repoUrl": "https://github.com/ghostty-org/ghostty",
                    "downloadUrl": "https://ghostty.org/download",
                    "iconUrl": "https://ghostty.org/icon.png",
                    "version": "1.2.0",
                    "minMacos": "13",
                    "category": "developer-tools",
                    "license": "MIT",
                    "warnings": ["Using the repository homepage."],
                    "internal": "must not pass",
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_metadata_fetch", {"url": "https://github.com/ghostty-org/ghostty"}
        )
        data = cast(dict[str, Any], result.data)
        assert calls[0].method == "POST"
        assert calls[0].url.path == "/api/v1/developer/fetch"
        assert calls[0].headers.get("X-OpenNavo-Caps") == "link"
        assert json.loads(calls[0].content) == {"url": "https://github.com/ghostty-org/ghostty"}
        assert data["name"] == "Ghostty"
        assert data["warnings"] == ["Using the repository homepage."]
        assert "internal" not in json.dumps(data)


async def test_developer_submission_create_posts_the_receipt_request() -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "id": 123,
                    "reference": "DS-000123",
                    "token": "test-receipt-token",
                    "statusUrl": "https://opennavo.com/developer-map?tab=submit&submission=test-receipt-token",
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_submission_create",
            {
                "kind": "new",
                "contact_email": "dev@example.com",
                "source_kind": "github",
                "source_url": "https://github.com/ghostty-org/ghostty",
                "payload": {
                    "name": "Ghostty",
                    "summary": "Fast terminal.",
                    "countryCode": "US",
                },
            },
        )
        data = cast(dict[str, Any], result.data)
        assert calls[0].method == "POST"
        assert calls[0].url.path == "/api/v1/developer/submissions"
        assert calls[0].headers.get("X-OpenNavo-Caps") == "link"
        body = json.loads(calls[0].content)
        assert body["kind"] == "new"
        assert body["contactEmail"] == "dev@example.com"
        assert body["sourceUrl"] == "https://github.com/ghostty-org/ghostty"
        assert body["payload"]["countryCode"] == "US"
        assert "city" not in body["payload"]
        assert "website" not in body
        assert data["reference"] == "DS-000123"
        assert data["token"] == "test-receipt-token"  # noqa: S105


async def test_developer_submission_location_accepts_a_two_letter_country() -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {"reference": "DS-000123", "token": "test-receipt-token"},
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_submission_create",
            {
                "kind": "location",
                "contact_email": "dev@example.com",
                "package_kind": "formula",
                "package_token": "ghostty",
                "payload": {"countryCode": "us"},
            },
        )
        assert result.is_error is False
        body = json.loads(calls[0].content)
        assert body["packageKind"] == "formula"
        assert body["packageToken"] == "ghostty"
        assert body["payload"]["countryCode"] == "us"


async def test_developer_submission_status_selects_events_and_masks_unknown_fields() -> None:
    def handle(request: httpx2.Request) -> httpx2.Response:
        assert request.headers.get("X-OpenNavo-Caps") == "link"
        assert request.url.path.endswith("/api/v1/developer/submissions/test-receipt-token")
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "reference": "DS-000123",
                    "kind": "new",
                    "status": "published",
                    "createdAt": "2026-11-10T10:00:00Z",
                    "updatedAt": "2026-11-11T10:00:00Z",
                    "decidedAt": "2026-11-11T10:00:00Z",
                    "decisionNote": "Approved.",
                    "package": {"kind": "link", "token": "ghostty", "name": "Ghostty"},
                    "events": [
                        {
                            "status": "published",
                            "note": "Approved.",
                            "actor": "admin",
                            "createdAt": "2026-11-11T10:00:00Z",
                            "internal": "must not pass",
                        }
                    ],
                    "internal": "must not pass",
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_submission_status",
            {"token": "test-receipt-token"},
        )
        data = cast(dict[str, Any], result.data)
        assert data["status"] == "published"
        assert data["package"] == {"kind": "link", "token": "ghostty", "name": "Ghostty"}
        assert data["events"][0]["note"] == "Approved."
        assert "internal" not in json.dumps(data)


@pytest.mark.parametrize(
    ("arguments", "message"),
    [
        (
            {"kind": "new", "contact_email": "not-an-email", "source_url": "https://example.com"},
            "invalid_contact_email",
        ),
        (
            {"kind": "new", "contact_email": "dev@example.com"},
            "source_url_required_for_new_submission",
        ),
        (
            {"kind": "claim", "contact_email": "dev@example.com"},
            "package_kind_required",
        ),
        (
            {
                "kind": "claim",
                "contact_email": "dev@example.com",
                "package_kind": "cask",
            },
            "package_token_required",
        ),
        (
            {
                "kind": "location",
                "contact_email": "dev@example.com",
                "package_kind": "link",
                "package_token": "demo",
                "payload": {},
            },
            "invalid_country_code",
        ),
        (
            {
                "kind": "location",
                "contact_email": "dev@example.com",
                "package_kind": "link",
                "package_token": "demo",
                "payload": {"countryCode": "USA"},
            },
            "invalid_country_code",
        ),
    ],
)
async def test_developer_submission_create_validates_required_fields(
    arguments: dict[str, Any], message: str
) -> None:
    calls: list[httpx2.Request] = []

    def handle(request: httpx2.Request) -> httpx2.Response:
        calls.append(request)
        return httpx2.Response(200, json={"code": "0000", "data": {}})

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(
            "developer_submission_create", arguments, raise_on_error=False
        )
        assert result.is_error is True
        first = result.content[0] if result.content else None
        assert isinstance(first, TextContent)
        assert message in first.text
        assert calls == []


async def test_get_app_returns_disabled_package_with_lifecycle_notices() -> None:
    notice = {
        "date": "2026-10-01",
        "reason": "no longer maintained",
        "replacement": {"kind": "cask", "token": "successor"},
    }

    def handle(_: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "token": "demo",
                    "disabled": True,
                    "deprecated": True,
                    "deprecation": {**notice, "internal": "must not pass"},
                    "disable": notice,
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool("get_app", {"token": "demo"})
        assert result.is_error is False
        data = cast(dict[str, Any], result.data)
        assert data["link"] == "https://opennavo.com/apps/demo"
        assert data["safety"] == {
            "deprecated": True,
            "disabled": True,
            "deprecation": notice,
            "disable": notice,
        }


@pytest.mark.parametrize(
    ("tool", "arguments"),
    [
        ("search_apps", {"q": "demo"}),
        ("similar_apps", {"token": "demo"}),
        ("rankings", {}),
        ("collections", {"slug": "demo"}),
    ],
)
async def test_package_lists_preserve_packages_and_explicit_status(
    tool: str, arguments: dict[str, Any]
) -> None:
    packages = [
        {"token": "deprecated-app", "deprecated": True},
        {"token": "disabled-app", "disabled": True},
        {"token": "default-app"},
        {"token": "hidden-app", "hidden": True, "deprecated": False, "disabled": False},
    ]

    def handle(request: httpx2.Request) -> httpx2.Response:
        data: Any
        if request.url.path.endswith("/search"):
            data = {"records": [{"package": package} for package in packages]}
        elif request.url.path.endswith("/related"):
            data = packages
        elif request.url.path.endswith("/rankings"):
            data = {"records": [{"package": package} for package in packages]}
        elif request.url.path.endswith("/collections/demo"):
            data = {"items": [{"package": package} for package in packages]}
        else:
            data = {"records": [{"slug": "demo", "previewItems": packages}]}
        return httpx2.Response(200, json={"code": "0000", "data": data})

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool(tool, arguments)
        assert result.is_error is False
        data = cast(dict[str, Any], result.data)
        if tool == "rankings":
            apps = [row["app"] for row in data["rankings"]]
        else:
            apps = data["apps"]
        assert [app["token"] for app in apps] == [package["token"] for package in packages]
        assert [(app["deprecated"], app["disabled"]) for app in apps] == [
            (True, False),
            (False, True),
            (False, False),
            (False, False),
        ]
        assert all(type(app[key]) is bool for app in apps for key in ("deprecated", "disabled"))


async def test_collection_previews_only_include_upstream_lifecycle_flags() -> None:
    packages = [
        {"token": "unknown-app"},
        {"token": "deprecated-app", "deprecated": True},
        {"token": "disabled-app", "disabled": True},
        {"token": "active-app", "deprecated": False, "disabled": False},
        {"token": "partially-known-app", "disabled": False},
    ]

    def handle(_: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {"records": [{"slug": "demo", "previewItems": packages}]},
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool("collections", {})
        assert result.is_error is False
        previews = cast(dict[str, Any], result.data)["collections"][0]["apps"]
        assert [preview["token"] for preview in previews] == [item["token"] for item in packages]
        assert [
            {key: app[key] for key in ("deprecated", "disabled") if key in app} for app in previews
        ] == [
            {},
            {"deprecated": True},
            {"disabled": True},
            {"deprecated": False, "disabled": False},
            {"disabled": False},
        ]


async def test_dependency_packages_only_include_status_when_present_recursively() -> None:
    def handle(_: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(
            200,
            json={
                "code": "0000",
                "data": {
                    "dependencies": [
                        {
                            "token": "disabled-app",
                            "disabled": True,
                            "children": [
                                {"token": "deprecated-app", "deprecated": True},
                                {"token": "explicit-app", "deprecated": False, "disabled": False},
                                {"token": "unknown-app"},
                            ],
                        }
                    ],
                    "dependents": {"count": 1, "top": [{"token": "default-app"}]},
                },
            },
        )

    api = PublicAPIClient(settings(), httpx2.AsyncClient(transport=httpx2.MockTransport(handle)))
    async with Client(create_public_server(settings(), api)) as client:
        result = await client.call_tool("get_app_dependencies", {"token": "demo"})
        assert result.is_error is False
        data = cast(dict[str, Any], result.data)
        dependency = data["dependencies"][0]
        child = dependency["children"][0]
        dependent = data["dependents"]["top"][0]
        assert dependency["disabled"] is True and "deprecated" not in dependency
        assert child["deprecated"] is True and "disabled" not in child
        explicit = dependency["children"][1]
        assert explicit["deprecated"] is False and explicit["disabled"] is False
        for node in (dependency["children"][2], dependent):
            assert "deprecated" not in node and "disabled" not in node


def test_selected_outputs_have_links_and_size_caps() -> None:
    summary = app_summary({"kind": "cask", "token": "demo", "hidden": True})
    assert summary["link"] == "https://opennavo.com/apps/demo"
    assert "hidden" not in summary
    assert summary["deprecated"] is False
    assert summary["disabled"] is False
    result = fit_public_output(
        {"link": "https://opennavo.com/apps/demo", "items": ["x" * 10_000] * 100},
        "https://opennavo.com/apps/demo",
    )
    assert len(json.dumps(result, ensure_ascii=False).encode()) <= MAX_OUTPUT_BYTES
    assert result["link"] == "https://opennavo.com/apps/demo"


async def test_public_gcra_ipv4_addresses_have_independent_quotas() -> None:
    limiter = PublicGCRALimiter(2, 60)
    assert (await limiter.check("203.0.113.1", now=0.0))[0] is True
    assert (await limiter.check("203.0.113.1", now=0.0))[0] is True
    allowed, retry_after, _ = await limiter.check("203.0.113.1", now=0.0)
    assert allowed is False and retry_after > 0
    assert (await limiter.check("203.0.113.2", now=0.0))[0] is True


@pytest.mark.parametrize("overflow", [False, True])
async def test_public_gcra_ipv6_addresses_in_same_64_share_quota(overflow: bool) -> None:
    limiter = PublicGCRALimiter(2, 60, max_entries=1)
    if overflow:
        await limiter.check("203.0.113.1", now=0.0)
    assert (await limiter.check("2001:db8:abcd:1234::1", now=0.0))[0] is True
    assert (await limiter.check("2001:db8:abcd:1234:ffff::abcd", now=0.0))[0] is True
    assert (await limiter.check("2001:0db8:abcd:1234::2", now=0.0)) == (False, 30, 0)


async def test_public_gcra_different_ipv6_64s_have_independent_quotas() -> None:
    limiter = PublicGCRALimiter(1, 60)
    assert (await limiter.check("2001:db8:abcd:1::1", now=0.0))[0] is True
    assert (await limiter.check("2001:db8:abcd:1::2", now=0.0))[0] is False
    assert (await limiter.check("2001:db8:abcd:2::1", now=0.0))[0] is True
    assert (await limiter.check("2001:db8:abcd:2::2", now=0.0))[0] is False


@pytest.mark.parametrize("overflow", [False, True])
@pytest.mark.parametrize(
    ("first", "second"),
    [
        ("203.0.113.1", "::ffff:203.0.113.1"),
        ("::ffff:cb00:7101", "203.0.113.1"),
    ],
)
async def test_public_gcra_ipv4_mapped_ipv6_shares_ipv4_quota(
    first: str, second: str, overflow: bool
) -> None:
    limiter = PublicGCRALimiter(1, 60, max_entries=1)
    if overflow:
        await limiter.check("198.51.100.1", now=0.0)
    assert (await limiter.check(first, now=0.0))[0] is True
    assert (await limiter.check(second, now=0.0)) == (False, 60, 0)


async def test_public_gcra_caps_buckets_and_isolates_flooded_overflow_shards(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr("opennavo_mcp.public_server._PUBLIC_GCRA_HASH_KEY", b"overflow-test-key")
    limiter = PublicGCRALimiter(1, 60, max_entries=3)
    for key in ("a", "b", "c"):
        assert (await limiter.check(key, now=0.0))[0] is True
    assert list(limiter._buckets) == ["a", "b", "c"]  # pyright: ignore[reportPrivateUsage]

    # Denied checks update recency, but all active buckets keep their quota.
    allowed, _, _ = await limiter.check("a", now=0.0)
    assert allowed is False
    assert list(limiter._buckets) == ["b", "c", "a"]  # pyright: ignore[reportPrivateUsage]
    flooder = "203.0.113.1"
    shard = limiter._overflow_index(flooder)  # pyright: ignore[reportPrivateUsage]
    peers = [f"203.0.113.{index}" for index in range(2, 256)]
    same_shard = next(
        peer
        for peer in peers
        if limiter._overflow_index(peer) == shard  # pyright: ignore[reportPrivateUsage]
    )
    other_shard = next(
        peer
        for peer in peers
        if limiter._overflow_index(peer) != shard  # pyright: ignore[reportPrivateUsage]
    )
    assert (await limiter.check(flooder, now=1.0))[0] is True
    for _ in range(100):
        assert (await limiter.check(flooder, now=1.0)) == (False, 60, 0)
        assert (await limiter.check(same_shard, now=1.0)) == (False, 60, 0)
    assert (await limiter.check(other_shard, now=1.0))[0] is True
    assert (await limiter.check(other_shard, now=1.0)) == (False, 60, 0)
    assert list(limiter._buckets) == ["b", "c", "a"]  # pyright: ignore[reportPrivateUsage]
    assert len(limiter._buckets) == 3  # pyright: ignore[reportPrivateUsage]
    assert len(limiter._overflow) == PUBLIC_GCRA_OVERFLOW_SHARDS == 64  # pyright: ignore[reportPrivateUsage]
    for key in ("a", "b", "c"):
        assert (await limiter.check(key, now=1.0))[0] is False
    assert (await limiter.check(same_shard, now=61.0))[0] is True


async def test_public_gcra_preserves_victim_quota_after_ten_thousand_new_identities() -> None:
    limiter = PublicGCRALimiter(2, 60)
    assert (await limiter.check("victim", now=0.0)) == (True, 0, 1)
    victim = limiter._buckets["victim"]  # pyright: ignore[reportPrivateUsage]
    for index in range(10_000):
        await limiter.check(f"new-{index}", now=0.0)
    assert len(limiter._buckets) == 10_000  # pyright: ignore[reportPrivateUsage]
    assert limiter._buckets["victim"] is victim  # pyright: ignore[reportPrivateUsage]
    assert (await limiter.check("victim", now=0.0)) == (True, 0, 0)
    assert (await limiter.check("victim", now=0.0)) == (False, 30, 0)


async def test_public_gcra_evicts_expired_bucket_before_active_lru() -> None:
    limiter = PublicGCRALimiter(1, 60, max_entries=2)
    assert (await limiter.check("expired", now=0.0))[0] is True
    assert (await limiter.check("active", now=30.0))[0] is True
    assert (await limiter.check("expired", now=30.0))[0] is False
    assert list(limiter._buckets) == ["active", "expired"]  # pyright: ignore[reportPrivateUsage]
    assert (await limiter.check("fresh", now=60.0))[0] is True
    assert list(limiter._buckets) == ["active", "fresh"]  # pyright: ignore[reportPrivateUsage]
    assert (await limiter.check("active", now=60.0)) == (False, 30, 0)


async def test_public_gcra_purges_expired_buckets_on_access() -> None:
    limiter = PublicGCRALimiter(1, 60, max_entries=3)
    assert (await limiter.check("expired-a", now=0.0))[0] is True
    assert (await limiter.check("expired-b", now=0.0))[0] is True
    assert (await limiter.check("fresh", now=60.0))[0] is True
    assert list(limiter._buckets) == ["fresh"]  # pyright: ignore[reportPrivateUsage]


@pytest.mark.parametrize(
    ("trusted", "forwarded", "expected"),
    [
        ([], "198.51.100.9", "203.0.113.10"),
        (["203.0.113.0/24"], "198.51.100.9", "198.51.100.9"),
    ],
)
async def test_public_peer_only_trusts_forwarded_for(
    trusted: list[str], forwarded: str, expected: str
) -> None:
    async def endpoint(scope: Scope, receive: Receive, send: Send) -> None:
        await JSONResponse({"peer": scope["state"]["public_peer"]})(scope, receive, send)

    app = PublicPeerMiddleware(endpoint, trusted)
    transport = httpx2.ASGITransport(app=app, client=("203.0.113.10", 1234))
    async with httpx2.AsyncClient(transport=transport, base_url="http://test") as client:
        response = await client.get("/", headers={"X-Forwarded-For": forwarded})
    assert response.json() == {"peer": expected}


async def test_public_rate_middleware_returns_clean_429() -> None:
    async def endpoint(scope: Scope, receive: Receive, send: Send) -> None:
        await JSONResponse({"ok": True})(scope, receive, send)

    limiter = PublicGCRALimiter(60, 60)
    app = PublicRateLimitMiddleware(endpoint, limiter)
    transport = httpx2.ASGITransport(app=app, client=("203.0.113.10", 1234))
    async with httpx2.AsyncClient(transport=transport, base_url="http://test") as client:
        responses = [await client.post("/mcp", json={}) for _ in range(61)]
    assert all(response.status_code == 200 for response in responses[:-1])
    limited = responses[-1]
    assert limited.status_code == 429
    assert limited.headers["Retry-After"] == "1"
    assert limited.json() == {
        "error": "rate_limited",
        "message": (
            "Public MCP rate limit exceeded: per IPv4 address or IPv6 /64 "
            "60 requests / 60 s; retry after 1 s"
        ),
    }


async def test_public_rate_quotas_are_per_resolved_peer_and_ignore_forged_xff() -> None:
    async def endpoint(scope: Scope, receive: Receive, send: Send) -> None:
        await JSONResponse({"ok": True})(scope, receive, send)

    limiter = PublicGCRALimiter(2, 60)
    app = PublicPeerMiddleware(PublicRateLimitMiddleware(endpoint, limiter), [])
    client_a_transport = httpx2.ASGITransport(app=app, client=("203.0.113.10", 1234))
    client_b_transport = httpx2.ASGITransport(app=app, client=("203.0.113.11", 1234))
    async with (
        httpx2.AsyncClient(transport=client_a_transport, base_url="http://test") as client_a,
        httpx2.AsyncClient(transport=client_b_transport, base_url="http://test") as client_b,
    ):
        headers = {"X-Forwarded-For": "198.51.100.11"}
        assert (await client_a.post("/mcp", json={}, headers=headers)).status_code == 200
        assert (await client_a.post("/mcp", json={}, headers=headers)).status_code == 200
        assert (
            await client_a.post("/mcp", json={}, headers={"X-Forwarded-For": "198.51.100.12"})
        ).status_code == 429
        assert (await client_b.post("/mcp", json={}, headers=headers)).status_code == 200
