# Camoufox (a Firefox build) as a Playwright browser server. The application
# drives it over ws://camoufox:9333/<CAMOUFOX_WS_PATH> for the providers
# whose sign-in only works in Firefox; the path is required at start. See
# README.md. CI builds it beside the
# application image and tags it the same, so AGENTIFI_TAG pins both.
FROM python:3.12.14-slim-bookworm

# A locale, not C: Firefox reports it to the page. The time zone is the
# installer's to set (TZ in the compose file); without one it is UTC.
ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    LANG=en_US.UTF-8 \
    LANGUAGE=en_US:en

# **playwright must be the version playwright-go's driver is** (go.mod:
# playwright-go v0.6000.0 runs driver 1.60.0). A Playwright client refuses a
# server of another version, and the server here is the Python package's own
# driver.
#
# The browser runs headful on a virtual display (serve.py), so the image
# carries Xvfb, which Playwright's Firefox dependencies do not include. Xvfb
# run as the unprivileged user cannot create its socket directory itself.
RUN pip install \
        "camoufox==0.5.6" \
        "playwright==1.60.0" \
    && python -m playwright install-deps firefox \
    && apt-get update \
    && apt-get install -y --no-install-recommends xvfb \
    && rm -rf /var/lib/apt/lists/* \
    && install -d -m 1777 /tmp/.X11-unix \
    && useradd --create-home --uid 10001 browser

USER browser
WORKDIR /home/browser
# The browser itself lands in ~/.cache/camoufox. Fetched at build time, not at
# start: a container that downloads its browser on every restart is a pull
# that fails for a minute after every reboot.
RUN python -m camoufox fetch

COPY --chown=browser serve.py .

EXPOSE 9333
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD python -c "import socket; socket.create_connection(('127.0.0.1', 9333), 4).close()"
CMD ["python", "serve.py"]
