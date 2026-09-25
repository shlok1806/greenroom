#!/bin/bash
# Runs the host side of remote access (ADR 0021): the token, the Cloudflare tunnel and the
# client installer the daemon serves. The host is the Mac that runs the daemon, Tart and the
# VMs; the client Mac only ever talks to https://<public host> through the tunnel.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"   # scripts/remote
repo="$(cd "$here/../.." && pwd)"

# Resolve the env file exactly as apps/daemon/scripts/install.sh does, so the token written
# here is the one the launchd daemon reads. .env is git-ignored, so a worktree falls back to
# the main checkout's. GREENROOM_ENV overrides both and is used as given, even when missing:
# a typo there must never send a token into the main checkout's .env.
if [ -n "${GREENROOM_ENV:-}" ]; then
  env_file="$GREENROOM_ENV"
else
  env_file="$repo/.env"
  if [ ! -f "$env_file" ]; then
    main="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>/dev/null | sed 's|/\.git$||' || true)"
    [ -n "$main" ] && [ -f "$main/.env" ] && env_file="$main/.env"
  fi
fi

default_host="greenroom.shlokthakkar.com"
tunnel_name="greenroom"
cf_dir="$HOME/.cloudflared"
cf_config="$cf_dir/greenroom.yml"
root="$HOME/.greenroom"
dist="$root/dist"
origin="http://127.0.0.1:7777"

# Temp files live in globals, not locals, so the EXIT trap still sees them after a die from
# deep inside a function. hdr holds the token, so it must never outlive the script.
tmp=""
stage=""
hdr=""
cleanup() {
  [ -z "$tmp" ] || rm -f "$tmp"
  [ -z "$stage" ] || rm -rf "$stage"
  [ -z "$hdr" ] || rm -f "$hdr"
}
trap cleanup EXIT

die() {
  echo "$*" >&2
  exit 1
}

# env_get KEY prints KEY's value from the env file the way the daemon's loadEnvFile reads it:
# the first KEY= line wins, an optional "export " prefix, one matching quote pair stripped.
# Inline "# comments" are not handled; the lines this script writes never have them.
env_get() {
  [ -f "$env_file" ] || return 0
  awk -v key="$1" '
    {
      line = $0
      sub(/^[ \t]+/, "", line)
      sub(/^export[ \t]+/, "", line)
      eq = index(line, "=")
      if (eq == 0) next
      k = substr(line, 1, eq - 1)
      sub(/[ \t]+$/, "", k)
      if (k != key) next
      v = substr(line, eq + 1)
      sub(/^[ \t]+/, "", v)
      sub(/[ \t\r]+$/, "", v)
      q = substr(v, 1, 1)
      if ((q == "\"" || q == "'\''") && length(v) >= 2 && substr(v, length(v), 1) == q) {
        v = substr(v, 2, length(v) - 2)
      }
      print v
      exit
    }
  ' "$env_file"
}

# The environment wins so a one-off run can point elsewhere; then the env file (what the
# daemon itself uses); then the one hostname this project owns.
public_host() {
  local host="${GREENROOM_PUBLIC_HOST:-}"
  [ -n "$host" ] || host="$(env_get GREENROOM_PUBLIC_HOST)"
  [ -n "$host" ] || host="$default_host"
  # The name lands in YAML, a sed replacement and a URL, so keep it to hostname characters.
  case "$host" in
    *[!A-Za-z0-9.-]* | "" | .* | *. ) die "not a hostname: $host" ;;
  esac
  printf '%s\n' "$host"
}

# end_line adds a final newline when a file lacks one, so an appended KEY=VALUE line does not
# glue itself onto the last line. $(...) strips a trailing newline, so a newline reads as empty.
end_line() {
  if [ -s "$1" ] && [ -n "$(tail -c 1 "$1")" ]; then
    echo >>"$1"
  fi
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is not installed. $2"
}

