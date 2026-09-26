#!/bin/sh
# : greenroom-grow-disk
# disk.sh: make the guest's APFS container fill its disk, after build-image.sh grew the disk
# with `tart set --disk-size` (ADR 0026). Runs in the guest as the admin user through
# `greenroom prepare-image`, before Xcode is copied in.
#
# Measured on 26.6.2 (Cirrus tahoe base, tart-guest-agent 2.37): the Cirrus image's
# LaunchDaemon runs `tart-guest-agent --run-daemon`, which is `--resize-disk`, so the
# container grows by itself at boot, about 35 s after the agent answers. This script waits
# for that, resizes the container itself if it never happens, and reads the result back.
# Layout: disk0s1 is the APFS ISC container, disk0s2 the system container, and nothing
# follows it, so `resizeContainer <store> 0` can take the free space.
set -u

# size <device>: its size in bytes.
size() { diskutil info -plist "$1" | plutil -extract Size raw -o - -; }

store="$(diskutil info -plist / | plutil -extract APFSPhysicalStores.0.APFSPhysicalStore raw -o - - 2>/dev/null)"
whole="$(diskutil info -plist "$store" 2>/dev/null | plutil -extract ParentWholeDisk raw -o - - 2>/dev/null)"
if [ -z "$store" ] || [ -z "$whole" ]; then
  echo "disk: cannot find the physical store of / (store '$store', disk '$whole')" >&2
  exit 1
fi

# full: the store leaves less than 1 GB of its disk unused (the ISC container is 0.5 GB).
full() { [ $(( $(size "/dev/$whole") - $(size "/dev/$store") )) -lt 1000000000 ]; }

n=0
while ! full && [ $n -lt 90 ]; do sleep 2; n=$((n+1)); done
if ! full; then
  echo "disk: the container did not grow at boot; resizing $store" >&2
  sudo -n diskutil apfs resizeContainer "$store" 0 >&2 || true
fi
if ! full; then
  echo "disk: $store is $(size "/dev/$store") bytes of $whole's $(size "/dev/$whole"): the container does not fill the disk" >&2
  exit 1
fi
echo "disk: ok, $store fills $whole ($(( $(size "/dev/$whole") / 1000000000 )) GB), $(df -h / | awk 'NR==2 {print $4}') free on /"
