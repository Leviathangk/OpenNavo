"""Independently compare contracts, docs, backend allowlists and the handwritten tool manifest."""

import ast
import re
from typing import Any, cast

import pytest
from conftest import DOCUMENT, PERMISSIONS, ROOT, example, resolve

from opennavo_mcp.contracts import OPERATIONS
from opennavo_mcp.tools.manifest import RESOURCE_PERMISSIONS, TOOLS


def test_contract_and_permission_drift() -> None:
    assert len(TOOLS) == len({tool.name for tool in TOOLS}) == 88
    assert len(RESOURCE_PERMISSIONS) == 8
    assert len(PERMISSIONS) == len(set(PERMISSIONS)) == 23
    assert {tool.permission for tool in TOOLS} - {""} == set(PERMISSIONS)
    assert {op for tool in TOOLS for op in tool.operations} == set(OPERATIONS)
    assert len(OPERATIONS) == 108
    actual: dict[str, Any] = {}
    for item in DOCUMENT["paths"].values():
        for raw in item.values():
            if not isinstance(raw, dict):
                continue
            op = cast(dict[str, Any], raw)
            if isinstance(op.get("x-agent-access"), dict):
                actual[op["operationId"]] = op["x-agent-access"]
    assert {name: spec["policy"] for name, spec in OPERATIONS.items()} == actual
    settings = next(
        cast(dict[str, Any], op)
        for item in DOCUMENT["paths"].values()
        for op in item.values()
        if isinstance(op, dict)
        and cast(dict[str, Any], op).get("operationId") == "getAgentSettings"
    )
    response = resolve(settings["responses"]["200"])
    permissions = example(response["content"]["application/json"]["schema"])["data"][
        "grantablePermissions"
    ]
    assert set(permissions) == set(PERMISSIONS)
    for tool in TOOLS:
        first = OPERATIONS[tool.operations[0]]
        assert set(first["policy"].get("permissions", [])) == (
            {tool.permission} if tool.permission else set()
        )
        assert tool.delete == (first["policy"].get("deleteAction") == "always")
        for operation in tool.operations:
            spec = OPERATIONS[operation]
            assert set(spec["policy"].get("permissions", [])) <= set(PERMISSIONS)
            path = spec["path"]
            assert not path.startswith(
                ("/system", "/mirrors", "/remote-config", "/users", "/roles")
            )
            assert not path.startswith("/agents/") or operation == "introspectAgentToken"
            if path.startswith("/desktop/releases") and spec["method"] != "GET":
                assert operation == "updateDesktopReleaseNotes"
            if tool.write and spec["method"] != "GET":
                assert "dryRun" in spec["parameters"]["query"]


# Development docs are not shipped publicly.
# Compare the section 2.5 tool list only when docs exist locally.
@pytest.mark.skipif(
    not (ROOT / "docs/11-agent-interface.md").exists(),
    reason="docs/11-agent-interface.md is not part of the public repository",
)
def test_documented_manifest() -> None:
    document = (ROOT / "docs/11-agent-interface.md").read_text()
    section = document.split("### 2.5")[1].split("### 2.6")[0]
    names = set(re.findall(r"`([a-z]+(?:_[a-z]+)+|whoami)`", section))
    assert {tool.name for tool in TOOLS} <= names
    for uri in RESOURCE_PERMISSIONS:
        assert uri in document


def test_runtime_boundary() -> None:
    forbidden = {"subprocess", "psycopg", "redis", "boto3", "docker", "sqlite3", "socket"}
    for path in (ROOT / "apps/mcp/src/opennavo_mcp").rglob("*.py"):
        if "gen" in path.parts:
            continue
        tree = ast.parse(path.read_text())
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                assert all(item.name.split(".")[0] not in forbidden for item in node.names)
            if isinstance(node, ast.ImportFrom):
                assert (node.module or "").split(".")[0] not in forbidden
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                assert node.func.attr not in {"system", "popen", "execv", "spawn"}
    assert (
        "follow_redirects=False" in (ROOT / "apps/mcp/src/opennavo_mcp/agent_client.py").read_text()
    )


def test_corrupt_manifest_fails_drift(monkeypatch: Any) -> None:
    import sys
    from dataclasses import replace

    original = TOOLS
    for corrupted in [
        replace(original[1], permission="system:user:edit"),
        replace(original[1], operations=("createDesktopRelease",)),
    ]:
        monkeypatch.setattr(sys.modules[__name__], "TOOLS", (original[0], corrupted, *original[2:]))
        with pytest.raises(AssertionError):
            test_contract_and_permission_drift()


def test_prism_config_and_announcement_examples() -> None:
    schemas = DOCUMENT["components"]["schemas"]
    examples = DOCUMENT["components"]["examples"]
    for data in [
        schemas["AppConfigListResponse"]["example"]["data"],
        schemas["AppConfigListResponseResult"]["example"]["data"],
        examples["AppConfigListResponseResultSixLocales"]["value"]["data"],
    ]:
        assert data
        assert all(row["key"] != "desktop.announcement" for row in data)
    announcement = examples["AnnouncementSixLocales"]["value"]["data"]
    assert len(announcement["i18n"]) == 6
    assert announcement["i18n"][announcement["sourceLocale"]]["status"] == "source"
