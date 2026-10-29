import json

import pytest
from conftest import Backend, execution_args
from fastmcp import Client


@pytest.mark.parametrize("custom", [False, True])
@pytest.mark.parametrize("dry_run", [False, True])
async def test_tap_site_token_transport(backend: Backend, custom: bool, dry_run: bool) -> None:
    args = {"cask": "stablyai/orca/orca", "dry_run": dry_run}
    if custom:
        args["site_token"] = "stablyai-orca"  # noqa: S105 -- Public catalog slug.
    async with Client(backend.server) as client:
        result = await client.call_tool("tap_package_add", execution_args("tap_package_add", args))
        assert not result.is_error
        tool = next(tool for tool in await client.list_tools() if tool.name == "tap_package_add")
        assert "site_token" in tool.input_schema["properties"]
        assert "site_token" not in tool.input_schema.get("required", [])
    request = next(request for op, request in backend.calls if op == "addTapPackage")
    expected = {"cask": "stablyai/orca/orca"}
    if custom:
        expected["siteToken"] = "stablyai-orca"
    assert json.loads(request.content) == expected
    assert request.url.params["dryRun"] == str(dry_run).lower()


@pytest.mark.parametrize("token", ["", "Orca", "bad/token", "two words", "-flag", "a" * 129])
async def test_invalid_site_token_never_reaches_backend(backend: Backend, token: str) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "tap_package_add",
            execution_args(
                "tap_package_add",
                {"cask": "stablyai/orca/orca", "site_token": token, "dry_run": True},
            ),
            raise_on_error=False,
        )
        assert result.is_error
    assert not any(op == "addTapPackage" for op, _ in backend.calls)
