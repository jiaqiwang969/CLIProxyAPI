#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_DIR="${ROOT_DIR}/apps/menubar-swift"
APP_NAME="CLIProxyMenuBar"
OUTPUT_APP="${1:-${ROOT_DIR}/build/${APP_NAME}.app}"
CONTENTS_DIR="${OUTPUT_APP}/Contents"
MACOS_DIR="${CONTENTS_DIR}/MacOS"
PLIST_PATH="${CONTENTS_DIR}/Info.plist"

swift build --package-path "${PACKAGE_DIR}"
BIN_DIR="$(swift build --package-path "${PACKAGE_DIR}" --show-bin-path)"
BIN_PATH="${BIN_DIR}/${APP_NAME}"

if [[ ! -x "${BIN_PATH}" ]]; then
  echo "missing Swift menubar binary: ${BIN_PATH}" >&2
  exit 1
fi

/bin/rm -rf "${OUTPUT_APP}"
mkdir -p "${MACOS_DIR}"

cp "${BIN_PATH}" "${MACOS_DIR}/${APP_NAME}"

cat > "${PLIST_PATH}" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>CLIProxyMenuBar</string>
  <key>CFBundleIdentifier</key>
  <string>com.jiaqi.CLIProxyMenuBar</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>CLIProxyMenuBar</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0.0</string>
  <key>CFBundleVersion</key>
  <string>1</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>LSUIElement</key>
  <true/>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
EOF

codesign --force --deep -s - "${OUTPUT_APP}" >/dev/null 2>&1 || true

echo "${OUTPUT_APP}"
