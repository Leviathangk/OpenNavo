import json
from typing import Any

import pytest
from conftest import DOCUMENT, Backend, example, execution_args, resolve
from fastmcp import Client

from opennavo_mcp.contracts import OPERATIONS, snake
from opennavo_mcp.tools.manifest import RESOURCE_PERMISSIONS, TOOLS, ToolSpec


def arguments(spec: ToolSpec) -> dict[str, Any]:
    if spec.name == "asset_upload_from_url":
        return execution_args(
            spec.name, {"url": "https://example.com/icon.png", "kind": "icon", "dry_run": True}
        )
    operation = OPERATIONS[spec.operations[0]]
    raw = DOCUMENT["paths"][operation["path"]][operation["method"].lower()]
    args: dict[str, Any] = {}
    for value in raw.get("parameters", []):
        param = resolve(value)
        if param.get("required") and param["in"] != "header":
            args[snake(param["name"])] = example(param["schema"], True)
    if "requestBody" in raw:
        body = resolve(raw["requestBody"])["content"]
        schema = next(iter(body.values()))["schema"]
        args.update({snake(k): v for k, v in example(schema, True).items()})
    if any(
        OPERATIONS[op]["path"].startswith(("/packages/{id}", "/links/packages/{id}"))
        for op in spec.operations
    ):
        args["package"] = args.pop("id", 1)
    if spec.name == "asset_upload_from_url":
        args = {"url": "https://example.com/icon.png", "kind": "icon"}
    if spec.write:
        args["dry_run"] = True
    return execution_args(spec.name, args)


@pytest.mark.parametrize("spec", TOOLS, ids=lambda spec: spec.name)
async def test_all_tools(backend: Backend, spec: ToolSpec) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(spec.name, arguments(spec), raise_on_error=False)
        assert not result.is_error, result.content
        assert result.structured_content is not None
        assert "untrusted" in result.structured_content
    requests = [request for op, request in backend.calls if op == spec.operations[0]]
    assert requests
    if spec.write:
        assert requests[-1].url.params["dryRun"] == "true"


async def test_resources_and_discovery(backend: Backend) -> None:
    async with Client(backend.server) as client:
        assert {tool.name for tool in await client.list_tools()} == {spec.name for spec in TOOLS}
        assert {str(item.uri) for item in await client.list_resources()} == set(
            RESOURCE_PERMISSIONS
        )
        for uri in RESOURCE_PERMISSIONS:
            content = await client.read_resource(uri)
            assert content
            data = json.loads(content[0].text)  # type: ignore[union-attr]
            if "/guide/" in uri:
                assert data["guide"]


async def test_release_notes_explain_unrecorded_versions(backend: Backend) -> None:
    async with Client(backend.server) as client:
        tools = {tool.name: tool.description or "" for tool in await client.list_tools()}
        content = await client.read_resource("opennavo://guide/release-notes")
    guide = json.loads(content[0].text)["guide"]  # type: ignore[union-attr]
    # Agents skip versions that were never shipped instead of waiting for the backend.
    for text in (tools["release_write_notes"], guide):
        assert "package_versions" in text and "1002" in text
    assert "newest 100 commits" in tools["tap_package_add"]
