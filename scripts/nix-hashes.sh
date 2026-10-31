#!/usr/bin/env bash
# Rewrites the fixed-output hashes in flake.nix and nix/ that a dependency
# update has made stale, such as vendorHash in nix/package.nix after a go.sum
# change. It builds the package, and for each hash mismatch Nix reports,
# replaces the hash Nix was given with the one it got, until the build passes.
#
#   scripts/nix-hashes.sh [installable]    default .#agentifi
#
# Each replacement is printed to stdout as "<old> <new>". It exits non-zero
# when the build fails for any other reason, or when the stale hash is not
# written exactly once in those files (lib.fakeHash, say, is not replaced).
set -euo pipefail

cd "$(dirname "$0")/.."
INSTALLABLE=${1:-.#agentifi}
FILES=(flake.nix nix/*.nix)
HASH='sha256-[A-Za-z0-9+/]{43}='
# Each round fixes one derivation; more rounds than the files hold hashes
# means a hash that does not settle.
ROUNDS=8

log=$(mktemp)
trap 'rm -f "${log}"' EXIT

for _ in $(seq "${ROUNDS}"); do
  if nix --extra-experimental-features 'nix-command flakes' build --no-link -L "${INSTALLABLE}" 2>"${log}"; then
    exit 0
  fi
  specified=$(sed -nE "s/^[[:space:]]*specified:[[:space:]]*(${HASH})[[:space:]]*$/\1/p" "${log}" | head -n 1)
  got=$(sed -nE "s/^[[:space:]]*got:[[:space:]]*(${HASH})[[:space:]]*$/\1/p" "${log}" | head -n 1)
  if [[ -z ${specified} || -z ${got} ]]; then
    tail -n 50 "${log}" >&2
    echo "the build failed without a hash mismatch" >&2
    exit 1
  fi
  count=$(cat "${FILES[@]}" | { grep -oF -- "${specified}" || true; } | wc -l)
  if [[ ${count} -ne 1 ]]; then
    echo "${specified} is written ${count} times in ${FILES[*]}, not once; replace it with ${got} by hand" >&2
    exit 1
  fi
  file=$(grep -lF -- "${specified}" "${FILES[@]}")
  # The hashes are base64: no | and no character sed reads as special.
  sed "s|${specified}|${got}|" "${file}" >"${log}"
  cat "${log}" >"${file}"
  echo "${specified} ${got}"
done

echo "the build still reports a stale hash after ${ROUNDS} rounds" >&2
exit 1
