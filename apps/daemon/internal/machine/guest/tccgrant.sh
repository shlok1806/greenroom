#!/bin/sh
# : greenroom-tcc-grant
# tccgrant.sh: grant an app under test the TCC rows base.sh already grants every pre-installed
# app, plus the family a test machine commonly needs beyond Apple Events (ADR 0038, issue
# #252). This is machine_approve_control's guest half, the run-time twin of base.sh's build-time
# enumeration: base.sh cannot know a bundle id that does not exist until something is built or
# synced in the guest. $1 is the app's .app bundle path, absolute or relative to the guest home.
# $2 is the scope: "any" (the default, the coder's machine_approve_control) or "home" (the
# verifier's, ADR 0044, issue #269), which refuses a bundle or executable that resolves outside
# the guest home, where everything a run builds or syncs lands. Pre-installed apps already have
# their Apple Events rows from base.sh.
#
# Granted, all client_type 1 (path), auth_value 2 (allowed):
#   - kTCCServiceAppleEvents: tart-guest-agent -> the app's bundle id, and sshd-keygen-wrapper
#     -> the app's bundle id, so a machine_exec/osascript call or an ssh session can drive it,
#     as base.sh grants for every pre-installed app.
#   - kTCCServiceAppleEvents: the app's own resolved executable -> com.apple.systemevents, and
#     the app's own executable -> its own bundle id, so an app that scripts System Events, or
#     scripts itself, does not prompt either.
#   - kTCCServiceAccessibility, kTCCServiceScreenCapture (the plain TCC row; a different alert
#     from machine_approve_capture's replayd bypass), kTCCServiceSystemPolicyDesktopFolder,
#     kTCCServiceSystemPolicyDocumentsFolder, kTCCServiceSystemPolicyDownloadsFolder,
#     kTCCServiceCamera, kTCCServiceMicrophone: the app's own executable, no indirect object.
#
# Measured live (issue #252 repro): writing TCC.db after a prompt is already on screen does not
# unblock the process waiting on it (a prompt is a one-time decision tccd already queued); the
# write only prevents a prompt for the *next* Apple Event. Call this before the first thing that
# might control, script or otherwise touch the app.
set -eu

app="$1"
scope="${2:-any}"
case "$scope" in any|home) : ;; *) echo "unknown scope $scope (want any or home)" >&2; exit 2 ;; esac
case "$app" in /*) : ;; *) app="$HOME/$app" ;; esac
[ -d "$app" ] || { echo "no application bundle at $app" >&2; exit 1; }
app="$(realpath "$app")"
info="$app/Contents/Info.plist"
bid="$(plutil -extract CFBundleIdentifier raw -o - "$info" 2>/dev/null)" || { echo "$app has no CFBundleIdentifier" >&2; exit 1; }
exe_name="$(plutil -extract CFBundleExecutable raw -o - "$info" 2>/dev/null)" || { echo "$app has no CFBundleExecutable" >&2; exit 1; }
exe="$(realpath "$app/Contents/MacOS/$exe_name" 2>/dev/null)" || { echo "$app's executable ($exe_name) is missing" >&2; exit 1; }

# Both resolved paths, so a link in the home to an installed app, or a bundle whose executable
# links to a system binary, is refused too: the grants are keyed to the executable.
if [ "$scope" = home ]; then
  home="$(realpath "$HOME")"
  for p in "$app" "$exe"; do
    case "$p" in
      "$home"/*) : ;;
      *) echo "$p is outside the guest home ($home): only an app built or synced in this run can be approved here; pre-installed apps are approved in the image" >&2; exit 1 ;;
    esac
  done
fi

uid="$(id -u)"
agent="$(realpath "$(command -v tart-guest-agent || echo /opt/homebrew/bin/tart-guest-agent)")"
keygen="$(realpath /usr/libexec/sshd-keygen-wrapper)"
system_db="/Library/Application Support/com.apple.TCC/TCC.db"
user_db="$(sudo -n lsof -a -u "$uid" -c tccd -Fn 2>/dev/null | sed -n 's|^n\(/.*/com\.apple\.TCC/TCC\.db\)$|\1|p' | grep -v '^/Library/' | sort -u | head -n 1)"
[ -n "$user_db" ] || user_db="$HOME/Library/Application Support/com.apple.TCC/TCC.db"

ae() { # ae <client> <target bundle id>
  printf "INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, indirect_object_identifier_type, indirect_object_identifier, flags) VALUES ('kTCCServiceAppleEvents', '%s', 1, 2, 0, 1, 0, '%s', 0);" "$1" "$2"
}
svc() { # svc <service> <client>
  printf "INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, indirect_object_identifier_type, indirect_object_identifier, flags) VALUES ('%s', '%s', 1, 2, 0, 1, 0, '', 0);" "$1" "$2"
}

families="kTCCServiceAccessibility kTCCServiceScreenCapture kTCCServiceSystemPolicyDesktopFolder kTCCServiceSystemPolicyDocumentsFolder kTCCServiceSystemPolicyDownloadsFolder kTCCServiceCamera kTCCServiceMicrophone"

sql="$(ae "$agent" "$bid")$(ae "$keygen" "$bid")$(ae "$exe" com.apple.systemevents)$(ae "$exe" "$bid")"
for s in $families; do sql="$sql$(svc "$s" "$exe")"; done

for db in "$system_db" "$user_db"; do
  sudo -n sqlite3 "$db" "$sql"
done
killall tccd 2>/dev/null || true
sudo -n killall tccd 2>/dev/null || true

# Read back the row every caller needs first: the Apple Events grant from tart-guest-agent,
# which is what machine_exec's osascript calls run as.
for db in "$system_db" "$user_db"; do
  got="$(sudo -n sqlite3 "$db" "SELECT auth_value FROM access WHERE service='kTCCServiceAppleEvents' AND client='$agent' AND indirect_object_identifier='$bid'")"
  [ "$got" = 2 ] || { echo "the Apple Events row for $bid did not take in $db (got ${got:-nothing})" >&2; exit 1; }
done

work="$(mktemp -d /tmp/greenroom-tccgrant.XXXXXX)"
trap 'rm -rf "$work"' EXIT
plist="$work/out.plist"
plutil -create xml1 "$plist" >/dev/null
plutil -insert bundleId -string "$bid" "$plist"
plutil -insert executable -string "$exe" "$plist"
granted="[\"kTCCServiceAppleEvents\""
for s in $families; do granted="$granted,\"$s\""; done
granted="$granted]"
plutil -insert granted -json "$granted" "$plist"
plutil -convert json -o - "$plist"
