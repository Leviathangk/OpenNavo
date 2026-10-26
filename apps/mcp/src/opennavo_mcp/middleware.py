"""Enforce authorization for stdio too; HTTP-only component auth checks are insufficient."""

from collections.abc import Callable
from typing import Any

from fastmcp.server.middleware import CallNext, Middleware, MiddlewareContext
from mcp.shared.exceptions import MCPError

from opennavo_mcp.agent_client import AgentError, denied
from opennavo_mcp.auth import CURRENT, Authenticator, Credential
from opennavo_mcp.tools.manifest import BY_NAME, RESOURCE_PERMISSIONS


class PermissionMiddleware(Middleware):
    def __init__(self, authenticator: Authenticator, credentials: Callable[[], Credential]):
        self.authenticator, self.credentials = authenticator, credentials

    async def on_request(
        self, context: MiddlewareContext[Any], call_next: CallNext[Any, Any]
    ) -> Any:
        cred = self.credentials()
        try:
            identity = await self.authenticator.authenticate(cred)
        except AgentError as error:
            raise MCPError(-32001, str(error)) from None
        marker = CURRENT.set((cred, identity))
        try:
            return await call_next(context)
        finally:
            CURRENT.reset(marker)

    async def on_list_tools(
        self, context: MiddlewareContext[Any], call_next: CallNext[Any, Any]
    ) -> Any:
        _, identity = CURRENT.get()
        tools = await call_next(context)
        return [
            tool
            for tool in tools
            if tool.name in BY_NAME
            and identity.allows(BY_NAME[tool.name].permission)
            and (not BY_NAME[tool.name].delete or identity.allow_delete)
        ]

    async def on_call_tool(
        self, context: MiddlewareContext[Any], call_next: CallNext[Any, Any]
    ) -> Any:
        _, identity = CURRENT.get()
        spec = BY_NAME.get(context.message.name)
        if spec is None:
            return denied("operation_not_exposed").result()
        if not identity.allows(spec.permission):
            return denied("permission_required:" + spec.permission).result()
        if spec.delete and not identity.allow_delete:
            return denied("delete_not_allowed").result()
        return await call_next(context)

    async def on_list_resources(
        self, context: MiddlewareContext[Any], call_next: CallNext[Any, Any]
    ) -> Any:
        _, identity = CURRENT.get()
        resources = await call_next(context)
        return [
            resource
            for resource in resources
            if str(resource.uri) in RESOURCE_PERMISSIONS
            and identity.allows(RESOURCE_PERMISSIONS[str(resource.uri)])
        ]

    async def on_read_resource(
        self, context: MiddlewareContext[Any], call_next: CallNext[Any, Any]
    ) -> Any:
        _, identity = CURRENT.get()
        permission = RESOURCE_PERMISSIONS.get(str(context.message.uri))
        if permission is None:
            raise denied("operation_not_exposed")
        if not identity.allows(permission):
            raise denied("permission_required:" + permission)
        return await call_next(context)
