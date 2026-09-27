#!/bin/bash
# How install.sh installs the daemon this time: the environment first, then what the existing
# launchd job was installed with, then the defaults. So an update (scripts/update.sh, root
# ADR 0033), which runs install.sh with none of the variables a person set by hand, keeps the
# verifier, image, env file, tart and extra environment of the install it replaces.
#
#   install-settings.sh <plist> <env file install.sh would pick by itself>
#
# Prints one KEY=value a line (install.sh reads them; keep the keys):
#   verifier=, verifier_from=   always set (default nim)
#   image=, image_from=         empty: install.sh picks the image itself
#   env_file=, env_file_from=   empty: the env file install.sh picks by itself
#   tart=, tart_from=           empty: install.sh resolves tart itself
#   keep=KEY=value              each other variable of the old job's EnvironmentVariables
# *_from is "environment", "previous install" or "default".
#
# A setting counts as chosen, and is carried over, only when it was: install.sh writes
# GREENROOM_IMAGE, GREENROOM_ENV and GREENROOM_TART into the job's EnvironmentVariables only
# when they were set. A job from before that has only its arguments, so there an -image other
# than the ones install.sh picks by itself, or an -env-file other than the one it would pick
# now, counts as chosen. -verifier is always carried over. A missing or unreadable plist
# carries nothing over.
set -euo pipefail

plist="${1:?usage: install-settings.sh <plist> <auto env file>}"
auto_env="${2:-}"
pb=/usr/libexec/PlistBuddy

# The images install.sh picks by itself, in order (keep in step with install.sh).
auto_images="greenroom-lean-a greenroom-base ghcr.io/cirruslabs/macos-tahoe-base:latest"

readable=false
if [ -f "$plist" ] && "$pb" -c "Print :ProgramArguments" "$plist" >/dev/null 2>&1; then
  readable=true
fi

# The value after flag $1 in the old job's ProgramArguments.
arg_after() {
  $readable || return 0
  local i=0 value prev=""
  while value="$("$pb" -c "Print :ProgramArguments:$i" "$plist" 2>/dev/null)"; do
    if [ "$prev" = "$1" ]; then
      printf '%s' "$value"
      return 0
    fi
    prev="$value"
    i=$((i + 1))
  done
}

# The old job's environment variable $1.
env_of() {
  $readable || return 0
  "$pb" -c "Print :EnvironmentVariables:$1" "$plist" 2>/dev/null || true
}

prev_verifier="$(arg_after -verifier)"
prev_image="$(env_of GREENROOM_IMAGE)"
if [ -z "$prev_image" ]; then
  image_arg="$(arg_after -image)"
  case " $auto_images " in
    *" $image_arg "*) ;;
    *) prev_image="$image_arg" ;;
  esac
fi
prev_env="$(env_of GREENROOM_ENV)"
if [ -z "$prev_env" ]; then
  env_arg="$(arg_after -env-file)"
  [ -n "$env_arg" ] && [ "$env_arg" != "$auto_env" ] && prev_env="$env_arg"
fi
prev_tart="$(env_of GREENROOM_TART)"

# setting <name> <from the environment> <from the previous install> [default]
setting() {
  if [ -n "$2" ]; then
    printf '%s=%s\n%s_from=environment\n' "$1" "$2" "$1"
  elif [ -n "$3" ]; then
    printf '%s=%s\n%s_from=previous install\n' "$1" "$3" "$1"
  else
    printf '%s=%s\n%s_from=default\n' "$1" "${4:-}" "$1"
  fi
}

setting verifier "${GREENROOM_VERIFIER:-}" "$prev_verifier" nim
setting image "${GREENROOM_IMAGE:-}" "$prev_image"
setting env_file "${GREENROOM_ENV:-}" "$prev_env"
setting tart "${GREENROOM_TART:-}" "$prev_tart"

# Every other variable of the old job: install.sh writes these again as they were. PATH and
# GREENROOM_CHECKOUT are install.sh's own, and the four above are decided just now.
if $readable; then
  "$pb" -c "Print :EnvironmentVariables" "$plist" 2>/dev/null |
    sed -n 's/^    \([A-Za-z_][A-Za-z0-9_]*\) = \(.*\)$/\1=\2/p' |
    while IFS= read -r pair; do
      case "${pair%%=*}" in
        PATH | GREENROOM_CHECKOUT | GREENROOM_IMAGE | GREENROOM_ENV | GREENROOM_TART | GREENROOM_VERIFIER) ;;
        *) printf 'keep=%s\n' "$pair" ;;
      esac
    done
fi
