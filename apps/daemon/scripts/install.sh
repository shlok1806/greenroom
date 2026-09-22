#!/bin/bash
# Install the daemon as a launchd user agent, so it runs on 127.0.0.1:7777
# without a terminal: at login, and again whenever it crashes.
set -euo pipefail

cd "$(dirname "$0")/.."     # apps/daemon
repo="$(cd ../.. && pwd)"
# .env is git-ignored, so a worktree falls back to the main checkout's. GREENROOM_ENV overrides both.
env_file="${GREENROOM_ENV:-$repo/.env}"
if [ ! -f "$env_file" ]; then
  main="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>/dev/null | sed 's|/\.git$||')"
  [ -n "$main" ] && [ -f "$main/.env" ] && env_file="$main/.env"
fi
echo "env file: $env_file"
echo "verifier: ${GREENROOM_VERIFIER:-nim}"

# Resolve tart the way the daemon does (internal/tart/version.go): GREENROOM_TART, the pinned install, then PATH.
pinned="$(sed -n 's/^const PinnedVersion = "\(.*\)"$/\1/p' internal/tart/version.go)"
tart="${GREENROOM_TART:-$HOME/.local/tart-$pinned/tart.app/Contents/MacOS/tart}"
if [ -z "${GREENROOM_TART:-}" ] && [ ! -x "$tart" ]; then
  tart="tart"
fi

# Prefer the prepared image (issue #12) when it exists. GREENROOM_IMAGE overrides.
image="${GREENROOM_IMAGE:-}"
if [ -z "$image" ]; then
  if "$tart" list --source local 2>/dev/null | grep -q '^local[[:space:]]\{1,\}greenroom-base[[:space:]]'; then
    image="greenroom-base"
  else
    image="ghcr.io/cirruslabs/macos-tahoe-base:latest"
  fi
fi
echo "image: $image"

label="com.greenroom.daemon"
root="$HOME/.greenroom"
bin="$root/bin/greenroom"
log="$root/daemon.log"
plist="$HOME/Library/LaunchAgents/$label.plist"
addr="127.0.0.1:7777"

echo "building $bin"
mkdir -p "$root/bin"
go build -o "$bin" .

# tart lives in /opt/homebrew/bin, and a launchd agent inherits almost no PATH.
mkdir -p "$HOME/Library/LaunchAgents"
cat >"$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$bin</string>
    <string>serve</string>
    <string>-addr</string>
    <string>$addr</string>
    <string>-root</string>
    <string>$root</string>
    <string>-env-file</string>
    <string>$env_file</string>
    <string>-verifier</string>
    <string>${GREENROOM_VERIFIER:-nim}</string>
    <string>-image</string>
    <string>$image</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$log</string>
  <key>StandardErrorPath</key>
  <string>$log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
</dict>
</plist>
PLIST
echo "wrote $plist"

# Bootstrapping while the old job lingers fails with "5: Input/output error", so unload and wait for it.
if launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1; then
  echo "unloading the $label job that is already running"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  for _ in $(seq 1 100); do
    launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1 || break
    sleep 0.1
  done
  if launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1; then
    echo "$label is still loaded after 10 s; bootout it yourself and run this again." >&2
    exit 1
  fi
fi

# Replace a greenroom daemon holding 7777; refuse if anything else holds it.
killed=""
while read -r pid; do
  [ -n "$pid" ] || continue
  cmd="$(ps -o command= -p "$pid" 2>/dev/null || true)"
  echo "port 7777 is held by pid $pid: $cmd"
  # Match by name, not path: dev builds live elsewhere.
  case "$cmd" in
    *greenroom*" serve "*)
      echo "stopping the greenroom daemon already on 7777"
      kill "$pid" 2>/dev/null || true
      killed="yes"
      ;;
    *)
      echo "aborting: something that is not greenroom is listening on 7777." >&2
      echo "Stop it yourself, or free the port, then run this again." >&2
      exit 1
      ;;
  esac
done < <(lsof -nP -iTCP:7777 -sTCP:LISTEN -t 2>/dev/null || true)

# Wait for the socket to close, or the new daemon fails to bind and launchd flaps it.
if [ -n "$killed" ]; then
  for _ in $(seq 1 100); do
    [ -z "$(lsof -nP -iTCP:7777 -sTCP:LISTEN -t 2>/dev/null || true)" ] && break
    sleep 0.1
  done
  if [ -n "$(lsof -nP -iTCP:7777 -sTCP:LISTEN -t 2>/dev/null || true)" ]; then
    echo "7777 is still held 10 s after stopping the old daemon; try again." >&2
    exit 1
  fi
fi

# bootstrap races launchd's own bookkeeping, so give it a few tries.
bootstrap_err="$(mktemp -t greenroom-bootstrap)"
trap 'rm -f "$bootstrap_err"' EXIT
for attempt in $(seq 1 5); do
  if launchctl bootstrap "gui/$(id -u)" "$plist" 2>"$bootstrap_err"; then
    break
  fi
  if [ "$attempt" = 5 ]; then
    echo "launchctl bootstrap failed 5 times:" >&2
    cat "$bootstrap_err" >&2
    exit 1
  fi
  echo "bootstrap attempt $attempt failed ($(tr -d '\n' <"$bootstrap_err")); retrying"
  sleep 1
done
launchctl kickstart -k "gui/$(id -u)/$label"

health=""
for _ in $(seq 1 20); do
  health="$(curl -s "http://$addr/healthz" || true)"
  [ -n "$health" ] && break
  sleep 0.5
done
if [ -z "$health" ]; then
  echo "the daemon did not answer /healthz within 10 s; see $log" >&2
  exit 1
fi

echo "healthz: $health"
echo "api: http://127.0.0.1:7777/api/  log: ~/.greenroom/daemon.log"
