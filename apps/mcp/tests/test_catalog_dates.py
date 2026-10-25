import logging
from typing import Any, cast

import pytest
from conftest import Backend
from fastmcp import Client
from jsonschema import Draft202012Validator, FormatChecker

from opennavo_mcp.tools.manifest import TOOLS
from opennavo_mcp.tools.runtime import InputValidator, tool_schema


@pytest.mark.parametrize(
    "values,expected",
    [
        ({}, {}),
        ({"from": None, "to": None}, {}),
        ({"type": None, "has_editorial": None}, {}),
        (
            {
                "from": "2026-10-25T11:55:03Z",
                "to": "2026-10-25T13:00:03Z",
                "current": 1,
                "size": 100,
                "sort": "changed",
                "type": None,
                "has_editorial": None,
            },
            {
                "from": "2026-10-25T11:55:03Z",
                "to": "2026-10-25T13:00:03Z",
                "current": "1",
                "size": "100",
                "sort": "changed",
            },
        ),
        ({"from": "", "to": ""}, {}),
        ({"has_editorial": False, "size": 1}, {"hasEditorial": "false", "size": "1"}),
        (
            {"from": "2026-10-25T00:00:00Z", "to": "2026-10-25T01:00:00Z"},
            {"from": "2026-10-25T00:00:00Z", "to": "2026-10-25T01:00:00Z"},
        ),
    ],
)
async def test_catalog_tool_query(
    backend: Backend,
    values: dict[str, Any],
    expected: dict[str, str],
    caplog: pytest.LogCaptureFixture,
) -> None:
    caplog.set_level(logging.INFO, logger="opennavo_mcp.agent_client")
    async with Client(backend.server) as client:
        result = await client.call_tool("catalog_changes", values)
    assert not result.is_error
    request = next(r for op, r in backend.calls if op == "listAdminCatalogChanges")
    assert dict(request.url.params) == expected
    assert "Catalog request path=/catalog/changes" in caplog.text
    assert request.headers["x-request-id"] in caplog.text
    assert "test-only-" not in caplog.text


async def test_invalid_date_never_reaches_backend(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "catalog_changes", {"from": "private-invalid-date"}, raise_on_error=False
        )
    assert result.is_error
    assert "private-invalid-date" not in str(result.content)
    assert all(op != "listAdminCatalogChanges" for op, _ in backend.calls)


def test_published_date_examples_are_rfc3339() -> None:
    schema = tool_schema(next(t for t in TOOLS if t.name == "catalog_changes"))
    validator = Draft202012Validator(
        {"type": "string", "format": "date-time"}, format_checker=FormatChecker()
    )
    for field in ("from", "to"):
        for value in schema["properties"][field]["examples"]:
            cast(InputValidator, validator).validate(value)
    assert schema["properties"]["from"]["examples"][0] < schema["properties"]["to"]["examples"][0]
