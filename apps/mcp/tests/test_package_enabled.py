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
        "visibility_reason",
        "clear_disabled",
        "dry_run",
        "expected_revision",
        "idempotency_key",
    }
    assert set(tool.input_schema["required"]) == {"package", "enabled"}
    assert tool.input_schema["properties"]["enabled"]["type"] == "boolean"
    assert tool.input_schema["properties"]["clear_disabled"]["default"] is False
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
                "package_set_enabled",
                {
                    "package": package,
                    "enabled": enabled,
                    "dry_run": dry_run,
                    "visibility_reason": "Visibility fixture",
                },
            ),
        )
    assert result.structured_content == {"untrusted": preview if dry_run else None}
    writes = [request for operation, request in backend.calls if operation == "updatePackageMeta"]
    assert len(writes) == 1
    request = writes[0]
    assert request.method == "PUT" and request.url.path == "/agent-api/packages/42/meta"
    assert json.loads(request.content) == {
        "hidden": not enabled,
        "visibilityReason": "Visibility fixture",
    }
    assert request.url.params["dryRun"] == str(dry_run).lower()
    assert request.headers["X-Agent-Tool"] == "package_set_enabled"
    assert request.headers["X-Expected-Revision"] == "0"
    assert request.headers["Idempotency-Key"] == "fixture-write-001"
    assert any(op == "listAdminPackages" for op, _ in backend.calls) == isinstance(package, str)


async def test_package_enabled_clear_disabled_is_forwarded(backend: Backend) -> None:
    preview = {
        "dryRun": True,
        "changes": [
            {
                "before": {"packages": [{"disabled": True, "disable_reason": "unsafe"}]},
                "after": {"packages": [{"disabled": False, "disable_reason": "unsafe"}]},
            }
        ],
    }
    backend.responses["updatePackageMeta"] = preview
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "package_set_enabled",
            execution_args(
                "package_set_enabled",
                {"package": 42, "enabled": True, "clear_disabled": True, "dry_run": True},
            ),
        )
    assert result.structured_content == {"untrusted": preview}
    writes = [request for operation, request in backend.calls if operation == "updatePackageMeta"]
    assert len(writes) == 1
    assert json.loads(writes[0].content) == {"hidden": False, "clearDisabled": True}


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


@pytest.mark.parametrize(
    ("tool", "patch"),
    [
        ("package_set_enabled", {"enabled": False}),
        ("package_update_meta", {"hidden": True}),
        ("package_update_meta", {"private": True}),
    ],
)
@pytest.mark.parametrize("reason", [None, "", " \n\t "])
async def test_visibility_requires_reason_before_any_backend_write(
    backend: Backend, tool: str, patch: dict[str, Any], reason: str | None
) -> None:
    args = {"package": 42, **patch}
    if reason is not None:
        args["visibility_reason"] = reason
    async with Client(backend.server) as client:
        result = await client.call_tool(tool, execution_args(tool, args), raise_on_error=False)
    assert result.is_error
    assert not any(op != "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize("private", [True, False])
@pytest.mark.parametrize("dry_run", [True, False])
async def test_private_metadata_uses_admin_api(
    backend: Backend, private: bool, dry_run: bool
) -> None:
    args: dict[str, Any] = {"package": 42, "private": private, "dry_run": dry_run}
    expected: dict[str, Any] = {"private": private}
    if private:
        args["visibility_reason"] = "  Catalog visibility review.  "
        expected["visibilityReason"] = "Catalog visibility review."
    async with Client(backend.server) as client:
        await client.call_tool("package_update_meta", execution_args("package_update_meta", args))
    writes = [request for operation, request in backend.calls if operation == "updatePackageMeta"]
    assert len(writes) == 1
    assert writes[0].url.path == "/agent-api/packages/42/meta"
    assert writes[0].url.params["dryRun"] == str(dry_run).lower()
    assert json.loads(writes[0].content) == expected
