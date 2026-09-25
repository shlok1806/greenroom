#!/bin/sh
# Installs the greenroom client on this Mac (ADR 0021): the `greenroom` binary, whose
# `connect` command is Claude Code's MCP server for a remote daemon, and the Companion app.
#
#   curl -fsSL __GREENROOM_URL__/install.sh | sh -s -- <token>
#
# The host's scripts/remote/host.sh dist renders this file with its public URL and the daemon
# serves it. It is POSIX sh because it runs as `| sh`, which is macOS /bin/sh, not bash.
# Re-running it updates everything in place.
set -eu

url="__GREENROOM_URL__"
root="$HOME/.greenroom"
bin="$root/bin/greenroom"
client_json="$root/client.json"
app_name="Greenroom Companion.app"

say() {
  printf '==> %s\n' "$*"
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  printf 'usage: curl -fsSL %s/install.sh | sh -s -- <token>\n' "$url" >&2
  printf 'The token is GREENROOM_TOKEN from the host (host.sh token --show).\n' >&2
  exit 1
}

check_token() {
  [ -n "$token" ] || usage
  [ "${#token}" -ge 32 ] || {
    printf 'error: the token is %s characters; a greenroom token has at least 32.\n' "${#token}" >&2
    usage
  }
  # Only URL-safe characters, so the token can go into JSON and an HTTP header unescaped.
  case "$token" in
    *[!A-Za-z0-9._~-]*) die "the token has characters other than A-Z a-z 0-9 . _ ~ -; check you copied it whole" ;;
  esac
}

check_mac() {
  say "checking this Mac"
  [ "$(uname -s)" = Darwin ] || die "greenroom's client runs on macOS only"
  arch="$(uname -m)"
  [ "$arch" = arm64 ] || die "this is a $arch Mac; the client is built for Apple silicon (arm64) only"
  version="$(sw_vers -productVersion)"
  major="${version%%.*}"
  case "$major" in
    '' | *[!0-9]*) die "cannot read the macOS version from sw_vers ($version)" ;;
  esac
  [ "$major" -ge 15 ] || die "this is macOS $version; the Companion needs macOS 15 or later"
}

download() {
  say "downloading from $url"
  for file in SHA256SUMS greenroom GreenroomCompanion.zip; do
    curl -fsSL --retry 2 -o "$tmp/$file" "$url/dl/$file" ||
      die "could not download $url/dl/$file; is the host's tunnel running, and has it run host.sh dist?"
  done

  say "verifying checksums"
  # Check exactly the two files we install, and insist both are listed: a SHA256SUMS that
  # silently lacks a line would otherwise pass `shasum -c`.
  awk '$2 == "greenroom" || $2 == "GreenroomCompanion.zip"' "$tmp/SHA256SUMS" >"$tmp/SUMS"
  [ "$(wc -l <"$tmp/SUMS" | tr -d ' ')" = 2 ] || die "SHA256SUMS from the host does not list both files"
  (cd "$tmp" && shasum -a 256 -c SUMS >/dev/null) ||
    die "a download does not match its checksum; run the installer again"
}

install_binary() {
  say "installing $bin"
  mkdir -p "$root/bin"
  # Copy beside the old binary and rename over it: a running `greenroom connect` keeps its
  # inode, while writing into it in place can get the process killed by code signing.
  cp "$tmp/greenroom" "$bin.new"
  chmod 755 "$bin.new"
  mv -f "$bin.new" "$bin"
}

write_client_json() {
  say "writing $client_json"
  # umask in a subshell: the token grants control of every machine on the host, so the file
  # is never readable by anyone else, not even for the moment before a chmod. The url and
  # the checked token hold no character JSON needs escaped.
  (
    umask 077
    printf '{\n  "url": "%s",\n  "token": "%s"\n}\n' "$url" "$token" >"$client_json.new"
  )
  chmod 600 "$client_json.new"
  mv -f "$client_json.new" "$client_json"
}

