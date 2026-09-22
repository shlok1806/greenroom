# Test plan (2026-09-17)

**Result:** the daemon went from 0% to 82.8% statement coverage with no VM needed, and
four defects were found and fixed (issues #1-#5). Eight items remain open (end of this
doc; 1 and 3-6 re-checked against the code on 2026-09-22). How to use the test seams today: `apps/daemon/CLAUDE.md`.

Written against commit `6e4951f`, when the daemon was ~1,000 lines and seven tools.

## Starting point

- `go test ./... -cover`: 0.0%. The only test was the `-tags tart` e2e test (3 min,
  33 GB disk), so `pnpm test` passed while proving nothing.
- A black-box sweep of 28 calls against a live daemon mostly passed: unknown runIds,
  schema validation, non-zero exits as data, timeouts, sync input checks, restart
  reattach, 6 concurrent calls with unique step numbers.

## Defects found and fixed

| ID | Defect | Fix | Issue |
| --- | --- | --- | --- |
| D1 | `machine_create` reported `booting` when tart had already exited (host VM limit); caller waited 3 min for "context deadline exceeded" | `tart.Start` returns a `Process`; `waitReady` fails at once with the tail of `vm.log`. `checkHostCapacity` refuses before cloning | #1, #2 |
| D2 | `machine_sync` right after `ready` failed with "No route to host" (sshd not up) | Ready now waits for sshd | #3 |
| D3 | `dest` could escape the guest home (`../../../tmp`) | `guestDest` refuses absolute or climbing paths; `source` must be absolute | #5 |
| D4 | Concurrent screenshots overwrote each other (shared step number) | Recorder `begin`/`complete` claims numbers under lock | #4 |

Review of the fixes found more, also fixed:

- Capacity check raced under concurrent creates. `Create` now holds `createMu`
  (`TestConcurrentCreatesRespectTheHostLimit`).
- Boot phases shared one budget; the agent phase used 112.6 of 180 s. Each phase now has
  its own.
- Timeouts said only "context deadline exceeded". They now name what did not answer.
- The capacity error named VMs the caller could not destroy. It now separates our runs
  from foreign VMs.

## What was built

- **Layer 0:** `WithTartBin` plus a fake `tart` script with failure control files.
- **Layer 1:** pure unit tests (`shellQuote`, `rsyncSummary`, recorder, `toJPEG`, ids).
- **Layer 2:** the manager against the fake tart: create, boot phases, wait, exec, sync,
  screenshot, destroy, `loadState`.
- **Layer 3:** every MCP tool through a real MCP client over `httptest`.
- **Layer 4:** real-VM tests behind `-tags tart`.
- **Layer 5:** `-race` everywhere; CI runs `-race -count=2`, and a manual 10x soak.

After: 72 tests, 82.8% coverage, `-race` clean. `main.go` stays untested (wiring).

## Still open

1. `Destroy` removes the machine from the map before stopping the VM; a failed stop leaves
   an unreachable VM.
2. Failed machines stay in the map and `state.json`. Decide the contract.
3. One unreadable run directory makes `loadState` fail and the daemon not start.
4. `recorder.complete` ignores write errors; evidence can be lost silently.
5. Every screenshot uses `/tmp/greenroom-shot.png` in the guest.
6. The HTTP endpoint has no authentication; anything on loopback can drive machines.
7. `machine_create` on an image not yet pulled blocks for the whole pull.
8. No coverage gate in `pnpm test`. Target was 90% for `machine` and `mcpserver`, 80% for
   `tart`.
