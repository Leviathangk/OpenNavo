import json
from typing import Any

import pytest
from conftest import CURRENT_TOKEN, Backend, execution_args
from fastmcp import Client


async def test_package_enabled_discovery(backend: Backend) -> None:
    async with Client(backend.server) as client:
        tool = next(
            tool for tool in await client.list_tools() if tool.name == "package_set_enabled"
        )
    assert set(tool.input_schema["properties"]) == {
        "package",
        "enabled",
        "dry_run",
        "expected_revision",
        "idempotency_key",
    }
    assert set(tool.input_schema["required"]) == {"package", "enabled"}
    assert tool.input_schema["properties"]["enabled"]["type"] == "boolean"
    assert tool.annotations is not None and tool.annotations.read_only_hint is False
    assert "hidden" in (tool.description or "")
    assert "Homebrew" in (tool.description or "")


@pytest.mark.parametrize("package", [42, "pearcleaner"])
@pytest.mark.parametrize("enabled", [True, False])
@pytest.mark.parametrize("dry_run", [True, False])
async def test_package_enabled_uses_metadata_write(
    backend: Backend, package: int | str, enabled: bool, dry_run: bool
) -> None:
    backend.responses["listAdminPackages"] = {
        "records": [{"id": 41, "token": "pearcleaner-helper"}, {"id": 42, "token": "pearcleaner"}],
        "total": 2,
    }
    preview = {
        "dryRun": True,
        "changes": [{"before": {"hidden": enabled}, "after": {"hidden": not enabled}}],
    }
    backend.responses["updatePackageMeta"] = preview if dry_run else None
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_enabled",
            execution_args(
                "package_set_enabled", {"package": package, "enabled": enabled, "dry_run": dry_run}
            ),
        )
    assert result.structured_content == {"untrusted": preview if dry_run else None}
    writes = [request for operation, request in backend.calls if operation == "updatePackageMeta"]
    assert len(writes) == 1
    request = writes[0]
    assert request.method == "PUT" and request.url.path == "/agent-api/packages/42/meta"
    assert json.loads(request.content) == {"hidden": not enabled}
    assert request.url.params["dryRun"] == str(dry_run).lower()
    assert request.headers["X-Agent-Tool"] == "package_set_enabled"
    assert request.headers["X-Expected-Revision"] == "0"
    assert request.headers["Idempotency-Key"] == "fixture-write-001"
    assert any(op == "listAdminPackages" for op, _ in backend.calls) == isinstance(package, str)


@pytest.mark.parametrize(
    "args",
    [
        {"package": 42},
        {"package": 42, "enabled": None},
        {"package": 42, "enabled": "true"},
        {"package": 42, "enabled": 1},
        {"package": 42, "enabled": True, "hidden": True},
        {"package": 42, "enabled": True, "disabled": False},
        {"package": 42, "enabled": True, "developer": "unrelated patch"},
    ],
)
async def test_package_enabled_rejects_invalid_or_unrelated_fields(
    backend: Backend, args: dict[str, Any]
) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool("package_set_enabled", args, raise_on_error=False)
    assert result.is_error
    assert not any(op != "introspectAgentToken" for op, _ in backend.calls)


async def test_package_enabled_requires_edit_permission(backend: Backend) -> None:
    token = CURRENT_TOKEN.set("test-only-read")
    try:
        async with Client(backend.server) as client:
            assert "package_set_enabled" not in {tool.name for tool in await client.list_tools()}
            result = await client.call_tool(
                "package_set_enabled", {"package": 42, "enabled": True}, raise_on_error=False
            )
        assert result.is_error
        assert not any(op != "introspectAgentToken" for op, _ in backend.calls)
    finally:
        CURRENT_TOKEN.reset(token)


async def test_package_enabled_numeric_id_needs_no_view_permission(backend: Backend) -> None:
    backend.permissions["test-only-full"] = ["catalog:package:edit"]
    async with Client(backend.server) as client:
        await client.call_tool("package_set_enabled", {"package": 42, "enabled": True})
        result = await client.call_tool(
            "package_set_enabled", {"package": "pearcleaner", "enabled": True}, raise_on_error=False
        )
    assert result.is_error
    assert [op for op, _ in backend.calls] == ["introspectAgentToken", "updatePackageMeta"]


async def test_package_enabled_unknown_token_never_writes(backend: Backend) -> None:
    backend.responses["listAdminPackages"] = {"records": [], "total": 0}
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_enabled", {"package": "missing-app", "enabled": True}, raise_on_error=False
        )
    assert result.is_error
    assert result.structured_content is not None
    assert result.structured_content["error"]["code"] == "1002"
    assert not any(op == "updatePackageMeta" for op, _ in backend.calls)