check_connection() {
  say "checking the connection to $url"
  # </dev/null: this script arrives on sh's stdin, and a child that read it would eat the
  # rest of the script.
  if out="$("$bin" connect -check </dev/null 2>&1)"; then
    case "$out" in
      ok*) printf '    %s\n' "$out" ;;
      *) printf '%s\n' "$out" >&2; die "greenroom connect -check did not report ok" ;;
    esac
  else
    printf '%s\n' "$out" >&2
    die "cannot reach the daemon with this token. Is the token right, and is the host's tunnel running?"
  fi
}

quit_companion() {
  # `open` on a running bundle only activates the old process, so a reinstall would keep
  # showing the previous build. Only ask it to quit when it runs: `tell application` would
  # otherwise launch it.
  pattern="$app_name/Contents/MacOS/Companion"
  pgrep -fq "$pattern" || return 0
  say "quitting the running Companion"
  osascript -e 'tell application id "com.greenroom.companion" to quit' </dev/null >/dev/null 2>&1 || true
  i=0
  while [ "$i" -lt 20 ] && pgrep -fq "$pattern"; do
    sleep 0.25
    i=$((i + 1))
  done
  pkill -f "$pattern" 2>/dev/null || true
}

install_companion() {
  # No sudo: an admin can write /Applications; anyone else gets ~/Applications, which
  # Launchpad and Spotlight also index.
  if [ -w /Applications ]; then
    apps=/Applications
  else
    apps="$HOME/Applications"
    mkdir -p "$apps"
  fi
  dest="$apps/$app_name"
  say "installing $dest"

  ditto -x -k "$tmp/GreenroomCompanion.zip" "$tmp/app"
  [ -d "$tmp/app/$app_name" ] || die "the Companion zip does not hold $app_name"

  quit_companion
  rm -rf "$dest"
  mv "$tmp/app/$app_name" "$dest"
}

find_claude() {
  claude="$(command -v claude 2>/dev/null || true)"
  [ -n "$claude" ] && return 0
  for candidate in "$HOME/.local/bin/claude" "$HOME/.claude/local/claude" /opt/homebrew/bin/claude /usr/local/bin/claude; do
    if [ -x "$candidate" ]; then
      claude="$candidate"
      return 0
    fi
  done
  claude=""
}

register_claude() {
  find_claude
  if [ -z "$claude" ]; then
    say "Claude Code is not installed; after installing it, run:"
    manual_mcp
    registered="no"
    return 0
  fi
  say "registering greenroom with Claude Code ($claude)"
  # remove then add: `mcp add` refuses a name that exists, and the old entry may point at
  # another command.
  "$claude" mcp remove greenroom -s user </dev/null >/dev/null 2>&1 || true
  if "$claude" mcp add -s user greenroom -- "$bin" connect </dev/null >/dev/null 2>"$tmp/mcp.err"; then
    registered="yes"
  else
    cat "$tmp/mcp.err" >&2
    say "could not register it; run these yourself:"
    manual_mcp
    registered="no"
  fi
}

manual_mcp() {
  printf '    claude mcp remove greenroom -s user\n'
  printf '    claude mcp add -s user greenroom -- "%s" connect\n' "$bin"
}

main() {
  token="${1:-}"
  check_token
  check_mac

  tmp="$(mktemp -d "${TMPDIR:-/tmp}/greenroom-install.XXXXXX")"
  trap 'rm -rf "$tmp"' EXIT
  trap 'exit 1' HUP INT TERM

  download
  install_binary
  write_client_json
  check_connection
  install_companion
  register_claude

  say "opening the Companion"
  open "$dest" </dev/null || say "could not open $dest; open it from Finder"

  printf '\ndone.\n'
  printf '  binary     %s\n' "$bin"
  printf '  config     %s (url and token, mode 600)\n' "$client_json"
  printf '  companion  %s\n' "$dest"
  if [ "$registered" = yes ]; then
    printf '  claude     greenroom MCP server at user scope\n'
    printf '\nOpen Claude Code in any project and ask it to use greenroom to create a machine.\n'
  else
    printf '  claude     not registered (see the commands above)\n'
    printf '\nThen open Claude Code in any project and ask it to use greenroom to create a machine.\n'
  fi
}

# Everything runs from main, called on the last line, so sh has parsed the whole file
# before it acts: a download cut short cannot run half an installer.
main "$@"
