"""Write-safety metadata crosses the MCP/HTTP boundary only as headers; guides follow OpenSEO."""

import json
import re

import pytest
from conftest import Backend, execution_args
from fastmcp import Client

from opennavo_mcp.contracts import OPERATIONS
from opennavo_mcp.resources import GUIDES
from opennavo_mcp.tools.manifest import BY_NAME, TOOLS
from opennavo_mcp.tools.runtime import LEGACY, tool_schema


async def test_catalog_write_sends_only_write_safety_headers(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text",
            {
                "package": 42,
                "source_locale": "en-US",
                "summary": "Mac utility",
                "idempotency_key": "catalog-text-001",
                "expected_revision": 7,
                "dry_run": True,
            },
            raise_on_error=False,
        )
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "updatePackageText")
    assert req.headers["idempotency-key"] == "catalog-text-001"
    assert req.headers["x-expected-revision"] == "7"
    assert not [name for name in req.headers if name.startswith("x-seo-")]
    assert json.loads(req.content) == {"sourceLocale": "en-US", "summary": "Mac utility"}
    assert req.url.params["dryRun"] == "true"


@pytest.mark.parametrize("missing", ["idempotency_key", "expected_revision"])
async def test_write_safety_metadata_is_optional(backend: Backend, missing: str) -> None:
    args = execution_args(
        "package_set_text", {"package": 42, "source_locale": "en-US", "summary": "Mac utility"}
    )
    del args[missing]
    async with Client(backend.server) as client:
        result = await client.call_tool("package_set_text", args, raise_on_error=False)
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "updatePackageText")
    assert ("x-expected-revision" in req.headers) == (missing != "expected_revision")
    assert ("idempotency-key" in req.headers) == (missing != "idempotency_key")


@pytest.mark.parametrize("value", [None, ""])
async def test_null_write_safety_values_send_no_header(backend: Backend, value: object) -> None:
    args = {"package": 42, "source_locale": "en-US", "summary": "Mac utility"}
    revision = None if value == "" else value  # expected_revision takes integers or null
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text",
            args | {"expected_revision": revision, "idempotency_key": value},
            raise_on_error=False,
        )
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "updatePackageText")
    assert "x-expected-revision" not in req.headers and "idempotency-key" not in req.headers


@pytest.mark.parametrize("legacy", [("task_id", 7), ("fencing", 3), ("evidence_ids", [11])])
async def test_legacy_execution_metadata_is_rejected(
    backend: Backend, legacy: tuple[str, object]
) -> None:
    args = {"package": 42, "source_locale": "en-US", "summary": "Mac utility", legacy[0]: legacy[1]}
    async with Client(backend.server) as client:
        result = await client.call_tool("package_set_text", args, raise_on_error=False)
    assert result.is_error
    assert result.structured_content is not None
    assert result.structured_content["error"]["code"] == "1001"
    assert all(name == "introspectAgentToken" for name, _ in backend.calls)


def test_write_schemas_expose_only_write_safety_headers() -> None:
    for spec in TOOLS:
        properties = tool_schema(spec)["properties"]
        assert not set(LEGACY) & set(properties), spec.name
        if spec.write and any(OPERATIONS[op]["parameters"].get("header") for op in spec.operations):
            assert {"idempotency_key", "expected_revision", "dry_run"} <= set(properties)


async def test_routines_follow_openseo_and_business_tools(backend: Backend) -> None:
    async with Client(backend.server) as client:
        seo_content = await client.read_resource("opennavo://guide/seo-routine")
        daily_content = await client.read_resource("opennavo://guide/daily-routine")
    seo = json.loads(seo_content[0].text)  # type: ignore[union-attr]
    daily = json.loads(daily_content[0].text)  # type: ignore[union-attr]
    assert seo["version"] == "seo-routine-v3"
    assert daily["version"] == "daily-routine-v3"
    for text in (
        "OpenSEO",
        "inspect_urls",
        "page_index_get",
        "intent_page_set",
        "package_seo_set",
        "translations_write",
        "idempotency_key",
        "expected_revision",
        "revision_conflict",
        "Template and technical problems are not fixed through MCP",
        "one owning agent",
    ):
        assert text in seo["guide"], text
    for text in ("after_seq", "opennavo://guide/seo-routine", "another agent owns the rest"):
        assert text in daily["guide"], text
    for guide in (seo["guide"], daily["guide"]):
        assert "geo-routine" not in guide
        assert not set(re.findall(r"\bseo_[a-z_]+", guide)) - {"seo_title", "seo_description"}
        assert not any(name in guide for name in LEGACY)
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize(
    ("slug", "version"),
    [
        ("content-style", "content-style-v2"),
        ("release-notes", "release-notes-v2"),
        ("article-writing", "article-writing-v2"),
        ("app-intro", "app-intro-v1"),
    ],
)
async def test_guide_resource_versions(backend: Backend, slug: str, version: str) -> None:
    async with Client(backend.server) as client:
        result = await client.read_resource(f"opennavo://guide/{slug}")
    value = json.loads(result[0].text)  # type: ignore[union-attr]
    assert value["version"] == version
    assert value["guide"] == GUIDES[slug]


def test_guides_name_only_existing_tools() -> None:
    # Snake-case words in guides name tools, apart from parameters, rejection reasons and
    # OpenSEO's inspect_urls.
    others = {
        "after_seq",
        "batch_too_large",
        "body_markdown",
        "dry_run",
        "expected_revision",
        "idempotency_key",
        "idempotency_key_reused",
        "inspect_urls",
        "revision_conflict",
        "seo_description",
        "seo_title",
        "source_locale",
    }
    for slug, guide in GUIDES.items():
        for name in set(re.findall(r"\b[a-z]+(?:_[a-z]+)+\b", guide)) - others:
            assert name in BY_NAME, f"{slug} names unknown tool {name}"
