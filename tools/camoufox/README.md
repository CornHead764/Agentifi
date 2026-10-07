# Camoufox

A Camoufox browser (a Firefox build) served over Playwright's own protocol.
The application connects to it with playwright-go and drives it exactly as it
drives Chrome: the same modules, sign-in loop and readers. This container
holds no logic.

It is chosen **per provider**, for the ones whose sign-in only works in
Firefox. For those, everything happens here: the sign-in, the pull, and the
provider's own API calls. Nothing is handed between browsers. A provider opts
in by implementing `browser.FirefoxProvider` (`RunsInFirefox() bool`).

```
CAMOUFOX_WS_PATH=<openssl rand -hex 24>                  required by serve.py
CAMOUFOX_URL=ws://camoufox:9333/<CAMOUFOX_WS_PATH>       what the application dials
```

A Playwright server has no authentication of its own, so the websocket path is
the shared secret: `serve.py` refuses to start unless `CAMOUFOX_WS_PATH` is at
least 24 characters. It reads the variable, or else the file
`CAMOUFOX_WS_PATH_FILE` names (default `/run/secrets/CAMOUFOX_WS_PATH`). In
the root `docker-compose.yml` the `secrets` service generates the path on first
start and writes both files, so neither container carries it in its
environment. With `CAMOUFOX_URL` unset, a provider that runs here fails with
"this provider runs in Camoufox and CAMOUFOX_URL is not set"; it never falls
back to Chrome.

## What a caller has to know

- **Scripts run in an isolated world.** `Evaluate` and init scripts see the
  DOM and the origin's storage, but not the page's own JavaScript. A hook on
  `fetch` or XHR patches a copy of `window` that the app never calls. Read
  what the app stores, or watch requests at the protocol level, instead.
- **It runs headed.** Some sign-in pages need a headed browser, so
  `serve.py` starts an Xvfb virtual display and launches Firefox headed on
  it. The display is 1x1; the page's window size is the context's
  viewport.
- **Pointer and keys are paced.** The browser is launched with Camoufox's
  cursor-movement option (`humanize`), so a click or mouse move a client asks
  for travels to its target rather than jumping there. It is browser configuration and applies
  to every context a client opens. Typing is the client's side:
  `browser.Page.TypeInto` clicks a field and types it key by key at
  `browser.TypingPace`, which a sign-in form that acts only on typed input
  needs; `Fill` sets the value whole.
- **No profile on disk.** A session is a sealed storage state, seeded into a
  fresh context for every pull.
- **No live view.** The screencast is Chrome's DevTools protocol; every
  provider signs in through the typed form.
- **Versions must match.** `playwright` in the Dockerfile must be the driver
  version playwright-go runs (go.mod), or the connection is refused.
- **Set the time zone.** The browser reports its zone and locale to the page,
  and some providers check them at sign-in. The image
  sets the locale; the compose file passes `TZ` from `.env`, set to the
  deployment's own time zone.

It publishes no port on purpose, and in `docker-compose.yml` it sits on a
network (`agentifi-browser`) that only the application shares.

## Running it for development

For a backend run from source on the same machine, build the image and publish its port on loopback only:

```sh
docker build -t agentifi-camoufox tools/camoufox
docker run --rm -e CAMOUFOX_WS_PATH=<path> -e TZ=<your zone> \
  -p 127.0.0.1:9333:9333 agentifi-camoufox
```

and set `CAMOUFOX_URL=ws://127.0.0.1:9333/<path>` in the backend's `.env`.

With the same `CAMOUFOX_URL` exported, the invented Costco sign-in page in
`backend/internal/merchants/costco_b2c_live_test.go` runs against this
browser and checks that the form is clicked, typed and pressed through real
browser input events, with the pointer moved along a drawn path:

```sh
cd backend && go test ./internal/merchants/ -run TypedAndPressed -v
```

`PLAYWRIGHT_DRIVER_PATH` points playwright-go at a driver it did not install
itself, such as the `playwright/driver` directory of a Python `playwright`
of the same version.
