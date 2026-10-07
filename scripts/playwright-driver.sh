#!/usr/bin/env bash
# Assemble the Playwright driver playwright-go runs: the playwright-core
# package from npm and a Node binary beside it, laid out as playwright-go's own
# download would leave them (<dir>/package/cli.js and <dir>/node). That
# download fetches from a CDN that answers 404, so this is how the driver is
# installed, by the image's driver stage and on a workstation alike.
#
#   scripts/playwright-driver.sh [<dir>]    assemble the driver into <dir>
#   scripts/playwright-driver.sh --version  print the version and stop
#
# <dir> defaults to PLAYWRIGHT_DRIVER_PATH, else to where playwright-go looks
# without it: ~/.cache/ms-playwright-go/<version> (~/Library/Caches/… on
# macOS). An existing driver there is replaced.
#
# The version is the one the playwright-go module in backend/go.mod pins, read
# from that module's source with `go`; the driver must match it exactly or
# playwright-go refuses to start it. PLAYWRIGHT_VERSION set in the environment
# is taken as already read, for a build stage that has npm but no Go.
#
# Needs npm, and node on PATH: that binary is copied into the driver, so after
# upgrading Node, run this again.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MODULE=github.com/playwright-community/playwright-go

pinned_version() {
  local dir
  dir=$(cd "${ROOT}/backend" && go mod download -json "${MODULE}" \
    | sed -n 's/^[[:space:]]*"Dir": "\(.*\)",\{0,1\}$/\1/p')
  [[ -n ${dir} ]] || { echo "could not find ${MODULE} through backend/go.mod" >&2; exit 1; }
  sed -n 's/^const playwrightCliVersion = "\(.*\)"$/\1/p' "${dir}/run.go"
}

VERSION=${PLAYWRIGHT_VERSION:-$(pinned_version)}
[[ -n ${VERSION} ]] || { echo "could not read the Playwright version ${MODULE} pins" >&2; exit 1; }

if [[ ${1:-} == --version ]]; then
  echo "${VERSION}"
  exit 0
fi

DIR=${1:-${PLAYWRIGHT_DRIVER_PATH:-}}
if [[ -z ${DIR} ]]; then
  case "$(uname -s)" in
    Darwin) DIR="${HOME}/Library/Caches/ms-playwright-go/${VERSION}" ;;
    *) DIR="${HOME}/.cache/ms-playwright-go/${VERSION}" ;;
  esac
fi

if [[ -e ${DIR} && -n $(ls -A "${DIR}") && ! -f ${DIR}/package/cli.js ]]; then
  echo "${DIR} is not empty and holds no driver; refusing to replace it" >&2
  exit 1
fi

NODE=$(command -v node) || { echo "node is not on PATH" >&2; exit 1; }
WORK=$(mktemp -d)
trap 'rm -rf "${WORK}"' EXIT

echo "→ playwright-core ${VERSION} from npm"
(cd "${WORK}" && npm pack --silent "playwright-core@${VERSION}" >/dev/null)
mkdir -p "${WORK}/driver/package"
tar xzf "${WORK}/playwright-core-${VERSION}.tgz" --strip-components=1 -C "${WORK}/driver/package"
cp -L "${NODE}" "${WORK}/driver/node"

reported=$("${WORK}/driver/node" "${WORK}/driver/package/cli.js" --version)
[[ ${reported} == *"${VERSION}"* ]] || { echo "the assembled driver reports ${reported}, not ${VERSION}" >&2; exit 1; }

rm -rf "${DIR}"
mkdir -p "$(dirname "${DIR}")"
mv "${WORK}/driver" "${DIR}"
echo "✓ ${reported} in ${DIR}"
