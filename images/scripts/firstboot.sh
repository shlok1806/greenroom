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
#
# Verified end to end on 2026-09-21 against a booted VM: seed mounts, hostname is
# set, authorized_keys and run.env land as admin:staff 600, and the repo clones as
# admin:staff. See the "first boot, measured" table in docs/09-image-strategy.md.

# No -e on purpose. One failed step must not abandon the rest of the
# personalization, so every step records its own outcome and the script decides at
# the end whether the VM is personalized.
set -uo pipefail

LOG=/var/log/greenroom-firstboot.log
exec >>"${LOG}" 2>&1
echo "=== greenroom firstboot $(date -u) ==="

SEED_VOL="/Volumes/GRSEED"
SEED="${SEED_VOL}/personalize.json"
STAMP="/var/db/.greenroom-personalized"
RESULT="/var/db/greenroom-firstboot.json"

GUEST_USER="admin"
GUEST_HOME="/Users/${GUEST_USER}"
SEED_WAIT_SECONDS=60

# Steps that failed, reported in the result file. The run record reads this, so a
# silent partial personalization is not possible.
FAILURES=()
fail_step() { echo "FAIL: $1"; FAILURES+=("$1"); }

# The result file is the evidence the daemon attaches to the run record. Write it
# on every exit path, including the early ones, or a VM that failed to personalize
# looks exactly like one that was never meant to be.
write_result() {
  local status="$1" detail="$2" failed_json=""
  if [ ${#FAILURES[@]} -gt 0 ]; then
    failed_json=$(printf '"%s",' "${FAILURES[@]}"); failed_json="${failed_json%,}"
  fi
  cat >"${RESULT}" <<EOF
{
  "status": "${status}",
  "detail": "${detail}",
  "at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "hostname": "${HOSTNAME_VALUE:-}",
  "repo": "${REPO:-}",
  "branch": "${BRANCH:-}",
  "workdir": "${PROJECT:+/Users/admin/work/${PROJECT}}",
  "failed": [${failed_json}]
}
EOF
  chmod 644 "${RESULT}"
  echo "result: ${status} ${detail}"
}

if [ -f "${STAMP}" ]; then
  echo "already personalized, nothing to do"
  exit 0
fi

# diskarbitrationd mounts asynchronously, so RunAtLoad can start before the seed is
# there. Poll for it. Measured: the volume was present well inside this budget, but
# the budget is generous because the cost of being wrong is a generic VM.
for _ in $(seq 1 "${SEED_WAIT_SECONDS}"); do
  [ -f "${SEED}" ] && break
  sleep 1
done

if [ ! -f "${SEED}" ]; then
  echo "no seed at ${SEED} after ${SEED_WAIT_SECONDS}s; leaving VM generic"
  # Not a failure. A VM booted with no seed disk is a legitimate thing to do, and
  # no stamp is written so a later boot with a seed still personalizes.
  write_result "no-seed" "no seed disk attached"
  exit 0
fi

echo "seed found"

# plutil reads JSON, so the image needs no jq. Verified on the real seed image:
# `plutil -extract hostname raw -o -` returns the bare value.
seed_get() { /usr/bin/plutil -extract "$1" raw -o - "${SEED}" 2>/dev/null || true; }

# Not named HOSTNAME: bash sets that itself, and shadowing it here made the value
# look right in this script while anything sourcing it later disagreed.
HOSTNAME_VALUE="$(seed_get hostname)"
REPO="$(seed_get repo)"
BRANCH="$(seed_get branch)"

if [ -n "${HOSTNAME_VALUE}" ]; then
  if scutil --set ComputerName  "${HOSTNAME_VALUE}" \
  && scutil --set LocalHostName "${HOSTNAME_VALUE}" \
  && scutil --set HostName      "${HOSTNAME_VALUE}"; then
    echo "hostname=${HOSTNAME_VALUE}"
  else
    fail_step "hostname"
  fi
fi

if [ -f "${SEED_VOL}/authorized_keys" ]; then
  if install -d -o "${GUEST_USER}" -g staff -m 700 "${GUEST_HOME}/.ssh" \
  && install -o "${GUEST_USER}" -g staff -m 600 \
       "${SEED_VOL}/authorized_keys" "${GUEST_HOME}/.ssh/authorized_keys"; then
    echo "authorized_keys installed"
  else
    fail_step "authorized_keys"
  fi
fi

# Environment for the run. Secrets arrive here rather than in the image, because
# image layers are content-addressed and immutable, so a pushed secret cannot be
# taken back out.
if [ -f "${SEED_VOL}/run.env" ]; then
  if install -o "${GUEST_USER}" -g staff -m 600 \
       "${SEED_VOL}/run.env" "${GUEST_HOME}/.greenroom-run.env"; then
    echo "run.env installed"
  else
    fail_step "run.env"
  fi
fi

if [ -n "${REPO}" ]; then
  echo "cloning ${REPO} (${BRANCH:-default branch})"

  # GIT_TERMINAL_PROMPT=0 and BatchMode are what keep this from hanging forever.
  # There is no terminal in a LaunchDaemon, so a private repo over https would
  # otherwise block on a username prompt nobody can answer, and an unknown ssh host
  # key would block on the yes/no question, and the VM would sit there until the
  # run timed out with no indication why. Closing both prompts is the fix; there is
  # deliberately no wall-clock watchdog here, because macOS ships no `timeout` and
  # a hand-rolled one would have to kill across the `sudo` boundary to work. A slow
  # clone is a slow clone, and the run has its own timeout above this.
  #
  # HOME must be set explicitly: `sudo -u` keeps root's HOME, so git would read
  # /var/root/.gitconfig and write the repo's config as the wrong user's.
  # The repo lands at ~/work/<project>, never at ~/work itself, because that
  # is where machine_sync puts a project and the two must agree. A SwiftPM
  # build cache is keyed to the absolute path it was built at, so a project
  # that arrives by clone and a project that arrives by sync ending up at
  # different paths is not a cache miss, it is "error: missing required module
  # 'SwiftShims'". See machine.GuestWorkDir in apps/daemon.
  PROJECT="$(basename "${REPO}")"
  PROJECT="${PROJECT%.git}"
  if [ -z "${PROJECT}" ]; then
    PROJECT="project"
  fi

  CLONE_ARGS=(git clone --depth 1)
  [ -n "${BRANCH}" ] && CLONE_ARGS+=(--branch "${BRANCH}")
  CLONE_ARGS+=("${REPO}" "${GUEST_HOME}/work/${PROJECT}")

  if sudo -u "${GUEST_USER}" \
       env HOME="${GUEST_HOME}" \
           GIT_TERMINAL_PROMPT=0 \
           GIT_SSH_COMMAND="ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new" \
       "${CLONE_ARGS[@]}"; then
    echo "clone ok"
  else
    fail_step "clone"
  fi
fi

if [ ${#FAILURES[@]} -eq 0 ]; then
  touch "${STAMP}"
  write_result "ok" "personalized"
  echo "firstboot done"
  exit 0
fi

# No stamp on failure, so the next boot tries again rather than leaving a
# half-personalized machine that reports itself as ready.
write_result "failed" "${#FAILURES[@]} step(s) failed"
echo "firstboot FAILED: ${FAILURES[*]}"
exit 1
