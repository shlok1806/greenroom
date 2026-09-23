#!/bin/bash
# Build the greenroom base image (issue #12): clone the default OCI image, boot it, run
# `greenroom prepare-image` to bake in the input helper, ssh key, screen-capture approvals and desktop
# preferences, and stop it.
# Clones then skip the ~27 s first-control cost. -force replaces an existing image.
# -lean also applies the lean profile (machine/guest/lean.sh, docs/image-experiment):
#   scripts/build-image.sh -lean -name greenroom-lean-a
set -euo pipefail

cd "$(dirname "$0")/.." # apps/daemon

base="ghcr.io/cirruslabs/macos-tahoe-base:latest" # matches defaultImage in main.go
name="greenroom-base"
force=""
lean=""

while [ $# -gt 0 ]; do
  case "$1" in
    -base) base="$2"; shift 2 ;;
    -name) name="$2"; shift 2 ;;
    -force) force="yes"; shift ;;
    -lean) lean="-lean"; shift ;;
    *) echo "usage: $0 [-base <oci image>] [-name greenroom-base] [-lean] [-force]" >&2; exit 2 ;;
  esac
done

# Resolve tart the way the daemon does (internal/tart/version.go): GREENROOM_TART, the pinned install, then PATH.
pinned="$(sed -n 's/^const PinnedVersion = "\(.*\)"$/\1/p' internal/tart/version.go)"
tart="${GREENROOM_TART:-$HOME/.local/tart-$pinned/tart.app/Contents/MacOS/tart}"
if [ -z "${GREENROOM_TART:-}" ] && [ ! -x "$tart" ]; then
  tart="tart"
fi

echo "base image:  $base"
echo "vm name:     $name"
echo "tart:        $tart"
if [ -n "$lean" ]; then echo "profile:     lean"; else echo "profile:     standard"; fi

# tart images live under ~/.tart on /; refuse early rather than fail mid-clone.
free_kb="$(df -k / | awk 'NR==2 {print $4}')"
free_gb="$((free_kb / 1024 / 1024))"
need_gb=10
if [ "$free_gb" -lt "$need_gb" ]; then
  echo "only ${free_gb} GB free on /, need at least ${need_gb} GB. Free some disk (tart images live under ~/.tart) and try again." >&2
  exit 1
fi
echo "free disk:   ${free_gb} GB"

command -v jq >/dev/null 2>&1 || { echo "jq is required (brew install jq) to read tart's VM list." >&2; exit 1; }

state="$("$tart" list --source local --format json 2>/dev/null |
  jq -r --arg n "$name" '.[] | select(.Name == $n) | .State' | head -n1)"

if [ "$state" = "running" ]; then
  echo "a VM named $name is already running. Stop it yourself ($tart stop $name) and try again." >&2
  exit 1
fi
if [ -n "$state" ]; then
  if [ -z "$force" ]; then
    echo "a stopped VM named $name already exists. Pass -force to delete and rebuild it." >&2
    exit 1
  fi
  echo "deleting the existing $name ($force via -force)"
  "$tart" delete "$name"
fi

echo "cloning $base -> $name"
"$tart" clone "$base" "$name"

log="$(mktemp -t greenroom-build-image)"
echo "booting $name (log: $log)"
"$tart" run "$name" --no-graphics >"$log" 2>&1 &
run_pid=$!
cleanup() {
  if kill -0 "$run_pid" 2>/dev/null; then
    echo "stopping $name"
    "$tart" stop "$name" 2>/dev/null || true
    wait "$run_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "waiting for the guest agent (up to 3 minutes)"
ready=""
for _ in $(seq 1 180); do
  if ! kill -0 "$run_pid" 2>/dev/null; then
    echo "tart run exited before the guest agent came up; see $log" >&2
    exit 1
  fi
  if "$tart" exec "$name" true >/dev/null 2>&1; then
    ready="yes"
    break
  fi
  sleep 1
done
if [ -z "$ready" ]; then
  echo "the guest agent never answered within 3 minutes; see $log" >&2
  exit 1
fi
echo "guest agent is up"

echo "preparing the guest (compiling the input helper, installing the ssh key, pre-approving screen capture, setting desktop preferences${lean:+, applying the lean profile})"
go run . prepare-image -tart "$tart" -vm "$name" $lean

trap - EXIT
echo "stopping $name"
"$tart" stop "$name"
for _ in $(seq 1 60); do
  kill -0 "$run_pid" 2>/dev/null || break
  sleep 0.5
done
if kill -0 "$run_pid" 2>/dev/null; then
  echo "$name did not stop within 30 s" >&2
  exit 1
fi
wait "$run_pid" 2>/dev/null || true
rm -f "$log" # kept only when something failed, since the messages above point at it
echo "$name is stopped"

echo
echo "image ready: $name"
echo "run the daemon against it with: greenroom serve -image $name"
