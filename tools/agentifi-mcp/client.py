"""HTTP client for a running Agentifi instance.

Standard library only, so the server starts with any python3 and no install
step. It speaks exactly the HTTP the frontend speaks — bearer token, an
`X-Space-Id` header on everything tenant-scoped — which is what makes this
useful: anything a page can do, a tool call can do, and a difference between
the two is a bug in one of them.

Credentials come from the environment or from `.env.local`, which is
gitignored. Nothing here writes a credential anywhere, and the token is held
in memory for the life of the process.

A model drives this, so it is held to the same line the in-app assistant is:
every route the server marks `denied` in its route listing — signing in to a
provider, answering its challenge, a credential, the assistant's
own machinery — is refused here too, whatever the verb. Writes are refused
unless AGENTIFI_MCP_WRITE=1.
"""

from __future__ import annotations

import ipaddress
import json
import os
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any

DEFAULT_TIMEOUT = 30


class AgentifiError(RuntimeError):
    """A request the server refused, carrying what it said about it."""

    def __init__(self, status: int, detail: str, path: str) -> None:
        super().__init__(f"{status} on {path}: {detail}")
        self.status = status
        self.detail = detail
        self.path = path


def _load_env_file(path: str) -> None:
    """Read .env.local into the environment, without overriding what is set.

    The shell wins so that `AGENTIFI_BASE_URL=...` before an MCP client's own
    launch command points one session somewhere else without editing a file.
    """
    if not os.path.exists(path):
        return
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            os.environ.setdefault(key.strip(), value.strip().strip("'\""))


@dataclass
class Config:
    base_url: str
    email: str
    password: str
    # A bearer token already issued elsewhere, used instead of signing in.
    # The way in for an account with a second factor or SSO, where a password
    # alone cannot get a token — and the way to run this without a password
    # sitting in a file at all.
    token: str = ""
    # Space to act in. Empty means "the first one this account is a member
    # of", which is right for a single-household install and wrong to guess
    # for anything else — so a multi-space account has to say.
    space: str = ""
    timeout: int = DEFAULT_TIMEOUT
    # Whether the write verbs are allowed at all.
    write: bool = False
    # Plain HTTP to a host that is not this machine carries the password and
    # the bearer in clear, so it has to be asked for by name.
    allow_insecure_http: bool = False

    @classmethod
    def from_env(cls) -> "Config":
        here = os.path.dirname(os.path.abspath(__file__))
        _load_env_file(os.path.join(here, ".env.local"))
        base = os.environ.get("AGENTIFI_BASE_URL", "http://localhost:8000").rstrip("/")
        config = cls(
            base_url=base,
            email=os.environ.get("AGENTIFI_EMAIL", ""),
            password=os.environ.get("AGENTIFI_PASSWORD", ""),
            token=os.environ.get("AGENTIFI_TOKEN", ""),
            space=os.environ.get("AGENTIFI_SPACE", ""),
            timeout=int(os.environ.get("AGENTIFI_TIMEOUT", DEFAULT_TIMEOUT)),
            write=os.environ.get("AGENTIFI_MCP_WRITE", "") == "1",
            allow_insecure_http=os.environ.get("AGENTIFI_ALLOW_INSECURE_HTTP", "") == "1",
        )
        config.check_base_url()
        return config

    def check_base_url(self) -> None:
        parsed = urllib.parse.urlsplit(self.base_url)
        if parsed.scheme == "https":
            return
        if parsed.scheme != "http":
            raise ValueError(f"AGENTIFI_BASE_URL must be http or https, not {self.base_url!r}")
        if _is_loopback(parsed.hostname or "") or self.allow_insecure_http:
            return
        raise ValueError(
            f"{self.base_url} is plain HTTP to another machine, which would send the password "
            "and the token in clear; use https, or set AGENTIFI_ALLOW_INSECURE_HTTP=1 for a "
            "network you trust")


def _is_loopback(host: str) -> bool:
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _under_route(pattern: str, path: str) -> bool:
    """Whether path is the route or below it, a `{placeholder}` matching any
    one segment — the rule the server's own dispatch refuses by."""
    want = [one for one in pattern.strip("/").split("/") if one]
    got = [one for one in path.strip("/").split("/") if one]
    if len(got) < len(want):
        return False
    for expected, actual in zip(want, got):
        if expected.startswith("{") and expected.endswith("}"):
            continue
        if expected != actual:
            return False
    return True


