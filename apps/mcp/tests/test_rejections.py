import json
from typing import Any

import httpx2
import pytest
from conftest import Backend
from fastmcp import Client

from opennavo_mcp.agent_client import AgentError


@pytest.mark.parametrize(
    "reason",
    [
        "rejected: evidence_valid_until_invalid",
        "rejected: field_too_long:collection.subtitle.de-DE",
        "rejected: field_invalid:items[0].evidence.validUntil",
    ],
)
async def test_backend_rejection_detail_is_forwarded(backend: Backend, reason: str) -> None:
    backend.code, backend.msg = "1001", reason
    with pytest.raises(AgentError) as raised:
        await backend.client.call("listAdminPackages", "test-only-full", "203.0.113.10", "test")
    assert str(raised.value) == f"1001: Invalid parameters ({reason})"


@pytest.mark.parametrize(
    "detail",
    [
        "rejected: field_invalid:https://example.test/private",
        "rejected: field_invalid:password=value",
        "rejected: field_invalid:field\nsecret",
        "rejected: field_invalid:" + "a" * 97,
        "rejected: " + "a" * 65,
        "field password: test-only-sensitive",
    ],
)
def test_arbitrary_backend_details_are_redacted(detail: str) -> None:
    assert str(AgentError("1001", detail)) == "1001: Invalid parameters"
    assert str(AgentError("5000", "rejected: field_invalid:field")) == (
        "5000: Backend temporarily unavailable"
    )


@pytest.mark.parametrize(
    "values,expected",
    [
        ({"id": "test-only-sensitive"}, "field id: int_type"),
        ({}, "field id: missing"),
        (
            {"id": 42, "test-only-sensitive-key": "test-only-sensitive"},
            "field <key>: extra_forbidden",
        ),
    ],
)
async def test_local_validation_does_not_echo_values(
    backend: Backend, values: dict[str, Any], expected: str
) -> None:
    with pytest.raises(AgentError) as raised:
        await backend.client.call(
            "getAdminPackage", "test-only-full", "203.0.113.10", "test", values
        )
    assert expected in str(raised.value)
    assert "test-only" not in str(raised.value)
    assert not backend.calls


async def test_nested_pydantic_field_path(backend: Backend) -> None:
    with pytest.raises(AgentError) as raised:
        await backend.client.call(
            "upsertReleaseNotes",
            "test-only-full",
            "203.0.113.10",
            "test",
            {
                "id": 42,
                "version": "1.0",
                "body": {
                    "sourceLocale": "en-US",
                    "summary": "Fixture",
                    "sections": [{"area": "Fixture", "items": [123]}],
                },
            },
        )
    assert "field sections.0.items.0: string_type" in str(raised.value)
    assert "123" not in str(raised.value)
    assert not backend.calls


async def test_invalid_path_names_field_without_echoing_value(backend: Backend) -> None:
    with pytest.raises(AgentError) as raised:
        await backend.client.call(
            "upsertReleaseNotes",
            "test-only-full",
            "203.0.113.10",
            "test",
            {
                "id": 42,
                "version": "../private-value",
                "body": {
                    "sourceLocale": "en-US",
                    "summary": "Fixture",
                    "sections": [],
                },
            },
        )
    assert "field version: path_invalid" in str(raised.value)
    assert "private-value" not in str(raised.value)
    assert not backend.calls


async def test_tool_schema_failure_names_missing_field(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text", {"summary": "test-only-sensitive"}, raise_on_error=False
        )
    assert result.is_error
    assert "field package: missing" in str(result.content)
    assert "test-only-sensitive" not in str(result.content)
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


async def test_release_notes_omits_sections_without_inserting_default(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "release_write_notes",
            {
                "package": 42,
                "version": "1.0",
                "source_locale": "en-US",
                "summary": "Fixture",
            },
        )
    assert not result.is_error
    req = next(req for op, req in backend.calls if op == "upsertReleaseNotes")
    assert json.loads(req.content) == {"sourceLocale": "en-US", "summary": "Fixture"}


async def test_network_errors_keep_redacted_message(backend: Backend) -> None:
    def fail(_: httpx2.Request) -> httpx2.Response:
        raise httpx2.ConnectError("test-only-sensitive")

    await backend.client.close()
    backend.client.http = httpx2.AsyncClient(transport=httpx2.MockTransport(fail))
    with pytest.raises(AgentError, match="^5000: Backend temporarily unavailable$"):
        await backend.client.call("listAdminPackages", "test-only-full", "203.0.113.10", "test")
