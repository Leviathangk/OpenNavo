"""Package reads stay usable on platforms that cut long tool results."""

import hashlib
import json
import re
from typing import Any, cast

import pytest
from conftest import Backend
from fastmcp import Client

from opennavo_mcp.tools.packages import LIMIT, TEXT_BYTES, size

INTRO = {
    "en-US": "WordCraft helps you draft, outline and revise long documents. ",
    "zh-CN": "WordCraft 帮你起草、整理大纲并修订长篇文档，支持版本和导出。",
    "ja-JP": "WordCraft は長い文書の下書き、構成、推敲を助けます。",
    "es-ES": "WordCraft te ayuda a redactar, estructurar y revisar documentos largos. ",
    "pt-BR": "WordCraft ajuda a redigir, estruturar e revisar documentos longos. ",
    "ru-RU": "WordCraft помогает писать, структурировать и править длинные документы. ",
    "de-DE": "WordCraft hilft beim Entwerfen, Gliedern und Überarbeiten langer Dokumente. ",
    "fr-FR": "WordCraft vous aide à rédiger, structurer et réviser de longs documents. ",
    "it-IT": "WordCraft ti aiuta a scrivere, strutturare e rivedere documenti lunghi. ",
    "zh-Hant": "WordCraft 幫你起草、整理大綱並修訂長篇文件，支援版本和匯出。",
}


