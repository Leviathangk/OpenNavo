"""The only outbound channel; accepts only generated allowlisted operationIds."""

import json
import logging
from typing import Any, cast
from urllib.parse import quote
from uuid import uuid4

import httpx2
from fastmcp.exceptions import ToolError
from pydantic import ValidationError

from opennavo_mcp.config import Settings
from opennavo_mcp.contracts import OPERATIONS, input_model

logger = logging.getLogger(__name__)
MESSAGES = {
    "1001": "Invalid parameters",
    "1002": "Object not found",
    "1003": "State conflict; read the object again",
    "1004": "Permission or deletion policy denies this operation",
    "1005": "Call quota exceeded",
    "1006": "Operation not allowed in the current state",
    "1007": "Invalid image or upload URL",
    "7777": "Token revoked",
    "8888": "Token invalid or expired",
    "8889": "Agent paused or disabled",
    "1301": "Upstream service temporarily unavailable",
    "1302": "Translation failed or budget exhausted",
    "5000": "Backend temporarily unavailable",
}


class AgentError(ToolError):
    def __init__(self, code: str):
        self.code = code if code in MESSAGES else "5000"
        super().__init__(f"{self.code}: {MESSAGES[self.code]}")


class AgentClient:
    def __init__(self, settings: Settings, http: httpx2.AsyncClient | None = None):
        self.settings = settings
        self.http = http or httpx2.AsyncClient(timeout=30, follow_redirects=False, trust_env=False)

    async def close(self) -> None:
        await self.http.aclose()

    async def call(
        self, operation: str, token: str, ip: str, tool: str, values: dict[str, Any] | None = None
    ) -> Any:
        if operation not in OPERATIONS:
            raise AgentError("1004")
        spec = OPERATIONS[operation]
        try:
            model = input_model(operation).model_validate_json(
                json.dumps(values or {}), strict=True
            )
            data = model.model_dump(mode="json", exclude_unset=True, by_alias=True)
        except (ValidationError, ValueError, TypeError):
            raise AgentError("1001") from None
        path = spec["path"]
        for key in spec["parameters"]["path"]:
            value = str(data.pop(key))
            if value in {".", ".."} or "/" in value or "\\" in value:
                raise AgentError("1001")
            path = path.replace("{" + key + "}", quote(value, safe=""))
        query: dict[str, Any] = {}
        for key in spec["parameters"]["query"]:
            if key in data:
                value = data.pop(key)
                query[key] = (
                    ",".join(map(str, cast(list[Any], value))) if isinstance(value, list) else value
                )
        body = data.pop("body", None)
        headers = {
            "Authorization": "Bearer " + token,
            "X-Agent-Gateway-Key": self.settings.gateway_secret.get_secret_value(),
            "X-Agent-Client-IP": ip,
            "X-Agent-Tool": tool,
            "X-Request-ID": str(uuid4()),
        }
        for key in spec["parameters"].get("header", []):
            if key in data:
                value = data.pop(key)
                headers[spec["headerNames"][key]] = (
                    ",".join(map(str, cast(list[Any], value)))
                    if isinstance(value, list)
                    else str(value)
                )
        kwargs: dict[str, Any] = {"params": query, "headers": headers}
        if operation == "uploadAsset":
            kwargs["files"] = {key: (None, str(value)) for key, value in body.items()}
        elif body is not None:
            kwargs["json"] = body
        try:
            response = await self.http.request(
                spec["method"], self.settings.agent_api_base + path, **kwargs
            )
            if response.status_code != 200 or len(response.content) > 8 * 1024 * 1024:
                raise AgentError("5000")
            payload = response.json()
            if not isinstance(payload, dict):
                raise AgentError("5000")
            payload = cast(dict[str, Any], payload)
            if not isinstance(payload.get("code"), str):
                raise AgentError("5000")
            if payload["code"] != "0000":
                raise AgentError(payload["code"])
            return payload.get("data")
        except (httpx2.HTTPError, ValueError, TypeError):
            # Raw exceptions may contain URLs or request headers; never forward or print them.
            logger.warning("Agent API request failed (%s)", operation)
            raise AgentError("5000") from None
