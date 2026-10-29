"""Article identity IDs reach the server resolver, with structured errors intact."""

import json
from typing import Any

import pytest
from conftest import Backend, execution_args
from fastmcp import Client


@pytest.mark.parametrize("revision_id", [None, 401])
async def test_article_id_read_and_write(backend: Backend, revision_id: int | None) -> None:
    read: dict[str, Any] = {"mode": "read", "entity": "article", "article_id": 31}
    ref: dict[str, Any] = {"entity": "article"}
    if revision_id is not None:
        read["id"] = revision_id
        ref["id"] = revision_id
    write = {
        "ref": ref,
        "article_id": 31,
        "translations": [{"locale": "de-DE", "fields": {"title": "Referenzhandbuch"}}],
    }
    async with Client(backend.server) as client:
        assert not (await client.call_tool("translations_write", read)).is_error
        assert not (await client.call_tool("translations_write", write)).is_error
    get = next(req for op, req in backend.calls if op == "getTranslationsSource")
    assert get.url.params["articleId"] == "31"
    assert get.url.params.get("id") == (str(revision_id) if revision_id is not None else None)
    post = next(req for op, req in backend.calls if op == "writeTranslations")
    assert json.loads(post.content) == {
        "ref": ref,
        "articleId": 31,
        "translations": write["translations"],
    }


@pytest.mark.parametrize("mode", ["read", "write"])
@pytest.mark.parametrize(
    ("reason", "detail"),
    [
        (
            "article_ref_must_be_published_revision",
            "article_id_probably_passed_use_articleId_or_article_get.published.id",
        ),
        ("article_must_be_published_first", None),
        ("article_ref_mismatch", "articleId_and_ref.id"),
    ],
)
async def test_article_ref_rejection_has_reason_and_no_value(
    backend: Backend, mode: str, reason: str, detail: str | None
) -> None:
    # Even an erroneous backend envelope carrying data must not expose it.
    operation = "getTranslationsSource" if mode == "read" else "writeTranslations"
    backend.responses[operation] = {"value": "unrelated translation sentinel"}
    args: dict[str, Any] = (
        {"mode": "read", "entity": "article", "id": 31}
        if mode == "read"
        else {
            "ref": {"entity": "article", "id": 31},
            "translations": [{"locale": "de-DE", "fields": {"title": "Referenzhandbuch"}}],
        }
    )
    async with Client(backend.server) as client:
        await client.list_tools()
        backend.code = "1001"
        backend.msg = "rejected: " + reason + (":" + detail if detail else "")
        result = await client.call_tool("translations_write", args, raise_on_error=False)
    assert result.is_error
    assert result.structured_content is not None
    error = result.structured_content["error"]
    assert error["reason"] == reason
    assert error["detail"] == detail
    assert "unrelated translation sentinel" not in str(result)
    assert "value" not in result.structured_content
    assert backend.calls[-1][0] == operation


@pytest.mark.parametrize("mode", ["read", "write"])
@pytest.mark.parametrize("article_id", [0, -1, True, 1.5, "31"])
async def test_invalid_article_id_is_not_forwarded(
    backend: Backend, mode: str, article_id: Any
) -> None:
    args: dict[str, Any] = (
        {"mode": "read", "entity": "article", "article_id": article_id}
        if mode == "read"
        else {
            "ref": {"entity": "article"},
            "article_id": article_id,
            "translations": [{"locale": "de-DE", "fields": {"title": "Referenzhandbuch"}}],
        }
    )
    async with Client(backend.server) as client:
        result = await client.call_tool("translations_write", args, raise_on_error=False)
    assert result.is_error
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize("revision_id", [None, 31])
async def test_status_and_fix_accept_article_identity(
    backend: Backend, revision_id: int | None
) -> None:
    read: dict[str, Any] = {"entity": "article", "article_id": 32}
    ref: dict[str, Any] = {"entity": "article"}
    if revision_id is not None:
        read["id"] = revision_id
        ref["id"] = revision_id
    write = {
        "ref": ref,
        "article_id": 32,
        "locale": "de-DE",
        "fields": {"title": "Korrigiertes Handbuch"},
    }
    async with Client(backend.server) as client:
        assert not (await client.call_tool("translation_status", read)).is_error
        assert not (
            await client.call_tool("translation_fix", execution_args("translation_fix", write))
        ).is_error
    get = next(req for op, req in backend.calls if op == "getTranslationStatus")
    assert get.url.params["articleId"] == "32"
    assert get.url.params.get("id") == (str(revision_id) if revision_id is not None else None)
    put = next(req for op, req in backend.calls if op == "fixTranslation")
    assert json.loads(put.content) == {
        "ref": ref,
        "articleId": 32,
        "locale": "de-DE",
        "fields": write["fields"],
    }


@pytest.mark.parametrize(
    ("tool", "operation"),
    [("translation_status", "getTranslationStatus"), ("translation_fix", "fixTranslation")],
)
@pytest.mark.parametrize(
    "reason", ["article_ref_must_be_published_revision", "article_must_be_published_first"]
)
async def test_status_and_fix_rejections_do_not_echo_content(
    backend: Backend, tool: str, operation: str, reason: str
) -> None:
    backend.responses[operation] = {"value": "unrelated translation sentinel"}
    args: dict[str, Any] = (
        {"entity": "article", "id": 31}
        if tool == "translation_status"
        else {
            "ref": {"entity": "article", "id": 31},
            "locale": "de-DE",
            "fields": {"title": "Korrigiertes Handbuch"},
        }
    )
    async with Client(backend.server) as client:
        await client.list_tools()
        backend.code = "1001"
        backend.msg = "rejected: " + reason
        result = await client.call_tool(tool, execution_args(tool, args), raise_on_error=False)
    assert result.is_error
    assert result.structured_content is not None
    assert result.structured_content["error"]["reason"] == reason
    assert "unrelated translation sentinel" not in str(result)
    assert backend.calls[-1][0] == operation
