#!/usr/bin/env bash
# Puts the companion in /Applications and opens it. Everything it installs is
# built by scripts/bundle.sh; this script only moves and launches.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

DEST="/Applications/Greenroom Companion.app"

scripts/bundle.sh

# `open` on a bundle that is already running only activates the old process,
# so a reinstall would keep showing the previous build. Quit it first.
if pgrep -fq "$DEST/Contents/MacOS/Companion"; then
  osascript -e 'tell application "Greenroom Companion" to quit' >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    pgrep -fq "$DEST/Contents/MacOS/Companion" || break
    sleep 0.25
  done
  pkill -f "$DEST/Contents/MacOS/Companion" 2>/dev/null || true
  echo "quit the running app"
fi

rm -rf "$DEST"
cp -R .build/Companion.app "$DEST"
echo "installed $DEST"

open "$DEST"
echo "opened $DEST"
