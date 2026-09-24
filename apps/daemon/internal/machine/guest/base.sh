#!/bin/sh
# : greenroom-base-profile
# base.sh: every fix a greenroom image needs so that nothing on a fresh machine waits for a
# click (ADR 0018, issues #25 and #60). Runs in the guest as the auto-logged-in admin user,
# through `greenroom prepare-image`, for the base and the lean image alike. The screen-capture
# approvals and desktop preferences are separate scripts that boot also runs
# (capturealert.go, desktopprefs.go); Software Update is disabled last, by prepare-image
# itself (softwareupdate.go), because lean.sh still talks to it.
#
# Every setting is read back at the end. A failed read-back fails the build with the name of
# the check, as lean.sh does.
set -eu

uid="$(id -u)"
fail=""
check() { # check <name> <command...>
  name="$1"; shift
  if ! "$@" >/dev/null 2>&1; then
    fail="$fail $name"
    echo "base: check failed: $name" >&2
  fi
}
is() { [ "$1" = "$2" ]; }

# --- Apple Events: no "tart-guest-agent wants access to control X" prompt (issue #25). ------
# TCC checks the responsible process, which is tart-guest-agent for everything run through
# `tart exec` (the daemon's commands, the input helper) and sshd-keygen-wrapper for anything
# over ssh. Apple Events are granted per target bundle id. The sender path is resolved here,
# at build time, so a tart-guest-agent upgrade in the base image cannot leave the rows keyed
# to a path that no longer exists. SIP is off in the Cirrus base, which is what lets root
# write TCC.db; no csreq is needed then. Named columns, never positional: the table gained
# columns in Sonoma. Both databases get the rows: tccd consults the user one for Apple Events.
agent="$(realpath "$(command -v tart-guest-agent || echo /opt/homebrew/bin/tart-guest-agent)")"
keygen="$(realpath /usr/libexec/sshd-keygen-wrapper)"
targets="com.apple.finder com.apple.Terminal com.apple.systempreferences com.apple.Safari com.apple.TextEdit
  com.apple.Preview com.apple.ActivityMonitor com.apple.Console com.apple.systemevents"
system_db="/Library/Application Support/com.apple.TCC/TCC.db"
# macOS 27 moved the per-user database; ask the running tccd which file it has open.
user_db="$(sudo -n lsof -a -u "$uid" -c tccd -Fn 2>/dev/null | sed -n 's|^n\(/.*/com\.apple\.TCC/TCC\.db\)$|\1|p' | grep -v '^/Library/' | sort -u | head -n 1)"
[ -n "$user_db" ] || user_db="$HOME/Library/Application Support/com.apple.TCC/TCC.db"
sql=""
for s in "$agent" "$keygen"; do
  for t in $targets; do
    sql="$sql INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, indirect_object_identifier_type, indirect_object_identifier, flags) VALUES ('kTCCServiceAppleEvents', '$s', 1, 2, 0, 1, 0, '$t', 0);"
  done
done
for db in "$system_db" "$user_db"; do
  sudo -n sqlite3 "$db" "$sql"
done
# tccd caches decisions; launchd restarts it on demand.
killall tccd 2>/dev/null || true
sudo -n killall tccd 2>/dev/null || true

# Automation mode without a password prompt, for XCUITest-style automation an app under
# test may start. Cirrus usually ships it this way already; expect answers the prompt if not.
if ! automationmodetool 2>/dev/null | grep -q "DOES NOT REQUIRE"; then
  expect -c '
    set timeout 20
    spawn automationmodetool enable-automationmode-without-authentication
    expect {
      "*assword*" { send "admin\r"; exp_continue }
      eof
    }' >/dev/null 2>&1 || true
fi

# --- Safari: "Allow JavaScript from Apple Events", so `do JavaScript` works (issue #25). ----
# defaults routes com.apple.Safari into Safari's container.
defaults write com.apple.Safari AllowJavaScriptFromAppleEvents -bool true

