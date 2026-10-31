"""What each page loader asks the API for.

    python3 -m unittest discover -s tools/agentifi-mcp

The fake client answers every GET with nothing and records the path and query,
so these check the requests against the routes, not the figures.
"""

from __future__ import annotations

import datetime as dt
import os
import re
import sys
import unittest
from typing import Any
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import pages  # noqa: E402
from pages import PAGES  # noqa: E402
from tools import TOOLS  # noqa: E402

TODAY = dt.date(2026, 10, 30)
ACCOUNT = "00000000-0000-0000-0000-00000000000a"

# The query keys each route's handler reads, from backend/internal/api. A page
# that sends any other key is asking for something the server ignores.
WINDOW = {"from", "to", "date_field"}
ROUTE_KEYS: dict[str, set[str]] = {
    r"/accounts": {"include_closed"},
    r"/accounts/[^/]+": set(),
    r"/accounts/[^/]+/summary": WINDOW,
    r"/connections": set(),
    r"/net-worth": WINDOW | {"granularity"},
    r"/cash-flow": WINDOW | {"threshold", "account_id"},
    r"/spending-plan/\d{4}-\d{2}": set(),
    r"/occurrences": WINDOW | {"search", "kind", "account_id", "is_active",
                               "include_fulfilled"},
    r"/transactions": WINDOW | {"account_id", "filter_id", "search", "reviewed",
                                "limit", "offset", "padding", "order"},
    r"/reports": set(),
    r"/reports/presets": set(),
    r"/reports/run": WINDOW | {"preset", "mode", "rows", "columns", "time_grain", "sign",
                               "filter_id"},
    r"/series": {"search", "kind", "account_id", "is_active"},
    r"/series/suggested": {"limit", "dismissed"},
    r"/series/refunds": set(),
    r"/holdings": {"account_id"},
    r"/securities": set(),
    r"/performance": WINDOW | {"granularity", "account_id"},
    r"/goals": set(),
    r"/watchlists": set(),
    r"/categories": set(),
    r"/tags": set(),
    r"/rules": set(),
    r"/notifications": {"unread"},
    r"/notifications/settings": set(),
}


# One value for every optional argument read_page offers, so each page is also
# run with every key it could send.
FILLED: dict[str, Any] = {
    "month": "2026-09", "from": "2026-01-01", "to": "2026-03-31",
    "account_id": ACCOUNT, "account_ids": [ACCOUNT], "filter_id": "f1",
    "search": "coffee", "months": 3, "limit": 20, "preset": "income",
    "mode": "summary", "rows": "payee", "columns": "time", "time_grain": "quarter",
    "sign": "both",
}

# Keys a page refuses, which the schema must not offer.
REFUSED = {"category_ids", "group_by", "kind"}


class FakeClient:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, Any]]] = []
        # Every key each request carried, empty or not: the real client drops
        # empty values, so `calls` alone would hide a key the route never reads.
        self.keys: list[tuple[str, set[str]]] = []

    def get(self, path: str, params: dict[str, Any] | None = None) -> Any:
        sent = {k: v for k, v in (params or {}).items() if v not in (None, "", [])}
        self.calls.append((path, sent))
        self.keys.append((path, set(params or {})))
        return []

    def params(self, path: str) -> list[dict[str, Any]]:
        return [sent for called, sent in self.calls if called == path]


def load(page: str, **arguments: Any) -> FakeClient:
    client = FakeClient()
    with mock.patch.object(pages, "_today", return_value=TODAY):
        PAGES[page](client, arguments)
    return client


