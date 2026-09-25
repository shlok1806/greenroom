#!/usr/bin/env bash
# Copies the repo's design data (`design/tokens.json`, `design/themes/*.json`) into the
# Companion target's resources. SwiftPM bundles only files inside a target, so the app
# ships a copy; `DesignBundleTests` fails when the copy and `design/` differ. Run it after
# every change under `design/`.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

SOURCE="../../design"
TARGET="Sources/Companion/Resources/Design"

rm -rf "$TARGET"
mkdir -p "$TARGET/themes"
cp "$SOURCE/tokens.json" "$TARGET/tokens.json"
cp "$SOURCE"/themes/*.json "$TARGET/themes/"

echo "synced $SOURCE into $TARGET"
