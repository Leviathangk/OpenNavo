"""Curated links use the same content tools but never become install requests."""

import json

import pytest
from conftest import Backend, execution_args
from fastmcp import Client


async def test_link_release_filter_discovery_and_create_preview(backend: Backend) -> None:
    async with Client(backend.server) as client:
        tools = {tool.name: tool for tool in await client.list_tools()}
        for name in ("link_app_add", "link_app_update"):
            properties = tools[name].input_schema["properties"]
            assert properties["github_tag_regex"]["anyOf"][0]["maxLength"] == 1024
            assert properties["github_asset_pattern"]["anyOf"][0]["maxLength"] == 512
            assert properties["include_prereleases"]["type"] == "boolean"
            assert "include_prereleases" not in tools[name].input_schema["required"]
        result = await client.call_tool(
            "link_app_add",
            execution_args(
                "link_app_add",
                {
                    "token": "tensorfold",
                    "name": "TensorFold",
                    "pricing": "open_source",
                    "github_repo": "ashhart/TensorFold",
                    "github_tag_regex": r"^v1\.0\.0-beta\.[0-9]+$",
                    "github_asset_pattern": "*.dmg",
                    "include_prereleases": True,
                    "dry_run": True,
                },
            ),
            raise_on_error=False,
        )
        assert not result.is_error, result.content
    request = next(req for name, req in backend.calls if name == "addLinkPackage")
    assert request.url.params["dryRun"] == "true"
    assert json.loads(request.content) == {
        "token": "tensorfold",
        "name": "TensorFold",
        "pricing": "open_source",
        "githubRepo": "ashhart/TensorFold",
        "githubTagRegex": r"^v1\.0\.0-beta\.[0-9]+$",
        "githubAssetPattern": "*.dmg",
        "includePrereleases": True,
    }


async def test_link_release_filter_update_and_clear(backend: Backend) -> None:
    async with Client(backend.server) as client:
        for values in (
            {"github_tag_prefix": "rapid-mac-v", "github_asset_pattern": "*.dmg"},
            {
                "github_tag_prefix": None,
                "github_tag_regex": None,
                "github_asset_pattern": None,
                "include_prereleases": False,
            },
        ):
            result = await client.call_tool(
                "link_app_update",
                execution_args("link_app_update", {"package": 42, **values, "dry_run": True}),
                raise_on_error=False,
            )
            assert not result.is_error, result.content
    updates = [req for name, req in backend.calls if name == "updateLinkPackage"]
    assert json.loads(updates[0].content) == {
        "githubTagPrefix": "rapid-mac-v",
        "githubAssetPattern": "*.dmg",
    }
    assert json.loads(updates[1].content) == {
        "githubTagPrefix": None,
        "githubTagRegex": None,
        "githubAssetPattern": None,
        "includePrereleases": False,
    }


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("github_tag_prefix", "x" * 257),
        ("github_tag_regex", "x" * 1025),
        ("github_asset_pattern", "x" * 513),
        ("github_tag_regex", ""),
        ("include_prereleases", None),
        ("include_prereleases", "true"),
    ],
)
async def test_link_release_filter_contract_rejection(
    backend: Backend, field: str, value: str | None
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "link_app_update",
            execution_args("link_app_update", {"package": 42, field: value, "dry_run": True}),
            raise_on_error=False,
        )
        assert result.is_error
    assert not any(name == "updateLinkPackage" for name, _ in backend.calls)


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
            execution_args(
                "link_app_update",
                {
                    "package": "external-example",
                    "download_url": "https://example.com/download",
                    "dry_run": True,
                },
            ),
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
            "link_app_remove",
            execution_args("link_app_remove", {"package": 42, "dry_run": True}),
            raise_on_error=False,
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
            execution_args(
                "package_set_text",
                {"package": "editable-link", "source_locale": "en-US", "summary": "Human summary"},
            ),
            raise_on_error=False,
        )
        assert not result.is_error, result.content
    lookups = [req for name, req in backend.calls if name == "listAdminPackages"]
    assert [req.url.params["current"] for req in lookups] == ["1", "2"]
    update = next(req for name, req in backend.calls if name == "updatePackageText")
    assert "/42/" in update.url.path
    assert json.loads(update.content) == {"sourceLocale": "en-US", "summary": "Human summary"}
