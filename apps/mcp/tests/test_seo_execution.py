"""Execution metadata crosses the MCP/HTTP boundary without entering business data."""

import json

import pytest
from conftest import Backend, execution_args
from fastmcp import Client


async def test_catalog_write_without_task_or_evidence(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text",
            {
                "package": 42,
                "source_locale": "en-US",
                "summary": "Mac utility",
                "idempotency_key": "catalog-text-001",
                "expected_revision": 7,
            },
            raise_on_error=False,
        )
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "updatePackageText")
    assert "x-seo-task-id" not in req.headers
    assert "x-seo-evidence-ids" not in req.headers
    assert req.headers["idempotency-key"] == "catalog-text-001"
    assert req.headers["x-seo-expected-revision"] == "7"
    assert json.loads(req.content) == {"sourceLocale": "en-US", "summary": "Mac utility"}


@pytest.mark.parametrize(
    "args",
    [
        {},  # an unleased catalog edit still needs an idempotency key
        {"idempotency_key": "catalog-text-002", "task_id": 7},  # a partial lease
    ],
)
async def test_catalog_write_envelope_is_complete(
    backend: Backend, args: dict[str, object]
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_text",
            {"package": 42, "source_locale": "en-US", "summary": "Mac utility", **args},
            raise_on_error=False,
        )
    assert result.is_error
    assert not [name for name, _ in backend.calls if name == "updatePackageText"]


@pytest.mark.parametrize("missing", ["task_id", "fencing", "idempotency_key", "expected_revision"])
async def test_business_write_requires_complete_execution(backend: Backend, missing: str) -> None:
    args = execution_args(
        "package_set_text", {"package": 42, "source_locale": "en-US", "summary": "Mac utility"}
    )
    del args[missing]
    async with Client(backend.server) as client:
        result = await client.call_tool("package_set_text", args, raise_on_error=False)
    assert result.is_error
    assert not any(name == "updatePackageText" for name, _ in backend.calls)


async def test_business_execution_is_sent_only_in_headers(backend: Backend) -> None:
    args = execution_args(
        "package_set_text",
        {
            "package": 42,
            "source_locale": "en-US",
            "summary": "Mac utility",
            "evidence_ids": [11, 12],
            "expected_revision": 67,
            "dry_run": True,
        },
    )
    async with Client(backend.server) as client:
        result = await client.call_tool("package_set_text", args, raise_on_error=False)
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "updatePackageText")
    assert req.headers["x-seo-task-id"] == "7"
    assert req.headers["x-seo-fencing"] == "3"
    assert req.headers["idempotency-key"] == "fixture-write-001"
    assert req.headers["x-seo-expected-revision"] == "67"
    assert req.headers["x-seo-evidence-ids"] == "11,12"
    assert json.loads(req.content) == {"sourceLocale": "en-US", "summary": "Mac utility"}
    assert req.url.params["dryRun"] == "true"


async def test_claim_has_its_own_idempotent_body_and_no_lease_headers(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "seo_task_claim",
            {"idempotency_key": "claim-001", "limit": 3, "kinds": ["translate"]},
            raise_on_error=False,
        )
    assert not result.is_error, result.content
    req = next(req for name, req in backend.calls if name == "claimSeoTasks")
    assert "x-seo-task-id" not in req.headers
    assert "x-seo-fencing" not in req.headers
    assert json.loads(req.content) == {
        "idempotencyKey": "claim-001",
        "limit": 3,
        "kinds": ["translate"],
    }


async def test_workbook_version_and_server_claim_first(backend: Backend) -> None:
    async with Client(backend.server) as client:
        content = await client.read_resource("opennavo://guide/seo-routine")
        daily = await client.read_resource("opennavo://guide/daily-routine")
    data = json.loads(content[0].text)  # type: ignore[union-attr]
    routine = json.loads(daily[0].text)  # type: ignore[union-attr]
    assert data["version"] == "seo-routine-v1"
    assert "seo_task_claim" in routine["guide"]
    for name in ("seo_overview", "seo_task_defer", "translations_write", "expected_revision"):
        assert name in data["guide"]


async def test_geo_guide_version_boundaries_and_no_execution(backend: Backend) -> None:
    async with Client(backend.server) as client:
        content = await client.read_resource("opennavo://guide/geo-routine")
        daily = await client.read_resource("opennavo://guide/daily-routine")
        seo = await client.read_resource("opennavo://guide/seo-routine")
    data = json.loads(content[0].text)  # type: ignore[union-attr]
    assert data["version"] == "geo-routine-v1"
    guide = data["guide"]
    for guard in (
        "未授权不运行",
        "paidCalls=0",
        "summary_only",
        "evidence_unavailable",
        "2000 credits",
        "$15/天",
        "$100/月",
        "$12/月",
        "在途预占",
        "observation ID",
        "answerText/bodyText/rawResponse/html",
        "seotune.Receipt",
        "seobudget.Record",
        "不会",
        "不代理收费",
        "不增加 scheduler",
        "seo_task_defer",
        "topic:uninstall-mac-apps",
        "source_hash",
        "manual/machine",
        "人工",
        "十种",
        "claim_evidence",
    ):
        assert guard in guide
    for prompt in (
        "obsidian alternatives for mac",
        "best homebrew gui for mac",
        "how to install mac apps with homebrew",
        "open source alternative to the mac app store",
        "best free mac apps",
        "best free antivirus for mac",
        "free firewall app for mac",
        "is vlc safe for mac",
        "what is xprotect on mac",
        "how to completely uninstall apps on mac",
    ):
        assert prompt in guide
    for response, version in ((daily, "daily-routine-v1"), (seo, "seo-routine-v1")):
        entry = json.loads(response[0].text)  # type: ignore[union-attr]
        assert entry["version"] == version
        assert "opennavo://guide/geo-routine" in entry["guide"]
        assert "未授权不运行" in entry["guide"]
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize(
    "boundary",
    [
        "没有授权的数据保持 unknown",  # no authorized Bing source
        "IndexNow 成功只表示接受通知",  # notification-only
        "announce=false",  # suppressed completion is not delivery
        "仅日志未见",  # no observation is not undiscovered
        "自测 5xx 可独立报技术故障",  # a local failure is not a Bing index state
        "只有抓取记录或只有未索引状态时为 unknown",  # insufficient classification
        "已有 indexed 且现在出错仍 indexed",  # retain independently observed facts
        "冲突",  # conflicting or stale observations remain unknown
        "旧 URL 已 301",  # old/new canonical states remain separate
        "K=0 时比例 unavailable",  # no known denominator is not zero
    ],
)
async def test_bing_diagnostic_guide_requires_evidence(backend: Backend, boundary: str) -> None:
    async with Client(backend.server) as client:
        content = await client.read_resource("opennavo://guide/geo-routine")
    data = json.loads(content[0].text)  # type: ignore[union-attr]
    assert boundary in data["guide"]
    assert "只提 **tech_alert**" in data["guide"]
    assert "不周期性重提全部 URL" in data["guide"]
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)
