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

HOSTNAME=""
REPO=""
BRANCH=""
KEYS=""
ENVFILE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --hostname)        HOSTNAME="$2"; shift 2 ;;
    --repo)            REPO="$2";     shift 2 ;;
    --branch)          BRANCH="$2";   shift 2 ;;
    --authorized-keys) KEYS="$2";     shift 2 ;;
    --env)             ENVFILE="$2";  shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

# plutil reads this in the guest; no jq dependency inside the image.
cat >"${STAGE}/personalize.json" <<EOF
{
  "hostname": "${HOSTNAME}",
  "repo": "${REPO}",
  "branch": "${BRANCH}"
}
EOF

[ -n "${KEYS}" ]    && cp "${KEYS}"    "${STAGE}/authorized_keys"
[ -n "${ENVFILE}" ] && cp "${ENVFILE}" "${STAGE}/run.env"

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
