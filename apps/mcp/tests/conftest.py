"""Agent API stub with no real credentials, network or infrastructure."""

import json
import re
from collections.abc import Iterator
from contextvars import ContextVar
from pathlib import Path
from typing import Any

import httpx2
import pytest
import yaml

from opennavo_mcp.agent_client import AgentClient
from opennavo_mcp.auth import Authenticator, Credential
from opennavo_mcp.config import Settings
from opennavo_mcp.contracts import OPERATIONS
from opennavo_mcp.server import create_server

ROOT = Path(__file__).resolve().parents[3]
DOCUMENT: dict[str, Any] = yaml.safe_load((ROOT / "apps/server/api/admin.openapi.yaml").read_text())
PERMISSIONS: list[str] = DOCUMENT["components"]["schemas"]["AgentPermission"]["enum"]
CURRENT_TOKEN: ContextVar[str] = ContextVar("test_token", default="test-only-full")


def resolve(schema: dict[str, Any]) -> dict[str, Any]:
    while "$ref" in schema:
        value: Any = DOCUMENT
        for part in schema["$ref"].split("/")[1:]:
            value = value[part]
        schema = value
    return schema


def example(schema: dict[str, Any], required_only: bool = False) -> Any:
    schema = resolve(schema)
    if "example" in schema:
        return json.loads(json.dumps(schema["example"], default=str))
    if "enum" in schema:
        return schema["enum"][0]
    if "allOf" in schema:
        result: dict[str, Any] = {}
        for part in schema["allOf"]:
            result.update(example(part, required_only))
        return result
    if "oneOf" in schema or "anyOf" in schema:
        return example((schema.get("oneOf") or schema["anyOf"])[0], required_only)
    kind = schema.get("type")
    if kind == "object" or "properties" in schema:
        return {
            key: example(value, required_only)
            for key, value in schema.get("properties", {}).items()
            if not required_only or key in schema.get("required", [])
        }
    if kind == "array":
        return [example(schema["items"], required_only) for _ in range(schema.get("minItems", 0))]
    if kind in {"integer", "number"}:
        return schema.get("minimum", 1)
    if kind == "boolean":
        return False
    return {"date-time": "2026-10-07T00:00:00Z", "uri": "https://example.com/image.png"}.get(
        schema.get("format", ""), "test"
    )


class Backend:
    def __init__(self) -> None:
        self.calls: list[tuple[str, httpx2.Request]] = []
        self.permissions = {
            "test-only-full": PERMISSIONS,
            "test-only-read": ["catalog:package:view"],
        }
        self.allow_delete = True
        self.active = True
        self.expected_ip = "203.0.113.10"
        self.code = "0000"
        self.msg = "test-only-sensitive-do-not-echo"
        self.responses: dict[str, Any] = {}
        self.settings = Settings.model_validate({"AGENT_GATEWAY_SECRET": "test-only-" * 4})
        self.http = httpx2.AsyncClient(transport=httpx2.MockTransport(self.handle))
        self.client = AgentClient(self.settings, self.http)
        self.auth = Authenticator(self.client)
        self.server = create_server(
            self.settings,
            self.client,
            lambda: Credential(CURRENT_TOKEN.get(), "203.0.113.10"),
            self.auth,
        )

    def handle(self, request: httpx2.Request) -> httpx2.Response:
        assert request.url.path.startswith("/agent-api/")
        assert request.headers["x-agent-gateway-key"] == "test-only-" * 4
        assert request.headers["x-agent-client-ip"] == self.expected_ip
        operation = next(
            name
            for name, value in OPERATIONS.items()
            if request.method == value["method"]
            and re.fullmatch(
                re.sub(r"\{[^}]+\}", "[^/]+", "/agent-api" + value["path"]), request.url.path
            )
        )
        self.calls.append((operation, request))
        token = request.headers["authorization"].removeprefix("Bearer ")
        if operation == "introspectAgentToken":
            data: Any = {
                "active": self.active and token in self.permissions,
                "paused": False,
                "clientId": 1,
                "tokenId": 2,
                "permissions": self.permissions.get(token, []),
                "allowDelete": self.allow_delete,
                "expiresAt": None,
            }
        elif operation in self.responses:
            data = self.responses[operation]
            if callable(data):
                data = data()
        else:
            spec = OPERATIONS[operation]
            response = resolve(
                DOCUMENT["paths"][spec["path"]][spec["method"].lower()]["responses"]["200"]
            )
            data = example(response["content"]["application/json"]["schema"])["data"]
        return httpx2.Response(200, json={"code": self.code, "msg": self.msg, "data": data})


@pytest.fixture
def backend() -> Iterator[Backend]:
    yield Backend()


def execution_args(name: str, args: dict[str, Any]) -> dict[str, Any]:
    """Supply fictional write-safety headers (retry key, expected revision) to write fixtures."""
    from opennavo_mcp.tools.manifest import BY_NAME

    if not any(OPERATIONS[op]["parameters"].get("header") for op in BY_NAME[name].operations):
        return args
    return {"idempotency_key": "fixture-write-001", "expected_revision": 0, **args}
