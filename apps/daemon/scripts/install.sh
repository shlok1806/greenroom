#!/bin/bash
# Install the daemon as a launchd user agent, so it runs on 127.0.0.1:7777
# without a terminal: at login, and again whenever it crashes.
set -euo pipefail

cd "$(dirname "$0")/.."     # apps/daemon
repo="$(cd ../.. && pwd)"   # the checkout, which is where .env lives
# A git worktree has no .env of its own (it is git-ignored), so fall back
# to the main checkout's copy. GREENROOM_ENV overrides both.
env_file="${GREENROOM_ENV:-$repo/.env}"
if [ ! -f "$env_file" ]; then
  main="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>/dev/null | sed 's|/\.git$||')"
  [ -n "$main" ] && [ -f "$main/.env" ] && env_file="$main/.env"
fi
echo "env file: $env_file"
echo "verifier: ${GREENROOM_VERIFIER:-nim}"

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

# launchd still owns the old job for a moment after we ask it to go away, and it
# answers a bootstrap in that window with "Bootstrap failed: 5: Input/output error".
# Unload first and wait for the label to actually disappear.
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

# Port 7777 must be ours before launchd claims it. Our own daemon is replaced;
# anything else is the user's and is left alone.
killed=""
while read -r pid; do
  [ -n "$pid" ] || continue
  cmd="$(ps -o command= -p "$pid" 2>/dev/null || true)"
  echo "port 7777 is held by pid $pid: $cmd"
  # Match the binary by name, not by path: a dev build may be called
  # greenroom-w or live in /tmp and it is still ours.
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

# A killed process keeps the listening socket for a moment; the new daemon would
# then fail to bind and launchd would flap it.
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
for attempt in $(seq 1 5); do
  if launchctl bootstrap "gui/$(id -u)" "$plist" 2>/tmp/greenroom-bootstrap.err; then
    break
  fi
  if [ "$attempt" = 5 ]; then
    echo "launchctl bootstrap failed 5 times:" >&2
    cat /tmp/greenroom-bootstrap.err >&2
    exit 1
  fi
  echo "bootstrap attempt $attempt failed ($(tr -d '\n' </tmp/greenroom-bootstrap.err)); retrying"
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
