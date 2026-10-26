"""Page decisions, intent page switches and SEO overrides; zh-Hant stays read-only."""

import json
from typing import Any

import pytest
from conftest import Backend
from fastmcp import Client


async def test_page_decision_reads_one_url(backend: Backend) -> None:
    url = "https://opennavo.example/zh/apps/example-app/versions?page=2"
    async with Client(backend.server) as client:
        result = await client.call_tool("page_index_get", {"url": url})
    assert not result.is_error
    request = next(req for op, req in backend.calls if op == "getPageDecision")
    assert request.url.path == "/agent-api/page-decision"
    assert dict(request.url.params) == {"url": url}


async def test_seo_override_resolves_token_and_clears_with_null(backend: Backend) -> None:
    backend.responses["listAdminPackages"] = {"records": [{"id": 42, "token": "example-app"}]}
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_seo_set",
            {
                "package": "example-app",
                "locale": "en-US",
                "title": None,
                "description": "Fixture description",
                "expected_revision": 5,
                "idempotency_key": "seo-override-001",
            },
        )
    assert not result.is_error
    request = next(req for op, req in backend.calls if op == "setPackageSeo")
    assert (request.method, request.url.path) == ("PUT", "/agent-api/packages/42/seo")
    assert json.loads(request.content) == {
        "locale": "en-US",
        "title": None,
        "description": "Fixture description",
    }
    assert request.headers["x-expected-revision"] == "5"
    assert request.headers["idempotency-key"] == "seo-override-001"


async def test_intent_page_switches_dry_run(backend: Backend) -> None:
    items = [{"pageKind": "alternatives", "locale": "en-US", "enabled": True}]
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "intent_page_set", {"package": 42, "items": items, "dry_run": True}
        )
    assert not result.is_error
    request = next(req for op, req in backend.calls if op == "setPackageIntentPages")
    assert (request.method, request.url.path) == ("PUT", "/agent-api/packages/42/intent-pages")
    assert request.url.params["dryRun"] == "true"
    assert json.loads(request.content) == {"items": items}


@pytest.mark.parametrize(
    ("tool", "args"),
    [
        ("package_seo_set", {"package": 42, "locale": "zh-Hant", "title": "Fixture"}),
        (
            "intent_page_set",
            {
                "package": 42,
                "items": [{"pageKind": "uninstall", "locale": "zh-Hant", "enabled": True}],
            },
        ),
    ],
)
async def test_derived_locale_is_not_writable(
    backend: Backend, tool: str, args: dict[str, Any]
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(tool, args, raise_on_error=False)
    assert result.is_error
    assert result.structured_content is not None
    assert result.structured_content["error"]["code"] == "1001"
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize(
    ("tool", "operation"),
    [
        ("package_get", "getAdminPackage"),
        ("intent_pages_get", "getPackageIntentPages"),
        ("package_seo_get", "getPackageSeo"),
    ],
)
async def test_reads_pass_revision_through(backend: Backend, tool: str, operation: str) -> None:
    backend.responses[operation] = {"packageId": 42, "token": "example-app", "revision": 17}
    async with Client(backend.server) as client:
        result = await client.call_tool(tool, {"package": 42})
    assert result.structured_content is not None
    assert result.structured_content["untrusted"]["revision"] == 17


@pytest.mark.parametrize(
    ("permission", "visible", "hidden"),
    [
        ("site:index:view", {"page_index_get", "site_settings_get"}, {"intent_pages_get"}),
        (
            "catalog:package:view",
            {"intent_pages_get", "intent_pages_list", "package_seo_get", "fact_records_due"},
            {"page_index_get", "site_settings_get"},
        ),
    ],
)
async def test_site_index_reads_follow_their_permission(
    backend: Backend, permission: str, visible: set[str], hidden: set[str]
) -> None:
    backend.permissions["test-only-full"] = [permission]
    async with Client(backend.server) as client:
        names = {tool.name for tool in await client.list_tools()}
    assert visible <= names and not hidden & names
