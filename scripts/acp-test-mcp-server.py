#!/usr/bin/env python3
"""Minimal stdio MCP server used by ACP integration tests (Neovim / Agentic.nvim)."""
import json
import os
import sys


def send(payload: dict) -> None:
    sys.stdout.write(json.dumps(payload, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def main() -> int:
    hint = os.environ.get("ACP_HINT_VALUE", "42")
    for raw in sys.stdin:
        line = raw.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        method = msg.get("method", "")
        msg_id = msg.get("id")
        if msg_id is None:
            # JSON-RPC notification (e.g. notifications/initialized)
            continue
        if method == "initialize":
            send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "editor-mcp-fixture", "version": "1.0.0"},
                },
            })
        elif method == "tools/list":
            send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "tools": [
                        {
                            "name": "project_hint",
                            "description": "Returns the target integer configured by the editor for math.go.",
                            "inputSchema": {
                                "type": "object",
                                "properties": {},
                            },
                        }
                    ]
                },
            })
        elif method == "tools/call":
            params = msg.get("params") or {}
            if params.get("name") == "project_hint":
                send({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "result": {
                        "content": [
                            {"type": "text", "text": f"TARGET_RETURN={hint}"}
                        ],
                        "isError": False,
                    },
                })
            else:
                send({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "error": {"code": -32602, "message": "unknown tool"},
                })
        elif method in ("resources/list", "prompts/list", "resources/templates/list"):
            key = "resourceTemplates" if "templates" in method else method.split("/")[0]
            send({"jsonrpc": "2.0", "id": msg_id, "result": {key: []}})
        else:
            send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "error": {"code": -32601, "message": f"method not found: {method}"},
            })
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
