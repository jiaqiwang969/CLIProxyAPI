#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="CLIProxyMenuBar.app"
DEST_APP="${HOME}/Applications/${APP_NAME}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cliproxy-menubar.XXXXXX")"
BUILD_APP="${TMP_DIR}/${APP_NAME}"

cleanup() {
  /bin/rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

"${ROOT_DIR}/scripts/build-menubar-app.sh" "${BUILD_APP}" >/dev/null

mkdir -p "$(dirname "${DEST_APP}")"
/bin/rm -rf "${DEST_APP}"
ditto "${BUILD_APP}" "${DEST_APP}"

if [[ "${1:-}" == "--open" ]]; then
  open "${DEST_APP}"
fi

echo "installed ${DEST_APP}"
