"""Curated links use the same content tools but never become install requests."""

import json

from conftest import Backend
from fastmcp import Client


async def test_link_token_resolution_and_dry_run(backend: Backend) -> None:
    backend.responses["listAdminPackages"] = {
        "records": [{"id": 42, "kind": "link", "token": "external-example"}],
        "total": 1,
        "current": 1,
        "size": 100,
    }
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "link_app_update",
            {
                "package": "external-example",
                "download_url": "https://example.com/download",
                "dry_run": True,
            },
            raise_on_error=False,
        )
        assert not result.is_error, result.content
    lookup = next(req for name, req in backend.calls if name == "listAdminPackages")
    assert "kind" not in lookup.url.params
    update = next(req for name, req in backend.calls if name == "updateLinkPackage")
    assert update.url.path == "/agent-api/links/packages/42"
    assert update.url.params["dryRun"] == "true"
    assert json.loads(update.content) == {"downloadUrl": "https://example.com/download"}


async def test_link_remove_requires_delete_permission(backend: Backend) -> None:
    backend.allow_delete = False
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "link_app_remove", {"package": 42, "dry_run": True}, raise_on_error=False
        )
        assert result.is_error
    assert not any(name == "removeLinkPackage" for name, _ in backend.calls)


async def test_link_content_token_on_second_page_uses_package_text_operation(
    backend: Backend,
) -> None:
    pages = iter(
        [
            {"records": [{"id": 1, "kind": "cask", "token": "other"}], "total": 101},
            {"records": [{"id": 42, "kind": "link", "token": "editable-link"}], "total": 101},
        ]
    )
    backend.responses["listAdminPackages"] = lambda: next(pages)
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text",
            {"package": "editable-link", "source_locale": "en-US", "summary": "Human summary"},
            raise_on_error=False,
        )
        assert not result.is_error, result.content
    lookups = [req for name, req in backend.calls if name == "listAdminPackages"]
    assert [req.url.params["current"] for req in lookups] == ["1", "2"]
    update = next(req for name, req in backend.calls if name == "updatePackageText")
    assert "/42/" in update.url.path
    assert json.loads(update.content) == {"sourceLocale": "en-US", "summary": "Human summary"}
