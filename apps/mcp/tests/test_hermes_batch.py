"""H8/H10 adapters use generated contracts and a local mock transport only."""

import json
from typing import Any

import pytest
from conftest import Backend
from fastmcp import Client
from test_tools import arguments

from opennavo_mcp.tools.manifest import BY_NAME


@pytest.mark.parametrize(
    "tool,args,operation",
    [
        ("seo_automation_set", {"mode": "read"}, "getSeoAutomation"),
        (
            "translations_write",
            {"mode": "read", "entity": "category", "id": 702},
            "getTranslationsSource",
        ),
    ],
)
async def test_hermes_read_modes(
    backend: Backend, tool: str, args: dict[str, Any], operation: str
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(tool, args, raise_on_error=False)
        assert not result.is_error, result.content
    assert backend.calls[-1][0] == operation


async def test_batch_has_per_language_preconditions(backend: Backend) -> None:
    args = arguments(BY_NAME["translations_write"])
    async with Client(backend.server) as client:
        result = await client.call_tool("translations_write", args, raise_on_error=False)
        assert not result.is_error, result.content
    op, request = backend.calls[-1]
    assert op == "writeTranslations"
    body = json.loads(request.content)
    assert body["translations"] == args["translations"]
    assert body["model"] == args["model"]
    assert "sourceHash" in body["translations"][0]
    assert "expectedRevision" in body["translations"][0]
    assert "idempotencyKey" in body["translations"][0]


async def test_execution_token_cannot_tune(backend: Backend) -> None:
    backend.permissions["test-only-full"] = ["seo:task:handle", "translation:review"]
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "seo_automation_set", {"mode": "read"}, raise_on_error=False
        )
        assert result.is_error
    assert not any(op == "getSeoAutomation" for op, _ in backend.calls)


async def test_geo_import_is_control_dry_run_without_paid_or_business_calls(
    backend: Backend,
) -> None:
    args = arguments(BY_NAME["seo_geo_record"])
    async with Client(backend.server) as client:
        result = await client.call_tool("seo_geo_record", args, raise_on_error=False)
    assert not result.is_error, result.content
    calls = [(op, req) for op, req in backend.calls if op != "introspectAgentToken"]
    assert len(calls) == 1
    op, req = calls[0]
    assert op == "recordSeoGeo"
    assert req.url.params["dryRun"] == "true"
    assert "x-seo-task-id" not in req.headers
    assert "x-seo-fencing" not in req.headers
    assert json.loads(req.content)["snapshot"] == args["snapshot"]


@pytest.mark.parametrize("field", ["answerText", "bodyText", "rawResponse", "html"])
async def test_geo_full_text_rejected_before_backend(backend: Backend, field: str) -> None:
    args = arguments(BY_NAME["seo_geo_record"])
    args["snapshot"]["cells"][0][field] = "fixture forbidden response"
    async with Client(backend.server) as client:
        result = await client.call_tool("seo_geo_record", args, raise_on_error=False)
    assert result.is_error
    assert not any(op == "recordSeoGeo" for op, _ in backend.calls)


async def test_geo_reader_cannot_import_and_writer_cannot_read(backend: Backend) -> None:
    backend.permissions["test-only-full"] = ["seo:insight:view"]
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "seo_geo_record", arguments(BY_NAME["seo_geo_record"]), raise_on_error=False
        )
    assert result.is_error
    assert not any(op == "recordSeoGeo" for op, _ in backend.calls)
    backend.permissions["test-only-full"] = ["seo:task:handle"]
    async with Client(backend.server) as client:
        result = await client.call_tool("seo_geo_list", {}, raise_on_error=False)
    assert result.is_error
    assert not any(op == "listSeoGeo" for op, _ in backend.calls)
