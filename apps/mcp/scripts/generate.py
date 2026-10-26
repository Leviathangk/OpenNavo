"""Read contracts only at build time; runtime never reads the repository or invokes generators."""

from __future__ import annotations

import argparse
import json
from datetime import date, datetime
from pathlib import Path
from typing import Any, cast

import yaml
from datamodel_code_generator import InputFileType, LiteralType, PythonVersion, generate
from datamodel_code_generator.enums import DataModelType

ROOT = Path(__file__).resolve().parents[3]
DEST = ROOT / "apps/mcp/src/opennavo_mcp/gen"


def json_scalar(value: Any) -> str:
    # YAML timestamps must remain RFC 3339 when emitted into JSON schema examples.
    if isinstance(value, (datetime, date)):
        return value.isoformat()
    raise TypeError(f"Unsupported schema value: {type(value).__name__}")


def generate_files() -> dict[str, str]:
    document: dict[str, Any] = yaml.safe_load(
        (ROOT / "apps/server/api/admin.openapi.yaml").read_text()
    )
    schemas = document["components"]["schemas"]
    operations: dict[str, Any] = {}

    def resolve(value: dict[str, Any]) -> dict[str, Any]:
        if "$ref" not in value:
            return value
        result: Any = document
        for part in value["$ref"].split("/")[1:]:
            result = result[part]
        return result

    for path, item in document["paths"].items():
        for method, operation in item.items():
            policy = (
                cast(dict[str, Any], operation).get("x-agent-access")
                if isinstance(operation, dict)
                else None
            )
            if not isinstance(policy, dict):
                continue
            operation = cast(dict[str, Any], operation)
            name: str = operation["operationId"]
            properties: dict[str, Any] = {}
            required: list[str] = []
            parameters: dict[str, list[str]] = {"path": [], "query": [], "header": []}
            # Only the generic write-safety headers cross MCP; other headers are never forwarded.
            write_headers = {
                "X-Expected-Revision": "expectedRevision",
                "Idempotency-Key": "idempotencyKey",
            }
            header_names: dict[str, str] = {}
            for raw in item.get("parameters", []) + operation.get("parameters", []):
                param = resolve(raw)
                location = param["in"]
                if location not in parameters:
                    continue
                key = param["name"]
                if location == "header":
                    if key not in write_headers:
                        continue
                    header_names[write_headers[key]] = key
                    key = write_headers[key]
                parameters[location].append(key)
                properties[key] = param["schema"]
                if param.get("required"):
                    required.append(key)
            body = operation.get("requestBody")
            body_schema: dict[str, Any] | None = None
            if body:
                body = resolve(body)
                contents = body["content"]
                media = (
                    "application/json" if "application/json" in contents else "multipart/form-data"
                )
                body_schema = contents[media]["schema"]
                properties["body"] = body_schema
                if body.get("required"):
                    required.append("body")
            model = name[0].upper() + name[1:] + "Input"
            schemas[model] = {
                "type": "object",
                "properties": properties,
                "required": required,
                "additionalProperties": False,
            }
            operations[name] = {
                "path": path,
                "method": method.upper(),
                "model": model,
                "parameters": parameters,
                "headerNames": header_names,
                "policy": policy,
                "summary": operation.get("summary", ""),
                "body": body_schema,
            }
    rendered = generate(
        json.dumps(document, default=json_scalar),
        input_file_type=InputFileType.OpenAPI,
        output_model_type=DataModelType.PydanticV2BaseModel,
        target_python_version=PythonVersion.PY_313,
        disable_timestamp=True,
        field_constraints=True,
        strict_nullable=True,
        # An omitted property is not JSON null. Preserve OpenAPI's distinction so
        # tool schemas and Pydantic validation agree with the Go request validator.
        use_missing_sentinel=True,
        use_annotated=True,
        use_standard_collections=True,
        use_union_operator=True,
        enum_field_as_literal=LiteralType.All,
        allow_remote_refs=False,
        formatters=[],
    )
    if not isinstance(rendered, str):
        raise RuntimeError("model generator returned no source")
    return {
        "admin_models.py": rendered,
        "operations.json": json.dumps(
            operations, default=json_scalar, ensure_ascii=False, indent=2, sort_keys=True
        )
        + "\n",
        "locales.json": (ROOT / "packages/shared/src/locales.json").read_text(),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    for name, text in generate_files().items():
        path = DEST / name
        if args.check:
            if not path.exists() or path.read_text() != text:
                raise SystemExit(f"Generated file differs: {name}")
        else:
            path.write_text(text)


if __name__ == "__main__":
    main()
