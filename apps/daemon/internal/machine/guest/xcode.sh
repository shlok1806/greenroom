#!/bin/sh
# : greenroom-xcode-setup
# xcode.sh: make the Xcode that prepare-image copied to $1 (/Applications/Xcode.app) the
# guest's developer directory, with nothing left that waits for a click (ADR 0026). Runs in
# the guest as the admin user, which has passwordless sudo, through `greenroom prepare-image`.
# $2 is the macOS version the host's Xcode says it needs (LSMinimumSystemVersion), only for
# the message when it does not run here.
#
# Every setting is read back at the end, and a failed read-back fails the build with the
# name of the check, as base.sh does.
set -u

app="${1:?usage: xcode.sh <Xcode.app> [minimum macOS]}"
min="${2:-unknown}"
dev="$app/Contents/Developer"
fail=""
check() { # check <name> <command...>
  name="$1"; shift
  if ! "$@" >/dev/null 2>&1; then
    fail="$fail $name"
    echo "xcode: check failed: $name" >&2
  fi
}
is() { [ "$1" = "$2" ]; }

if ! DEVELOPER_DIR="$dev" "$dev/usr/bin/xcodebuild" -version >/dev/null 2>&1; then
  echo "xcode: $app does not run on this guest (macOS $(sw_vers -productVersion)); this Xcode needs macOS $min or later. Build from a newer base image, or pass build-image.sh -xcode an older Xcode." >&2
  exit 1
fi

# Login Items & Extensions (Background Task Management, BTM). Xcode carries a Quick Look
# previewer and a Spotlight importer. When LaunchServices registers Xcode,
# backgroundtaskmanagementd records them, and about 12 s later its agent posts "Multiple
# Extensions Added" as an alert: it stays on screen until someone closes it, and usernoted
# keeps it, so every clone of the image showed it at every login. Register now, so BTM posts
# while the rest of this script runs; the wait below makes sure it has posted, and base.sh
# closes the alert once tart-guest-agent may script System Events. lsregister is not on PATH;
# it goes last so a test can put its own first.
PATH="$PATH:/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support"
lsregister -f "$app"
bundle_id="$(plutil -extract CFBundleIdentifier raw -o - "$app/Contents/Info.plist")"
# btm_pending prints how many of Xcode's BTM items are not yet notified (BTM marks them
# notified when it posts), or "none" when BTM lists none of them.
btm_pending() {
  sudo -n sfltool dumpbtm 2>/dev/null | awk -v url="file://$app/" -v parent="$bundle_id" '
    function flush() { if (mine) { items++; if (waiting) pending++ } mine = 0; waiting = 0 }
    /^[[:space:]]*#[0-9]+:[[:space:]]*$/ { flush(); next }
    $1 == "URL:" && $2 == url { mine = 1 }
    $1 == "Parent" && $2 == "Identifier:" && $3 ~ ("^[0-9]+\\." parent "$") { mine = 1 }
    $1 == "Disposition:" && /not notified/ { waiting = 1 }
    END { flush(); if (items) print pending + 0; else print "none" }'
}

# The developer directory every tool resolves (swift, xcrun, xcodebuild). Without it they
# use the Command Line Tools the Cirrus base has.
sudo -n xcode-select -s "$dev"
# The license, else every xcodebuild refuses to run until someone agrees to it.
sudo -n xcodebuild -license accept
# First launch: installs the system packages (MobileDevice and friends) that Xcode.app
# otherwise offers in its "Install additional components" dialog.
sudo -n xcodebuild -runFirstLaunch >/dev/null
# Debugging and test runners attach to processes; with developer mode off, macOS asks for
# an administrator password in a dialog ("Developer Tools Access") the first time they do.
sudo -n DevToolsSecurity -enable >/dev/null
sudo -n dseditgroup -o edit -a "$(id -un)" -t user _developer
# BTM has posted once every Xcode item reads notified. Left to happen later, the alert could
# arrive after base.sh and be baked into every image. When BTM still lists nothing of this
# Xcode after 30 s, it has nothing to post about.
i=0
while [ $i -lt 90 ]; do
  pending="$(btm_pending)"
  [ "$pending" = 0 ] && break
  [ "$pending" = none ] && [ $i -ge 30 ] && break
  sleep 1
  i=$((i+1))
done

# --- Read back. -----------------------------------------------------------------------------
version="$(plutil -extract CFBundleShortVersionString raw -o - "$app/Contents/Info.plist")"
check xcode-select is "$(xcode-select -p)" "$dev"
check xcodebuild-version sh -c 'xcodebuild -version | grep -q "^Xcode "'
check license is "$(defaults read /Library/Preferences/com.apple.dt.Xcode IDEXcodeVersionForAgreedToGMLicense 2>/dev/null)" "$version"
check first-launch xcodebuild -checkFirstLaunchStatus
check developer-mode sh -c 'DevToolsSecurity -status | grep -q "currently enabled"'
check developer-group dseditgroup -o checkmember -m "$(id -un)" _developer
# The copy kept every signature: a broken seal would make Gatekeeper refuse Xcode.app and
# its helper apps, and codesign refuse what it builds against them.
check codesign codesign --verify --deep --strict "$app"
check gatekeeper spctl --assess --type execute "$app"
check no-quarantine sh -c "! xattr -p com.apple.quarantine '$app'"
check btm-notified sh -c '[ "$1" = 0 ] || [ "$1" = none ]' _ "$(btm_pending)"

if [ -n "$fail" ]; then
  echo "xcode: failed:$fail" >&2
  exit 1
fi
echo "xcode: ok, $(xcodebuild -version | tr '\n' ' ' | sed 's/ *$//') at $dev"
