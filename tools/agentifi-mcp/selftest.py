#!/usr/bin/env python3
"""Check the server against a live instance.

    AGENTIFI_BASE_URL=http://192.0.2.10:8100 AGENTIFI_ALLOW_INSECURE_HTTP=1 \
    AGENTIFI_EMAIL=you@example.com AGENTIFI_PASSWORD=... \
    python3 tools/agentifi-mcp/selftest.py

Read-only. It signs in, lists the routes the deployment actually serves, and
loads every page, reporting which came back and which did not — which is the
question worth asking after an API change, and the one a unit test in the
backend cannot answer because it does not know what the tools ask for.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from client import AgentifiClient, AgentifiError, Config  # noqa: E402
from pages import PAGES  # noqa: E402
from tools import call  # noqa: E402


def main() -> int:
    client = AgentifiClient(config=Config.from_env())
    try:
        client.login()
    except AgentifiError as error:
        print(f"sign-in failed: {error}")
        return 1
    print(f"signed in to {client.config.base_url} as {client.config.email}")
    print(f"acting in {client.space_name!r} ({client.space_id})\n")

    routes = call(client, "list_routes", {})
    print(f"routes served: {len(routes)}\n")

    failures = 0
    for name in sorted(PAGES):
        arguments = {"page": name}
        if name == "account_detail":
            accounts = client.get("/accounts") or []
            if not accounts:
                print(f"  {name:<16} skipped (no accounts)")
                continue
            arguments["account_id"] = accounts[0]["id"]
        try:
            answer = call(client, "read_page", arguments)
            print(f"  {name:<16} ok ({len(str(answer)):,} chars)")
        except AgentifiError as error:
            failures += 1
            print(f"  {name:<16} FAILED {error}")

    print(f"\n{len(PAGES) - failures}/{len(PAGES)} pages read")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
