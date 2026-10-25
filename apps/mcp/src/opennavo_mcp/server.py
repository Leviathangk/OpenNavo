"""Stateless Streamable HTTP/stdio service entry point."""

import ipaddress
import logging
import sys
from collections.abc import AsyncGenerator, Callable
from contextlib import asynccontextmanager
from typing import Any

import uvicorn
from fastmcp import FastMCP
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Message, Receive, Scope, Send

from opennavo_mcp.agent_client import AgentClient
from opennavo_mcp.auth import PEER, Authenticator, Credential, GatewayVerifier, credential
from opennavo_mcp.config import Settings
from opennavo_mcp.middleware import PermissionMiddleware
from opennavo_mcp.resources import AdapterResource
from opennavo_mcp.tools.manifest import RESOURCE_PERMISSIONS, TOOLS
from opennavo_mcp.tools.runtime import AdapterTool


class PeerMiddleware:
    def __init__(self, app: ASGIApp, trusted: list[str], oauth_issuer: str | None = None):
        self.app = app
        self.oauth_issuer = oauth_issuer
        self.trusted = [ipaddress.ip_network(value) for value in trusted]

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        peer = scope.get("client", ("", 0))[0]
        try:
            address = ipaddress.ip_address(peer)
            headers = dict(scope.get("headers", []))
            if any(address in network for network in self.trusted):
                forwarded = headers.get(b"x-forwarded-for", b"").decode("ascii")
                for candidate in reversed(forwarded.split(",")):
                    if not candidate.strip():
                        continue
                    address = ipaddress.ip_address(candidate.strip())
                    if not any(address in network for network in self.trusted):
                        break
            marker = PEER.set(str(address))
        except (ValueError, UnicodeError):
            await JSONResponse({"error": "invalid peer"}, status_code=400)(scope, receive, send)
            return

        async def oauth_send(message: Message) -> None:
            if (
                self.oauth_issuer
                and scope.get("path") == "/mcp"
                and message["type"] == "http.response.start"
                and message["status"] == 401
            ):
                message = dict(message)
                headers = [
                    (k, v)
                    for k, v in message.get("headers", [])
                    if k.lower() not in {b"www-authenticate", b"cache-control"}
                ]
                challenge = (
                    f'Bearer resource_metadata="{self.oauth_issuer}'
                    '/.well-known/oauth-protected-resource/mcp"'
                )
                headers.extend(
                    [
                        (b"www-authenticate", challenge.encode("ascii")),
                        (b"cache-control", b"no-store"),
                    ]
                )
                message["headers"] = headers
            await send(message)

        try:
            await self.app(scope, receive, oauth_send)
        finally:
            PEER.reset(marker)


def create_server(
    settings: Settings,
    client: AgentClient | None = None,
    credentials: Callable[[], Credential] | None = None,
    authenticator: Authenticator | None = None,
) -> FastMCP[Any]:
    client = client or AgentClient(settings)
    authenticator = authenticator or Authenticator(client)

    @asynccontextmanager
    async def lifespan(_: FastMCP[Any]) -> AsyncGenerator[dict[str, Any]]:
        try:
            yield {}
        finally:
            await client.close()

    server: FastMCP[Any] = FastMCP(
        "OpenNavo",
        version="0.1.0",
        lifespan=lifespan,
        auth=GatewayVerifier(authenticator),
        mask_error_details=True,
        strict_input_validation=True,
        middleware=[
            PermissionMiddleware(authenticator, credentials or (lambda: credential(settings)))
        ],
    )
    for spec in TOOLS:
        server.add_tool(AdapterTool(spec, client, authenticator))
    for uri in RESOURCE_PERMISSIONS:
        server.add_resource(AdapterResource(uri, client))

    @server.custom_route("/healthz", methods=["GET"])
    async def health(_: Request) -> JSONResponse:
        return JSONResponse({"status": "ok"})

    return server


def main() -> None:
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    # Prevent dependency debug/access logs from echoing URLs, requests and model parameters.
    for name in ("httpx2", "httpcore", "fastmcp", "mcp", "uvicorn.access"):
        logging.getLogger(name).setLevel(logging.CRITICAL)
    try:
        settings = Settings.model_validate({})
    except ValueError:
        raise SystemExit("Invalid MCP environment configuration") from None
    server = create_server(settings)
    if settings.transport == "stdio":
        if settings.token is None:
            raise SystemExit("OPENNAVO_TOKEN is required for stdio")
        server.run(transport="stdio", show_banner=False)
    else:
        app = server.http_app(path=settings.path, stateless_http=True)
        uvicorn.run(
            PeerMiddleware(app, settings.trusted_proxies, settings.oauth_issuer),
            host=settings.host,
            port=settings.port,
            proxy_headers=False,
            access_log=False,
            log_level="warning",
        )


if __name__ == "__main__":
    main()
