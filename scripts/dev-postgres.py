#!/usr/bin/env python3
"""Start a PostgreSQL for development without Docker or root.

    uv run --with pgserver scripts/dev-postgres.py          # start, print the URL
    uv run --with pgserver scripts/dev-postgres.py --stop   # shut it down

The application's database is SQLite; this Postgres serves only the
end-to-end tests of internal/pgimport, which copies an install from the
earlier Postgres-backed release. It needs no Docker daemon and no `sudo`;
without it those tests silently skip.

`pgserver` ships its own server binaries and runs over a unix socket in its
data directory, `.dev-postgres/` at the repo root, which is gitignored.
Nothing is installed system-wide and nothing listens on a TCP port. It creates
the `agentifi` and `agentifi_test` databases. The tests read
TEST_DATABASE_URL (see CONTRIBUTING.md):

    export TEST_DATABASE_URL="postgres://postgres@/agentifi_test?host=$PWD/.dev-postgres"
    cd backend && go test ./internal/pgimport/
"""

import argparse
import pathlib
import sys

try:
    import pgserver
except ImportError:  # pragma: no cover - the message is the whole point
    sys.exit("pgserver is not installed. Run: uv run --with pgserver scripts/dev-postgres.py")

DATA_DIR = pathlib.Path(__file__).resolve().parent.parent / ".dev-postgres"
DATABASES = ("agentifi", "agentifi_test")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stop", action="store_true", help="shut the server down")
    parser.add_argument("--database", default="agentifi", help="which database to print a URL for")
    args = parser.parse_args()

    DATA_DIR.mkdir(exist_ok=True)

    if args.stop:
        pgserver.get_server(str(DATA_DIR), cleanup_mode="stop")
        print("stopped")
        return 0

    # cleanup_mode=None leaves the server running after this process exits. The
    # default stops it, which would make every test run pay a fresh initdb and
    # would defeat the point of starting it from a shell.
    server = pgserver.get_server(str(DATA_DIR), cleanup_mode=None)

    for name in DATABASES:
        if "1 row" not in server.psql(f"select 1 from pg_database where datname='{name}'"):
            server.psql(f"create database {name}")

    socket = server.get_uri().split("host=")[1]
    print(f"postgres://postgres@/{args.database}?host={socket}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
