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


def _shift_months(day: dt.date, months: int) -> dt.date:
    index = day.year * 12 + day.month - 1 + months
    year, mon = divmod(index, 12)
    last = (dt.date(year + (mon == 11), (mon + 1) % 12 + 1, 1) - dt.timedelta(days=1)).day
    return dt.date(year, mon + 1, min(day.day, last))


def _months(params: dict[str, Any], default: int) -> int:
    try:
        return max(1, int(params.get("months") or default))
    except (TypeError, ValueError):
        return default


def _trailing(params: dict[str, Any], default_months: int) -> tuple[str, str]:
    """A balance history's window: the last `months` up to today, unless given."""
    if params.get("from") and params.get("to"):
        return str(params["from"]), str(params["to"])
    today = _today()
    return _shift_months(today, -_months(params, default_months)).isoformat(), today.isoformat()


def _ahead(params: dict[str, Any], default_months: int) -> tuple[str, str]:
    """A projection's window: today and the next `months`, unless given."""
    if params.get("from") and params.get("to"):
        return str(params["from"]), str(params["to"])
    today = _today()
    return today.isoformat(), _shift_months(today, _months(params, default_months)).isoformat()


def _refuse_category_ids(params: dict[str, Any]) -> None:
    """The transaction list has no category key.

    The app narrows by category through a stored filter, which takes a write to
    create, so a caller passes an existing `filter_id` instead. Refused rather
    than dropped, because a dropped key answers every row and looks like an
    answer.
    """
    if params.get("category_ids"):
        raise ValueError("the transaction list has no category key; "
                         "pass the id of a saved filter as filter_id")


def _register_filter(params: dict[str, Any]) -> dict[str, Any]:
    """What the transaction list can be narrowed by besides the window."""
    _refuse_category_ids(params)
    accounts = params.get("account_ids") or (
        [params["account_id"]] if params.get("account_id") else None)
    return {"account_id": accounts, "filter_id": params.get("filter_id"),
            "search": params.get("search")}


REPORT_KEYS = ("preset", "mode", "rows", "columns", "time_grain", "sign", "filter_id")


def _report_config(params: dict[str, Any]) -> dict[str, Any]:
    for old, new in (("group_by", "rows"), ("kind", "preset or sign")):
        if params.get(old):
            raise ValueError(f"/reports/run does not read {old}; use {new}")
    config = {key: params.get(key) for key in REPORT_KEYS}
    # Without a preset, spending by category. With one, the preset decides, and
    # a default here would override it.
    if not config["preset"]:
        config["rows"] = config["rows"] or "category"
        config["sign"] = config["sign"] or "expenses"
    return config


def _limit(params: dict[str, Any], default: int = 50) -> int:
    try:
        return max(1, min(int(params.get("limit", default)), 500))
    except (TypeError, ValueError):
        return default


def dashboard(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    worth_from, worth_to = _trailing({"months": p.get("months")}, 6)
    return {
        "window": {"from": start, "to": end},
        "accounts": c.get("/accounts"),
        "net_worth": c.get("/net-worth", {"from": worth_from, "to": worth_to}),
        "spending_plan": c.get(f"/spending-plan/{_month(p)}"),
        "upcoming": c.get("/occurrences", {"from": start, "to": end}),
        "recent": c.get("/transactions", {"from": start, "to": end, "limit": 10}),
    }


def transactions(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    query: dict[str, Any] = {
        "from": start, "to": end, "limit": _limit(p), **_register_filter(p),
    }
    # One call, not two. The list response carries `count`, `total` and the
    # `window` it was read over, so the register's totals line and its rows
    # cannot disagree about which window they describe — which is the failure a
    # separate summary endpoint would invite.
    return {"window": {"from": start, "to": end}, "transactions": c.get("/transactions", query)}


def accounts(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"accounts": c.get("/accounts"), "connections": c.get("/connections")}


def account_detail(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    _refuse_category_ids(p)
    account_id = p["account_id"]
    start, end = _window(p)
    return {
        "account": c.get(f"/accounts/{account_id}"),
        "summary": c.get(f"/accounts/{account_id}/summary", {"from": start, "to": end}),
        "transactions": c.get("/transactions",
                              {"account_id": account_id, "filter_id": p.get("filter_id"),
                               "from": start, "to": end, "limit": _limit(p)}),
    }


def spending_plan(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    return {"month": _month(p), "plan": c.get(f"/spending-plan/{_month(p)}")}


def reports(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _window(p)
    return {
        "window": {"from": start, "to": end},
        "saved": c.get("/reports"),
        "presets": c.get("/reports/presets"),
        "result": c.get("/reports/run", {"from": start, "to": end, **_report_config(p)}),
    }


def net_worth(c: AgentifiClient, p: dict[str, Any]) -> dict[str, Any]:
    start, end = _trailing(p, 12)
    return c.get("/net-worth", {"from": start, "to": end})


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
    start, end = _ahead(p, 6)
    return c.get("/cash-flow", {"from": start, "to": end})


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
