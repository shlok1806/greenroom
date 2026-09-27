# 0001. machine_sync can mirror, and always counts strays

Date: 2026-09-27
Status: accepted.

## Context

Issue #188. `machine_sync` is rsync `-a` from a host directory into the guest: new and
changed files move, nothing is ever deleted. An agent that reuses one machine across
branches by re-syncing each worktree into the same `dest` keeps every file the earlier
branch had and this one does not. A stray `.go` file is compiled and tested with the next
branch, so a guest `go test` can pass or fail on code that is not in the change. In the
dogfooding runs that hit it the agent found the strays only by diffing host and guest file
lists by hand, about three calls per issue.

Two fixes were proposed: a `fresh: true` that empties `dest` first, or a mirror that deletes
what the source does not have (rsync `--delete`). Emptying loses the incremental copy and
every build cache in `dest` (`.build`, `node_modules`, DerivedData), which is the reason to
reuse a machine at all. rsync `--delete` keeps both.

## Decision

1. **`mirror: true`** on `machine_sync` adds rsync `--delete`: after the sync, `dest` holds
   what `source` holds, and nothing else. The default stays `false`: a sync never deletes
   unless asked.
2. **Excluded paths are never deleted.** rsync protects a receiver path that matches an
   `exclude` pattern from `--delete` (we never pass `--delete-excluded`). So
   `exclude: [".build", "node_modules"]` keeps the guest's own build caches through a
   mirror, and an excluded path is not a stray. To drop a cache, run `rm -rf` in the guest
   with `machine_exec`.
3. **Every sync reports strays**: `strays` is the number of paths (files, directories,
   symlinks) in `dest` that the source does not have and no exclude covers, and
   `strayPaths` lists the first 20, relative to `dest`, directories with a trailing `/`.
   With `mirror` they are what was deleted, taken from rsync's own `deleting` lines. Without
   it a second rsync pass, `--dry-run --delete` with the same excludes, lists them, so the
   count uses rsync's own exclude semantics rather than a copy of them. The summary says the
   count and how to remove them. If that dry run fails the sync still succeeds, `strays` is
   left out and the summary says the count is unknown.
4. **A mirror deletes only inside `dest`, and only a `dest` it is safe to empty.** On top of
   the `dest` rules every sync has (relative to the home, not the home itself, no `..`), a
   mirror refuses:
   - a `dest` with fewer than two components (`work`, `myapp`): a top-level directory of the
     guest home is too likely to hold something that is not the project. The default,
     `work/<basename>`, has two.
   - a `dest` under `Library` or under a hidden top-level directory (`.ssh`, `.config`):
     the guest's own state, including the key the daemon reaches it with.
   - a `dest` any component of which is a symlink in the guest. rsync's receiver follows a
     symlinked destination directory, so `--delete` would empty wherever it points. The
     guest checks this (`syncGuardScript`: the physical path of `dest` must be the home's
     physical path plus `dest`) before rsync runs, with `dest` as an argument, never shell
     text.
   Inside `dest`, rsync `-a` deletes a symlink as a link and never follows it.
5. **Through `greenroom connect`** (ADR 0021) the source is unpacked into a staging
   directory on the daemon's host with excluded paths already left out. connect now sends
   `mirror` and every `exclude` pattern to the upload route
   (`PUT /api/runs/{id}/sync?mirror=true&exclude=...`), which hands them to the same
   `Manager.Sync`, so the guest's excluded paths are protected and counted the same way as
   on a local sync. A connect older than this drops `mirror`; the result's `mirror: false`
   and its stray count say so.

## Consequences

- An agent that re-syncs a different worktree into a reused machine passes `mirror: true`
  and gets exactly that tree, with its build caches intact when it excludes them.
- A plain sync costs one more rsync pass (a file list, no data) to count strays.
- `Manager.Sync` takes a `SyncOptions` (dest, exclude, mirror) instead of positional
  arguments.
