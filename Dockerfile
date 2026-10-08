# Agentifi ships as one static binary with the React application inside it.
#
# A self-hosted install is this image and a SQLite file, nothing else. See
# docs/architecture.md.
#
# **The image does not carry Google Chrome**, which the bill and merchant
# connectors drive in this process: Google's terms do not allow
# redistributing it. It carries scripts/install-chrome.sh, which the compose
# file's one-shot chrome service runs to download Chrome stable from Google
# into a volume on each start. `agentifi browser-selftest` is the acceptance
# step:
#
#   docker build -t agentifi .
#   docker run --rm -v agentifi-chrome:/chrome --entrypoint install-chrome agentifi /chrome
#   docker run --rm -v agentifi-chrome:/chrome:ro agentifi browser-selftest

# ---- the frontend, built first so its output can be embedded ----------------
FROM node:22.23.3-slim AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# The import dialog bundles the Simplifi exporter from ../tools, beside /src.
COPY tools/extractors/extract-simplifi.js /tools/extractors/extract-simplifi.js
RUN npm run build

# ---- the binary -------------------------------------------------------------
FROM golang:1.26.8 AS backend
WORKDIR /src

# Dependencies first, so a source-only change does not re-download the module
# cache on every build.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# internal/web embeds this directory. The committed placeholder is replaced by
# the real build; //go:embed reads from disk at compile time, so the copy has to
# happen before `go build` and cannot be a runtime mount.
COPY --from=frontend /src/dist ./internal/web/dist

# The commit and build time Server admin shows. The context has no .git, so
# whoever builds passes them, as CI does. Empty reads as unknown.
ARG AGENTIFI_COMMIT=""
ARG AGENTIFI_BUILT_AT=""

# CGO off and a trimmed path so the result does not leak build machine paths
# into panics.
ENV CGO_ENABLED=0
RUN go build -trimpath \
    -ldflags="-s -w \
      -X github.com/CornHead764/agentifi/backend/internal/buildinfo.Commit=${AGENTIFI_COMMIT} \
      -X github.com/CornHead764/agentifi/backend/internal/buildinfo.BuiltAt=${AGENTIFI_BUILT_AT}" \
    -o /agentifi ./cmd/agentifi

# ---- the Playwright driver: a Node binary and the playwright-core package ----
#
# Assembled from npm by scripts/playwright-driver.sh, the same script a
# workstation runs, rather than downloaded by playwright-go, which fetches
# `playwright-<version>-linux.zip` from playwright.azureedge.net (404 for every
# version since Microsoft retired that CDN) and from cdn.playwright.dev (400 for
# the same path). The zip *is* this: the playwright-core package plus a Node
# binary. Assembling it at build time also means the running application never
# reaches for the network to get one.
#
# The version is the one the playwright-go module in go.mod pins, which the
# driver must match exactly. Reading it takes Go and assembling takes npm, so
# a Go stage reads it and hands it to the npm stage.
FROM golang:1.26.8 AS playwright-version
WORKDIR /src
COPY backend/go.mod backend/go.sum backend/
COPY scripts/playwright-driver.sh scripts/
RUN scripts/playwright-driver.sh --version > /playwright-version

FROM node:22.23.3-slim AS driver
WORKDIR /src
COPY scripts/playwright-driver.sh scripts/
COPY --from=playwright-version /playwright-version /playwright-version
RUN PLAYWRIGHT_VERSION="$(cat /playwright-version)" scripts/playwright-driver.sh /driver

# ---- what ships -------------------------------------------------------------
#
# Not distroless: the browser wants a hundred or so shared libraries, fonts
# and a writable home. Plain Ubuntu plus Playwright's own `install-deps
# chromium` list supplies exactly those, and no browser: the image carries
# none. The Playwright driver comes from the stage above; Chrome itself is
# installed at run time by install-chrome into /chrome, and is what the
# application drives (FindChrome in internal/browser).
#
# Why Chrome and not Playwright's bundled Chromium: providers' sign-in pages
# are built for Google Chrome.
FROM ubuntu:24.04
COPY --from=backend /agentifi /agentifi
COPY --from=driver /driver /opt/playwright-driver
COPY scripts/install-chrome.sh /usr/local/bin/install-chrome
RUN apt-get update -qq \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
        ca-certificates curl wget \
    && DEBIAN_FRONTEND=noninteractive /opt/playwright-driver/node \
        /opt/playwright-driver/package/cli.js install-deps chromium \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

ENV PLAYWRIGHT_DRIVER_PATH=/opt/playwright-driver \
    PLAYWRIGHT_NODEJS_PATH=/opt/playwright-driver/node \
    DATABASE_PATH=/data/db/agentifi.db \
    STORAGE_PATH=/data/attachments \
    AGENT_PROFILES_DIR=/profiles \
    HTTP_ADDR=:8000

# uid 65532 is what the host's attachments directory and secret files must be
# owned by (the compose file's secrets service sees to both), and what the
# profiles volume is owned by. HOME has to be writable: Chrome
# writes a crashpad database and a singleton lock there even when it has a
# profile directory of its own. /chrome is empty and owned by 65532 so that a
# new chrome volume, which takes the ownership of the directory it is mounted
# on, is writable by install-chrome running as that user.
RUN groupadd -g 65532 nonroot \
    && useradd -u 65532 -g 65532 -m -d /home/nonroot nonroot \
    && mkdir -p /data/attachments /profiles /chrome \
    && chown -R 65532:65532 /data /profiles /chrome /home/nonroot

VOLUME ["/data", "/profiles"]
EXPOSE 8000
USER 65532:65532
ENTRYPOINT ["/agentifi"]
CMD ["serve"]
