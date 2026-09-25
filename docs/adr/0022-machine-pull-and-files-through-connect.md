# 0022. machine_pull, and files an agent is handed reach its own computer

Date: 2026-09-25
Status: accepted

## Context

Issue #123. `machine_sync` only goes host to guest. An agent that built and checked a
feature could not get its screenshots or a built `.app` out for a pull request; the
workaround was `machine_exec` with `base64` in chunks, capped at 8 KiB head and 24 KiB
tail per stream. Through `greenroom connect` it was worse: ADR 0021 accepted that the
paths the daemon returns (the lossless PNG of `machine_screenshot`) name files on the
daemon's host, which a remote agent cannot open.

## Decision

1. **`machine_pull {runId, source, dest, exclude}`** copies a guest file or directory to
   the host. `source` is a guest path, relative to the home, `~/x` or absolute (unlike a
   sync `dest`, it may be anywhere: `/tmp` logs, app bundles). A directory's contents land
   in `dest`; a file lands in `dest` under its own name, and a symlink to a file is copied
   as the file. `dest` is an absolute host directory. Without it the copy goes to
   `runs/<runId>/NNN-pull/`, named by the step's number, so a pull is also evidence. It is
   `Manager.Pull`: the same rsync over ssh as `machine_sync`, reversed, and one
   `machine_pull` step.
2. **Through connect, `machine_pull` is answered on the agent's computer**, like
   `machine_sync`. The daemon's `GET /api/runs/{id}/pull?src=&exclude=` streams a gzipped
   tar the guest makes (`tar` under `tart exec`, stdout streamed, never held) and records
   the step; its number is the `Greenroom-Step` header, and a tar failure after the
   archive has begun is the `Greenroom-Error` trailer. connect unpacks it as it arrives
   with the rules the upload route already applies (`internal/tarball`: no `..`, no
   absolute names, no symlink that leaves the directory, nothing written through a link),
   because a guest decides what is in the archive. `dest` is then a directory on the
   agent's computer; without it, `~/.greenroom/connect/runs/<runId>/NNN-pull` (`-dir`
   changes the root).
3. **A pull from the public host never names a host path.** Before this, a token holder
   could run code only in guests: `machine_exec` runs there, and every host write the
   daemon made went under its own root. A `dest` would let them write any file of the host
   user (`~/.zshrc`, `~/Library/LaunchAgents`, `~/.ssh/authorized_keys`), which is code
   execution on the host. So `api.Guard` marks a request it admitted through the public
   host (`api.FromPublicHost`), `routes` answers such a request with a second MCP server
   built with `mcpserver.ForPublicHost()`, and that server's `machine_pull` refuses any
   `dest` ("through the tunnel, dest is chosen by greenroom connect on your computer; omit
   dest") and always copies into the run directory. A loopback caller is on the host and
   keeps `dest`. The pull route writes nothing on the daemon's host at all: it streams.
4. **connect fetches `machine_screenshot`'s PNG** through the existing
   `GET /api/runs/{id}/artifacts/{name}` into `~/.greenroom/connect/runs/<runId>/` and
   rewrites `path` in the result to it. The inline image is unchanged. A failed download
   keeps the daemon's path and says so; the picture still arrived.

## Consequences

- Every path a remote agent is handed by these tools opens on its own computer. This
  replaces ADR 0021's consequence that returned paths name the daemon's host, for the
  screenshot path and pulls; the run directory in the server's instructions still names
  the host.
- `exclude` means rsync's simple forms on both paths. Through connect, the guest's tar
  prunes only literal names (`node_modules`, `.git`), where tar agrees with rsync, and
  connect applies every pattern while unpacking; so a pattern tar would read differently
  costs bandwidth, never a wrong result.
- A pull through connect refuses an archive with hard links, devices, fifos, absolute
  symlinks or links that leave the tree, which a local rsync pull would copy as they are.
- Through the public host, `machine_pull` writes only under `runs/<runId>/`, like every
  other artifact. A new tool that writes on the host from a caller's path must be refused
  on the `ForPublicHost` server the same way. Reading host paths is still open to a token
  holder (`machine_sync`'s `source` on the daemon's own MCP endpoint); per-client tokens
  (ADR 0021) would have to limit that.
- Re-running a pull is harmless to the guest and writes a new numbered directory (or the
  same files again), so it could be retried; connect answers it itself, so it is not in
  `readOnlyTools`, which governs forwarded calls only.
