import json

from jsonschema import Draft202012Validator

from opennavo_mcp.tools.manifest import TOOLS
from opennavo_mcp.tools.runtime import tool_schema


def test_traditional_is_readable_but_not_a_write_target():
    tools = {tool.name: tool for tool in TOOLS}
    for name in ("translations_write", "translation_fix", "category_update", "feature_upsert"):
        assert '"zh-Hant"' not in json.dumps(tool_schema(tools[name]))
    assert '"zh-Hant"' in json.dumps(tool_schema(tools["translation_list"]))


def test_seo_metadata_still_accepts_traditional():
    tools = {tool.name: tool for tool in TOOLS}
    schema = tool_schema(tools["seo_keywords_upsert"])
    validator = Draft202012Validator(schema)
    errors = list(
        validator.iter_errors(
            {
                "records": [
                    {
                        "keyword": "編輯器",
                        "source": "gsc",
                        "engine": "google",
                        "country": "TW",
                        "language": "zh-Hant",
                        "device": "desktop",
                        "period": "2026-10",
                        "volumeState": "unknown",
                        "observedAt": "2026-10-24T00:00:00Z",
                    }
                ],
                "targets": [
                    {
                        "entityKey": "pkg:701",
                        "intent": "download",
                        "locale": "zh-Hant",
                        "targetUrl": "https://example.test/zh-hant/apps/example-editor",
                    }
                ],
            }
        )
    )
    assert not errors, [error.message for error in errors]


def test_topic_nomination_accepts_traditional_target():
    tools = {tool.name: tool for tool in TOOLS}
    schema = tool_schema(tools["seo_topic_nominate"])
    locale = schema["properties"]["locale"]
    Draft202012Validator({**locale, "$defs": schema["$defs"]}).validate("zh-Hant")
