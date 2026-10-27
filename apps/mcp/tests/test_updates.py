"""Partial updates send deterministic patches for durable server-side replay."""

import json
from typing import Any

import httpx2
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
async def test_update_only_sends_supplied_fields(
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
    expected: dict[str, Any] = {}
    if change == "language":
        args["i18n"] = {"de-DE": {label: "Changed"}}
        expected["de-DE"] = {label: "Changed"}
    elif change == "source":
        args["source_locale"] = "de-DE"
    elif change == "delete":
        args["i18n"] = {"de-DE": None}
        expected["de-DE"] = None
    async with Client(backend.server) as client:
        result = await client.call_tool(name, execution_args(name, args), raise_on_error=False)
    assert not result.is_error, result.content
    request = next(request for operation, request in backend.calls if operation == write)
    body = json.loads(request.content)
    assert body.get("i18n", {}) == expected
    assert body.get("sourceLocale") == args.get("source_locale")
    assert not any(operation == read for operation, _ in backend.calls)
    assert before["i18n"]["de-DE"][secondary] == "Keep German"


@pytest.mark.parametrize(
    ("name", "operation", "patch"),
    [
        ("category_update", "updateCategory", {"visible": False}),
        ("collection_update", "updateCollection", {"sort": 3}),
        ("feature_upsert", "updateFeature", {"i18n": {"de-DE": {"badge": "Changed"}}}),
    ],
)
@pytest.mark.parametrize("revision", [None, 7])
@pytest.mark.parametrize("expected_state", [None, "a" * 64])
async def test_retry_after_lost_response_sends_identical_request(
    backend: Backend,
    name: str,
    operation: str,
    patch: dict[str, Any],
    revision: int | None,
    expected_state: str | None,
) -> None:
    args = {"id": 1, "idempotency_key": "lost-response-001", **patch}
    if revision is not None:
        args["expected_revision"] = revision
    if expected_state is not None:
        args["expected_state"] = expected_state
    signatures: list[tuple[bytes, str | None]] = []

    def write() -> dict[str, bool]:
        request = backend.calls[-1][1]
        signatures.append((request.content, request.headers.get("x-expected-revision")))
        if len(signatures) == 1:
            # The server committed, but the transport lost its response. Reads
            # after that commit would now return a different revision/content.
            for read in ("getCategoryTree", "getAdminCollection", "getFeature"):
                backend.responses[read] = {"revision": 8, "i18n": {"en-US": {"title": "Later"}}}
            raise httpx2.ReadError("fixture response lost")
        assert signatures[0] == signatures[1]
        return {"replayed": True}

    backend.responses[operation] = write
    async with Client(backend.server) as client:
        lost = await client.call_tool(name, args, raise_on_error=False)
        assert lost.is_error
        replay = await client.call_tool(name, args, raise_on_error=False)
    assert not replay.is_error, replay.content
    assert replay.structured_content == {"untrusted": {"replayed": True}}
    assert len(signatures) == 2
    assert signatures[0][1] == (None if revision is None else str(revision))