class Arguments(dict):
    """A tool call's arguments that remember which keys were read."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.read: set[str] = set()

    def get(self, key: str, default: Any = None) -> Any:
        self.read.add(key)
        return super().get(key, default)

    def __getitem__(self, key: str) -> Any:
        self.read.add(key)
        return super().__getitem__(key)


SCHEMA = TOOLS["read_page"]["inputSchema"]


class EveryPage(unittest.TestCase):
    def check_routes(self, page: str, client: FakeClient) -> None:
        for path, sent in client.keys:
            known = [keys for pattern, keys in ROUTE_KEYS.items()
                     if re.fullmatch(pattern, path)]
            self.assertEqual(len(known), 1, f"{page}: unknown or ambiguous route {path}")
            self.assertLessEqual(sent, known[0], f"{page}: {path}")

    def test_sends_only_keys_its_routes_read(self) -> None:
        for page in PAGES:
            self.check_routes(page, load(page, account_id=ACCOUNT))

    def test_sends_only_keys_its_routes_read_with_every_argument(self) -> None:
        for page in PAGES:
            self.check_routes(page, load(page, **FILLED))

    def test_schema_offers_what_the_pages_read(self) -> None:
        offered = set(SCHEMA["properties"]) - {"page"}
        self.assertEqual(SCHEMA["properties"]["page"]["enum"], sorted(PAGES))
        self.assertFalse(offered & REFUSED, "the schema offers a key the pages refuse")
        self.assertEqual(offered, set(FILLED), "FILLED needs a value for every argument")
        read: set[str] = set()
        for page in PAGES:
            arguments = Arguments(FILLED, page=page)
            with mock.patch.object(pages, "_today", return_value=TODAY):
                TOOLS["read_page"]["handler"](FakeClient(), arguments)
            read |= arguments.read
        self.assertEqual(offered - read, set(), "the schema offers keys no page reads")

    def test_schema_enums_are_filled_with_offered_values(self) -> None:
        for key, value in FILLED.items():
            enum = SCHEMA["properties"][key].get("enum")
            if enum:
                self.assertIn(value, enum, key)


class SpendingPlan(unittest.TestCase):
    def test_month_is_in_the_path(self) -> None:
        for page in ("spending_plan", "dashboard"):
            client = load(page, month="2026-09")
            self.assertEqual(client.params("/spending-plan/2026-09"), [{}], page)
            self.assertEqual(client.params("/spending-plan"), [], page)

    def test_defaults_to_this_month(self) -> None:
        self.assertEqual(load("spending_plan").params("/spending-plan/2026-10"), [{}])


class Windows(unittest.TestCase):
    def test_net_worth_reads_back_from_today(self) -> None:
        self.assertEqual(load("net_worth").params("/net-worth"),
                         [{"from": "2025-10-30", "to": "2026-10-30"}])
        self.assertEqual(load("net_worth", months=3).params("/net-worth"),
                         [{"from": "2026-07-30", "to": "2026-10-30"}])

    def test_dashboard_net_worth_is_six_months(self) -> None:
        self.assertEqual(load("dashboard").params("/net-worth"),
                         [{"from": "2026-04-30", "to": "2026-10-30"}])

    def test_dashboard_net_worth_keeps_its_own_range(self) -> None:
        sent = load("dashboard", months=3, **{"from": "2026-01-01", "to": "2026-03-31"})
        self.assertEqual(sent.params("/net-worth"), [{"from": "2026-07-30", "to": "2026-10-30"}])
        self.assertEqual(sent.params("/occurrences"), [{"from": "2026-01-01", "to": "2026-03-31"}])

    def test_cash_flow_reads_ahead_of_today(self) -> None:
        self.assertEqual(load("cash_flow").params("/cash-flow"),
                         [{"from": "2026-10-30", "to": "2027-04-30"}])

    def test_explicit_window_wins(self) -> None:
        window = {"from": "2026-01-01", "to": "2026-03-31"}
        for page, path in (("net_worth", "/net-worth"), ("cash_flow", "/cash-flow")):
            self.assertEqual(load(page, **window).params(path), [window], page)

    def test_month_end_clamps(self) -> None:
        self.assertEqual(pages._shift_months(dt.date(2026, 8, 31), -6), dt.date(2026, 2, 28))
        self.assertEqual(pages._shift_months(dt.date(2026, 12, 15), 1), dt.date(2027, 1, 15))


class Register(unittest.TestCase):
    def test_account_detail_summary_and_rows_share_a_window(self) -> None:
        client = load("account_detail", account_id=ACCOUNT, month="2026-09")
        window = {"from": "2026-09-01", "to": "2026-09-30"}
        self.assertEqual(client.params(f"/accounts/{ACCOUNT}/summary"), [window])
        (rows,) = client.params("/transactions")
        self.assertEqual(rows["account_id"], ACCOUNT)
        self.assertEqual({k: rows[k] for k in window}, window)

    def test_transactions_narrow_by_account_id(self) -> None:
        other = "00000000-0000-0000-0000-00000000000b"
        (sent,) = load("transactions", account_ids=[ACCOUNT, other]).params("/transactions")
        self.assertEqual(sent["account_id"], [ACCOUNT, other])
        (sent,) = load("transactions", account_id=ACCOUNT).params("/transactions")
        self.assertEqual(sent["account_id"], [ACCOUNT])

    def test_filter_id_passes_through(self) -> None:
        (sent,) = load("transactions", filter_id="f1").params("/transactions")
        self.assertEqual(sent["filter_id"], "f1")

    def test_category_ids_are_refused(self) -> None:
        for page in ("transactions", "account_detail"):
            with self.assertRaisesRegex(ValueError, "filter_id", msg=page):
                load(page, account_id=ACCOUNT, category_ids=["c1"])

    def test_account_detail_follows_the_refusals_advice(self) -> None:
        with self.assertRaisesRegex(ValueError, "filter_id"):
            load("account_detail", account_id=ACCOUNT, category_ids=["c1"])
        client = load("account_detail", account_id=ACCOUNT, filter_id="f1")
        (rows,) = client.params("/transactions")
        self.assertEqual((rows["account_id"], rows["filter_id"]), (ACCOUNT, "f1"))
        self.assertEqual(client.params(f"/accounts/{ACCOUNT}/summary"),
                         [{"from": "2026-10-01", "to": "2026-10-31"}])


class Reports(unittest.TestCase):
    def test_default_is_spending_by_category(self) -> None:
        (sent,) = load("reports").params("/reports/run")
        self.assertEqual(sent, {"from": "2026-10-01", "to": "2026-10-31",
                                "rows": "category", "sign": "expenses"})

    def test_preset_is_not_overridden(self) -> None:
        (sent,) = load("reports", preset="income_summary").params("/reports/run")
        self.assertEqual(sent, {"from": "2026-10-01", "to": "2026-10-31",
                                "preset": "income_summary"})

    def test_real_keys_pass_through(self) -> None:
        config = {"mode": "summary", "rows": "payee", "columns": "time",
                  "time_grain": "quarter", "sign": "both", "filter_id": "f1"}
        (sent,) = load("reports", **config).params("/reports/run")
        self.assertEqual({k: sent[k] for k in config}, config)

    def test_old_keys_are_refused(self) -> None:
        for key in ("group_by", "kind"):
            with self.assertRaisesRegex(ValueError, "does not read"):
                load("reports", **{key: "category"})


if __name__ == "__main__":
    unittest.main()