cmd_token() {
  local rotate="" show=""
  for arg in "$@"; do
    case "$arg" in
      --rotate) rotate="yes" ;;
      --show) show="yes" ;;
      *) die "token: unknown flag $arg (want --rotate or --show)" ;;
    esac
  done

  local existing
  existing="$(env_get GREENROOM_TOKEN)"

  if [ -n "$show" ] && [ -z "$rotate" ]; then
    [ -n "$existing" ] || die "no GREENROOM_TOKEN in $env_file; run: $0 token"
    printf '%s\n' "$existing"
    return
  fi

  # The file already holds the model key, and now a credential that drives every machine on
  # this host, so nobody else on the Mac should read it.
  if [ ! -f "$env_file" ]; then
    (umask 077 && : >"$env_file")
    echo "created $env_file"
  fi
  chmod 600 "$env_file"

  local changed=""
  if [ -n "$existing" ] && [ -z "$rotate" ]; then
    # The daemon refuses to start with a public host and a shorter token, so say so here.
    [ "${#existing}" -ge 32 ] || die "GREENROOM_TOKEN in $env_file is ${#existing} characters; the daemon needs 32 or more. Run: $0 token --rotate"
    echo "GREENROOM_TOKEN is already set in $env_file (keeping it; --rotate replaces it)"
  else
    need openssl "It ships with macOS."
    local token
    token="$(openssl rand -hex 32)"
    [ "${#token}" -eq 64 ] || die "openssl rand did not return 64 hex characters"
    # Drop every old token line (the daemon reads the first, so a stale one left above the
    # new one would win), then append. Rewrite in place so the file keeps its mode.
    tmp="$(mktemp -t greenroom-env)"
    awk '{ l = $0; sub(/^[ \t]+/, "", l); sub(/^export[ \t]+/, "", l) }
         l !~ /^GREENROOM_TOKEN[ \t]*=/' "$env_file" >"$tmp"
    end_line "$tmp"
    printf 'GREENROOM_TOKEN=%s\n' "$token" >>"$tmp"
    cat "$tmp" >"$env_file"
    rm -f "$tmp"
    tmp=""
    if [ -n "$existing" ]; then
      echo "rotated GREENROOM_TOKEN in $env_file (every client must re-run the installer)"
    else
      echo "wrote GREENROOM_TOKEN to $env_file"
    fi
    changed="yes"
  fi

  local file_host want_host
  file_host="$(env_get GREENROOM_PUBLIC_HOST)"
  want_host="${GREENROOM_PUBLIC_HOST:-$default_host}"
  if [ -z "$file_host" ]; then
    case "$want_host" in
      *[!A-Za-z0-9.-]* | .* | *. ) die "not a hostname: $want_host" ;;
    esac
    end_line "$env_file"
    printf 'GREENROOM_PUBLIC_HOST=%s\n' "$want_host" >>"$env_file"
    echo "wrote GREENROOM_PUBLIC_HOST=$want_host to $env_file"
    changed="yes"
  elif [ -n "${GREENROOM_PUBLIC_HOST:-}" ] && [ "$GREENROOM_PUBLIC_HOST" != "$file_host" ]; then
    echo "note: $env_file already names GREENROOM_PUBLIC_HOST=$file_host; left it (edit the file to change it)"
  fi

  if [ -n "$show" ]; then
    env_get GREENROOM_TOKEN
  fi

  # launchd started the daemon with the old environment; only a reinstall re-reads the file.
  if [ -n "$changed" ]; then
    echo "reinstall the daemon so it reloads the env file: $repo/apps/daemon/scripts/install.sh"
  fi
}

# tunnel_id prints the id of the tunnel named greenroom, or nothing.
tunnel_id() {
  cloudflared tunnel list --name "$tunnel_name" 2>/dev/null |
    awk -v name="$tunnel_name" '$2 == name && $1 ~ /^[0-9a-f-]+$/ { print $1; exit }'
}

