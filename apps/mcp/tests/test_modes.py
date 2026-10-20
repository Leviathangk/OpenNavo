import json
from typing import Any

import pytest
from conftest import Backend, execution_args
from fastmcp import Client
from test_tools import arguments

from opennavo_mcp.tools.manifest import BY_NAME

MODES = [
    ("seo_automation_set", {"mode": "read"}, "getSeoAutomation"),
    (
        "translations_write",
        {"mode": "read", "entity": "category", "id": 702},
        "getTranslationsSource",
    ),
    ("logs_search", {"kind": "calls", "id": 1}, "getAgentCall"),
    ("logs_search", {"kind": "translations"}, "listTranslationLogs"),
    ("logs_search", {"kind": "translations", "id": 1}, "getTranslationLog"),
    ("logs_search", {"kind": "revisions"}, "listContentRevisions"),
    ("logs_search", {"kind": "revisions", "id": 1}, "getContentRevision"),
    ("logs_search", {"kind": "trash"}, "listTrash"),
    ("logs_search", {"kind": "trash", "id": 1}, "getTrashItem"),
    (
        "revision_restore",
        {"kind": "request", "request_id": "72c52322-428c-4139-b305-56ca7f4133c9", "dry_run": True},
        "revertRequest",
    ),
    ("revision_restore", {"kind": "trash", "id": 1, "dry_run": True}, "restoreTrash"),
    ("package_set_icon", {"package": 1, "remove": True, "dry_run": True}, "deletePackageIcon"),
    (
        "collection_publish",
        {"id": 1, "action": "unpublish", "dry_run": True},
        "unpublishCollection",
    ),
    ("features_list", {"id": 1}, "getFeature"),
    ("releases_list", {"id": 1}, "getAdminRelease"),
    ("releases_list", {"view": "releases"}, "listAdminReleases"),
    ("jobs_runs", {"run_id": 1}, "getJobRun"),
    ("search_insights", {"kind": "zero"}, "listZeroResultQueries"),
    ("desktop_releases_list", {"id": 1}, "getDesktopRelease"),
]


@pytest.mark.parametrize("name,args,operation", MODES)
async def test_modes(backend: Backend, name: str, args: dict[str, Any], operation: str) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(name, execution_args(name, args), raise_on_error=False)
        assert not result.is_error, result.content
    assert backend.calls[-1][0] == operation
    if BY_NAME[name].write and backend.calls[-1][1].method != "GET":
        assert backend.calls[-1][1].url.params["dryRun"] == "true"


@pytest.mark.parametrize("name", ["feature_upsert", "synonym_upsert"])
async def test_upsert_update_branch(backend: Backend, name: str) -> None:
    args = arguments(BY_NAME[name]) | {"id": 1}
    async with Client(backend.server) as client:
        result = await client.call_tool(name, execution_args(name, args), raise_on_error=False)
        assert not result.is_error, result.content
    assert backend.calls[-1][0] == BY_NAME[name].operations[1]


async def test_clear_notes_and_eight_sections(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "release_write_notes",
            execution_args(
                "release_write_notes",
                {"package": 1, "version": "1.0", "clear": True, "dry_run": True},
            ),
            raise_on_error=False,
        )
        assert not result.is_error, result.content
        assert json.loads(backend.calls[-1][1].content) == {"clear": True}
        args = arguments(BY_NAME["release_write_notes"])
        args["sections"] = [{"area": "改进", "items": ["修正窗口"]}] * 8
        assert not (
            await client.call_tool(
                "release_write_notes",
                execution_args("release_write_notes", args),
                raise_on_error=False,
            )
        ).is_error
        count = len(backend.calls)
        args["sections"] *= 2
        assert (
            await client.call_tool(
                "release_write_notes",
                execution_args("release_write_notes", args),
                raise_on_error=False,
            )
        ).is_error
        assert len(backend.calls) == count


