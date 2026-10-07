# Security

Agentifi stores bank connection tokens, the credentials and signed-in browser
sessions of the accounts its connectors reach, and a household's whole
financial history. Please treat a vulnerability in it accordingly.

## Reporting a vulnerability

**Do not open a public issue.** Report it privately through the repository
host's private vulnerability reporting (on GitHub: the **Security** tab, then
**Report a vulnerability**).

Include what an attacker can do, the version or commit, and the steps to
reproduce. You should hear back within a week. Please give a fix a reasonable
time to ship before you disclose.

## Supported versions

Only the latest release and the current `master` receive fixes.

## Running it safely

- Serve it over HTTPS behind a reverse proxy, and set `TRUSTED_PROXY_CIDRS`
  to that proxy. See [`docs/operations.md`](docs/operations.md).
- Never set `DEBUG=true` on an install anyone else can reach: it accepts a
  default or short `SECRET_KEY`. The compose file forces it off.
- `SECRET_KEY` and the other secrets are generated into `data/secrets` on
  the first start. Keep that directory, and the backups that include it, as
  private as the database, and read [Secrets](docs/operations.md#secrets)
  before rotating `SECRET_KEY`.
- The Camoufox browser server publishes no port; keep it that way. Anyone who
  can reach it can drive a browser.
- The browser-profiles volume and the database both hold live sessions and
  credentials. Restrict access to the host and to its backups.
