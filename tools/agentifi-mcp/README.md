# Agentifi MCP server

Drives a running Agentifi instance over its HTTP API, so the app can be read
and changed from tool calls instead of a browser. The browser is then only
needed for what it is actually better at: looking at layout, spacing, and
other visual problems.

Optional developer tooling, not part of the application. Standard library
only — no virtualenv, no install step, any python3.

## Setup

```bash
cp tools/agentifi-mcp/.env.local.example tools/agentifi-mcp/.env.local
$EDITOR tools/agentifi-mcp/.env.local     # base URL + credentials
```

Set `AGENTIFI_EMAIL` and `AGENTIFI_PASSWORD`, or set `AGENTIFI_TOKEN` to a
bearer token issued elsewhere — which is the way in for an account with a
second factor or SSO, and the way to run this with no password in a file.

`.env.local` is gitignored. Anything already exported in the shell wins over
it, so `AGENTIFI_BASE_URL=http://localhost:8000` before your MCP client's
own launch command points elsewhere for one session without editing the
file. `AGENTIFI_TIMEOUT` sets the per-request timeout in seconds (30 by
default).

Two switches, both off unless set to `1`:

| Variable | Effect |
| --- | --- |
| `AGENTIFI_MCP_WRITE` | Registers the write tools (`api_post`, `api_patch`, `api_put`, `api_delete`) and lets the client send anything but GET. Without it the server is read-only, and a non-GET call is refused before it is sent. |
| `AGENTIFI_ALLOW_INSECURE_HTTP` | Allows a plain `http://` base URL on a host other than loopback. Without it such a URL is refused, because the password and the token would cross the network in clear. Needed for a LAN instance with no TLS, e.g. `AGENTIFI_BASE_URL=http://192.0.2.10:8100`. |

**Token renewal.** An access token expires. With `AGENTIFI_EMAIL` and
`AGENTIFI_PASSWORD` set, a call answered 401 signs in again and is retried
once, so a long session keeps working. A `AGENTIFI_TOKEN` given from outside is
never renewed: when it expires, calls fail with 401 until you replace it.

Register it with your MCP client, in whatever file it reads a server list
from (gitignore that file if it sits in the repository):

```json
{
  "mcpServers": {
    "agentifi": {
      "command": "python3",
      "args": ["/path/to/agentifi/tools/agentifi-mcp/server.py"]
    }
  }
}
```

Check it against a live instance before trusting it:

```bash
python3 tools/agentifi-mcp/selftest.py
```

That signs in, counts the routes the deployment serves, and loads every page.
It is read-only.

## Tools

| Tool | What it does |
| --- | --- |
| `read_page` | One screen as it would render — the same calls the page makes, over the same window |
| `whoami` | The account, the space being acted in, and every space it could act in |
| `list_routes` | Every route the running server registers, read from the server itself |
| `api_get` | GET any path |
| `api_post` / `api_patch` / `api_put` / `api_delete` | The write verbs, against a real ledger; only with `AGENTIFI_MCP_WRITE=1` |

Pages: `accounts`, `account_detail`, `bills`, `cash_flow`, `categories`,
`dashboard`, `goals`, `investments`, `net_worth`, `notifications`, `reports`,
`rules`, `spending_plan`, `transactions`, `watchlists`.

## What it is not

A development tool that lives outside the app. It holds a password, it can
write when told to, and it is not part of what ships — nothing in `backend/` imports it and
the deployed image does not contain it.

**It is held to the in-app assistant's line.** The server's route listing
(`/routes`) marks some routes `denied`: signing in to a provider, answering its
challenge, credentials, the assistant's own machinery. The client reads that
list once and refuses any path under a denied route, whatever the verb and
whether or not writes are on. A server whose listing does not carry the
`denied` field is refused outright rather than guessed about, so an instance
whose API predates that field has to be upgraded first.

Two details worth knowing before reading its output:

- **Everything is scoped to one space.** `whoami` says which. A household with
  more than one has to set `AGENTIFI_SPACE`, because picking the first one
  silently is how you read the wrong ledger and never notice.
- **The window is explicit.** An omitted date filter does not mean "all time"
  everywhere in this API, so every page loader passes the same window to the
  list endpoint and the summary endpoint. A figure that disagrees with the app
  is worth reporting as a bug rather than working around here.
