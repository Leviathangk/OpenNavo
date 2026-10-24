"""Partial updates must not replay unrelated legacy translations."""

import json
from typing import Any

import pytest
from conftest import Backend, execution_args
from fastmcp import Client


@pytest.mark.parametrize(
    ("name", "read", "write", "label", "extra"),
    [
        ("collection_update", "getAdminCollection", "updateCollection", "title", {"sort": 0}),
        (
            "category_update",
            "getCategoryTree",
            "updateCategory",
            "name",
            {"icon": "lucide:box", "appliesTo": "cask", "visible": True, "hiddenByDefault": False},
        ),
        (
            "feature_upsert",
            "getFeature",
            "updateFeature",
            "title",
            {
                "placement": "home_hero",
                "targetType": "url",
                "url": "https://vendor.example/",
                "status": "draft",
                "sort": 0,
            },
        ),
    ],
)
@pytest.mark.parametrize("change", ["metadata", "language", "source", "delete"])
async def test_update_only_sends_requested_and_source_languages(
    backend: Backend,
    name: str,
    read: str,
    write: str,
    label: str,
    extra: dict[str, Any],
    change: str,
) -> None:
    if change == "delete" and name == "category_update":
        pytest.skip("Categories do not accept null translations")
    secondary = "description" if name == "category_update" else "subtitle"
    source = {label: "Source", secondary: "Keep source"}
    before: dict[str, Any] = {
        "id": 1,
        "slug": "fixture",
        "sourceLocale": "en-US",
        "i18n": {
            "en-US": source,
            "de-DE": {label: "Deutsch", secondary: "Keep German"},
            # Old translations exceed current write limits and must be omitted.
            "fr-FR": {label: "x" * 200, secondary: "x" * 300},
        },
        **extra,
    }
    backend.responses[read] = [before] if read == "getCategoryTree" else before
    args: dict[str, Any] = {"id": 1, "dry_run": True}
    expected: dict[str, Any] = {"en-US": source}
    if change == "language":
        args["i18n"] = {"de-DE": {label: "Changed"}}
        expected["de-DE"] = {label: "Changed", secondary: "Keep German"}
    elif change == "source":
        args["source_locale"] = "de-DE"
        expected = {"de-DE": before["i18n"]["de-DE"]}
    elif change == "delete":
        args["i18n"] = {"de-DE": None}
        expected["de-DE"] = None
    async with Client(backend.server) as client:
        result = await client.call_tool(name, execution_args(name, args), raise_on_error=False)
    assert not result.is_error, result.content
    request = next(request for operation, request in backend.calls if operation == write)
    body = json.loads(request.content)
    assert body["i18n"] == expected
    assert body["sourceLocale"] == args.get("source_locale", "en-US")
    assert before["i18n"]["de-DE"][secondary] == "Keep German"
