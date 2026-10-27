"""Regression coverage for query scope, write guards and discovery/runtime agreement."""

import json
from typing import Any, cast

import pytest
from conftest import DOCUMENT, Backend, example, resolve
from fastmcp import Client
from jsonschema import Draft202012Validator, ValidationError
from test_tools import arguments

from opennavo_mcp.contracts import OPERATIONS
from opennavo_mcp.resources import GUIDES
from opennavo_mcp.tools.manifest import BY_NAME, TOOLS, ToolSpec
from opennavo_mcp.tools.runtime import InputValidator, tool_schema
from opennavo_mcp.tools.updates import state_hash


def revision_reads(backend: Backend) -> None:
    for operation in ("getCategoryTree", "getAdminCollection", "getFeature"):
        spec = OPERATIONS[operation]
        response = resolve(DOCUMENT["paths"][spec["path"]]["get"]["responses"]["200"])
        data = example(response["content"]["application/json"]["schema"])["data"]
        records = cast(list[dict[str, Any]], data if isinstance(data, list) else [data])
        for record in records:
            record["revision"] = 7
        backend.responses[operation] = data


@pytest.mark.parametrize(
    ("spec", "parameter"),
    [
        (spec, parameter)
        for spec in TOOLS
        for parameter in ("idempotency_key", "expected_revision")
        if parameter in tool_schema(spec)["properties"]
    ],
    ids=lambda value: value.name if isinstance(value, ToolSpec) else value,
)
async def test_null_write_headers_match_discovery(
    backend: Backend, spec: ToolSpec, parameter: str
) -> None:
    revision_reads(backend)
    args = arguments(spec) | {parameter: None}
    cast(InputValidator, Draft202012Validator(tool_schema(spec))).validate(args)
    async with Client(backend.server) as client:
        result = await client.call_tool(spec.name, args, raise_on_error=False)
    assert not result.is_error, result.content
    request = next(req for operation, req in backend.calls if operation == spec.operations[0])
    if spec.name.startswith("article_"):
        assert "expectedRevision" not in json.loads(request.content)
        assert "idempotencyKey" not in json.loads(request.content)
    if parameter == "idempotency_key":
        assert "idempotency-key" not in request.headers
    else:
        assert "x-expected-revision" not in request.headers


async def test_listing_nulls_preserve_active_app_defaults(backend: Backend) -> None:
    async with Client(backend.server) as client:
        for args in (
            {},
            {key: None for key in ("gaps", "sort", "hidden", "disabled", "is_font", "removed")},
        ):
            result = await client.call_tool("listing_gaps", args)
            assert not result.is_error
            query = backend.calls[-1][1].url.params
            assert query["sort"] == "popular" and query["gaps"]
            for key in ("hidden", "disabled", "isFont", "removed"):
                assert query[key] == "false"
        await client.call_tool("listing_gaps", {"removed": True, "disabled": True, "is_font": True})
    query = backend.calls[-1][1].url.params
    assert all(query[key] == "true" for key in ("removed", "disabled", "isFont"))


@pytest.mark.parametrize(
    ("name", "args"),
    [
        ("releases_list", {"source": "editorial"}),
        ("releases_list", {"view": "versions", "hidden": False}),
        ("releases_list", {"id": 1, "q": "ignored"}),
        ("releases_list", {"view": "releases", "sort": "popular"}),
        ("logs_search", {"kind": "calls", "locale": "en-US"}),
        ("logs_search", {"id": 1, "current": 2}),
        ("jobs_runs", {"run_id": 1, "status": "failed"}),
        ("jobs_runs", {"id": 1, "run_id": 2}),
        ("features_list", {"id": 1, "placement": "home_hero"}),
        ("feature_upsert", arguments(BY_NAME["feature_upsert"]) | {"expected_state": "unused"}),
        ("translations_write", {"mode": "read", "entity": "category", "id": 1, "dry_run": True}),
    ],
)
async def test_inactive_mode_parameters_are_rejected(
    backend: Backend, name: str, args: dict[str, Any]
) -> None:
    with pytest.raises(ValidationError):
        cast(InputValidator, Draft202012Validator(tool_schema(BY_NAME[name]))).validate(args)
    async with Client(backend.server) as client:
        result = await client.call_tool(name, args, raise_on_error=False)
    assert result.is_error
    assert all(operation == "introspectAgentToken" for operation, _ in backend.calls)


async def test_article_key_uses_header_limit_and_never_legacy_body(backend: Backend) -> None:
    args = {
        "slug": "scoped-article",
        "source_locale": "en-US",
        "type": "news",
        "title": "Scoped article",
        "idempotency_key": "k" * 200,
    }
    async with Client(backend.server) as client:
        result = await client.call_tool("article_create", args)
    assert not result.is_error
    request = backend.calls[-1][1]
    assert request.headers["Idempotency-Key"] == args["idempotency_key"]
    assert "idempotencyKey" not in json.loads(request.content)


async def test_partial_update_forwards_state_guard_without_reading(backend: Backend) -> None:
    revision_reads(backend)
    before = backend.responses["getAdminCollection"]

    def concurrent_write() -> None:
        # The server checks the state digest in its write transaction, after
        # checking for a previously committed idempotent result.
        request = backend.calls[-1][1]
        assert "X-Expected-Revision" not in request.headers
        assert json.loads(request.content)["expectedState"] == state_hash(before)
        backend.code, backend.msg = "1003", "rejected: revision_conflict:8"

    backend.responses["updateCollection"] = concurrent_write
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "collection_update",
            {"id": before["id"], "sort": 3, "expected_state": state_hash(before)},
            raise_on_error=False,
        )
    assert result.is_error
    assert result.structured_content is not None
    assert result.structured_content["error"]["currentRevision"] == 8
    assert not any(operation == "getAdminCollection" for operation, _ in backend.calls)


def test_guides_match_short_field_limits_and_work_scopes() -> None:
    schemas = DOCUMENT["components"]["schemas"]
    for model, field, phrase in [
        ("CategoryI18n", "name", "category name"),
        ("CollectionI18n", "title", "collection title"),
        ("ScreenshotI18n", "caption", "screenshot caption"),
        ("CollectionItemI18n", "note", "recommendation"),
    ]:
        limit = schemas[model]["properties"][field]["maxLength"]
        assert f"{phrase} {limit}" in GUIDES["content-style"]
    assert "Unicode characters" in GUIDES["content-style"]
    assert "does not report editorial content edits" in GUIDES["seo-routine"]
    assert "removed=false" in GUIDES["daily-routine"]
    assert "never add their" in GUIDES["daily-routine"]
    assert "base version of a listed" in GUIDES["release-notes"]