@dataclass
class AgentifiClient:
    config: Config
    token: str = ""
    space_id: str = ""
    space_name: str = ""
    _spaces: list[dict[str, Any]] = field(default_factory=list)
    _denied: list[str] | None = None

    # --- plumbing --------------------------------------------------------

    def _url(self, path: str, params: dict[str, Any] | None = None) -> str:
        if not path.startswith("/"):
            path = "/" + path
        # Every route is under /api; accepting either spelling means a tool
        # call copied out of the network tab works unchanged.
        if not path.startswith("/api/"):
            path = "/api" + path
        url = self.config.base_url + path
        clean = {k: v for k, v in (params or {}).items() if v not in (None, "", [])}
        if clean:
            flat: list[tuple[str, str]] = []
            for key, value in clean.items():
                if isinstance(value, (list, tuple)):
                    flat.extend((key, str(one)) for one in value)
                else:
                    flat.append((key, str(value)))
            url += "?" + urllib.parse.urlencode(flat)
        return url

    def _send(self, method: str, url: str, body: Any, headers: dict[str, str],
              form: bool = False) -> Any:
        data = None
        if form:
            data = urllib.parse.urlencode(body or {}).encode("utf-8")
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        elif body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=self.config.timeout) as response:
                raw = response.read()
                if not raw:
                    return None
                text = raw.decode("utf-8")
                try:
                    return json.loads(text)
                except json.JSONDecodeError:
                    # Report exports come back as CSV. Returning the text is
                    # more useful than raising on a response that is fine.
                    return {"text": text}
        except urllib.error.HTTPError as error:
            raw = error.read().decode("utf-8", errors="replace")
            detail = raw
            try:
                detail = json.loads(raw).get("detail", raw)
            except json.JSONDecodeError:
                pass
            raise AgentifiError(error.code, detail, url) from None
        except urllib.error.URLError as error:
            raise AgentifiError(0, f"cannot reach {self.config.base_url}: {error.reason}",
                                url) from None

    # --- session ---------------------------------------------------------

    def login(self) -> None:
        """Get a bearer token: the configured one, or one from a sign-in."""
        if self.config.token:
            self.token = self.config.token
            self._resolve_space()
            return
        if not self.config.email or not self.config.password:
            raise AgentifiError(0, "set AGENTIFI_EMAIL and AGENTIFI_PASSWORD, or "
                                   "AGENTIFI_TOKEN "
                                   "(see tools/agentifi-mcp/README.md)", "/auth/token")
        # Form-encoded, and the email goes in `username`: the field names are
        # OAuth2's and the endpoint parses a form, not JSON. Posting JSON here
        # reads as an empty username and comes back 401 — which looks exactly
        # like a wrong password and is not one.
        answer = self._send("POST", self._url("/auth/token"),
                            {"username": self.config.email, "password": self.config.password},
                            {}, form=True)
        token = (answer or {}).get("access_token")
        if not token:
            # A second factor is a deliberate stop rather than something to
            # work around: this tool is not the place to hold a TOTP secret.
            raise AgentifiError(401, "no access token came back — is a second factor enabled "
                                     "on this account?", "/auth/token")
        self.token = token
        self._resolve_space()

    def _resolve_space(self) -> None:
        spaces = self._send("GET", self._url("/spaces"), None, self._headers(space=False)) or []
        self._spaces = [one for one in spaces if one.get("accepted_at") or one.get("is_owner")]
        if not self._spaces:
            self._spaces = list(spaces)
        if not self._spaces:
            raise AgentifiError(403, "this account is a member of no space", "/spaces")

        wanted = self.config.space.strip().lower()
        if wanted:
            for one in self._spaces:
                if wanted in (str(one.get("id", "")).lower(), str(one.get("name", "")).lower()):
                    self.space_id, self.space_name = one["id"], one.get("name", "")
                    return
            names = ", ".join(repr(one.get("name")) for one in self._spaces)
            raise AgentifiError(404, f"no space matching {self.config.space!r}; this account has "
                                     f"{names}", "/spaces")
        first = self._spaces[0]
        self.space_id, self.space_name = first["id"], first.get("name", "")

    def _headers(self, space: bool = True) -> dict[str, str]:
        headers = {"Accept": "application/json"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        if space and self.space_id:
            headers["X-Space-Id"] = self.space_id
        return headers

    def spaces(self) -> list[dict[str, Any]]:
        return list(self._spaces)

    # --- verbs -----------------------------------------------------------

    def _denied_routes(self) -> list[str]:
        """The routes the server says a model may not reach, read once.

        Fails closed: a server whose listing does not say is refused outright
        rather than guessed about."""
        if self._denied is None:
            routes = self._send("GET", self._url("/routes"), None, self._headers())
            if not isinstance(routes, list) or not all("denied" in one for one in routes):
                raise AgentifiError(0, "this server's route listing does not say which routes "
                                       "are off limits; update it before using this tool",
                                    "/routes")
            self._denied = [one["path"] for one in routes if one.get("denied")]
        return self._denied

    def _refuse(self, method: str, path: str) -> None:
        if method != "GET" and not self.config.write:
            raise AgentifiError(0, "writes are off; set AGENTIFI_MCP_WRITE=1 to allow them", path)
        bare = urllib.parse.urlsplit(path).path
        if bare.startswith("/api/"):
            bare = bare[len("/api"):]
        for pattern in self._denied_routes():
            if _under_route(pattern, bare):
                raise AgentifiError(0, f"{pattern} is refused to a model, as it is to the "
                                       "in-app assistant; a person does that in the app", path)

    def request(self, method: str, path: str, params: dict[str, Any] | None = None,
                body: Any = None) -> Any:
        method = method.upper()
        if not self.token:
            self.login()
        self._refuse(method, path if path.startswith("/") else "/" + path)
        try:
            return self._send(method, self._url(path, params), body, self._headers())
        except AgentifiError as error:
            # An access token has a lifetime; a session longer than that signs
            # in again once rather than failing every call after it.
            if error.status != 401 or not self.config.password or self.config.token:
                raise
            self.token = ""
            self.login()
            return self._send(method, self._url(path, params), body, self._headers())

    def get(self, path: str, params: dict[str, Any] | None = None) -> Any:
        return self.request("GET", path, params)

    def post(self, path: str, body: Any = None, params: dict[str, Any] | None = None) -> Any:
        return self.request("POST", path, params, body if body is not None else {})

    def patch(self, path: str, body: Any) -> Any:
        return self.request("PATCH", path, None, body)

    def put(self, path: str, body: Any) -> Any:
        return self.request("PUT", path, None, body)

    def delete(self, path: str) -> Any:
        return self.request("DELETE", path)
