# 0021. Remote access through a tunnel, a token and a thin client

Date: 2026-09-25
Status: accepted

## Context

ADR 0002 scoped v0 to one Mac: the agent, the daemon and the machines all run on the
person's own laptop, and the daemon answers only loopback (`api.LocalOnly`) with no
authentication. The cloud story ("your laptop installs nothing heavy; the machine the
agent proves its work on lives somewhere else") needs a second Mac to reach a daemon on
another host. We test that with two real Macs: one runs the daemon, Tart and the VMs and
plays the cloud; the other runs only Claude Code and the Companion.

Three things stop that today:

1. The daemon refuses any non-loopback `Host`, and it has no way to tell a trusted remote
   client from anything else.
2. `machine_sync` takes an absolute path on the daemon's host and rsyncs from there. A
   remote agent's project lives on its own Mac, so the core flow (copy my project in,
   build it, prove it) cannot work.
3. The Companion is ad-hoc signed and reads its address from an environment variable,
   which a Finder-launched app never sees.

## Decision

1. **The daemon stays on loopback.** A Cloudflare named tunnel (`cloudflared`, on the
   daemon's host) is the only way in from outside. The host runs it by hand, only while
   testing. The daemon never binds a public interface.
2. **One public host name, one token.** `serve -public-host <name>` (or
   `GREENROOM_PUBLIC_HOST`) names the tunnel's hostname. `GREENROOM_TOKEN` (the env file
   or the environment) is a static bearer token of at least 32 characters; the daemon
   refuses to start with a public host and no such token. `api.Guard` replaces
   `api.LocalOnly`:
   - A loopback `Host` behaves exactly as before (loopback `Origin` or none, no token).
   - A `Host` equal to the public name must carry `Authorization: Bearer <token>`
     (compared in constant time), and any `Origin` header is refused: no browser is a
     client. The only exceptions are the install files (item 5).
   - Any other `Host` is refused, as before.
   `cloudflared` connects from loopback, so the peer address cannot tell tunnel traffic
   from local traffic. The `Host` header is what separates them, and it is safe to trust
   here because the daemon is reachable only through loopback.
3. **`greenroom connect` is the remote agent's MCP server.** It is a stdio MCP server,
   in the same binary as the daemon, that reads `~/.greenroom/client.json` (`url`,
   `token`), lists the remote daemon's tools and forwards every call unchanged, with the
   token on each request. It handles `machine_sync` itself: it tars the local `source`
   (honouring `exclude`), gzips it and `PUT`s it to
   `/api/runs/{id}/sync?dest=&name=`. The daemon unpacks it into a staging directory
   under `<root>/uploads/<runId>/<name>` and runs the existing `Manager.Sync` from there,
   so rsync's incremental copy into the guest, `dest` rules and the recorded step are
   unchanged. The staging directory goes when the machine is destroyed. The agent sees
   the same tools with the same meaning, local or remote.
4. **The Companion reads `~/.greenroom/client.json`** (after `GREENROOM_URL` and
   `GREENROOM_TOKEN` in the environment, before the loopback default) and sends the
   token on every request.
5. **The daemon serves its own client installer.** `GET /install.sh` and `GET /dl/{file}`
   serve files from `<root>/dist/` without a token; nothing else is public. `install.sh`
   takes the token as its argument, checks for Apple silicon and macOS 15+, downloads the
   Companion zip and the `greenroom` binary, checks their SHA-256 sums, writes
   `client.json` (mode 600), installs the app and the binary, and registers
   `greenroom connect` with Claude Code at user scope when `claude` is installed. Files
   fetched by `curl` carry no quarantine attribute, so the ad-hoc-signed app opens
   without a Gatekeeper prompt, and no Apple Developer ID is needed for a test.

## Consequences

- The daemon is still single-tenant: whoever holds the token can drive every machine on
  the host and read every run. One token is right for a two-Mac test; per-client tokens,
  revocation and a pairing flow wait for a real hosted service.
- The tunnel is the security boundary for transport (TLS ends at Cloudflare, which sees
  the traffic). The token is the boundary for access.
- A run's screenshots reach the remote agent inline, as today. Paths the daemon returns
  (the lossless PNG, the run directory) name files on the daemon's host, which the remote
  agent cannot open. The Companion reads everything through the API and is unaffected.
- Nothing here makes the daemon a hosted service. ADR 0010 (Tart's licence) and Apple's
  macOS licence (docs/04-landscape.md) still stand between this and a fleet sold to
  others; a test between two people is neither.
- Amends ADR 0002's "local first, no hosting" boundary for this one path. Supersedes the
  loopback-only rule of `api.LocalOnly` (ADR 0007) for a configured public host.
