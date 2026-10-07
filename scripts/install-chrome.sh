#!/usr/bin/env bash
# Install Google Chrome stable from Google's own apt repository into a
# directory, for the engine to launch (AGENTIFI_CHROME_PATH). The image does
# not carry Chrome, because Google's terms do not allow redistributing it:
# each install downloads it from Google, and it is subject to Google's terms.
#
#   scripts/install-chrome.sh <dir>
#
# The compose file's `chrome` service runs this on every start, against the
# `chrome` volume; CI runs it on the runner. It reads the google-chrome-stable
# entry of the repository's Packages index, downloads that .deb over HTTPS,
# checks it against the index's SHA-256, and extracts it beside what is
# installed:
#
#   <dir>/<version>/chrome    the browser, with ORIGIN saying where it came from
#   <dir>/current             a symlink to the version the engine launches
#
# `current` is switched only once a version is fully in place, so a failed or
# partial download never breaks the Chrome already there. It moves when the
# index lists a newer stable than the installed one; the version before it is
# kept, for a browser still running from it, and older ones are removed.
#
# Without the network, an installed Chrome is kept and this exits 0; with
# none installed it exits 1.
#
# A connection that stalls, or drops under 50 KB/s for 30 seconds, counts
# as no network rather than waiting forever.
#
# CHROME_REPOSITORY is the repository's base URL, for a test; it defaults to
# Google's. Needs dpkg-deb, sha256sum, sort -V, and curl or wget.
set -euo pipefail

REPOSITORY=${CHROME_REPOSITORY:-https://dl.google.com/linux/chrome/deb}
PACKAGE=google-chrome-stable

DIR=${1:?usage: install-chrome.sh <dir>}
mkdir -p "${DIR}"
DIR=$(cd "${DIR}" && pwd)

say() { echo "chrome: $*"; }

fetch() {
  if command -v curl >/dev/null; then
    curl -fsSL --retry 2 --connect-timeout 20 --speed-limit 50000 --speed-time 30 -o "$2" "$1"
  else
    wget -q --tries=3 --timeout=30 -O "$2" "$1"
  fi
}

installed=""
if [[ -L ${DIR}/current && -x ${DIR}/current/chrome ]]; then
  installed=$(readlink "${DIR}/current")
fi

# Exits 0 when a Chrome is installed, so a host with no route out still
# starts, and 1 when there is none to launch.
give_up() {
  if [[ -n ${installed} ]]; then
    say "$1; keeping Chrome ${installed}"
    exit 0
  fi
  say "$1, and no Chrome is installed: the connectors cannot open a browser."
  say "Run \`docker compose up chrome\` once the host can reach dl.google.com,"
  say "or pre-seed the volume as docs/operations.md describes."
  exit 1
}

rm -rf "${DIR}"/.download.*
WORK=$(mktemp -d "${DIR}/.download.XXXXXX")
trap 'rm -rf "${WORK}"' EXIT

fetch "${REPOSITORY}/dists/stable/main/binary-amd64/Packages" "${WORK}/Packages" \
  || give_up "could not read ${REPOSITORY}'s package index"

# One line per google-chrome-stable stanza, "<version> <filename> <sha256>",
# the newest last.
latest=$(awk -v want="${PACKAGE}" '
  function flush() {
    if (pkg == want && ver != "" && file != "" && sum != "") print ver, file, sum
    pkg = ver = file = sum = ""
  }
  /^$/ { flush(); next }
  /^Package: / { pkg = $2 }
  /^Version: / { ver = $2 }
  /^Filename: / { file = $2 }
  /^SHA256: / { sum = $2 }
  END { flush() }
' "${WORK}/Packages" | sort -V -k1,1 | tail -n 1)
[[ -n ${latest} ]] || give_up "the package index lists no ${PACKAGE}"
read -r version filename sha256 <<<"${latest}"
# Both become paths, here and on the server.
if [[ ! ${version} =~ ^[0-9][A-Za-z0-9.+~-]*$ || ! ${filename} =~ ^pool/[A-Za-z0-9.+~_/-]+\.deb$ \
  || ${filename} == *..* ]]; then
  give_up "the package index names an unexpected version or file (${version} ${filename})"
fi

if [[ -n ${installed} \
  && $(printf '%s\n%s\n' "${installed}" "${version}" | sort -V | tail -n 1) == "${installed}" ]]; then
  say "Chrome ${installed} is installed, and the index lists nothing newer"
  exit 0
fi

say "downloading Chrome ${version} from ${REPOSITORY}"
fetch "${REPOSITORY}/${filename}" "${WORK}/chrome.deb" \
  || give_up "could not download ${REPOSITORY}/${filename}"
echo "${sha256}  ${WORK}/chrome.deb" | sha256sum -c --quiet - \
  || give_up "the download does not match the SHA-256 the package index gives"

dpkg-deb -x "${WORK}/chrome.deb" "${WORK}/unpacked" \
  || give_up "could not extract the downloaded package"
[[ -x ${WORK}/unpacked/opt/google/chrome/chrome ]] \
  || give_up "the package holds no opt/google/chrome/chrome"
printf 'package %s %s\nfrom    %s/%s\nsha256  %s\n' \
  "${PACKAGE}" "${version}" "${REPOSITORY}" "${filename}" "${sha256}" \
  > "${WORK}/unpacked/opt/google/chrome/ORIGIN"

rm -rf "${DIR:?}/${version}"
mv "${WORK}/unpacked/opt/google/chrome" "${DIR}/${version}"
ln -sfn "${version}" "${DIR}/.current.new"
mv -T "${DIR}/.current.new" "${DIR}/current"
say "installed Chrome ${version}"

for old in "${DIR}"/*; do
  name=$(basename "${old}")
  if [[ -d ${old} && ! -L ${old} && ${name} != "${version}" && ${name} != "${installed}" ]]; then
    rm -rf "${old}"
  fi
done
