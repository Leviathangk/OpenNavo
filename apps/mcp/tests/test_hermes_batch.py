"""H8/H10 adapters use generated contracts and a local mock transport only."""

import json

from conftest import Backend
from fastmcp import Client
from test_tools import arguments

from opennavo_mcp.tools.manifest import BY_NAME


async def test_hermes_read_mode(backend: Backend) -> None:
    args = {"mode": "read", "entity": "category", "id": 702}
    async with Client(backend.server) as client:
        result = await client.call_tool("translations_write", args, raise_on_error=False)
        assert not result.is_error, result.content
    assert backend.calls[-1][0] == "getTranslationsSource"


async def test_batch_has_per_language_preconditions(backend: Backend) -> None:
    args = arguments(BY_NAME["translations_write"])
    async with Client(backend.server) as client:
        result = await client.call_tool("translations_write", args, raise_on_error=False)
        assert not result.is_error, result.content
    op, request = backend.calls[-1]
    assert op == "writeTranslations"
    body = json.loads(request.content)
    assert body["translations"] == args["translations"]
    assert body["model"] == args["model"]
    assert "sourceHash" in body["translations"][0]
    assert "expectedRevision" in body["translations"][0]
    assert "idempotencyKey" in body["translations"][0]
