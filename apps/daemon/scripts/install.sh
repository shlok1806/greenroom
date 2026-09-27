#!/bin/bash
# Install the daemon as a launchd user agent, so it runs on 127.0.0.1:7777
# without a terminal: at login, and again whenever it crashes.
# It then checks the local default images against this daemon's input helper and image
# recipe (greenroom image-status, issue #159) and prints the command that rebuilds a stale one.
#   -rebuild   rebuild each stale image with build-image.sh (about 20 GB free each)
#   -dry-run   build to a temp path, check the images and say what it would do; installs nothing
set -euo pipefail

rebuild=""
dry_run=""
while [ $# -gt 0 ]; do
  case "$1" in
    -rebuild) rebuild="yes"; shift ;;
    -dry-run) dry_run="yes"; shift ;;
    *) echo "usage: $0 [-rebuild] [-dry-run]" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."     # apps/daemon
repo="$(cd ../.. && pwd)"
label="com.greenroom.daemon"
root="$HOME/.greenroom"
bin="$root/bin/greenroom"
log="$root/daemon.log"
plist="$HOME/Library/LaunchAgents/$label.plist"
addr="127.0.0.1:7777"

# The job's open file limit (daemon ADR 0002, issue #186): launchd's default soft limit is 256,
# every `tart run` the daemon starts inherits it, and a leak in tart's control socket runs a
# long-lived machine out. 65536, or the kernel's per-process cap when that is lower (setrlimit
# refuses more). The daemon raises its own limit again at start, so this is the first line.
open_files=65536
per_proc="$(sysctl -n kern.maxfilesperproc 2>/dev/null || true)"
if [[ "$per_proc" =~ ^[0-9]+$ ]] && [ "$per_proc" -lt "$open_files" ]; then
  open_files="$per_proc"
fi

# .env is git-ignored, so a worktree falls back to the main checkout's. GREENROOM_ENV overrides
# both and is used as given, even when missing: never another file in its place.
auto_env="$repo/.env"
if [ ! -f "$auto_env" ]; then
  # || true: outside a git checkout (a tarball) git fails, and with pipefail that ended the script with no word.
  main="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>/dev/null | sed 's|/\.git$||' || true)"
  [ -n "$main" ] && [ -f "$main/.env" ] && auto_env="$main/.env"
fi

# What this install keeps (scripts/install-settings.sh): GREENROOM_VERIFIER, GREENROOM_IMAGE,
# GREENROOM_ENV and GREENROOM_TART from the environment, else what the job being replaced was
# installed with, so an update (scripts/update.sh) keeps how the daemon was installed. The
# job's other environment variables are written again as they were.
verifier="" verifier_from="" image="" image_from="" env_file="" env_file_from="" tart="" tart_from=""
keep=()
settings="$(scripts/install-settings.sh "$plist" "$auto_env")"
while IFS= read -r line; do
  case "$line" in
    verifier=*) verifier="${line#*=}" ;;
    verifier_from=*) verifier_from="${line#*=}" ;;
    image=*) image="${line#*=}" ;;
    image_from=*) image_from="${line#*=}" ;;
    env_file=*) env_file="${line#*=}" ;;
    env_file_from=*) env_file_from="${line#*=}" ;;
    tart=*) tart="${line#*=}" ;;
    tart_from=*) tart_from="${line#*=}" ;;
    keep=*) keep+=("${line#keep=}") ;;
  esac
done <<<"$settings"
chosen_env="$env_file"
[ -n "$env_file" ] || env_file="$auto_env"
echo "env file: $env_file${chosen_env:+ ($env_file_from)}"
echo "verifier: $verifier ($verifier_from)"

# Resolve tart the way the daemon does (internal/tart/version.go): GREENROOM_TART, the pinned install, then PATH.
pinned="$(sed -n 's/^const PinnedVersion = "\(.*\)"$/\1/p' internal/tart/version.go)"
chosen_tart="$tart"
if [ -z "$tart" ]; then
  tart="$HOME/.local/tart-$pinned/tart.app/Contents/MacOS/tart"
  [ -x "$tart" ] || tart="tart"
else
  echo "tart: $tart ($tart_from)"
fi

