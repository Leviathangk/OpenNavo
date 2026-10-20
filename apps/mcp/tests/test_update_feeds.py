"""Update feed maintenance preserves dry-run and exact source identifiers."""

import json

from conftest import Backend, execution_args
from fastmcp import Client


async def test_update_feed_upsert_and_disable_dry_run(backend: Backend) -> None:
    async with Client(backend.server) as client:
        for tool, operation, fields in [
            (
                "update_feed_upsert",
                "upsertUpdateFeed",
                {"feed_url": "https://example.org/appcast.xml", "channel": "stable"},
            ),
            ("update_feed_disable", "disableUpdateFeed", {}),
        ]:
            result = await client.call_tool(
                tool,
                execution_args(
                    tool,
                    {
                        "bundle_id": "org.example.Fixture",
                        "source": "sparkle",
                        "dry_run": True,
                        **fields,
                    },
                ),
                raise_on_error=False,
            )
            assert not result.is_error, result.content
            request = next(req for name, req in backend.calls if name == operation)
            assert request.url.params["dryRun"] == "true"
            body = json.loads(request.content)
            assert body["bundleId"] == "org.example.Fixture"
            assert body["source"] == "sparkle"
            assert request.url.path.startswith("/agent-api/update-feeds")


async def test_github_and_vendor_rules_preserve_constraints_in_dry_run(backend: Backend) -> None:
    async with Client(backend.server) as client:
        for source, fields in [
            (
                "github",
                {"github_repo": "example/app", "asset_pattern": r"App-mac-.*\.zip$"},
            ),
            (
                "vendor",
                {
                    "feed_url": "https://example.org/releases.json",
                    "resolver": {
                        "mode": "json",
                        "versionPath": "version",
                        "downloadPath": "url",
                        "sha256Path": "sha256",
                    },
                },
            ),
        ]:
            result = await client.call_tool(
                "update_feed_upsert",
                execution_args(
                    "update_feed_upsert",
                    {
                        "bundle_id": "org.example.Fixture",
                        "source": source,
                        "team_id": "TESTTEAM01",
                        "dry_run": True,
                        **fields,
                    },
                ),
                raise_on_error=False,
            )
            assert not result.is_error, result.content
            request = [req for name, req in backend.calls if name == "upsertUpdateFeed"][-1]
            assert request.url.params["dryRun"] == "true"
            body = json.loads(request.content)
            assert body["source"] == source
            assert body["teamId"] == "TESTTEAM01"
            if source == "github":
                assert body["githubRepo"] == "example/app"
            else:
                assert body["resolver"]["sha256Path"] == "sha256"
