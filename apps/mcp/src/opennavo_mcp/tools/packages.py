"""Bounded package reads: pick fields and languages, read long texts in parts, cap reply size.

Agent platforms cut tool results that are too long. A reply either carries complete values or
names, under omitted, each value it left out (never half a value) and the call that reads it.
"""

import copy
import hashlib
import json
import re
from typing import TYPE_CHECKING, Any, cast

from opennavo_mcp.agent_client import AgentError
from opennavo_mcp.contracts import LOCALES
from opennavo_mcp.gen import admin_models
from opennavo_mcp.tools.display import package_display

if TYPE_CHECKING:
    from opennavo_mcp.tools.runtime import AdapterTool

LIMIT = 32 * 1024  # bytes of compact UTF-8 JSON
TEXT_BYTES = 30 * 1024  # serialized size of one package_text_get part's value
LONG = 512  # characters or bytes: leaving out anything smaller would save almost nothing
CODES = [locale["code"] for locale in LOCALES["locales"]]
EXTRAS = ("screenshots", "translations", "facts", "display")
KEPT = ("id", "token", "revision")
FIELDS = list(
    dict.fromkeys(
        [
            *(
                field.alias or name
                for name, field in admin_models.AdminPackageDetail.model_fields.items()
            ),
            *EXTRAS,
        ]
    )
)
TEXTS = ("displayName", "summary", "description")  # i18n row texts; raw is read as JSON text
HINT = (
    "Values in omitted were left out to keep this reply under 32 KB. Each entry's read is the"
    " call that returns it; package_text_get returns long text in parts with the sha256 of the"
    " whole text."
)


