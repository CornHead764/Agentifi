"""Camoufox as a Playwright browser server.

The application connects to ws://<host>:9333/<CAMOUFOX_WS_PATH> with
playwright-go and drives it exactly as it drives Chrome; there is no logic
here. See README.md.

A Playwright server has no authentication of its own: anyone who can reach the
socket drives a browser that, during a pull, holds a signed-in session. The
websocket path is the shared secret, so it is required and must be long and
random; the application's CAMOUFOX_URL carries the same path.
"""

import os
import sys
import threading

from camoufox.server import launch_server
from camoufox.virtdisplay import VirtualDisplay

MIN_PATH_LENGTH = 24


def read_ws_path() -> str:
    """CAMOUFOX_WS_PATH, else the file CAMOUFOX_WS_PATH_FILE names, else
    /run/secrets/CAMOUFOX_WS_PATH, where the compose file's secrets service
    writes it."""
    value = os.environ.get("CAMOUFOX_WS_PATH", "").strip()
    if value:
        return value
    path = os.environ.get("CAMOUFOX_WS_PATH_FILE") or "/run/secrets/CAMOUFOX_WS_PATH"
    try:
        with open(path) as f:
            return f.read().strip()
    except FileNotFoundError:
        return ""


ws_path = read_ws_path().strip("/")
if len(ws_path) < MIN_PATH_LENGTH:
    sys.exit(
        f"CAMOUFOX_WS_PATH (or the file CAMOUFOX_WS_PATH_FILE names) must hold a "
        f"random string of at least {MIN_PATH_LENGTH} characters (for example "
        f"`openssl rand -hex 24`)"
    )


def mask_secret_in_stdout(secret: str) -> None:
    """The launcher's Node child prints the full endpoint, path included, to
    the stdout it inherits, and a container log is not a place for the secret.
    Route fd 1 through a pipe that masks it; the child inherits the pipe."""
    real = os.dup(1)
    read_end, write_end = os.pipe()
    os.dup2(write_end, 1)
    os.close(write_end)
    sys.stdout = os.fdopen(1, "w", buffering=1, closefd=False)
    needle, mask = secret.encode(), b"<CAMOUFOX_WS_PATH>"

    def pump() -> None:
        with os.fdopen(read_end, "rb", buffering=0) as src, os.fdopen(real, "wb", buffering=0) as dst:
            for line in iter(src.readline, b""):
                dst.write(line.replace(needle, mask))

    threading.Thread(target=pump, daemon=True).start()


mask_secret_in_stdout(ws_path)

# Headed Firefox on an Xvfb display: some providers' sign-in pages need one.
# launch_server does not start the display for headless="virtual" the way
# Camoufox() does, so it is started here and handed over as the display to
# draw on.
#
# humanize, Camoufox's cursor-movement option, makes the browser itself move
# the cursor to wherever a client clicks or moves the mouse, rather than
# jumping there. It is browser
# configuration, so it applies to every context a remote client opens.
display = VirtualDisplay()
try:
    launch_server(
        headless=False,
        virtual_display=display.get(),
        humanize=True,
        host="0.0.0.0",
        port=int(os.environ.get("PORT", "9333")),
        ws_path=ws_path,
    )
finally:
    display.kill()
