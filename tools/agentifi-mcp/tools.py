"""The tool surface.

Three kinds of tool, in the order a session tends to want them:

  * `read_page` — a whole screen in one call, which is what "look at the app"
    usually means.
  * `list_routes` — what the API actually offers, read from the running
    server rather than from a copy here that would drift.
  * `api_get` / `api_post` / `api_patch` / `api_put` / `api_delete` — the
    escape hatch, for anything the page loaders do not cover.

The write verbs are real writes against a real ledger. They are offered only
with AGENTIFI_MCP_WRITE=1, because a development tool that can only read cannot
set up the state a bug needs, and they are named plainly so nobody reaches one
by accident. Whatever the verb, the client refuses every route the server marks
denied to the in-app assistant (see client.py).
"""

from __future__ import annotations

import os
from typing import Any, Callable

from client import AgentifiClient
from pages import PAGES

TOOLS: dict[str, dict[str, Any]] = {}

WRITES = os.environ.get("AGENTIFI_MCP_WRITE", "") == "1"


def tool(name: str, description: str, schema: dict[str, Any], writes: bool = False) -> Callable:
    def register(fn: Callable) -> Callable:
        if not writes or WRITES:
            TOOLS[name] = {"name": name, "description": description,
                           "inputSchema": schema, "handler": fn}
        return fn
    return register


def _object(properties: dict[str, Any], required: list[str] | None = None) -> dict[str, Any]:
    return {"type": "object", "properties": properties, "required": required or []}


PATH = {"type": "string", "description": "API path, with or without the /api prefix"}
PARAMS = {"type": "object", "description": "query string parameters"}
BODY = {"type": "object", "description": "JSON request body"}


@tool("read_page",
      "Read one screen of the app as it would render: " + ", ".join(sorted(PAGES)) + ".",
      _object({
          "page": {"type": "string", "enum": sorted(PAGES)},
          "month": {"type": "string", "description": "YYYY-MM; defaults to this month"},
          "from": {"type": "string", "description": "YYYY-MM-DD; overrides month with `to`"},
          "to": {"type": "string", "description": "YYYY-MM-DD"},
          "account_id": {"type": "string"},
          "account_ids": {"type": "array", "items": {"type": "string"}},
          "category_ids": {"type": "array", "items": {"type": "string"}},
          "search": {"type": "string"},
          "months": {"type": "integer"},
          "limit": {"type": "integer"},
          "group_by": {"type": "string"},
          "kind": {"type": "string"},
      }, ["page"]))
def read_page(c: AgentifiClient, p: dict[str, Any]) -> Any:
    page = p.get("page", "")
    loader = PAGES.get(page)
    if loader is None:
        raise ValueError(f"no page {page!r}; try one of {', '.join(sorted(PAGES))}")
    return loader(c, p)


@tool("whoami",
      "The signed-in account, the space being acted in, and every space it could act in. "
      "Worth calling first: everything else is scoped to that space.",
      _object({}))
def whoami(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return {"user": c.get("/auth/me"), "acting_in": {"id": c.space_id, "name": c.space_name},
            "spaces": c.spaces(), "base_url": c.config.base_url}


@tool("list_routes",
      "Every route the running server registers, with its method and whether it is "
      "tenant-scoped. Read from the server, so it cannot drift from what is deployed.",
      _object({"contains": {"type": "string", "description": "filter by substring"}}))
def list_routes(c: AgentifiClient, p: dict[str, Any]) -> Any:
    routes = c.get("/routes")
    needle = str(p.get("contains", "")).lower()
    if needle and isinstance(routes, list):
        routes = [one for one in routes
                  if needle in str(one.get("path", "")).lower()]
    return routes


@tool("api_get", "GET any API path.", _object({"path": PATH, "params": PARAMS}, ["path"]))
def api_get(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return c.get(p["path"], p.get("params"))


@tool("api_post", "POST any API path. This writes.",
      _object({"path": PATH, "body": BODY, "params": PARAMS}, ["path"]), writes=True)
def api_post(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return c.post(p["path"], p.get("body"), p.get("params"))


@tool("api_patch", "PATCH any API path. This writes.",
      _object({"path": PATH, "body": BODY}, ["path", "body"]), writes=True)
def api_patch(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return c.patch(p["path"], p["body"])


@tool("api_put", "PUT any API path. This writes.",
      _object({"path": PATH, "body": BODY}, ["path", "body"]), writes=True)
def api_put(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return c.put(p["path"], p["body"])


@tool("api_delete", "DELETE any API path. This writes.", _object({"path": PATH}, ["path"]),
      writes=True)
def api_delete(c: AgentifiClient, p: dict[str, Any]) -> Any:
    return c.delete(p["path"])


def list_tools() -> list[dict[str, Any]]:
    return [{"name": t["name"], "description": t["description"], "inputSchema": t["inputSchema"]}
            for t in TOOLS.values()]


def call(c: AgentifiClient, name: str, arguments: dict[str, Any]) -> Any:
    entry = TOOLS.get(name)
    if entry is None:
        raise ValueError(f"no tool named {name!r}")
    return entry["handler"](c, arguments or {})