# Prefer the lean image (docs/image-experiment/decision-log.md, decision 22), then the prepared
# image (issue #12), then upstream. GREENROOM_IMAGE overrides. A bare `greenroom serve` makes the
# same choice (machine.PreferredImages); keep the two lists, and install-settings.sh's, in step.
chosen_image="$image"
image_source="GREENROOM_IMAGE, $image_from"
if [ -z "$image" ]; then
  local_vms="$("$tart" list --source local 2>/dev/null || true)"
  for candidate in greenroom-lean-a greenroom-base; do
    if printf '%s\n' "$local_vms" | grep -q "^local[[:space:]]\{1,\}${candidate}[[:space:]]"; then
      image="$candidate"
      image_source="local image"
      break
    fi
  done
  if [ -z "$image" ]; then
    image="ghcr.io/cirruslabs/macos-tahoe-base:latest"
    image_source="no greenroom-lean-a or greenroom-base, upstream"
  fi
fi
echo "image: $image ($image_source)"

# The job's EnvironmentVariables beyond PATH: the checkout, the settings a person chose (so the
# next install tells them from its own picks), and whatever else the old job carried.
xml() { printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'; }
extra_env=""
add_env() { extra_env+="    <key>$(xml "$1")</key>"$'\n'"    <string>$(xml "$2")</string>"$'\n'; }
add_env GREENROOM_CHECKOUT "$repo"
[ -z "$chosen_image" ] || add_env GREENROOM_IMAGE "$chosen_image"
[ -z "$chosen_env" ] || add_env GREENROOM_ENV "$chosen_env"
[ -z "$chosen_tart" ] || add_env GREENROOM_TART "$chosen_tart"
for pair in ${keep[@]+"${keep[@]}"}; do
  echo "keeping ${pair%%=*} from the previous install"
  add_env "${pair%%=*}" "${pair#*=}"
done

# The build's identity (root ADR 0033): `greenroom version` and GET /api/version report it, and
# the Companion compares it with main. Dirty means any change git sees, untracked files included.
commit="$(git -C "$repo" rev-parse --short HEAD 2>/dev/null || echo "")"
dirty="false"
[ -n "$(git -C "$repo" status --porcelain 2>/dev/null)" ] && dirty="true"
built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pkg="github.com/shlok1806/greenroom/apps/daemon/internal/buildinfo"
ldflags="-X $pkg.commit=$commit -X $pkg.dirty=$dirty -X $pkg.builtAt=$built_at"
echo "commit: ${commit:-unknown}$([ "$dirty" = true ] && echo " (local changes)")"

if [ -n "$dry_run" ]; then
  bin="$(mktemp -d -t greenroom-install)/greenroom" # never replace the installed daemon
  echo "building $bin"
  go build -ldflags "$ldflags" -o "$bin" .
else
  # Build beside the binary and swap it in only once the build succeeded, so a failed build
  # leaves the running daemon and its binary as they were.
  echo "building $bin"
  mkdir -p "$root/bin"
  go build -ldflags "$ldflags" -o "$bin.new" .
  mv -f "$bin.new" "$bin"
fi

# Images built before this daemon's input helper or image recipe (issue #159): each machine
# from one compiles the helper at boot, or lacks Xcode. The check reads the stopped images'
# disks on the host, read-only; it only reports, and never stops the install.
echo "checking the local images against this daemon"
if ! "$bin" image-status -tart "$tart"; then
  echo "could not check the local images; the daemon warns when a machine from a stale one boots" >&2
elif [ -n "$rebuild" ]; then
  while read -r args; do
    [ -n "$args" ] || continue
    if [ -n "$dry_run" ]; then
      echo "dry run: would rebuild with scripts/build-image.sh $args"
      continue
    fi
    echo "rebuilding: scripts/build-image.sh $args"
    # shellcheck disable=SC2086 # the arguments are words
    scripts/build-image.sh $args
  done < <("$bin" image-status -tart "$tart" -rebuild-args)
fi

if [ -n "$dry_run" ]; then
  echo "dry run: would write $plist, (re)load $label and serve on $addr with image $image"
  rm -rf "$(dirname "$bin")"
  exit 0
fi

# tart lives in /opt/homebrew/bin, and a launchd agent inherits almost no PATH. GREENROOM_CHECKOUT is
# the checkout this build came from: GET /api/version reports it, and the Companion runs
# scripts/update.sh there (root ADR 0033).
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
    <string>$(xml "$env_file")</string>
    <string>-verifier</string>
    <string>$(xml "$verifier")</string>
    <string>-image</string>
    <string>$(xml "$image")</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>SoftResourceLimits</key>
  <dict>
    <key>NumberOfFiles</key>
    <integer>$open_files</integer>
  </dict>
  <key>HardResourceLimits</key>
  <dict>
    <key>NumberOfFiles</key>
    <integer>$open_files</integer>
  </dict>
  <key>StandardOutPath</key>
  <string>$log</string>
  <key>StandardErrorPath</key>
  <string>$log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
$extra_env  </dict>
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
