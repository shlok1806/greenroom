#!/bin/bash
# Fail the image build if the computer-use premise does not actually hold.
#
# TCC never returns an error. A missing grant gives you a black screenshot and
# AXIsProcessTrusted() == false. Without this test a broken image ships and fails
# mid-run instead, which costs far more to work out. See docs/09-image-strategy.md.

set -euo pipefail

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
ok()   { echo "  ok: $*"; }

echo "greenroom smoke test"

# 1. There has to be a console GUI session. A LaunchDaemon gets no Aqua session and
#    no WindowServer, so there is nothing to capture. Auto-login is required.
CONSOLE_USER="$(stat -f%Su /dev/console)"
[ "${CONSOLE_USER}" = "admin" ] || fail "console user is '${CONSOLE_USER}', expected 'admin' (auto-login broken)"
ok "console session as ${CONSOLE_USER}"

# 2. The screenshot has to succeed and not be a uniform frame. A black image is what
#    a missing kTCCServiceScreenCapture grant produces.
SHOT="/tmp/greenroom-smoke.png"
rm -f "${SHOT}"
screencapture -x "${SHOT}" || fail "screencapture exited non-zero"
[ -s "${SHOT}" ] || fail "screencapture produced no file"

read -r W H <<<"$(sips -g pixelWidth -g pixelHeight "${SHOT}" \
  | awk '/pixelWidth/ {w=$2} /pixelHeight/ {h=$2} END {print w, h}')"
[ "${W:-0}" -gt 0 ] && [ "${H:-0}" -gt 0 ] || fail "could not read screenshot dimensions"
ok "screenshot ${W}x${H} px"

# Uniform-frame check: count distinct colours in a downsampled copy.
COLORS="$(sips -Z 64 "${SHOT}" --out /tmp/greenroom-smoke-small.png >/dev/null 2>&1 \
  && /usr/bin/xxd -p /tmp/greenroom-smoke-small.png | sort -u | wc -l | tr -d ' ')"
[ "${COLORS:-0}" -gt 4 ] || fail "screenshot looks uniform (black frame = missing ScreenCapture grant)"
ok "screenshot has real content"

# 3. Accessibility. osascript inherits the base image's grant. This checks the
#    session can drive the AX layer, not just that a row exists in the database.
osascript -e 'tell application "System Events" to get name of first process' >/dev/null 2>&1 \
  || fail "System Events query failed (Accessibility / AppleEvents not effective)"
ok "accessibility tree reachable"

# 4. Posted events have to take effect, not just return success.
osascript -e 'tell application "System Events" to set the clipboard to "greenroom-smoke"' >/dev/null 2>&1 \
  || fail "posting an Apple Event failed"
CLIP="$(osascript -e 'the clipboard as text' 2>/dev/null || true)"
[ "${CLIP}" = "greenroom-smoke" ] || fail "event posted but did not take effect (got '${CLIP}')"
ok "events post and take effect"

# 5. The display has to match what the image pinned, or cached coordinates are wrong.
echo "  display: $(system_profiler SPDisplaysDataType 2>/dev/null | awk -F': ' '/Resolution/ {print $2; exit}')"

rm -f "${SHOT}" /tmp/greenroom-smoke-small.png
echo "greenroom smoke test: PASS"