def compact(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def size(value: Any) -> int:
    return len(compact(value).encode())


def digest(value: Any) -> str:
    """sha256 of a string's UTF-8 text, or of another value's compact JSON."""
    return hashlib.sha256(
        (value if isinstance(value, str) else compact(value)).encode()
    ).hexdigest()


async def read_package(
    tool: "AdapterTool",
    args: dict[str, Any],
    values: dict[str, Any],
    token: str | None,
    *,
    bound: bool = True,
) -> dict[str, Any]:
    """values are the detail read's own parameters (id, locale)."""
    package = args["id"]
    fields: list[str] | None = args.get("fields")
    wanted = set(args.get("include", [])) | set(fields or [])
    extras = [name for name in EXTRAS if name in wanted]
    # Only facts, translations or screenshots: their own reads suffice, without the detail.
    detailed = fields is None or "display" in extras or bool(set(fields) - set(EXTRAS) - {"id"})
    detail: dict[str, Any] = {}
    if detailed:
        detail = await tool.invoke("getAdminPackage", values)
    result: dict[str, Any] = detail
    if fields is not None:
        result = {"id": package, **({"token": token} if token else {})}
        result |= {
            key: detail[key] for key in (*KEPT, *fields) if key in detail and key not in EXTRAS
        }
    if "screenshots" in extras:
        result["screenshots"] = await tool.invoke("listPackageScreenshots", {"id": package})
    if "translations" in extras:
        result["translations"] = await tool.invoke(
            "getTranslationStatus", {"entity": "package", "id": package}
        )
    if "facts" in extras:
        result["facts"] = await tool.invoke("getPackageFacts", {"id": package})
    if "display" in extras:
        result["display"] = package_display(detail)
    if "locales" in args:
        keep_locales(result, set(args["locales"]))
    return bounded(result, package, args) if bound else result


def keep_locales(result: dict[str, Any], locales: set[str]) -> None:
    """Keep only these languages in i18n rows, localized maps and screenshot captions."""

    def only(value: Any) -> Any:
        if not isinstance(value, dict):
            return value
        return {k: v for k, v in cast(dict[str, Any], value).items() if k in locales}

    rows = result.get("i18n")
    if isinstance(rows, list):
        result["i18n"] = [
            row
            for row in cast(list[Any], rows)
            if not isinstance(row, dict) or cast(dict[str, Any], row).get("locale") in locales
        ]
    for key in ("displayName", "summary"):
        if key in result:
            result[key] = only(result[key])
    shots = result.get("screenshots")
    for shot in cast(list[Any], shots) if isinstance(shots, list) else []:
        if isinstance(shot, dict) and "i18n" in shot:
            shot = cast(dict[str, Any], shot)
            shot["i18n"] = only(shot["i18n"])
    for key, inner in (("translations", "i18n"), ("display", "locales")):
        block = result.get(key)
        if isinstance(block, dict) and inner in block:
            block = cast(dict[str, Any], block)
            block[inner] = only(block[inner])


# What to leave out: its size, the (parent, key) entries holding it, its path and its value.
Found = tuple[int, list[tuple[dict[str, Any], str]], str, Any]


def bounded(reply: dict[str, Any], package: int, args: dict[str, Any]) -> dict[str, Any]:
    """Leave whole values out, largest first, until the reply fits LIMIT; list each one."""
    if size(reply) <= LIMIT:
        return reply
    original = copy.deepcopy(reply)
    omitted: list[dict[str, Any]] = []
    reply["omitted"], reply["hint"] = omitted, HINT
    # 1. raw and long strings, such as each language's description; 2. one language of a
    # multilingual block (captions, previews, translation states); 3. whole top-level blocks.
    # Omitted values are always recoverable through bounded text/JSON parts.
    for tier in (long_values, languages, blocks):
        for _, entries, path, value in sorted(tier(reply), key=lambda found: (-found[0], found[2])):
            if size(reply) <= LIMIT:
                return reply
            for parent, key in entries:
                del parent[key]
            # Later tiers can contain values removed earlier. Describe the original whole value.
            value = select_path(original, path)
            omitted[:] = [entry for entry in omitted if not path_contains(path, entry["path"])]
            omitted.append(
                {
                    "path": path,
                    "chars": len(value) if isinstance(value, str) else len(compact(value)),
                    "sha256": digest(value),
                    "read": reader(package, path, args),
                }
            )
    return reply


def walk(reply: dict[str, Any]) -> list[tuple[dict[str, Any], str, Any, str]]:
    """Every dict entry below the kept keys as (parent, key, value, path); raw stays whole."""
    entries: list[tuple[dict[str, Any], str, Any, str]] = []

    def visit(node: Any, path: str) -> None:
        if isinstance(node, dict):
            node = cast(dict[str, Any], node)
            for key, value in node.items():
                if path or key not in (*KEPT, "omitted", "hint"):
                    child = f"{path}.{key}" if path else key
                    entries.append((node, key, value, child))
                    if child != "raw":
                        visit(value, child)
        elif isinstance(node, list):
            for index, item in enumerate(cast(list[Any], node)):
                locale = (
                    cast(dict[str, Any], item).get("locale") if isinstance(item, dict) else None
                )
                visit(item, f"{path}[{f'locale={locale}' if isinstance(locale, str) else index}]")

    visit(reply, "")
    return entries


def long_values(reply: dict[str, Any]) -> list[Found]:
    return [
        (size(value), [(parent, key)], path, value)
        for parent, key, value, path in walk(reply)
        if path == "raw" or (isinstance(value, str) and len(value) >= LONG)
    ]


def languages(reply: dict[str, Any]) -> list[Found]:
    """One language across a list, e.g. screenshots[*].i18n.de-DE: what locales=[de-DE] reads."""
    groups: dict[str, list[tuple[dict[str, Any], str]]] = {}
    for parent, key, value, path in walk(reply):
        if key in CODES and isinstance(value, dict):
            groups.setdefault(re.sub(r"\[\d+\]", "[*]", path), []).append((parent, key))
    found: list[Found] = []
    for path, entries in groups.items():
        values = [parent[key] for parent, key in entries]
        value = values if "[*]" in path else values[0]
        if size(value) >= LONG:
            found.append((size(value), entries, path, value))
    return found


def blocks(reply: dict[str, Any]) -> list[Found]:
    return [
        (size(value), [(reply, key)], key, value)
        for key, value in reply.items()
        if key not in (*KEPT, "omitted", "hint") and isinstance(value, (dict, list))
    ]


def reader(package: int, path: str, args: dict[str, Any]) -> dict[str, Any]:
    """The call that returns an omitted value whole."""
    if path == "raw":
        return {"tool": "package_text_get", "arguments": {"package": package, "field": "raw"}}
    text = re.fullmatch(r"i18n\[locale=([^\]]+)\]\.(displayName|summary|description)", path)
    if text:
        arguments = {"package": package, "locale": text[1], "field": text[2]}
        return {"tool": "package_text_get", "arguments": arguments}
    return {
        "tool": "package_text_get",
        "arguments": {
            "package": package,
            "field": "json",
            "path": path,
            **{key: args[key] for key in ("locale", "locales") if key in args},
        },
    }


def path_contains(parent: str, child: str) -> bool:
    pattern = re.escape(parent).replace(r"\[\*\]", r"\[\d+\]")
    return re.match(pattern + r"(?:$|[.\[])", child) is not None


def select_path(value: Any, path: str) -> Any:
    """Select generated dotted paths, locale rows, numeric indices and wildcard groups."""
    parts = re.findall(r"\[[^\]]+\]|[^.\[\]]+", path)

    def select(node: Any, remaining: list[str]) -> Any:
        if not remaining:
            return node
        head, *tail = remaining
        if head == "[*]":
            found: list[Any] = []
            for item in node:
                try:
                    found.append(select(item, tail))
                except (KeyError, StopIteration):
                    continue
            return found
        if head.startswith("[locale="):
            node = next(row for row in node if row.get("locale") == head[8:-1])
        elif head.startswith("["):
            node = node[int(head[1:-1])]
        else:
            node = node[head]
        return select(node, tail)

    return select(value, parts)


async def read_text(
    tool: "AdapterTool", args: dict[str, Any], values: dict[str, Any]
) -> dict[str, Any]:
    """One part of one language's text, or of raw as JSON; join parts and compare sha256."""
    text: Any = None
    field: str = args["field"]
    if field not in ("raw", "json") and "locale" not in args:
        raise AgentError("1001", validation="field locale: missing")
    if field == "json":
        path = args.get("path", "")
        top = re.split(r"[.\[]", path, maxsplit=1)[0]
        if top not in FIELDS:
            raise AgentError("1001", validation="field path: invalid")
        detail = await read_package(
            tool,
            {
                "id": args["id"],
                "fields": [top, "revision"],
                **{key: args[key] for key in ("locales",) if key in args},
            },
            values,
            None,
            bound=False,
        )
        try:
            text = compact(select_path(detail, path))
        except (KeyError, IndexError, TypeError, ValueError, StopIteration):
            raise AgentError("1001", validation="field path: not found") from None
    else:
        detail = await tool.invoke("getAdminPackage", values)
    if field == "raw":
        raw = detail.get("raw")
        text = None if raw is None else compact(raw)
    elif field != "json":
        rows = cast(list[dict[str, Any]], detail.get("i18n") or [])
        none: dict[str, Any] = {}
        text = next((row for row in rows if row.get("locale") == args["locale"]), none).get(field)
    offset: int = args.get("offset", 0)
    revision = detail.get("revision")
    if not isinstance(text, str):
        return {
            "value": None,
            "offset": offset,
            "length": 0,
            "total": 0,
            "sha256": None,
            "revision": revision,
        }
    value = text[offset : offset + args.get("limit", 8000)]
    if size(value) > TEXT_BYTES:
        # Keep each part under the reply cap: CJK takes three bytes, escapes take two.
        low, high = 0, len(value)
        while low < high:
            middle = (low + high + 1) // 2
            low, high = (middle, high) if size(value[:middle]) <= TEXT_BYTES else (low, middle - 1)
        value = value[:low]
    return {
        "value": value,
        "offset": offset,
        "length": len(value),
        "total": len(text),
        "sha256": digest(text),
        "revision": revision,
    }
