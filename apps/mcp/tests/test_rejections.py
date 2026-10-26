import json
from typing import Any

import httpx2
import pytest
from conftest import Backend
from fastmcp import Client

from opennavo_mcp.agent_client import MESSAGES, AgentError


@pytest.mark.parametrize(
    "reason",
    [
        "rejected: evidence_valid_until_invalid",
        "rejected: field_too_long:collection.subtitle.de-DE",
        "rejected: field_invalid:items[0].evidence.validUntil",
        "rejected: derived_locale_read_only:zh-Hant:zh-CN",
    ],
)
async def test_backend_rejection_detail_is_forwarded(backend: Backend, reason: str) -> None:
    backend.code, backend.msg = "1001", reason
    with pytest.raises(AgentError) as raised:
        await backend.client.call("listAdminPackages", "test-only-full", "203.0.113.10", "test")
    assert str(raised.value) == f"1001: Invalid parameters ({reason})"


@pytest.mark.parametrize(
    ("code", "msg", "revision"),
    [
        ("1003", "rejected: revision_conflict:17", 17),
        ("1003", "rejected: idempotency_key_reused", None),
        ("1001", "rejected: batch_too_large:100", None),
        # Tap reasons as BE-B emits them: tap paths, cask tokens, line numbers, HTTP statuses.
        ("1001", "rejected: tap_cask_not_found:example/tap/app", None),
        ("1001", "rejected: tap_cask_unsupported:line_12", None),
        ("1003", "rejected: tap_token_taken:font-example@2+beta", None),
        ("1301", "rejected: tap_github_http_error:502", None),
        ("1001", "rejected: seo_text_invalid:title", None),
        ("1001", "rejected: derived_locale_read_only:zh-Hant:zh-CN", None),
    ],
)
async def test_write_rejections_are_structured(
    backend: Backend, code: str, msg: str, revision: int | None
) -> None:
    async with Client(backend.server) as client:
        await client.list_tools()
        backend.code, backend.msg = code, msg
        result = await client.call_tool(
            "package_set_text",
            {"package": 42, "source_locale": "en-US", "summary": "Fixture", "expected_revision": 3},
            raise_on_error=False,
        )
    assert result.is_error
    text = f"{code}: {MESSAGES[code]} ({msg})"
    assert text in str(result.content)
    reason, _, detail = msg.removeprefix("rejected: ").partition(":")
    error: dict[str, object] = {
        "code": code,
        "message": text,
        "reason": reason,
        "detail": detail or None,
    }
    if revision is not None:
        error["currentRevision"] = revision
    assert result.structured_content == {"error": error}


@pytest.mark.parametrize(
    "detail",
    [
        "rejected: field_invalid:https://example.test/private",
        "rejected: field_invalid://example.test/private",
        "rejected: tap_invalid:owner/tap token",
        "rejected: derived_locale_read_only:zh-Hant::zh-CN",
        "rejected: derived_locale_read_only:zh-Hant:",
        "rejected: field_invalid:https:owner//tap",
        "rejected: field_invalid:a:" + "b" * 95,
        "rejected: field_invalid:password=value",
        "rejected: field_invalid:field\nsecret",
        "rejected: field_invalid:" + "a" * 97,
        "rejected: " + "a" * 65,
        "field password: test-only-sensitive",
    ],
)
def test_arbitrary_backend_details_are_redacted(detail: str) -> None:
    error = AgentError("1001", detail)
    assert str(error) == "1001: Invalid parameters"
    assert error.reason is None and error.detail is None
    assert str(AgentError("5000", "rejected: field_invalid:field")) == (
        "5000: Backend temporarily unavailable"
    )


def test_unrecognized_code_keeps_cleaned_backend_message() -> None:
    raw = "Cursor expired;\nread https://internal.example/x?token=1 again\t" + "x" * 400
    error = AgentError("1201", raw)
    message = str(error).removeprefix("1201: ")
    assert message.startswith("Cursor expired; read <url> again x")
    assert len(message) == 300 and "\n" not in message and "internal.example" not in message
    assert (error.code, error.reason, error.detail) == ("1201", None, message)
    assert str(AgentError("1201")) == "1201: Unrecognized backend error"
    rejected = AgentError("1201", "rejected: cursor_expired:14")
    assert (rejected.reason, rejected.detail) == ("cursor_expired", "14")
    # Codes that are not backend-shaped stay opaque.
    assert str(AgentError("unexpected-secret", "test-only-sensitive")) == (
        "5000: Backend temporarily unavailable"
    )


@pytest.mark.parametrize(
    ("status", "body", "expected"),
    [
        (409, {"code": "1003", "msg": "rejected: revision_conflict:5"}, "1003"),
        (502, "<html>proxy error</html>", "5000"),
        (500, {"code": "0000", "msg": "ok", "data": {}}, "5000"),
    ],
)
async def test_envelope_is_read_whatever_the_status(
    backend: Backend, status: int, body: object, expected: str
) -> None:
    def respond(_: httpx2.Request) -> httpx2.Response:
        if isinstance(body, str):
            return httpx2.Response(status, text=body)
        return httpx2.Response(status, json=body)

    await backend.client.close()
    backend.client.http = httpx2.AsyncClient(transport=httpx2.MockTransport(respond))
    with pytest.raises(AgentError) as raised:
        await backend.client.call("listAdminPackages", "test-only-full", "203.0.113.10", "test")
    assert raised.value.code == expected
    assert raised.value.current_revision == (5 if expected == "1003" else None)


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
