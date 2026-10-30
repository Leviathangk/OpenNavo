"""Project six-language previews from admin-returned text without public search or API calls."""

from typing import Any

from opennavo_mcp.contracts import LOCALES


def package_display(package: dict[str, Any]) -> dict[str, Any]:
    rows: dict[str, dict[str, Any]] = {row["locale"]: row for row in package.get("i18n", [])}
    source = package.get("sourceLocale", "en-US")
    previews: dict[str, Any] = {}
    for locale in LOCALES["locales"]:
        requested = locale["code"]
        empty: dict[str, Any] = {}
        selected = next(
            (
                rows[code]
                for code in (requested, "en-US", source)
                if code in rows
                and any(
                    rows[code].get(field) for field in ("displayName", "summary", "description")
                )
            ),
            empty,
        )
        previews[requested] = {
            "name": package["name"],
            "displayName": selected.get("displayName") or package["name"],
            "summary": selected.get("summary") or package.get("descEn"),
            "description": selected.get("description"),
            "sourceLocale": selected.get("sourceLocale", source),
            "textLocale": selected.get("locale", source),
            "translationStatus": selected.get("status", "none"),
        }
    return {
        "visible": not any(package.get(flag, False) for flag in ("hidden", "private", "removed")),
        "webUrl": package.get("webUrl"),
        "locales": previews,
    }
