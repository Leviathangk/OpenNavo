"""Read, merge and compare state before writes to legacy full-update endpoints."""

import hashlib
import json
from typing import TYPE_CHECKING, Any, cast

from opennavo_mcp.agent_client import AgentError
from opennavo_mcp.contracts import expand
from opennavo_mcp.tools.runtime import fields

if TYPE_CHECKING:
    from opennavo_mcp.tools.runtime import AdapterTool


def state_hash(value: Any) -> str:
    return hashlib.sha256(
        json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()


def project(value: Any, schema: dict[str, Any], definitions: dict[str, Any]) -> Any:
    schema = expand(schema, definitions)
    if isinstance(value, dict) and "properties" in schema:
        return {
            key: project(item, schema["properties"][key], definitions)
            for key, item in cast(dict[str, Any], value).items()
            if key in schema["properties"]
        }
    if isinstance(value, list) and "items" in schema:
        return [project(item, schema["items"], definitions) for item in cast(list[Any], value)]
    return cast(Any, value)


def merge(old: dict[str, Any], new: dict[str, Any]) -> dict[str, Any]:
    result = dict(old)
    for key, value in new.items():
        result[key] = (
            merge(result[key], cast(dict[str, Any], value))
            if isinstance(value, dict) and isinstance(result.get(key), dict)
            else value
        )
    return result


async def merge_update(
    tool: "AdapterTool", operation: str, args: dict[str, Any], values: dict[str, Any]
) -> dict[str, Any]:
    read = {
        "category_update": "getCategoryTree",
        "collection_update": "getAdminCollection",
        "feature_upsert": "getFeature",
    }[tool.name]

    async def current() -> dict[str, Any]:
        response = await tool.invoke(read, {} if read == "getCategoryTree" else {"id": args["id"]})
        if read != "getCategoryTree":
            return response
        pending = list(response)
        while pending:
            node = pending.pop()
            if node["id"] == args["id"]:
                return node
            pending.extend(node.get("children", []))
        raise AgentError("1002")

    before = await current()
    expected = args.get("expected_state", state_hash(before))
    if expected != state_hash(before):
        raise AgentError("1003")
    _, body, definitions = fields(operation)
    supplied = values.get("body", {})
    updated = merge(project(before, body, definitions), supplied)
    # Localized writes upsert supplied languages and preserve omitted languages.
    # Merge fields within each requested language, but do not replay unrelated
    # translations (legacy text may exceed today's write limits).
    if isinstance(updated.get("i18n"), dict):
        requested = supplied.get("i18n", {})
        locales = (
            set(cast(dict[str, Any], requested)) if isinstance(requested, dict) else set[str]()
        )
        source = updated.get("sourceLocale")
        if isinstance(source, str):
            locales.add(source)
        updated["i18n"] = {
            locale: value for locale, value in updated["i18n"].items() if locale in locales
        }
    values["body"] = updated
    if state_hash(await current()) != expected:
        raise AgentError("1003")
    return values