cmd_tunnel_setup() {
  need cloudflared "Install it with: brew install cloudflared"
  [ -f "$cf_dir/cert.pem" ] || die "no $cf_dir/cert.pem; run: cloudflared tunnel login"
  local host id
  host="$(public_host)"

  id="$(tunnel_id)"
  if [ -n "$id" ]; then
    echo "tunnel $tunnel_name exists: $id"
  else
    echo "creating tunnel $tunnel_name"
    cloudflared tunnel create "$tunnel_name"
    id="$(tunnel_id)"
    [ -n "$id" ] || die "created tunnel $tunnel_name but cannot find its id in 'cloudflared tunnel list'"
  fi

  # create writes the credentials here; a tunnel created on another machine has none, and
  # without them `run` cannot authenticate as the tunnel.
  local creds="$cf_dir/$id.json"
  [ -f "$creds" ] || die "no credentials at $creds for tunnel $id. It was created elsewhere: copy its json here, or delete the tunnel (cloudflared tunnel delete $tunnel_name) and run this again."

  # route dns is not idempotent: a second run reports an existing record. That is fine when
  # the record is ours, so only fail on other errors. It does not overwrite a record that
  # points somewhere else (that would need --overwrite-dns, which we will not do silently).
  local out
  if out="$(cloudflared tunnel route dns "$tunnel_name" "$host" 2>&1)"; then
    echo "routed $host to tunnel $tunnel_name"
  elif printf '%s' "$out" | grep -qi "already"; then
    echo "a DNS record for $host already exists; if it is not a CNAME to $id.cfargotunnel.com, fix it in the Cloudflare dashboard"
  else
    printf '%s\n' "$out" >&2
    die "cloudflared tunnel route dns failed"
  fi

  # No originRequest options: cloudflared forwards the original Host by default, and the
  # daemon's api.Guard authorises by Host (ADR 0021), so httpHostHeader must stay unset.
  # The catch-all 404 answers any other hostname Cloudflare sends this tunnel.
  tmp="$(mktemp "$cf_dir/greenroom.yml.XXXXXX")"
  cat >"$tmp" <<YAML
# Written by scripts/remote/host.sh tunnel-setup. Re-running it rewrites this file.
tunnel: $id
credentials-file: $creds

ingress:
  - hostname: $host
    service: $origin
  - service: http_status:404
YAML
  chmod 600 "$tmp"
  mv -f "$tmp" "$cf_config"
  tmp=""
  echo "wrote $cf_config"
  cloudflared tunnel --config "$cf_config" ingress validate
  echo "next: $0 tunnel"
}

cmd_tunnel() {
  need cloudflared "Install it with: brew install cloudflared"
  [ -f "$cf_config" ] || die "no $cf_config; run: $0 tunnel-setup"
  local host
  host="$(public_host)"
  echo "serving https://$host -> $origin until Ctrl-C"
  exec cloudflared tunnel --config "$cf_config" run "$tunnel_name"
}

cmd_dist() {
  need go "Install Go to build the client binary."
  need ditto "It ships with macOS."
  need shasum "It ships with macOS."
  local host
  host="$(public_host)"

  # Build next to dist so the final rename stays on one filesystem, and swap whole
  # directories: the daemon serves files straight from dist, so a client must never see a
  # new binary beside an old SHA256SUMS.
  mkdir -p "$root"
  stage="$(mktemp -d "$root/dist.new.XXXXXX")"

  echo "bundling the Companion"
  "$repo/apps/companion/scripts/bundle.sh"
  local app="$repo/apps/companion/.build/Companion.app"
  [ -d "$app" ] || die "bundle.sh did not produce $app"
  # Zip it under the name it is installed as, so the client moves one directory into place.
  # ditto keeps the ad-hoc signature, resource forks and symlinks that zip(1) can mangle.
  local named="$stage/app/Greenroom Companion.app"
  mkdir -p "$stage/app"
  ditto "$app" "$named"
  ditto -c -k --keepParent "$named" "$stage/GreenroomCompanion.zip"
  rm -rf "$stage/app"
  echo "zipped GreenroomCompanion.zip"

  # The client needs only `greenroom connect`, but it is the same binary as the daemon.
  # install.sh refuses anything but Apple silicon, so build exactly that.
  echo "building the client binary (darwin/arm64)"
  (cd "$repo/apps/daemon" && GOOS=darwin GOARCH=arm64 go build -o "$stage/greenroom" .)
  chmod 755 "$stage/greenroom"

  # The installer is served as-is, so bake the URL in rather than asking the client for it.
  sed "s|__GREENROOM_URL__|https://$host|g" "$here/client-install.sh" >"$stage/install.sh"
  if grep -q "__GREENROOM_URL__" "$stage/install.sh"; then
    die "install.sh still has a __GREENROOM_URL__ placeholder"
  fi
  sh -n "$stage/install.sh" || die "rendered install.sh does not parse"
  chmod 644 "$stage/install.sh"

  (cd "$stage" && shasum -a 256 greenroom GreenroomCompanion.zip >SHA256SUMS)

  # Two renames, not one atomic swap: between them dist is briefly missing and the daemon
  # answers 404, which a client retries; it never serves a half-built directory.
  local old=""
  if [ -e "$dist" ]; then
    old="$root/dist.old.$$"
    mv "$dist" "$old"
  fi
  mv "$stage" "$dist"
  stage=""
  [ -z "$old" ] || rm -rf "$old"

  echo "dist ready in $dist:"
  (cd "$dist" && ls -l)
  echo "clients install with: curl -fsSL https://$host/install.sh | sh -s -- <token>"
}

