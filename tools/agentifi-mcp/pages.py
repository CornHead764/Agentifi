"""What each screen shows, as one call.

A page in the app is several endpoints — the register is transactions plus a
summary plus the account tree — and reading them one at a time from tool calls
is slow and easy to get wrong. Each loader here composes the same calls the
page makes, in the same window, so what comes back is what a person would be
looking at.

The window matters more than it sounds. An omitted date filter does not mean
"all time" everywhere in this API, so a loader that paired a list endpoint with
a summary endpoint over different windows would report two figures that
disagree about the same screen.
"""

from __future__ import annotations

import datetime as dt
from typing import Any

from client import AgentifiClient


def _today() -> dt.date:
    return dt.date.today()


def _month(params: dict[str, Any]) -> str:
    return str(params.get("month") or _today().strftime("%Y-%m"))


def _month_bounds(month: str) -> tuple[str, str]:
    year, mon = (int(part) for part in month.split("-")[:2])
    start = dt.date(year, mon, 1)
    end = dt.date(year + (mon == 12), (mon % 12) + 1, 1) - dt.timedelta(days=1)
    return start.isoformat(), end.isoformat()


def _window(params: dict[str, Any]) -> tuple[str, str]:
    """The window both halves of a screen are read over."""
    if params.get("from") and params.get("to"):
        return str(params["from"]), str(params["to"])
    return _month_bounds(_month(params))


def _limit(params: dict[str, Any], default: int = 50) -> int:
    try:
        return max(1, min(int(params.get("limit", default)), 500))
    except (TypeError, ValueError):
        return default


def dashboard(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    return {
        "window": {"from": start, "to": end},
        "accounts": c.get("/accounts"),
        "net_worth": c.get("/net-worth", {"months": p.get("months", 6)}),
        "spending_plan": c.get("/spending-plan", {"month": _month(p)}),
        "upcoming": c.get("/occurrences", {"from": start, "to": end}),
        "recent": c.get("/transactions", {"from": start, "to": end, "limit": 10}),
    }


def transactions(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    query: dict[str, Any] = {
        "from": start, "to": end, "limit": _limit(p),
        "account_ids": p.get("account_ids"),
        "category_ids": p.get("category_ids"),
        "search": p.get("search"),
    }
    # One call, not two. The list response carries `count`, `total` and the
    # `window` it was read over, so the register's totals line and its rows
    # cannot disagree about which window they describe — which is the failure a
    # separate summary endpoint would invite.
    return {"window": {"from": start, "to": end}, "transactions": c.get("/transactions", query)}


def accounts(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"accounts": c.get("/accounts"), "connections": c.get("/connections")}


def account_detail(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    account_id = p["account_id"]
    start, end = _window(p)
    return {
        "account": c.get(f"/accounts/{account_id}"),
        "summary": c.get(f"/accounts/{account_id}/summary"),
        "transactions": c.get("/transactions",
                              {"account_ids": [account_id], "from": start, "to": end,
                               "limit": _limit(p)}),
    }


def spending_plan(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"month": _month(p), "plan": c.get("/spending-plan", {"month": _month(p)})}


def reports(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    return {
        "window": {"from": start, "to": end},
        "saved": c.get("/reports"),
        "presets": c.get("/reports/presets"),
        "result": c.get("/reports/run", {
            "from": start, "to": end,
            "group_by": p.get("group_by", "category"),
            "kind": p.get("kind", "spending"),
        }),
    }


def net_worth(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return c.get("/net-worth", {"months": p.get("months", 12)})


def bills(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    return {
        "window": {"from": start, "to": end},
        "series": c.get("/series"),
        "occurrences": c.get("/occurrences", {"from": start, "to": end}),
        "suggested": c.get("/series/suggested"),
        "refunds": c.get("/series/refunds"),
    }


def investments(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {
        "holdings": c.get("/holdings"),
        "securities": c.get("/securities"),
        "performance": c.get("/performance"),
    }


def goals(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return c.get("/goals")


def watchlists(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return c.get("/watchlists")


def categories(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"categories": c.get("/categories"), "tags": c.get("/tags")}


def rules(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return c.get("/rules")


def notifications(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"feed": c.get("/notifications"), "settings": c.get("/notifications/settings")}


def cash_flow(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return c.get("/cash-flow", {"months": p.get("months", 6)})


PAGES = {
    "dashboard": dashboard,
    "transactions": transactions,
    "accounts": accounts,
    "account_detail": account_detail,
    "spending_plan": spending_plan,
    "reports": reports,
    "net_worth": net_worth,
    "bills": bills,
    "investments": investments,
    "goals": goals,
    "watchlists": watchlists,
    "categories": categories,
    "rules": rules,
    "notifications": notifications,
    "cash_flow": cash_flow,
}
