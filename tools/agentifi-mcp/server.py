#!/usr/bin/env python3
"""Agentifi MCP server — drive a running instance without a browser.

JSON-RPC 2.0 over stdio, standard library only, so it starts with any python3
and no install step. It talks to the backend's public HTTP API exactly as the
frontend does, which means anything the app can do from a page, this can do
from a tool call — and the browser is left for what it is actually better at:
layout, spacing, and everything else you have to look at.

    python3 tools/agentifi-mcp/server.py

Configuration is environment only — see README.md.
"""

from __future__ import annotations

import json
import os
import sys
import traceback

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from client import AgentifiClient, AgentifiError, Config  # noqa: E402
from tools import call, list_tools  # noqa: E402

PROTOCOL_VERSION = "2024-11-05"
SERVER_INFO = {"name": "agentifi", "version": "1.0.0"}


def _result(request_id, payload):
    return {"jsonrpc": "2.0", "id": request_id, "result": payload}


def _error(request_id, code, message):
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}}


def _content(payload) -> dict:
    """MCP wants text content; JSON is what every caller here actually wants."""
    text = payload if isinstance(payload, str) else json.dumps(payload, indent=1, default=str)
    return {"content": [{"type": "text", "text": text}]}


def handle(client: AgentifiClient, message: dict):
    method = message.get("method", "")
    request_id = message.get("id")
    params = message.get("params") or {}

    if method == "initialize":
        return _result(request_id, {
            "protocolVersion": PROTOCOL_VERSION,
            "capabilities": {"tools": {}},
            "serverInfo": SERVER_INFO,
        })
    if method in ("notifications/initialized", "initialized"):
        return None
    if method == "tools/list":
        return _result(request_id, {"tools": list_tools()})
    if method == "tools/call":
        name = params.get("name", "")
        try:
            return _result(request_id, _content(call(client, name, params.get("arguments") or {})))
        except AgentifiError as error:
            # The server's own refusal, handed back as content rather than as a
            # protocol error: "404 on /accounts/x" is the answer to the
            # question, and a transport error would hide it.
            return _result(request_id, _content({"error": str(error), "status": error.status}))
        except Exception as error:  # noqa: BLE001 - a tool must not kill the server
            return _result(request_id, _content(
                {"error": f"{type(error).__name__}: {error}",
                 "traceback": traceback.format_exc(limit=3)}))
    if method == "ping":
        return _result(request_id, {})
    return _error(request_id, -32601, f"unknown method {method!r}")


def main() -> int:
    client = AgentifiClient(config=Config.from_env())
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        answer = handle(client, message)
        if answer is None:
            continue
        sys.stdout.write(json.dumps(answer) + "\n")
        sys.stdout.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
