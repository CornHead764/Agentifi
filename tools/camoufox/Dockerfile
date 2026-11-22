# Camoufox (a Firefox build) as a Playwright browser server. The application
# drives it over ws://camoufox:9333/<CAMOUFOX_WS_PATH> for the providers
# whose sign-in only works in Firefox; the path is required at start. See
# README.md. CI builds it beside the
# application image and tags it the same, so AGENTIFI_TAG pins both.
FROM python:3.12.14-slim-bookworm@sha256:392307d22300de8b5986851a12d9176dfc0fc073e65bf6523ebd7dcbeb23564e

# A locale, not C: Firefox reports it to the page. The time zone is the
# installer's to set (TZ in the compose file); without one it is UTC.
ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    LANG=en_US.UTF-8 \
    LANGUAGE=en_US:en

# requirements.txt pins every package and the hash of every file, so pip
# refuses anything else; wheels only, since a source build would fetch its
# build tools unchecked. **playwright must be the version playwright-go's
# driver is**: requirements.in says why.
#
# The browser runs headful on a virtual display (serve.py), so the image
# carries Xvfb, which Playwright's Firefox dependencies do not include. Xvfb
# run as the unprivileged user cannot create its socket directory itself.
COPY requirements.txt /tmp/requirements.txt
RUN pip install --require-hashes --only-binary=:all: --no-deps -r /tmp/requirements.txt \
    && rm /tmp/requirements.txt \
    && python -m playwright install-deps firefox \
    && apt-get update \
    && apt-get install -y --no-install-recommends xvfb \
    && rm -rf /var/lib/apt/lists/* \
    && install -d -m 1777 /tmp/.X11-unix \
    && useradd --create-home --uid 10001 browser \
    && fpgen_data="$(python -c 'import importlib.util, os; print(os.path.join(os.path.dirname(importlib.util.find_spec("fpgen").origin), "data"))')" \
    && install -d "$fpgen_data" && chown -R browser "$fpgen_data"

USER browser
WORKDIR /home/browser
# The browser lands in ~/.cache/camoufox, and fpgen's fingerprint model in its
# package data dir (owned by browser above, as camoufox fetch writes it). Fetched at build time, not at
# start: a container that downloads its browser on every restart is a pull
# that fails for a minute after every reboot.
#
# The camoufox package names the browser release (its browser-pin.json) and
# checks the download against the sha256 GitHub lists for the asset, then
# records that sha256 in the install's version.json. The build fails unless
# the recorded value is the one below for this architecture: the sha256 of
# camoufox-156.0.1-beta.36-lin.<arch>.zip from the daijro/camoufox release
# v156.0.1-beta.36. `camoufox fetch` exits 0 even when it fails, so this check
# is also what catches a failed fetch.
ARG TARGETARCH
RUN case "$TARGETARCH" in \
        amd64) want=72637885ee67c93466ad377831c647192f8f08fb5c6fd9279de1320b49dd2b53 ;; \
        arm64) want=8b5fd083721c807ee2a752265683482477105052fa0e21739388b527ae067c15 ;; \
        *) echo "no Camoufox browser hash for architecture '$TARGETARCH'" >&2; exit 1 ;; \
    esac \
    && python -m camoufox fetch \
    && python -c 'import glob, json, sys; got = [json.load(open(p)).get("sha256") for p in glob.glob(sys.argv[1] + "/browsers/*/*/version.json")]; sys.exit(None if got == [sys.argv[2]] else f"Camoufox browser sha256 {got}, want {sys.argv[2]}")' \
        "$(python -m camoufox path)" "$want" \
    && mkdir -p /home/browser/state

COPY --chown=browser serve.py .

EXPOSE 9333
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD python -c "import socket; socket.create_connection(('127.0.0.1', 9333), 4).close()"
CMD ["python", "serve.py"]