# --- Crash dialogs: never shown, and no queued report to show at the next login. ----------
launchctl disable "gui/$uid/com.apple.DiagnosticsReporter"
launchctl bootout "gui/$uid/com.apple.DiagnosticsReporter" 2>/dev/null || true
defaults write com.apple.CrashReporter DialogType -string none
report_dirs="/Library/Logs/DiagnosticReports $HOME/Library/Logs/DiagnosticReports /private/var/db/PanicReporter"
for d in $report_dirs; do
  sudo -n find "$d" -type f -delete 2>/dev/null || true
done

# --- Apps at login (issue #60). --------------------------------------------------------------
# loginwindow relaunches every app in its persistent-apps list at login, whatever
# TALLogoutSavesState or LoginwindowLaunchesRelaunchApps say (measured on 26.6.2: both false,
# and Terminal and Calendar planted in the list still came back after a reboot). The Cirrus
# base was saved with Terminal in that list, which is why every machine had Terminal running.
# The list follows the apps that run, so every app it names but Finder (in the Cirrus base,
# Terminal) is stopped first; this is the image being built, not a machine an agent uses.
# Saved window state goes too (the lean-a Terminal window with build shell history).
list="$HOME/Library/Group Containers/group.com.apple.loginwindow.persistent-apps/persistantApps"
i=0
while app="$(plutil -extract "PersistentApps.$i.Path" raw -o - "$list" 2>/dev/null)"; do
  i=$((i+1))
  [ "$app" = /System/Library/CoreServices/Finder.app ] && continue
  pkill -f "^$app/Contents/MacOS/" 2>/dev/null || true
  n=0
  while pgrep -f "^$app/Contents/MacOS/" >/dev/null && [ $n -lt 50 ]; do sleep 0.1; n=$((n+1)); done
done
rm -rf "$HOME/Library/Saved Application State"/* 2>/dev/null || true
for d in "$HOME"/Library/Containers/*/Data/Library/Saved\ Application\ State; do
  [ -d "$d" ] && rm -rf "$d"/* 2>/dev/null
done
mkdir -p "$(dirname "$list")"
cat > "$list" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PersistentApps</key>
	<array>
		<dict>
			<key>BackgroundState</key>
			<integer>2</integer>
			<key>BundleID</key>
			<string>com.apple.finder</string>
			<key>Hide</key>
			<false/>
			<key>Path</key>
			<string>/System/Library/CoreServices/Finder.app</string>
		</dict>
	</array>
</dict>
</plist>
EOF

# --- Read back. -----------------------------------------------------------------------------
# Settle first: loginwindow rewrites its list a few seconds after the set of running apps
# changes, so a rewrite that puts an app back must be there by the read-back.
sleep 5
for db in "$system_db" "$user_db"; do
  where="system"; [ "$db" = "$system_db" ] || where="user"
  for s in "$agent" "$keygen"; do
    for t in $targets; do
      check "appleevents-$where-$(basename "$s")-$t" is "$(sudo -n sqlite3 "$db" "SELECT auth_value FROM access WHERE service='kTCCServiceAppleEvents' AND client='$s' AND indirect_object_identifier='$t'")" 2
    done
  done
done
check automation-mode sh -c 'automationmodetool | grep -q "DOES NOT REQUIRE"'
check safari-javascript is "$(defaults read com.apple.Safari AllowJavaScriptFromAppleEvents)" 1
check diagnostics-reporter sh -c "launchctl print-disabled gui/$uid | grep -q '\"com.apple.DiagnosticsReporter\" => disabled'"
check crashreporter-dialog is "$(defaults read com.apple.CrashReporter DialogType)" none
for d in $report_dirs; do
  check "reports-empty-$d" is "$(sudo -n find "$d" -type f 2>/dev/null | head -n 1)" ""
done
check terminal-stopped sh -c '! pgrep -x Terminal'
check relaunch-list-finder-only is "$(plutil -extract PersistentApps.0.BundleID raw -o - "$list"),$(plutil -extract PersistentApps.1 raw -o - "$list" 2>/dev/null || echo none)" com.apple.finder,none
check saved-state-empty is "$(ls -A "$HOME/Library/Saved Application State" 2>/dev/null)" ""

if [ -n "$fail" ]; then
  echo "base: failed:$fail" >&2
  exit 1
fi
echo "base: ok"
