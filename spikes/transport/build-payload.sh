#!/bin/bash
# Stage greenroom's own repo as a warm-build payload, with isolated caches so nothing is
# written into apps/daemon or apps/companion. See README.md.
set -euo pipefail

DEST="${1:?usage: build-payload.sh <staging dir>}"
REPO="$(cd "$(dirname "$0")/../.." && pwd)"

rm -rf "$DEST"
mkdir -p "$DEST"/{repo,companion,daemon,gocaches/mod,gocaches/build}

rsync -a --exclude '.git' --exclude 'apps/companion' --exclude 'apps/daemon' \
      "$REPO/" "$DEST/repo/"
rsync -a --exclude '.build' "$REPO/apps/companion/" "$DEST/companion/"
rsync -a --exclude '.build' "$REPO/apps/daemon/"    "$DEST/daemon/"

echo "== swift build (companion) =="
( cd "$DEST/companion" && time swift build -c debug --scratch-path "$DEST/companion/.build" )

echo "== go build and test (daemon) =="
(
  cd "$DEST/daemon"
  export GOMODCACHE="$DEST/gocaches/mod" GOCACHE="$DEST/gocaches/build" GOFLAGS=-mod=mod
  time go build ./...
  time go test -count=1 ./... >/dev/null
)

echo
echo "payload staged at $DEST"
"$(dirname "$0")/payload-stats.py" "$DEST"
