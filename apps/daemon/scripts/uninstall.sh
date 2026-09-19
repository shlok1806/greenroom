#!/bin/bash
# Stop the launchd user agent and forget it. The binary and ~/.greenroom, which
# holds the run record, are the user's and are left alone.
set -euo pipefail

label="com.greenroom.daemon"
plist="$HOME/Library/LaunchAgents/$label.plist"

launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
rm -f "$plist"

echo "removed $plist"
echo "the binary at ~/.greenroom/bin/greenroom and the run record in ~/.greenroom are kept"
