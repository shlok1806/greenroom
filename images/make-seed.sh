#!/bin/bash
# Build a per-VM seed disk. This is how greenroom personalizes a machine.
#
#   ./images/make-seed.sh /tmp/run-123-seed.dmg \
#       --hostname gr-run-123 \
#       --repo git@github.com:acme/app.git \
#       --branch fix-123 \
#       --authorized-keys ~/.greenroom/id_ed25519.pub \
#       --env /tmp/run-123.env
#
#   tart run --no-graphics --suspendable --disk "/tmp/run-123-seed.dmg:ro" <vm>
#
# Use UDRW, not UDRO. Tart rejects compressed images with "Invalid disk image. The
# disk image format is not recognized." See docs/09-image-strategy.md.

set -euo pipefail

OUT="${1:-}"
[ -n "${OUT}" ] || { echo "usage: $0 <out.dmg> [options]" >&2; exit 1; }
shift

# Not named HOSTNAME: bash sets that itself, and shadowing it makes the value look
# right here while anything that reads the environment disagrees.
HOST=""
REPO=""
BRANCH=""
KEYS=""
ENVFILE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --hostname)        HOST="$2";    shift 2 ;;
    --repo)            REPO="$2";    shift 2 ;;
    --branch)          BRANCH="$2";  shift 2 ;;
    --authorized-keys) KEYS="$2";    shift 2 ;;
    --env)             ENVFILE="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

# Fail here rather than in the guest. A seed that names a file which does not exist
# produces a VM that boots, looks fine, and is missing the key you meant to install,
# and the only sign is a line in a log inside the guest.
for f in "${KEYS}" "${ENVFILE}"; do
  if [ -n "${f}" ] && [ ! -f "${f}" ]; then
    echo "no such file: ${f}" >&2; exit 1
  fi
done

# The guest reads these with plutil out of a JSON file, so a literal quote or
# backslash would produce a file plutil cannot parse and personalization would be
# skipped with only a log line to say so.
for v in "${HOST}" "${REPO}" "${BRANCH}"; do
  case "${v}" in
    *\"*|*\\*) echo "quotes and backslashes are not allowed in seed values: ${v}" >&2; exit 1 ;;
  esac
done

STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

# plutil reads this in the guest; no jq dependency inside the image.
cat >"${STAGE}/personalize.json" <<EOF
{
  "hostname": "${HOST}",
  "repo": "${REPO}",
  "branch": "${BRANCH}"
}
EOF

# Plain ifs, not `[ -n x ] && cp`. Under `set -e` that idiom is safe only because of
# an exception in the shell's own rules, which is not a thing to rely on.
if [ -n "${KEYS}" ]; then
  cp "${KEYS}" "${STAGE}/authorized_keys"
fi
if [ -n "${ENVFILE}" ]; then
  cp "${ENVFILE}" "${STAGE}/run.env"
fi

rm -f "${OUT}"
hdiutil create \
  -volname GRSEED \
  -srcfolder "${STAGE}" \
  -format UDRW \
  -fs HFS+ \
  -layout NONE \
  -quiet \
  "${OUT}"

echo "seed: ${OUT} ($(du -h "${OUT}" | cut -f1))"
