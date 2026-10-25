"""The only outbound channel; accepts only generated allowlisted operationIds."""

import json
import logging
import re
from collections.abc import Sequence
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


# Forward only short, structured rejection reasons; never arbitrary backend text.
REJECTION = re.compile(r"rejected: [a-z][a-z0-9_]{0,63}(:[A-Za-z0-9_.\[\]-]{1,96})?")


class AgentError(ToolError):
    def __init__(self, code: str, detail: object = None, *, validation: str = ""):
        self.code = code if code in MESSAGES else "5000"
        message = f"{self.code}: {MESSAGES[self.code]}"
        if self.code != "5000" and isinstance(detail, str) and REJECTION.fullmatch(detail):
            message += f" ({detail})"
        if self.code == "1001" and validation:
            message += f" ({validation})"
        super().__init__(message)


def field_detail(loc: Sequence[str | int], kind: str, schema: dict[str, Any]) -> str:
    # loc may contain caller-controlled dictionary keys. Only expose schema field names
    # and list indices, never input values, custom error messages or context.
    fields: set[str] = set()

    def collect(node: Any) -> None:
        if isinstance(node, dict):
            node = cast(dict[str, Any], node)
            fields.update(node.get("properties", {}))
            for child in node.values():
                collect(child)
        elif isinstance(node, list):
            for child in cast(list[Any], node):
                collect(child)

    collect(schema)
    branches = set(schema.get("$defs", {}))
    path = (
        ".".join(
            str(part) if isinstance(part, int) or part in fields else "<key>"
            for part in loc
            if part not in branches
        )
        or "request"
    )
    if not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", kind):
        kind = "invalid"
    return f"field {path[:200]}: {kind}"


def validation_detail(error: ValidationError, schema: dict[str, Any]) -> str:
    details: list[str] = []
    for item in error.errors(include_url=False, include_context=False, include_input=False):
        loc = list(item["loc"])
        if loc and loc[0] == "body":
            loc.pop(0)
        detail = field_detail(loc, item["type"], schema)
        if detail not in details:
            details.append(detail)
        if len(details) == 3:
            break
    return "; ".join(details)


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
        # Optional query values have no clearing semantics. Keep body nulls intact.
        supplied = dict(values or {})
        model_type = input_model(operation)
        optional_query = {
            field.alias or name
            for name, field in model_type.model_fields.items()
            if not field.is_required()
        } & set(spec["parameters"]["query"])
        omitted = sorted(
            key for key in optional_query if key in supplied and supplied[key] in (None, "")
        )
        for key in omitted:
            supplied.pop(key)
        try:
            model = model_type.model_validate_json(json.dumps(supplied), strict=True)
            data = model.model_dump(mode="json", exclude_unset=True, by_alias=True)
        except ValidationError as error:
            raise AgentError(
                "1001",
                validation=validation_detail(error, input_model(operation).model_json_schema()),
            ) from None
        except (ValueError, TypeError):
            raise AgentError("1001", validation="field request: json_invalid") from None
        path = spec["path"]
        for key in spec["parameters"]["path"]:
            value = str(data.pop(key))
            if value in {".", ".."} or "/" in value or "\\" in value:
                raise AgentError(
                    "1001",
                    validation=field_detail(
                        [key], "path_invalid", input_model(operation).model_json_schema()
                    ),
                )
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
        if operation == "listAdminCatalogChanges":
            # Only schema-validated catalog filters; never headers, tokens, or free text.
            logger.info(
                "Catalog request path=%s query=%s omitted=%s request_id=%s",
                path,
                query,
                omitted,
                headers["X-Request-ID"],
            )
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
                raise AgentError(payload["code"], payload.get("msg"))
            return payload.get("data")
        except (httpx2.HTTPError, ValueError, TypeError):
            # Raw exceptions may contain URLs or request headers; never forward or print them.
            logger.warning("Agent API request failed (%s)", operation)
            raise AgentError("5000") from None
