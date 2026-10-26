"""Optional content identifiers must not advertise null when Go rejects it."""

import json
from typing import Any, cast

import pytest
from conftest import Backend
from fastmcp import Client
from jsonschema import Draft202012Validator
from jsonschema import ValidationError as SchemaValidationError
from pydantic import ValidationError

from opennavo_mcp.contracts import input_model
from opennavo_mcp.tools.manifest import BY_NAME
from opennavo_mcp.tools.runtime import InputValidator, tool_schema


@pytest.mark.parametrize("secondary_id", [None, 0, -1, True, 1.5, "123"])
def test_invalid_secondary_id_rejected_by_schema_and_model(secondary_id: Any) -> None:
    body = {
        "ref": {"entity": "package", "id": 1, "secondaryId": secondary_id},
        "translations": [{"locale": "de-DE", "fields": {"summary": "Test"}}],
    }
    schema = tool_schema(BY_NAME["translations_write"])
    with pytest.raises(SchemaValidationError):
        cast(InputValidator, Draft202012Validator(schema)).validate(body)
    with pytest.raises(ValidationError):
        input_model("writeTranslations").model_validate_json(
            json.dumps({"body": body}), strict=True
        )


@pytest.mark.parametrize("secondary_id", [None, 0, -1, True, 1.5, "123"])
async def test_invalid_secondary_id_never_sent(backend: Backend, secondary_id: Any) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "translations_write",
            {
                "ref": {"entity": "package", "id": 1, "secondaryId": secondary_id},
                "translations": [{"locale": "de-DE", "fields": {"summary": "Test"}}],
            },
            raise_on_error=False,
        )
    assert result.is_error
    assert "ref.secondaryId" in str(result.content)
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


async def test_collection_item_requires_secondary_id(backend: Backend) -> None:
    async with Client(backend.server) as client:
        result = await client.call_tool(
            "translations_write",
            {
                "ref": {"entity": "collection_item", "id": 1},
                "translations": [{"locale": "de-DE", "fields": {"note": "Test"}}],
            },
            raise_on_error=False,
        )
    assert result.is_error
    assert "field ref.secondaryId: missing" in str(result.content)
    assert all(op == "introspectAgentToken" for op, _ in backend.calls)


@pytest.mark.parametrize(
    "ref",
    [{"entity": "package", "id": 1}, {"entity": "collection_item", "id": 1, "secondaryId": 2}],
)
async def test_valid_translation_ref_preserved(backend: Backend, ref: dict[str, Any]) -> None:
    body = {
        "ref": ref,
        "translations": [
            {
                "locale": "de-DE",
                "fields": {"note" if ref["entity"] == "collection_item" else "summary": "Test"},
            }
        ],
    }
    async with Client(backend.server) as client:
        result = await client.call_tool("translations_write", body)
    assert not result.is_error
    request = next(req for op, req in backend.calls if op == "writeTranslations")
    assert json.loads(request.content) == body
