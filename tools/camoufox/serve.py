"""Camoufox as a Playwright browser server.

The application connects to ws://<host>:9333/<CAMOUFOX_WS_PATH> with
playwright-go and drives it exactly as it drives Chrome; there is no logic
here. See README.md.

A Playwright server has no authentication of its own: anyone who can reach the
socket drives a browser that, during a pull, holds a signed-in session. The
websocket path is the shared secret, so it is required and must be long and
random; the application's CAMOUFOX_URL carries the same path.
"""

import json
import os
import sys
import threading

from camoufox.pkgman import installed_verstr
from camoufox.server import launch_server
from camoufox.utils import launch_options
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

FINGERPRINT_FILE = os.environ.get(
    "CAMOUFOX_FINGERPRINT_FILE", "/home/browser/state/fingerprint.json"
)


def firefox_major() -> str:
    return installed_verstr().split(".", 1)[0]


def resolved_config(options: dict) -> dict:
    """The Camoufox properties launch_options settled on, which it hands the
    browser as CAMOU_CONFIG_<n> chunks."""
    env = options.get("env") or {}
    chunks = sorted(
        (int(key.rsplit("_", 1)[1]), value)
        for key, value in env.items()
        if key.startswith("CAMOU_CONFIG_")
    )
    return json.loads("".join(value for _, value in chunks))


# The application opens every Camoufox page at 1280x720, so the device's screen
# must hold that window with room to spare: a page larger than its own screen
# is a device no one owns.
MIN_SCREEN = (1536, 864)
DRAWS = 100


def draw_device(display: str) -> dict:
    for _ in range(DRAWS):
        config = resolved_config(
            launch_options(
                os="windows",
                fingerprint_preset=True,
                locale="en-US",
                humanize=True,
                headless=False,
                virtual_display=display,
            )
        )
        width, height = config.get("screen.width", 0), config.get("screen.height", 0)
        if width >= MIN_SCREEN[0] and height >= MIN_SCREEN[1]:
            return config
    sys.exit(f"no Windows preset with a screen of at least {MIN_SCREEN[0]}x{MIN_SCREEN[1]}")


def pinned_config(display: str) -> dict:
    """One identity for the life of the installation. Left to itself Camoufox
    draws a new device on every start, and a bot manager that sees the same
    session cookies arrive from a different screen, font list and graphics
    card scores them as stolen. The first start draws a real-device Windows
    preset and keeps every resolved property; later starts hand it back, and
    launch_options only fills keys that are missing. A new Firefox major draws
    again, since the kept user agent would name the old one."""
    try:
        with open(FINGERPRINT_FILE) as f:
            kept = json.load(f)
        if kept.get("firefox") == firefox_major():
            return kept["config"]
        print("Firefox changed version: drawing a new fingerprint", flush=True)
    except FileNotFoundError:
        print("No kept fingerprint: drawing one", flush=True)

    config = draw_device(display)
    # Install paths, not identity: the next image may put them elsewhere.
    config.pop("addons", None)
    os.makedirs(os.path.dirname(FINGERPRINT_FILE), exist_ok=True)
    partial = FINGERPRINT_FILE + ".partial"
    with open(partial, "w") as f:
        json.dump({"firefox": firefox_major(), "config": config}, f)
    os.replace(partial, FINGERPRINT_FILE)
    return config


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
        config=pinned_config(display.get()),
        i_know_what_im_doing=True,
        headless=False,
        virtual_display=display.get(),
        humanize=True,
        host="0.0.0.0",
        port=int(os.environ.get("PORT", "9333")),
        ws_path=ws_path,
    )
finally:
    display.kill()
