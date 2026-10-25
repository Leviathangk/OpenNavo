from typing import Any

import httpx2
import pytest
from conftest import Backend
from starlette.responses import JSONResponse
from starlette.types import Receive, Scope, Send

from opennavo_mcp.auth import PEER, credential
from opennavo_mcp.config import Settings
from opennavo_mcp.server import PeerMiddleware, create_server


@pytest.mark.parametrize(
    "trusted,forwarded,expected",
    [
        ([], "198.51.100.9", "203.0.113.10"),
        (["203.0.113.0/24"], "198.51.100.9", "198.51.100.9"),
        (["203.0.113.0/24"], "127.0.0.1, 198.51.100.9", "198.51.100.9"),
    ],
)
async def test_peer_cannot_be_spoofed(trusted: list[str], forwarded: str, expected: str) -> None:
    async def endpoint(scope: Scope, receive: Receive, send: Send) -> None:
        await JSONResponse({"ip": PEER.get()})(scope, receive, send)

    transport = httpx2.ASGITransport(
        app=PeerMiddleware(endpoint, trusted), client=("203.0.113.10", 1234)
    )
    async with httpx2.AsyncClient(transport=transport, base_url="http://test") as client:
        result = await client.get(
            "/", headers={"X-Forwarded-For": forwarded, "X-Agent-Client-IP": "127.0.0.1"}
        )
        assert result.json() == {"ip": expected}


async def test_http_auth_health_and_protocol(backend: Backend) -> None:
    server = create_server(backend.settings, backend.client, authenticator=backend.auth)
    app = server.http_app(path="/mcp", stateless_http=True)
    wrapped = PeerMiddleware(app, [])
    async with app.router.lifespan_context(app):
        async with httpx2.AsyncClient(
            transport=httpx2.ASGITransport(app=wrapped, client=("203.0.113.10", 1234)),
            base_url="http://test",
        ) as client:
            assert (await client.get("/healthz")).json() == {"status": "ok"}
            request: dict[str, Any] = {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/list",
                "params": {},
            }
            headers = {"Accept": "application/json, text/event-stream"}
            assert (await client.post("/mcp", json=request, headers=headers)).status_code == 401
            headers["Authorization"] = "Bearer invalid-test-token"
            assert (await client.post("/mcp", json=request, headers=headers)).status_code == 401
            headers["Authorization"] = "Bearer test-only-read"
            result = await client.post("/mcp", json=request, headers=headers)
            assert result.status_code == 200
            assert "packages_search" in result.text and "package_set_text" not in result.text


def test_stdio_requires_token_and_never_uses_http_fallback() -> None:
    settings = Settings.model_validate(
        {
            "AGENT_GATEWAY_SECRET": "test-only-" * 4,
            "OPENNAVO_MCP_TRANSPORT": "stdio",
            "OPENNAVO_TOKEN": "test-only-stdio",
        }
    )
    assert credential(settings).token == "test-only-stdio"  # noqa: S105 — Test sentinel
    settings.transport = "http"
    with pytest.raises(Exception, match="8888"):
        credential(settings)


@pytest.mark.parametrize(
    "value",
    [
        "https://example.com/admin-api",
        "https://user:secret@example.com/agent-api",
        "https://example.com/agent-api?token=secret",
        "file:///agent-api",
    ],
)
def test_backend_base_rejects_credentials_and_wrong_path(value: str) -> None:
    with pytest.raises(ValueError):
        Settings.model_validate(
            {"AGENT_GATEWAY_SECRET": "test-only-" * 4, "OPENNAVO_AGENT_API_BASE": value}
        )


async def test_stdio_uses_same_live_policy(backend: Backend) -> None:
    from fastmcp import Client
    from pydantic import SecretStr

    backend.settings.transport = "stdio"
    backend.settings.token = SecretStr("test-only-read")
    backend.expected_ip = "127.0.0.1"
    server = create_server(backend.settings, backend.client, authenticator=backend.auth)
    async with Client(server) as client:
        names = {tool.name for tool in await client.list_tools()}
        assert "packages_search" in names and "package_set_text" not in names
        denied = await client.call_tool("category_delete", {"id": 1}, raise_on_error=False)
        assert denied.is_error
    assert {op for op, _ in backend.calls} == {"introspectAgentToken"}


async def test_oauth_challenge_uses_configured_issuer(backend: Backend) -> None:
    server = create_server(backend.settings, backend.client, authenticator=backend.auth)
    app = server.http_app(path="/mcp", stateless_http=True)
    wrapped = PeerMiddleware(app, [], "https://admin.example.test")
    async with app.router.lifespan_context(app):
        async with httpx2.AsyncClient(
            transport=httpx2.ASGITransport(app=wrapped), base_url="http://test"
        ) as client:
            result = await client.post("/mcp", json={})
            assert result.status_code == 401
            assert result.headers["www-authenticate"] == (
                'Bearer resource_metadata="https://admin.example.test'
                '/.well-known/oauth-protected-resource/mcp"'
            )
            assert result.headers["cache-control"] == "no-store"


@pytest.mark.parametrize(
    "issuer",
    [
        "http://example.test",
        "https://user:pass@example.test",
        "https://example.test/path",
        "https://example.test?x=1",
    ],
)
def test_oauth_issuer_rejects_non_origin(issuer: str) -> None:
    with pytest.raises(ValueError):
        Settings.model_validate(
            {"AGENT_GATEWAY_SECRET": "test-only-" * 4, "OPENNAVO_OAUTH_ISSUER": issuer}
        )
