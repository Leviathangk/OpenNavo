import json

from opennavo_mcp.tools.manifest import TOOLS
from opennavo_mcp.tools.runtime import tool_schema


def test_traditional_is_readable_but_not_a_write_target():
    tools = {tool.name: tool for tool in TOOLS}
    for name in ("translations_write", "translation_fix", "category_update", "feature_upsert"):
        assert '"zh-Hant"' not in json.dumps(tool_schema(tools[name]))
    assert '"zh-Hant"' in json.dumps(tool_schema(tools["translation_list"]))


def test_no_write_tool_targets_traditional():
    # zh-Hant derives from zh-CN; no write schema may offer it as a target.
    for tool in TOOLS:
        if tool.write:
            assert '"zh-Hant"' not in json.dumps(tool_schema(tool)), tool.name
