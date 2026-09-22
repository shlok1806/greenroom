#!/bin/bash
# Personalize this VM from its seed disk, once, on first boot.
#
# Baked into greenroom-base as root:wheel 755 and launched by
# /Library/LaunchDaemons/com.greenroom.firstboot.plist. Per-VM data arrives on a
# read-only seed disk attached at run time:
#
#   tart run --disk "seed.dmg:ro" <vm>
#
# This is why greenroom does not mutate disk images offline. The daemon and its
# script are installed once, with root inside the guest, so the ownership launchd
# wants is right from the start. Per VM we only produce data.
# See docs/09-image-strategy.md.

set -uo pipefail

LOG=/var/log/greenroom-firstboot.log
exec >>"${LOG}" 2>&1
echo "=== greenroom firstboot $(date -u) ==="

SEED_VOL="/Volumes/GRSEED"
SEED="${SEED_VOL}/personalize.json"
STAMP="/var/db/.greenroom-personalized"

if [ -f "${STAMP}" ]; then
  echo "already personalized, nothing to do"
  exit 0
fi

# diskarbitrationd mounts asynchronously, so RunAtLoad can start first. Poll for it.
for _ in $(seq 1 30); do
  [ -f "${SEED}" ] && break
  sleep 1
done

if [ ! -f "${SEED}" ]; then
  echo "no seed at ${SEED} after 30s; leaving VM generic"
  exit 0
fi

echo "seed found"

jq_get() { /usr/bin/plutil -extract "$1" raw -o - "${SEED}" 2>/dev/null || true; }

HOSTNAME="$(jq_get hostname)"
REPO="$(jq_get repo)"
BRANCH="$(jq_get branch)"

if [ -n "${HOSTNAME}" ]; then
  scutil --set ComputerName  "${HOSTNAME}"
  scutil --set LocalHostName "${HOSTNAME}"
  scutil --set HostName      "${HOSTNAME}"
  echo "hostname=${HOSTNAME}"
fi

if [ -f "${SEED_VOL}/authorized_keys" ]; then
  install -d -o admin -g staff -m 700 /Users/admin/.ssh
  install -o admin -g staff -m 600 "${SEED_VOL}/authorized_keys" /Users/admin/.ssh/authorized_keys
  echo "authorized_keys installed"
fi

# Environment for the run. Secrets arrive here rather than in the image, because
# image layers are content-addressed and immutable, so a pushed secret cannot be
# taken back out.
if [ -f "${SEED_VOL}/run.env" ]; then
  install -o admin -g staff -m 600 "${SEED_VOL}/run.env" /Users/admin/.greenroom-run.env
  echo "run.env installed"
fi

if [ -n "${REPO}" ]; then
  echo "cloning ${REPO} (${BRANCH:-default branch})"
  if [ -n "${BRANCH}" ]; then
    sudo -u admin git clone --branch "${BRANCH}" "${REPO}" /Users/admin/work
  else
    sudo -u admin git clone "${REPO}" /Users/admin/work
  fi
fi

touch "${STAMP}"
echo "firstboot done"