async def test_six_language_aliases_remain_exact(backend: Backend) -> None:
    args = arguments(BY_NAME["category_create"])
    locales = ["en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU"]
    args["i18n"] = {
        locale: {"name": "保留品牌", "description": "code() /path https://example.com"}
        for locale in locales
    }
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "category_create", execution_args("category_create", args), raise_on_error=False
        )
        assert not result.is_error, result.content
    body = json.loads(backend.calls[-1][1].content)
    assert body["i18n"] == args["i18n"]


async def test_listing_gaps_defaults(backend: Backend) -> None:
    async with Client(backend.server) as client:
        await client.call_tool("listing_gaps", execution_args("listing_gaps", {}))
    query = backend.calls[-1][1].url.params
    assert (
        query["gaps"]
        == "zhName,zhSummary,enSummary,primaryCategory,tags,icon,downloadSize,latestEditorial"
    )
    assert query["hidden"] == "false" and "isFont" not in query and query["sort"] == "popular"


async def test_optimistic_merge_detects_intervening_change(backend: Backend) -> None:
    # Do not write if another operator changes state between the two reads.
    from conftest import DOCUMENT, example, resolve

    schema = resolve(DOCUMENT["paths"]["/collections/{id}"]["get"]["responses"]["200"])["content"][
        "application/json"
    ]["schema"]
    before = example(schema)["data"]
    versions = iter([before, before | {"updatedAt": "2026-10-08T00:00:00Z"}])
    backend.responses["getAdminCollection"] = lambda: next(versions)
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "collection_update",
            execution_args("collection_update", {"id": 1, "dry_run": True}),
            raise_on_error=False,
        )
        assert result.is_error
    assert not any(op == "updateCollection" for op, _ in backend.calls)


async def test_display_preview_fallback(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = {
        "name": "Homebrew Name",
        "descEn": "Homebrew fallback",
        "sourceLocale": "zh-CN",
        "i18n": [
            {
                "locale": "zh-CN",
                "displayName": "中文名",
                "summary": "中文简介",
                "description": "中文介绍",
            },
            {"locale": "en-US", "displayName": "English Name", "summary": "English summary"},
            {"locale": "ja-JP", "displayName": "", "summary": "", "description": ""},
        ],
    }
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_get", execution_args("package_get", {"package": 1, "include": ["display"]})
        )
        assert result.structured_content is not None
        locales = result.structured_content["untrusted"]["display"]["locales"]
        assert len(locales) == 10
        assert locales["zh-CN"]["displayName"] == "中文名"
        assert locales["ja-JP"]["summary"] == "English summary"
        assert locales["ja-JP"]["textLocale"] == "en-US"


async def test_all_operations_reached_by_tools(backend: Backend) -> None:
    from opennavo_mcp.contracts import OPERATIONS
    from opennavo_mcp.tools.manifest import TOOLS

    async with Client(backend.server) as client:
        for spec in TOOLS:
            await client.call_tool(spec.name, arguments(spec))
        for name, args, _ in MODES:
            await client.call_tool(name, execution_args(name, args))
        for name in ("feature_upsert", "synonym_upsert"):
            await client.call_tool(name, arguments(BY_NAME[name]) | {"id": 1})
        await client.call_tool(
            "package_get",
            execution_args(
                "package_get",
                {"package": 1, "include": ["screenshots", "translations", "display", "facts"]},
            ),
        )
    assert {operation for operation, _ in backend.calls} == set(OPERATIONS)


async def test_token_resolution_continues_after_first_page(backend: Backend) -> None:
    pages = iter(
        [
            {"records": [{"id": 1, "token": "similar"}], "total": 101},
            {"records": [{"id": 101, "token": "exact"}], "total": 101},
        ]
    )
    backend.responses["listAdminPackages"] = lambda: next(pages)
    async with Client(backend.server) as client:
        await client.call_tool("package_get", execution_args("package_get", {"package": "exact"}))
    searches = [request for op, request in backend.calls if op == "listAdminPackages"]
    assert [request.url.params["current"] for request in searches] == ["1", "2"]
    assert backend.calls[-1][1].url.path.endswith("/packages/101")
