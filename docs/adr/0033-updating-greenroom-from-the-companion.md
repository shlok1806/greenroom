# 0033. Updating Greenroom from the Companion

Date: 2026-09-27
Status: accepted.

## Context

Greenroom is installed from source: `apps/daemon/scripts/install.sh` builds the daemon and
(re)loads its launchd job, and `apps/companion/scripts/install.sh` bundles the app, replaces
`/Applications/Greenroom Companion.app` and opens it. Every change on `main` needs someone to
pull and run both scripts by hand, in the right order. On 2026-09-26 the daemon ran for more
than a day on a build from before six verifier changes, and nobody could tell from the app
which build was running. The maintainer asked for a way to update from the app.

There are no signed releases yet, so an update means building the latest `main` locally.

## Decision

1. **Builds carry their identity.** Both install scripts stamp the commit (short sha, and
   whether the tree was dirty) and the build time into what they build: the daemon through
   `-ldflags -X`, the app through its Info.plist. `greenroom version` prints it.
2. **The daemon reports it,** read only: `GET /api/version` returns its commit, build time,
   input helper version, image recipe, the brain and vision models in use, and the path of the
   repository checkout it was built from (recorded at install). Behind `api.Guard` like every
   route. There is **no update route on the daemon**: nothing reachable over the network,
   including the tunnel, can make the host build or run code.
3. **`scripts/update.sh`** at the repo root does the update:
   - refuses when the checkout has local changes, is not on `main`, or cannot fast-forward
     (it never merges, rebases or discards);
   - `git fetch` and `git merge --ff-only origin/main`;
   - runs the daemon install, then the Companion install (which reopens the app);
   - prints one line per step, and `--check` only reports how far `main` is ahead
     (commit count and subjects) without changing anything.
4. **The Companion shows it and drives it.**
   - A "Greenroom" section (in the command palette and the "More" menu, per companion ADR
     0013) shows the app's build, the daemon's build, and whether they match `main`
     ("Up to date", or "N updates available" with the subjects from `update.sh --check`).
   - A mismatch between the app's and the daemon's commit is shown as such.
   - "Update" runs `update.sh` from the recorded checkout as a local process, streams its lines
     in a sheet, and the Companion install relaunches the app at the end.
   - Before updating, if any run has a verifier turn in progress, it says so and asks; a
     restart cuts such a turn off (#163).
   - It checks for updates when opened and every few hours, never updating on its own.
5. **Failure is visible and safe.** If a step fails, the sheet shows the failing step and its
   output, and the running daemon and app stay as they were: the daemon install replaces the
   binary only after a successful build, and the app install only after a successful bundle.

## Consequences

- One click keeps the whole install on `main`, and the app always says which build is
  running, which would have caught the stale daemon and the stale describer (#154).
- It only works for source installs with a local checkout. Distributing signed builds (and a
  Sparkle-style updater) is a later decision, when there are releases.
- An update keeps how greenroom was installed: the daemon's install reuses the verifier, image,
  env file, tart and extra environment of the launchd job it replaces unless the environment
  sets them.
- The update runs whatever is on `main`; `main` is protected by CI and review, and the app
  shows the incoming commit subjects before the user presses Update.
