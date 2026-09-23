#!/bin/bash
# Grant greenroom's guest binaries the privacy rights they need, and stop the
# screen-capture alert that macOS 15 and later show, which TCC does not cover.
#
# Runs INSIDE the guest at image-build time. Requires SIP off, which the Cirrus base
# image already provides. With SIP off, no csreq blob is needed.
#
# The base image already grants sshd-keygen-wrapper, osascript and tart-guest-agent.
# TCC checks the responsible process, so anything greenroom runs over SSH inherits
# those grants. These rows are for binaries started some other way, such as from the
# first-boot daemon or a LaunchAgent.
#
# See docs/09-image-strategy.md.

set -euo pipefail

# Paths of greenroom binaries that need rights in their own name. Add the Swift
# computer-use CLI here when it exists (M2).
GREENROOM_BINARIES=(
  "/usr/local/greenroom/firstboot.sh"
)

SERVICES=(
  kTCCServiceAccessibility
  kTCCServiceScreenCapture
  kTCCServicePostEvent
)

SYSTEM_DB="/Library/Application Support/com.apple.TCC/TCC.db"

# macOS 27 moved the per-user database into a ProtectedSystem container. Writing to
# the old path creates an empty database there and the grants go nowhere, with no
# error. Ask the running daemon which file it has open, and fail on anything other
# than exactly one match.
resolve_user_tcc_db() {
  local macos_major uid candidates count
  macos_major="$(sw_vers -productVersion | cut -d. -f1)"

  if [[ "${macos_major}" -lt 27 ]]; then
    echo "${HOME}/Library/Application Support/com.apple.TCC/TCC.db"
    return
  fi

  uid="$(id -u)"
  candidates="$(sudo lsof -a -u "${uid}" -c tccd -Fn 2>/dev/null \
    | sed -n 's|^n\(/private/var/containers/Data/ProtectedSystem/.*/com\.apple\.TCC/TCC\.db\)$|\1|p' \
    | sort -u)"

  count="$(printf '%s' "${candidates}" | grep -c . || true)"
  if [[ "${count}" -ne 1 ]]; then
    echo "FAIL: expected exactly one active user TCC database, found ${count}" >&2
    printf '%s\n' "${candidates}" >&2
    exit 1
  fi

  if [[ "$(sudo stat -f %u "${candidates}")" != "${uid}" ]]; then
    echo "FAIL: user TCC database ${candidates} is not owned by uid ${uid}" >&2
    exit 1
  fi

  echo "${candidates}"
}

# Named columns, never positional. The access table gained four columns in Sonoma,
# which is why positional inserts kept breaking the GitHub runner images.
emit_sql() {
  local binary service
  for binary in "${GREENROOM_BINARIES[@]}"; do
    local realpath_bin
    realpath_bin="$(realpath "${binary}" 2>/dev/null || echo "${binary}")"
    for service in "${SERVICES[@]}"; do
      cat <<-EOF
	INSERT OR REPLACE INTO access
	  (service, client_type, client, auth_value, auth_reason, auth_version,
	   indirect_object_identifier_type, indirect_object_identifier)
	VALUES
	  ('${service}', 1, '${realpath_bin}', 2, 0, 1, NULL, 'UNUSED');
	EOF
    done
    # Apple Events is per receiving app, so it needs the receiver's bundle id.
    cat <<-EOF
	INSERT OR REPLACE INTO access
	  (service, client_type, client, auth_value, auth_reason, auth_version,
	   indirect_object_identifier_type, indirect_object_identifier)
	VALUES
	  ('kTCCServiceAppleEvents', 1, '${realpath_bin}', 2, 0, 1, 0, 'com.apple.systemevents');
	EOF
  done
}

USER_DB="$(resolve_user_tcc_db)"
echo "system TCC db: ${SYSTEM_DB}"
echo "user   TCC db: ${USER_DB}"

SQL="$(emit_sql)"
printf '%s\n' "${SQL}" | sudo sqlite3 "${SYSTEM_DB}"
printf '%s\n' "${SQL}" | sudo sqlite3 "${USER_DB}"

# Without this, Apple Events prompts for a password that nobody is there to type.
if command -v automationmodetool >/dev/null 2>&1; then
  /usr/bin/expect -c '
    spawn automationmodetool enable-automationmode-without-authentication
    expect {
      "Enter the password for user *:" { send "admin\n"; exp_continue }
      eof
    }
  ' || echo "warn: automationmodetool step did not complete"
fi

# The screen-capture alert on macOS 15 and later ("... is requesting to bypass the
# system private window picker and directly access your screen and audio") is not
# TCC. replayd keeps a per-user record keyed by the capturing client's resolved
# executable path. The alert is decided by kScreenCaptureApprovalLastUsed alone
# (missing or over 30 days old alerts), and kScreenCapturePrivacyHintDate schedules
# the monthly banner, so all three dates go in 3024. replayd caches the file and
# writes its copy back, so it is stopped across the write and killed afterwards;
# launchd restarts it on demand. The daemon rewrites the same records at every boot
# and before captures (apps/daemon/internal/machine/capturealert.go), because an
# image's LastUsed ages; keep the two in step.
APPROVALS="${HOME}/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist"
FAR="3024-01-01 00:00:00 +0000"
mkdir -p "$(dirname "${APPROVALS}")"
REPLAYD="$(pgrep -x -u "$(id -u)" replayd || true)"
if [[ -n "${REPLAYD}" ]]; then
  # Killed on every exit, so a failed write cannot leave replayd stopped.
  trap 'kill -9 ${REPLAYD} 2>/dev/null || true' EXIT
  kill -STOP ${REPLAYD}
fi
for binary in "${GREENROOM_BINARIES[@]}" /opt/homebrew/bin/tart-guest-agent /usr/libexec/sshd-keygen-wrapper; do
  c="$(realpath "${binary}" 2>/dev/null || echo "${binary}")"
  defaults write "${APPROVALS}" "${c}" -dict \
    kScreenCaptureApprovalLastAlerted -date "${FAR}" \
    kScreenCaptureApprovalLastUsed -date "${FAR}" \
    kScreenCapturePrivacyHintDate -date "${FAR}"
  defaults read "${APPROVALS}" "${c}" | grep -q "kScreenCaptureApprovalLastUsed = \"${FAR}\""
done
if [[ -n "${REPLAYD}" ]]; then
  kill -9 ${REPLAYD} 2>/dev/null || true
  trap - EXIT
fi

# Desktop preferences for an agent that clicks by coordinates. "Click wallpaper to
# reveal desktop" hides every window when a click misses; window restore reopens
# whatever was open when the image was shut down. The daemon sets the same keys at
# every boot (apps/daemon/internal/machine/desktopprefs.go); keep the two in step.
defaults write com.apple.WindowManager EnableStandardClickToShowDesktop -bool false
defaults write NSGlobalDomain NSQuitAlwaysKeepsWindows -bool false
defaults write com.apple.loginwindow TALLogoutSavesState -bool false
# A guest display that sleeps turns every frame black with no error; the
# screensaver and the lock cover the app under test.
sudo -n pmset -a displaysleep 0 sleep 0
defaults -currentHost write com.apple.screensaver idleTime -int 0
sysadminctl -screenLock status 2>&1 | grep -q "screenLock is off" || sysadminctl -screenLock off -password admin

echo "greenroom-tcc: done"