def text(locale: str, chars: int) -> str:
    sample = INTRO[locale] + '"Quote" \\ path\n\n'
    return (sample * (chars // len(sample) + 1))[:chars]


def wordcraft() -> dict[str, Any]:
    """A production-sized detail: ten long introductions, Homebrew raw JSON, captioned shots."""
    lengths = {"zh-CN": 2400, "ja-JP": 2400, "zh-Hant": 2400, "ru-RU": 4800}
    i18n = [
        {
            "locale": locale,
            "displayName": "WordCraft",
            "summary": text(locale, 160),
            "description": text(locale, lengths.get(locale, 5900)),
            "status": "source" if locale == "en-US" else "machine",
            "source": "human",
            "sourceLocale": "en-US",
            "sourceHash": "a" * 64,
            "translatedAt": "2026-10-20T08:00:00Z",
        }
        for locale in INTRO
    ]
    captions = {
        locale: {
            "caption": text(locale, 90),
            "status": "machine",
            "sourceLocale": "en-US",
            "sourceHash": "b" * 64,
            "model": "fixture",
            "translatedAt": "2026-10-20T08:00:00Z",
        }
        for locale in INTRO
    }
    return {
        "id": 8559,
        "kind": "cask",
        "token": "wordcraft",
        "name": "WordCraft",
        "version": "3.2.1",
        "revision": 41,
        "sourceLocale": "en-US",
        "displayName": {locale: "WordCraft" for locale in INTRO},
        "summary": {locale: text(locale, 160) for locale in INTRO},
        "descEn": "Writing app for long documents",
        "webUrl": "https://opennavo.example/apps/wordcraft",
        "i18n": i18n,
        "screenshots": [
            {"id": n, "assetId": n, "url": f"https://opennavo.example/s/{n}.png", "i18n": captions}
            for n in range(1, 4)
        ],
        "raw": {
            "token": "wordcraft",
            "artifacts": [{"app": ["WordCraft.app"]}] * 20,
            "caveats": text("en-US", 1200),
            "variations": {f"os_{n}": {"url": f"https://example.com/{n}.dmg"} for n in range(30)},
        },
    }


COMPACT: dict[str, Any] = {"ensure_ascii": False, "separators": (",", ":")}
FACTS: dict[str, Any] = {
    "packageId": 8559,
    "price": {"model": "subscription", "free": False, "trial": True},
    "macos": {"minimum": "13.0", "arm64": True, "intel": True},
    "homebrew": {"installs30d": 1200, "rank30d": 512},
    "conflicts": [],
    "alternatives": [{"token": f"alternative-{n}", "reason": "Fixture reason"} for n in range(6)],
    "offerReady": True,
    "gaps": ["primaryCategory"],
    "updatedAt": "2026-10-20T08:00:00Z",
}


def leaves(node: Any, path: str = "") -> list[tuple[str, Any]]:
    """String leaves with package_get's path syntax: key.key, [locale=x] rows, [n] items."""
    found: list[tuple[str, Any]] = []
    if isinstance(node, dict):
        for key, value in cast(dict[str, Any], node).items():
            found += leaves(value, f"{path}.{key}" if path else key)
    elif isinstance(node, list):
        for index, item in enumerate(cast(list[Any], node)):
            locale = cast(dict[str, Any], item).get("locale") if isinstance(item, dict) else None
            found += leaves(item, f"{path}[{f'locale={locale}' if locale else index}]")
    elif isinstance(node, str):
        found.append((path, node))
    return found


def lookup(node: Any, path: str) -> Any:
    for part in re.findall(r"\[[^\]]+\]|[^.\[\]]+", path):
        if part.startswith("[locale="):
            node = next(row for row in node if row.get("locale") == part[8:-1])
        elif part.startswith("["):
            node = node[int(part[1:-1])]
        else:
            node = node[part]
    return node


def sha256(value: Any) -> str:
    data = value if isinstance(value, str) else json.dumps(value, **COMPACT)
    return hashlib.sha256(data.encode()).hexdigest()


async def call(client: Client[Any], tool: str, args: dict[str, Any]) -> dict[str, Any]:
    result = await client.call_tool(tool, args)
    assert not result.is_error, result.content
    assert result.structured_content is not None
    return result.structured_content["untrusted"]


async def read_parts(client: Client[Any], arguments: dict[str, Any]) -> tuple[str, dict[str, Any]]:
    """Read a text in parts the way the tool description tells agents to."""
    parts: list[str] = []
    part = await call(client, "package_text_get", arguments | {"offset": 0})
    while True:
        assert part["length"] == len(part["value"]) <= arguments.get("limit", 8000)
        assert size(part["value"]) <= TEXT_BYTES and size(part) <= LIMIT
        parts.append(part["value"])
        offset = part["offset"] + part["length"]
        if offset >= part["total"]:
            return "".join(parts), part
        part = await call(client, "package_text_get", arguments | {"offset": offset})


def description(locale: str, limit: int = 8000) -> dict[str, Any]:
    return {"package": 8559, "locale": locale, "field": "description", "limit": limit}


def select(node: Any, path: str) -> Any:
    """The value at path; [*] collects the entry from every list item that has it."""
    if "[*]" not in path:
        return lookup(node, path)
    head, tail = path.split("[*].", 1)
    found: list[Any] = []
    for item in lookup(node, head):
        try:
            found.append(lookup(item, tail))
        except KeyError:
            continue
    return found


async def read_back(client: Client[Any], item: dict[str, Any]) -> Any:
    """Run an omitted entry's read call and return the value it names."""
    tool, arguments = item["read"]["tool"], item["read"]["arguments"]
    if tool == "package_text_get":
        value = (await read_parts(client, arguments))[0]
        return json.loads(value) if arguments["field"] == "json" else value
    reply = await call(client, tool, arguments)
    assert item["path"] not in [entry["path"] for entry in reply.get("omitted", [])]
    return select(reply, item["path"])


def operations(backend: Backend) -> list[str]:
    return [op for op, _ in backend.calls if op != "introspectAgentToken"]


async def test_fields_and_locales_trim_the_detail(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    async with Client(backend.server) as client:
        reply = await call(
            client,
            "package_get",
            {"package": 8559, "fields": ["i18n", "summary"], "locales": ["en-US", "zh-CN"]},
        )
    assert set(reply) == {"id", "token", "revision", "i18n", "summary"}
    assert (reply["id"], reply["token"], reply["revision"]) == (8559, "wordcraft", 41)
    assert [row["locale"] for row in reply["i18n"]] == ["en-US", "zh-CN"]
    assert set(reply["summary"]) == {"en-US", "zh-CN"}
    assert reply["i18n"][1] == wordcraft()["i18n"][1]  # kept rows are complete
    assert operations(backend) == ["getAdminPackage"]


async def test_locales_trim_captions_without_fields(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    async with Client(backend.server) as client:
        reply = await call(client, "package_get", {"package": 8559, "locales": ["ja-JP"]})
    assert "omitted" not in reply and size(reply) <= LIMIT
    assert [row["locale"] for row in reply["i18n"]] == ["ja-JP"]
    assert all(set(shot["i18n"]) == {"ja-JP"} for shot in reply["screenshots"])
    assert set(reply["displayName"]) == {"ja-JP"} and "raw" in reply


@pytest.mark.parametrize("package", [8559, "wordcraft"])
async def test_facts_alone_skip_the_detail(backend: Backend, package: int | str) -> None:
    backend.responses["listAdminPackages"] = {"records": [{"id": 8559, "token": "wordcraft"}]}
    backend.responses["getPackageFacts"] = FACTS
    async with Client(backend.server) as client:
        reply = await call(client, "package_get", {"package": package, "fields": ["facts"]})
    # The token comes back only when the caller passed it; revision needs the detail read.
    expected: dict[str, Any] = {"id": 8559, "facts": FACTS}
    if isinstance(package, str):
        expected |= {"token": package}
    assert reply == expected
    assert "getAdminPackage" not in operations(backend)


async def test_revision_with_facts_reads_the_detail_once(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    backend.responses["getPackageFacts"] = FACTS
    async with Client(backend.server) as client:
        reply = await call(
            client, "package_get", {"package": 8559, "fields": ["facts", "revision"]}
        )
    assert reply == {"id": 8559, "token": "wordcraft", "revision": 41, "facts": FACTS}
    assert sorted(operations(backend)) == ["getAdminPackage", "getPackageFacts"]


async def test_oversized_reply_lists_whole_omitted_values(backend: Backend) -> None:
    original = wordcraft()
    backend.responses["getAdminPackage"] = wordcraft()
    async with Client(backend.server) as client:
        reply = await call(client, "package_get", {"package": 8559})
    assert size(original) > 2 * LIMIT and size(reply) <= LIMIT
    assert reply["hint"] and reply["omitted"]
    paths = [item["path"] for item in reply["omitted"]]
    for item in reply["omitted"]:
        value = lookup(original, item["path"])
        chars = len(value) if isinstance(value, str) else len(json.dumps(value, **COMPACT))
        assert (item["chars"], item["sha256"]) == (chars, sha256(value))
    # Largest first: the CJK introductions (three bytes a character) go before Latin ones.
    sizes = [size(lookup(original, path)) for path in paths]
    assert sizes == sorted(sizes, reverse=True)
    assert "i18n[locale=zh-CN].description" in paths
    # Everything still present is whole; nothing omitted is half there.
    for path, value in leaves({k: v for k, v in reply.items() if k not in ("omitted", "hint")}):
        assert value == lookup(original, path), path
    for path in paths:
        with pytest.raises((KeyError, StopIteration)):
            lookup(reply, path)


async def test_text_parts_rebuild_an_omitted_description(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    async with Client(backend.server) as client:
        reply = await call(client, "package_get", {"package": 8559})
        joined, last = await read_parts(client, description("ru-RU", 1500))
    entry = next(e for e in reply["omitted"] if e["path"] == "i18n[locale=ru-RU].description")
    assert joined == lookup(wordcraft(), "i18n[locale=ru-RU].description")
    assert sha256(joined) == last["sha256"] == entry["sha256"]
    assert last["total"] == entry["chars"] and last["revision"] == 41


async def test_text_parts_stay_under_the_reply_cap(backend: Backend) -> None:
    detail = wordcraft()
    detail["i18n"][1]["description"] = text("zh-CN", 16000)  # 48 KB of CJK text
    backend.responses["getAdminPackage"] = detail
    async with Client(backend.server) as client:
        joined, last = await read_parts(client, description("zh-CN", 16000))
    assert joined == detail["i18n"][1]["description"] and last["total"] == 16000
    assert sha256(joined) == last["sha256"]


async def test_missing_text_is_null(backend: Backend) -> None:
    detail = wordcraft()
    detail["i18n"] = detail["i18n"][:1]
    backend.responses["getAdminPackage"] = detail
    async with Client(backend.server) as client:
        part = await call(
            client,
            "package_text_get",
            {"package": 8559, "locale": "de-DE", "field": "description"},
        )
    assert part == {
        "value": None,
        "offset": 0,
        "length": 0,
        "total": 0,
        "sha256": None,
        "revision": 41,
    }


async def test_small_detail_is_returned_unchanged(backend: Backend) -> None:
    detail = wordcraft()
    detail["i18n"], detail["raw"] = detail["i18n"][:1], {}
    backend.responses["getAdminPackage"] = detail
    async with Client(backend.server) as client:
        assert await call(client, "package_get", {"package": 8559}) == detail


@pytest.mark.parametrize("fields", [None, ["i18n"]])
async def test_locale_still_reaches_the_detail_read(
    backend: Backend, fields: list[str] | None
) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    args: dict[str, Any] = {"package": 8559, "locale": "ja-JP"}
    async with Client(backend.server) as client:
        await call(client, "package_get", args | ({"fields": fields} if fields else {}))
    request = next(req for op, req in backend.calls if op == "getAdminPackage")
    assert dict(request.url.params) == {"locale": "ja-JP"}


def screenshot(n: int) -> dict[str, Any]:
    shot = dict(wordcraft()["screenshots"][0])
    return shot | {"id": n, "assetId": n, "url": f"https://opennavo.example/s/{n}.png"}


def huge_raw() -> dict[str, Any]:
    detail = wordcraft()
    detail["raw"]["variations"] = {
        f"os_{n}": {"url": f"https://example.com/{n}.dmg"} for n in range(1500)
    }
    return detail


def many_screenshots() -> dict[str, Any]:
    detail = wordcraft()
    detail["screenshots"] = [screenshot(n) for n in range(1, 101)]
    return detail


@pytest.mark.parametrize(
    ("detail", "include"),
    [
        (wordcraft, []),
        (wordcraft, ["screenshots", "translations", "facts", "display"]),
        (many_screenshots, []),
        (huge_raw, []),
    ],
    ids=["default", "include-all", "many-screenshots", "raw-over-32kb"],
)
async def test_every_omitted_value_reads_back_whole(
    backend: Backend, detail: Any, include: list[str]
) -> None:
    original = detail()
    backend.responses["getAdminPackage"] = detail()
    backend.responses["listPackageScreenshots"] = detail()["screenshots"]
    backend.responses["getPackageFacts"] = FACTS
    async with Client(backend.server) as client:
        reply = await call(client, "package_get", {"package": 8559, "include": include})
        assert size(reply) <= LIMIT and reply["omitted"]
        for item in reply["omitted"]:
            value = await read_back(client, item)
            chars = len(value) if isinstance(value, str) else len(json.dumps(value, **COMPACT))
            assert (sha256(value), chars) == (item["sha256"], item["chars"]), item["path"]
    reads = {item["read"]["tool"] for item in reply["omitted"]}
    if detail is huge_raw:
        raw = next(item for item in reply["omitted"] if item["path"] == "raw")
        assert raw["read"]["arguments"] == {"package": 8559, "field": "raw"}
        assert raw["chars"] > LIMIT and sha256(original["raw"]) == raw["sha256"]
    if detail is many_screenshots:
        # One entry per language of the captions, read back with that language alone.
        groups = [item for item in reply["omitted"] if item["path"].startswith("screenshots[*]")]
        assert groups and reads == {"package_text_get"}
        for item in groups:
            locale = item["path"].rsplit(".", 1)[1]
            assert item["read"]["arguments"] == {
                "package": 8559,
                "field": "json",
                "path": f"screenshots[*].i18n.{locale}",
            }


async def test_text_needs_a_locale_except_raw(backend: Backend) -> None:
    backend.responses["getAdminPackage"] = wordcraft()
    async with Client(backend.server) as client:
        missing = await client.call_tool(
            "package_text_get", {"package": 8559, "field": "summary"}, raise_on_error=False
        )
        assert missing.is_error and "field locale: missing" in str(missing.content)
        assert "getAdminPackage" not in operations(backend)
        raw, last = await read_parts(client, {"package": 8559, "field": "raw"})
    assert json.loads(raw) == wordcraft()["raw"] and last["sha256"] == sha256(wordcraft()["raw"])


@pytest.mark.parametrize("caption_chars", [90, 2000])
async def test_single_language_screenshots_are_recoverable(
    backend: Backend, caption_chars: int
) -> None:
    shots = [screenshot(n) for n in range(100)]
    for shot in shots:
        # Separate dictionaries also let us test varied values and original-value digests.
        shot["i18n"] = {
            "zh-CN": {"caption": text("zh-CN", caption_chars) + str(shot["id"])},
            "en-US": {"caption": "Not requested"},
        }
        shot["url"] += "x" * 400  # Force whole-block omission after caption omission.
    backend.responses["listPackageScreenshots"] = shots
    backend.responses["getAdminPackage"] = {"id": 8559, "revision": 41}
    original = {
        "screenshots": [shot | {"i18n": {"zh-CN": shot["i18n"]["zh-CN"]}} for shot in shots]
    }
    async with Client(backend.server) as client:
        reply = await call(
            client,
            "package_get",
            {"package": 8559, "fields": ["screenshots"], "locales": ["zh-CN"]},
        )
        assert size(reply) <= LIMIT
        assert reply["omitted"]
        for item in reply["omitted"]:
            value = await read_back(client, item)
            assert value == select(original, item["path"])
            assert sha256(value) == item["sha256"]
