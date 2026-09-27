#!/usr/bin/env bash
# Updates greenroom from its source checkout (root ADR 0033): fast-forwards main to origin/main,
# then installs the daemon and the Companion from it, in that order.
#
#   scripts/update.sh           update and reinstall
#   scripts/update.sh --check   say how far origin/main is ahead; change nothing
#
# It never merges, rebases or discards: it refuses a checkout with local changes (untracked
# files included), one not on main, and a main that cannot fast-forward to origin/main.
# The daemon's install swaps its binary only after a successful build, and the Companion's
# replaces the app only after a successful bundle, so a failed step leaves both as they were.
#
# The Companion runs this and reads its output, one fact per line:
#   --check:  "main <sha>" (origin/main), "ahead <n>", then "commit <sha> <subject>" for each
#             commit main lacks, newest first, and "refused: <why>" when an update would refuse.
#             Exit 0 unless the fetch fails.
#   update:   "step: <name>" as each step starts, that step's own output after it, then
#             "done: main is at <sha>" (exit 0), "refused: <why>" (exit 2) or
#             "failed: <step name>" (exit 1).
# Keep these words: the Companion parses them (UpdateOutput.swift).
set -euo pipefail

# Everything runs inside main, called on the last line: bash then has the whole script in
# memory before the fast-forward rewrites this file.
main() {
  local check=false
  case "${1:-}" in
    "") ;;
    --check) check=true ;;
    *)
      echo "usage: scripts/update.sh [--check]" >&2
      exit 64
      ;;
  esac

  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  cd "$repo"
  # The Companion starts this from a GUI app, whose PATH has no Homebrew and no Go.
  export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:$PATH"

  step="check the checkout"
  $check || echo "step: $step"
  local refusal=""
  if [ -n "$(git status --porcelain)" ]; then
    refusal="the checkout at $repo has local changes; commit, stash or remove them first"
  elif [ "$(git symbolic-ref --quiet --short HEAD || true)" != "main" ]; then
    refusal="the checkout at $repo is not on main"
  fi
  if [ -n "$refusal" ] && ! $check; then
    refuse "$refusal"
  fi

  step="fetch origin"
  $check || echo "step: $step"
  local fetched
  if ! fetched="$(git fetch --quiet origin main 2>&1)"; then
    [ -n "$fetched" ] && echo "$fetched"
    fail "$step"
  fi
  [ -n "$fetched" ] && echo "$fetched"

  if [ -z "$refusal" ] && ! git merge-base --is-ancestor HEAD origin/main; then
    refusal="main has commits origin/main does not; it cannot fast-forward"
  fi

  local ahead
  ahead="$(git rev-list --count HEAD..origin/main)"
  if $check; then
    echo "main $(git rev-parse --short origin/main)"
    echo "ahead $ahead"
    git log --format='commit %h %s' HEAD..origin/main
    [ -z "$refusal" ] || echo "refused: $refusal"
    exit 0
  fi
  [ -z "$refusal" ] || refuse "$refusal"

  step="fast-forward main"
  echo "step: $step ($ahead new commits)"
  git merge --ff-only --quiet origin/main || fail "$step"

  step="install the daemon"
  echo "step: $step"
  apps/daemon/scripts/install.sh || fail "$step"

  # Last: it quits the running Companion and opens the new one.
  step="install the Companion"
  echo "step: $step"
  apps/companion/scripts/install.sh || fail "$step"

  echo "done: main is at $(git rev-parse --short HEAD)"
}

refuse() {
  echo "refused: $1"
  exit 2
}

fail() {
  echo "failed: $1"
  exit 1
}

main "$@"; exit
