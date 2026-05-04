#!/usr/bin/env bash
#
# Date: 2026-05-04
# Author: Spicer Matthews <spicer@cloudmanic.com>
# Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
#
# Description: Build and deploy the spotify-shortcut server to stowe (a
# local Mac on the LAN). Cross-compiles for darwin/arm64, copies the
# binary, .env, and (if present) .spotify_token.json into
# ~/spotify-shortcut on stowe, installs a launchd plist so the service
# restarts on reboot, and verifies it's running.
#
# Usage:
#   scripts/deploy.sh
#
# Requirements:
#   - SSH access to deploy@stowe with key auth (already configured).
#   - Working .env locally with SPOTIFY_CLIENT_ID, SPOTIFY_CLIENT_SECRET,
#     API_ACCESS_TOKEN.
#

set -euo pipefail

# --- Configuration -----------------------------------------------------------

REMOTE_USER="deploy"
REMOTE_HOST="stowe"
REMOTE="${REMOTE_USER}@${REMOTE_HOST}"
REMOTE_HOME="/Users/${REMOTE_USER}"
REMOTE_DIR="${REMOTE_HOME}/spotify-shortcut"
SERVICE_LABEL="com.cloudmanic.spotify-shortcut"
PLIST_PATH="${REMOTE_HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist"
BINARY="spotify-shortcut"

# Resolve the project root regardless of where the script is invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_ROOT}"

# --- Pre-flight checks -------------------------------------------------------

if [ ! -f .env ]; then
  echo "Error: .env not found in $(pwd). Aborting." >&2
  exit 1
fi

# --- Build -------------------------------------------------------------------

echo "==> Building ${BINARY} for darwin/arm64..."
GOOS=darwin GOARCH=arm64 go build -trimpath -o "${BINARY}" .

# --- Remote prep -------------------------------------------------------------

echo "==> Ensuring remote dirs exist..."
ssh "${REMOTE}" "mkdir -p '${REMOTE_DIR}' '${REMOTE_HOME}/Library/LaunchAgents'"

echo "==> Stopping existing service (if running)..."
# bootout on macOS 12+; fall back to legacy unload. Ignore errors when service
# is not yet installed.
ssh "${REMOTE}" "launchctl bootout gui/\$(id -u) '${PLIST_PATH}' 2>/dev/null || launchctl unload '${PLIST_PATH}' 2>/dev/null || true"

# --- Copy artifacts ----------------------------------------------------------

echo "==> Copying binary..."
scp -q "${BINARY}" "${REMOTE}:${REMOTE_DIR}/${BINARY}"

echo "==> Copying .env..."
scp -q .env "${REMOTE}:${REMOTE_DIR}/.env"

# Token file is optional — if missing, deploy still succeeds and the user
# authenticates by visiting /auth on the deployed server.
if [ -f .spotify_token.json ]; then
  echo "==> Copying .spotify_token.json..."
  scp -q .spotify_token.json "${REMOTE}:${REMOTE_DIR}/.spotify_token.json"
else
  echo "==> No local .spotify_token.json — first start will need /auth flow."
fi

# --- Install launchd plist ---------------------------------------------------

# Generate plist locally then scp. Heredoc is single-quoted to avoid local
# variable expansion; we substitute REMOTE_DIR via sed for the only template
# slot we care about.
PLIST_TMP="$(mktemp -t spotify-shortcut-plist.XXXX)"
trap "rm -f '${PLIST_TMP}'" EXIT

cat > "${PLIST_TMP}" <<'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>__SERVICE_LABEL__</string>

  <key>WorkingDirectory</key>
  <string>__REMOTE_DIR__</string>

  <key>ProgramArguments</key>
  <array>
    <string>__REMOTE_DIR__/spotify-shortcut</string>
    <string>-server</string>
  </array>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>__REMOTE_DIR__/server.log</string>

  <key>StandardErrorPath</key>
  <string>__REMOTE_DIR__/server.err</string>

  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/local/bin:/usr/bin:/bin</string>
  </dict>
</dict>
</plist>
PLIST_EOF

# Substitute placeholders. Use a non-/ delimiter for sed since paths contain /.
sed -i '' \
  -e "s|__SERVICE_LABEL__|${SERVICE_LABEL}|g" \
  -e "s|__REMOTE_DIR__|${REMOTE_DIR}|g" \
  "${PLIST_TMP}"

echo "==> Installing launchd plist..."
scp -q "${PLIST_TMP}" "${REMOTE}:${PLIST_PATH}"

# --- Start service -----------------------------------------------------------

echo "==> Starting service..."
ssh "${REMOTE}" "launchctl bootstrap gui/\$(id -u) '${PLIST_PATH}' 2>/dev/null || launchctl load '${PLIST_PATH}'"

# --- Verify ------------------------------------------------------------------

echo "==> Waiting for service to come up..."
sleep 3

echo "==> Verifying via launchctl list..."
ssh "${REMOTE}" "launchctl list | grep '${SERVICE_LABEL}' || echo 'NOT FOUND'"

echo "==> Last 10 lines of server log..."
ssh "${REMOTE}" "tail -n 10 '${REMOTE_DIR}/server.log' 2>/dev/null || echo '(no log yet)'"

echo
echo "Deployed. Server should be reachable at http://${REMOTE_HOST}:8080"
echo "  curl -s \"http://${REMOTE_HOST}:8080/api/v1/devices?token=\$(jq -r .api_access_token ~/.config/spotify-shortcut.json)\" | jq"