# http_code URL [curl args...] prints the status curl got, or 000 when it could not connect.
http_code() {
  local url="$1"
  shift
  curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$@" "$url" || true
}

cmd_check() {
  local host token
  host="$(public_host)"
  token="$(env_get GREENROOM_TOKEN)"
  [ -n "$token" ] || die "no GREENROOM_TOKEN in $env_file; run: $0 token"
  local base="https://$host"

  # Hand curl the header through a file so the token never appears in ps or on screen.
  hdr="$(mktemp -t greenroom-check)"
  printf 'Authorization: Bearer %s\n' "$token" >"$hdr"

  local install anon authed
  install="$(http_code "$base/install.sh")"
  anon="$(http_code "$base/healthz")"
  authed="$(http_code "$base/healthz" -H "@$hdr")"
  rm -f "$hdr"
  hdr=""

  local fail=""
  [ "$install" = 200 ] || fail="yes"
  [ "$anon" = 401 ] || fail="yes"
  [ "$authed" = 200 ] || fail="yes"
  local line="install.sh=$install (want 200) healthz no-token=$anon (want 401) healthz token=$authed (want 200)"
  if [ -n "$fail" ]; then
    echo "FAIL $base: $line" >&2
    case "$install$anon$authed" in
      *000*) echo "000 means no answer: is '$0 tunnel' running?" >&2 ;;
    esac
    [ "$install" != 404 ] || echo "install.sh 404: run '$0 dist'" >&2
    exit 1
  fi
  echo "ok $base: $line"
}

usage() {
  cat <<USAGE
usage: $0 <command>

Host side of remote access (docs/adr/0021-remote-access-through-a-tunnel.md).
Env file: $env_file

  token [--rotate] [--show]  add GREENROOM_TOKEN and GREENROOM_PUBLIC_HOST to the env file
                             (keeps an existing token unless --rotate; --show prints it)
  tunnel-setup               create the cloudflared tunnel, its DNS route and $cf_config
  tunnel                     run the tunnel in the foreground; Ctrl-C ends public access
  dist                       build the client installer, binary and Companion into $dist
  check                      probe the public URL: install.sh open, /healthz needs the token
  help                       this text

First run: token, then apps/daemon/scripts/install.sh, then tunnel-setup, dist, tunnel,
and check from another terminal.
USAGE
}

main() {
  local cmd="${1:-help}"
  [ "$#" -eq 0 ] || shift
  case "$cmd" in
    token) cmd_token "$@" ;;
    tunnel-setup) cmd_tunnel_setup "$@" ;;
    tunnel) cmd_tunnel "$@" ;;
    dist) cmd_dist "$@" ;;
    check) cmd_check "$@" ;;
    help | -h | --help) usage ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
