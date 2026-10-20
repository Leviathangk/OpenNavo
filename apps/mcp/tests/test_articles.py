"""Article tools preserve revision guards, payloads and the publication permission boundary."""

import json
from typing import Any

import pytest
from conftest import Backend, execution_args
from fastmcp import Client

from opennavo_mcp.contracts import OPERATIONS
from opennavo_mcp.tools.manifest import TOOLS


def test_every_article_operation_has_a_tool() -> None:
    article_ops = {
        name
        for name, op in OPERATIONS.items()
        if op["path"].startswith(("/articles", "/article-authors"))
    }
    assert len(article_ops) == 16
    assert article_ops == {
        op
        for tool in TOOLS
        if tool.name.startswith(("article_", "articles_"))
        for op in tool.operations
    }


@pytest.mark.parametrize(
    ("name", "extra", "path", "body"),
    [
        ("article_publish", {}, "/articles/7/publish", {}),
        (
            "article_withdraw",
            {"reason": "Outdated"},
            "/articles/7/withdraw",
            {"reason": "Outdated"},
        ),
        ("article_restore", {}, "/articles/7/restore", {}),
        ("article_revert", {"revision_number": 2}, "/articles/7/revert", {"revisionNumber": 2}),
        (
            "article_set_slug",
            {"slug": "updated-guide"},
            "/articles/7/slug",
            {"slug": "updated-guide"},
        ),
        (
            "article_set_locale",
            {"locale": "zh-CN", "withheld": True},
            "/articles/7/locales/zh-CN",
            {"withheld": True},
        ),
    ],
)
async def test_article_revision_and_payload(
    backend: Backend, name: str, extra: dict[str, Any], path: str, body: dict[str, Any]
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            name, execution_args(name, {"id": 7, "expected_revision": 9, "dry_run": True, **extra})
        )
        assert not result.is_error
        request = backend.calls[-1][1]
        assert request.url.path.endswith(path)
        assert request.url.params["dryRun"] == "true"
        assert json.loads(request.content) == {"expectedRevision": 9, **body}
        backend.calls.clear()
        result = await client.call_tool(name, {"id": 7, **extra}, raise_on_error=False)
        assert result.is_error
        assert not backend.calls


async def test_editor_cannot_publish_but_can_list_authors(backend: Backend) -> None:
    backend.permissions["test-only-full"] = ["content:article:edit"]
    async with Client(backend.server) as client:
        names = {tool.name for tool in await client.list_tools()}
        assert {"article_authors_list", "article_revert"} <= names
        assert "article_publish" not in names
        result = await client.call_tool(
            "article_publish", {"id": 7, "expected_revision": 9}, raise_on_error=False
        )
        assert result.is_error
        assert not any(op == "publishArticle" for op, _ in backend.calls)
        result = await client.call_tool("article_authors_list", {})
        assert not result.is_error
