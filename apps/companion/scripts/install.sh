#!/usr/bin/env bash
# Puts the companion in /Applications and opens it. Everything it installs is
# built by scripts/bundle.sh; this script only moves and launches.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

DEST="/Applications/Greenroom Companion.app"

scripts/bundle.sh

rm -rf "$DEST"
cp -R .build/Companion.app "$DEST"
echo "installed $DEST"

open "$DEST"
echo "opened $DEST"
