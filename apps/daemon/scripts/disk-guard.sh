# Sourced by build-image.sh: the free-disk guard (issue #155). The host is shared with the bench,
# CI's VM suite and other machines, so the space the start of a build found can go while the
# guest grows. POSIX sh.

# free_gb prints the whole GB free on / (tart keeps its VMs under ~/.tart there).
free_gb() {
  df -k / | awk 'NR==2 {print int($4 / 1048576)}'
}

# guarded <min GB> <command...> runs the command, reading the free space every
# GREENROOM_DISK_POLL seconds (default 15). Under <min GB> it stops the command, says why on
# stderr and returns 75; otherwise it returns the command's own status.
guarded() {
  guard_min="$1"
  shift
  "$@" &
  guard_pid=$!
  guard_low=""
  guard_tick=0
  while kill -0 "$guard_pid" 2>/dev/null; do
    if [ "$guard_tick" -le 0 ]; then
      guard_free="$(free_gb)"
      if [ "$guard_free" -lt "$guard_min" ]; then
        guard_low="yes"
        echo "only ${guard_free} GB free on /, under ${guard_min} GB; stopping $1" >&2
        kill "$guard_pid" 2>/dev/null
        break
      fi
      guard_tick="${GREENROOM_DISK_POLL:-15}"
    fi
    sleep 1
    guard_tick=$((guard_tick - 1))
  done
  wait "$guard_pid" 2>/dev/null # no "Terminated" from the shell for a step it stopped
  guard_status=$?
  if [ -n "$guard_low" ]; then
    return 75
  fi
  return "$guard_status"
}
