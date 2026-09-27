#!/bin/bash
# Build a greenroom image, the only recipe there is (ADR 0018): clone the default OCI image,
# grow its disk, boot it, run `greenroom prepare-image` (the host's Xcode (ADR 0026), input
# helper, ssh key, screen-capture approvals, desktop preferences, the base profile in
# machine/guest/base.sh, the toolchain manifest, Software Update off), stop it, and then run
# the dialog gate, `greenroom check-image`, on a
# clone of a clone of it, before and after an in-guest reboot. The image is built under
# <name>-building and takes its name only when the gate passes; a failed gate deletes it and
# exits 1, leaving any existing <name> as it was. -force replaces an existing image.
# -lean also applies the lean profile (machine/guest/lean.sh, docs/image-experiment):
#   scripts/build-image.sh -lean -name greenroom-lean-a
# Every image carries Xcode, copied from this host (ADR 0026): -xcode <path/Xcode.app>, else
# the app `xcode-select -p` points into. A host without Xcode cannot build an image.
# -disk-size <GB> grows the guest disk (default 90) for Xcode, builds and DerivedData; the
# disk is sparse, so the host pays only for what the guest writes.
# The gate's screenshots and report go to $GREENROOM_CHECK_OUT, else a new temp directory.
# It needs 20 GB free to start, and stops (deleting what it built) if free space on / falls
# under $GREENROOM_BUILD_MIN_FREE_GB (default 5) while it clones, prepares or checks (#155).
set -euo pipefail

cd "$(dirname "$0")/.." # apps/daemon
. scripts/disk-guard.sh
min_gb="${GREENROOM_BUILD_MIN_FREE_GB:-5}"

base="ghcr.io/cirruslabs/macos-tahoe-base:latest" # matches defaultImage in main.go
name="greenroom-base"
force=""
lean=""
xcode=""
disk_gb=90

while [ $# -gt 0 ]; do
  case "$1" in
    -base) base="$2"; shift 2 ;;
    -name) name="$2"; shift 2 ;;
    -force) force="yes"; shift ;;
    -lean) lean="-lean"; shift ;;
    -xcode) xcode="$2"; shift 2 ;;
    -disk-size) disk_gb="$2"; shift 2 ;;
    *) echo "usage: $0 [-base <oci image>] [-name greenroom-base] [-lean] [-force] [-xcode <Xcode.app>] [-disk-size <GB>]" >&2; exit 2 ;;
  esac
done

# The host's Xcode, before anything is cloned (ADR 0026). prepare-image checks it again.
if [ -z "$xcode" ]; then
  dev="$(xcode-select -p 2>/dev/null || true)"
  case "$dev" in
    *.app/Contents/Developer) xcode="${dev%/Contents/Developer}" ;;
  esac
fi
if [ -z "$xcode" ] || [ ! -x "$xcode/Contents/Developer/usr/bin/xcodebuild" ]; then
  echo "no Xcode on this host (xcode-select -p: ${dev:-none}${xcode:+, -xcode: $xcode}). Every image carries the host's Xcode (ADR 0026):" >&2
  echo "install Xcode, select it with sudo xcode-select -s /Applications/Xcode.app, or pass -xcode /path/to/Xcode.app." >&2
  exit 1
fi

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
echo "xcode:       $xcode ($(plutil -extract CFBundleShortVersionString raw -o - "$xcode/Contents/Info.plist" 2>/dev/null || echo "unknown version"))"
echo "guest disk:  $disk_gb GB"

# tart images live under ~/.tart on /; refuse early rather than fail mid-clone.
free_gb="$(free_gb)"
need_gb=20 # Xcode is about 4 GB compressed in the guest, plus first launch, the probes and the gate's clones
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
if [ -n "$state" ] && [ -z "$force" ]; then
  echo "a stopped VM named $name already exists. Pass -force to rebuild it." >&2
  exit 1
fi

# Build under a temporary name, so a failed build or gate never leaves a half image behind the real name.
final="$name"
name="$final-building"
if "$tart" list --source local --format json 2>/dev/null | jq -e --arg n "$name" 'any(.[]; .Name == $n)' >/dev/null; then
  echo "deleting a leftover $name from an earlier build"
  "$tart" delete "$name"
fi

echo "cloning $base -> $name"
if ! guarded "$min_gb" "$tart" clone "$base" "$name"; then
  "$tart" delete "$name" 2>/dev/null || true
  exit 1
fi
# The guest grows its APFS container to fit at boot (tart-guest-agent --resize-disk, in the
# Cirrus base); prepare-image waits for that and reads it back (guest/disk.sh).
if ! "$tart" set "$name" --disk-size "$disk_gb"; then
  "$tart" delete "$name" || true
  exit 1
fi

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

echo "preparing the guest (Xcode, input helper, ssh key, screen capture, desktop preferences, base profile, toolchain manifest${lean:+, lean profile}, Software Update off)"
greenroom="$log.greenroom"
go build -o "$greenroom" .
if ! guarded "$min_gb" "$greenroom" prepare-image -tart "$tart" -vm "$name" -xcode "$xcode" $lean; then
  cleanup
  "$tart" delete "$name" || true
  exit 1
fi

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

# The dialog gate needs a VM slot of its own; the build VM has stopped.
out="${GREENROOM_CHECK_OUT:-$(mktemp -d -t greenroom-check)}"
echo "dialog check (clone of a clone, before and after a reboot); screenshots in $out"
check=0
guarded "$min_gb" "$greenroom" check-image -tart "$tart" -image "$name" -out "$out" || check=$?
if [ "$check" -ne 0 ]; then
  if [ "$check" -eq 75 ]; then
    echo "the dialog check stopped for low disk; deleting $name. $final is unchanged." >&2
  else
    echo "$name failed the dialog check; deleting it. $final is unchanged. Screenshots and report: $out" >&2
  fi
  "$tart" delete "$name" || true
  rm -f "$greenroom"
  exit 1
fi
rm -f "$greenroom"

if "$tart" list --source local --format json 2>/dev/null | jq -e --arg n "$final" 'any(.[]; .Name == $n)' >/dev/null; then
  echo "replacing the existing $final (-force)"
  "$tart" delete "$final"
fi
"$tart" rename "$name" "$final"

echo
echo "image ready: $final"
echo "run the daemon against it with: greenroom serve -image $final"
