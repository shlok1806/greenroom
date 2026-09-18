# Test plan and improvement plan

Status: written 2026-09-17, in ASD-STE100 Simplified Technical English. This document covers the
daemon as it exists at commit `6e4951f` plus the corrections below. It has four parts. Part 1 is
what the tests covered before this work. Part 2 is the results of a black-box sweep against a live
daemon. Part 3 is the plan for full coverage. Part 4 is the list of defects, and it records which
ones are corrected.

All four defects D1 to D4 are corrected. Each correction has a test that failed first. The five
tickets are issues 1 to 5 on `shlok1806/greenroom`. Part 4 marks the rest.

## 1. What the tests cover today

The daemon has 1,015 lines of production Go code in five files.

| File | Lines | Tests |
| ---- | ----- | ----- |
| `internal/machine/manager.go` | 495 | none |
| `internal/mcpserver/server.go` | 182 | none |
| `internal/tart/tart.go` | 147 | none |
| `main.go` | 98 | none |
| `internal/machine/run.go` | 93 | none |

`go test ./... -cover` reports **0.0% of statements** for all four packages. One test file exists,
`e2e_test.go`. It carries the build tag `tart`, so the normal test command never runs it. It needs
a real machine, about three minutes, and 33 GB of disk.

This means two things. Every line of logic is unverified except through manual use. Also, `pnpm test`
and `go test ./...` both pass and both prove nothing, which is worse than a red test.

## 2. Black-box results against a live daemon

I ran 28 tests against the running daemon over HTTP, in the same way that an agent calls it. The
daemon had one machine, and a second VM ran outside greenroom.

### What works correctly

| Test | Result |
| ---- | ------ |
| All five tools with an unknown runId | clean tool error, `no machine for run "..."` |
| `machine_exec` with no command | schema validation rejects it before the handler |
| `machine_create` with a bad image | tart authentication error, reported in full |
| `machine_exec` immediately after create | the booting guard answers after 20.0 s with the correct instruction |
| `machine_exec` with a non-zero exit | `exitCode: 7`, and **not** a tool error, per the invariant |
| `machine_exec` with stderr only | `exitCode: 2`, stderr captured, stdout empty |
| `machine_exec` with a bad cwd | `exitCode: 1`, the guest shell error in stderr |
| `machine_exec` with a 1 s timeout on a 5 s command | tool error, context deadline exceeded |
| The command after a timeout | does not survive in the guest, so there is no orphan process |
| `machine_sync` with a missing source, a file, or a relative path | all three rejected |
| `machine_sync` exclude patterns | correct, `node_modules` did not arrive |
| `machine_list` | correct before, during and after a machine |
| Daemon restart with a machine running | `loadState` reattached it, and `machine_exec` still worked |
| 6 concurrent tool calls | 18 steps, 18 unique sequence numbers, no collision |
| A failed create | recorded in its own run directory, and `state.json` stays clean |

### What is broken

Three defects appeared, and one of them is serious.

**D1. `machine_create` reports success when the machine never starts.** Two VMs already ran, and
Apple permits only two for each host. Tart exited at once and wrote the reason to `vm.log`:

```
The number of VMs exceeds the system limit (other running VMs: greenroom-..., gr-live)
```

The daemon returned a normal create result with status `booting`. `tart.Start` only checks that the
subprocess starts. Nothing reads the exit code, and nothing reads `vm.log`. The caller therefore
polls `machine_wait` for the full `readyTimeout` of three minutes. The error that the caller finally
gets is `context deadline exceeded`, which names neither the VM limit nor any other cause. The
machine also stays in `machine_list` with status `failed` after `cleanupVM` deletes the VM.

This is the most probable failure for this product. One agent for each machine is the premise, and
the host limit is two.

**D2. `machine_sync` can fail immediately after the machine is `ready`.** From the run earlier
today:

```
rsync: exit status 255: ssh: connect to host 192.168.64.2 port 22: No route to host
```

`machine_wait` had already reported `ready` with an IP address. Readiness waits for the guest agent
over vsock and installs the ssh key through the same channel. It never tests port 22. A retry a few
seconds later succeeded. Every rsync tool inherits this race.

**D3. `dest` escapes the guest home directory.** The schema says that `dest` is "relative to the
admin home". A sync with `dest` of `../../../tmp/escaped` returned success and wrote the files to
`/tmp/escaped` in the guest. Nothing rejects `..` and nothing rejects an absolute path.

## 2b. What the new tests found and proved

I built layers 0 to 3 of the plan below on 2026-09-17. The suite is now 72 tests, and
`go test ./... -race` passes.

| Measure | Before | After |
| ------- | ------ | ----- |
| Statement coverage, whole daemon | 0.0% | **82.8%** |
| Tests that run without a VM | 0 | **85** |
| `go test -race` | never run | clean |
| Defects known | 0 | 4 found, 4 corrected |

Two results are worth naming.

**The tests found a defect that the black-box sweep could not reach (D4).** Six screenshots at the
same time on one machine shared a file name and overwrote each other, and the race detector fired
on the unlocked read of `manifest.Steps` in `Screenshot`. Concurrent screenshots therefore lose
evidence, which is the one thing the run directory exists to hold. The test
`TestConcurrentScreenshotsGetDistinctFiles` proves this. It carries a skip today, and the skip
comes off with the correction.

**One test failure was my own error, not the code.** I expected `rsyncSummary` to keep the
"Number of created files" line. It does not, because that line does not match any of the three
prefixes. The code is correct.

A third result is a property to know rather than a defect. `machine_wait` returns `failed` before
`cleanupVM` finishes, so a caller that sees `failed` must not assume that the VM is already gone.

## 3. The plan

### Layer 0: make the code testable (prerequisite) - DONE

`Manager` calls `tart.New()` inside `NewManager`, so no test can substitute anything for the real
`tart` binary. `tart.Client` already has a `Bin` field, but `Manager` gives no way to set it.

`WithTartBin` is now an option on `NewManager`. `internal/testsupport` holds the fake tart, a shell
script that answers every subcommand and records each argument list. Control files turn on each
failure: `fail-clone`, `fail-run`, `fail-ip`, `fail-exec`, `fail-keyinstall`, `fail-stop`,
`fail-delete`, `exec-exit-<n>`, `agent-down` and `list-empty`. `Sync` and the rsync path need no
option, because a fake `rsync` earlier on PATH does the same job.

### Layer 1: pure unit tests, no machine - DONE

These need no fake and no VM. They are fast and they run on every commit.

1. `shellQuote` with quotes, spaces, newlines and an empty string.
2. `rsyncSummary` with real rsync output, with empty output, and with unexpected output.
3. `truncatedForLog` at, below and above the 64 KB limit.
4. `newRunID` for format and for uniqueness across many calls.
5. `recorder`: manifest written, steps appended as one JSON object for each line, `artifactPath`
   format, and correct step numbers from many goroutines.
6. `toJPEG` with a valid PNG, with random bytes, and with an empty slice.
7. `round` and the JSON tags of `Machine`, `Step` and `Manifest`.

### Layer 2: the manager against a fake tart - MOSTLY DONE

The fake tart makes each failure reachable. Test each of these:

1. `Create`: success, a clone failure, and a start failure. Check that a clone failure leaves no
   entry in the map, and that a start failure calls stop and delete.
2. `finishBoot`: the ready path, a guest agent that never answers, a failed IP lookup, and a failed
   ssh key installation. Check the status, the error text and `bootSeconds`.
3. `waitReady`: retry until success, and the deadline.
4. `Wait`: an unknown runId, a timeout while the machine boots, and an immediate return when ready.
5. `awaitReady`: the 20 second grace path and the failed-machine path.
6. `Exec`: the cwd prefix and its quoting, a non-zero exit is not an error, a tart failure is an
   error, and the timeout.
7. `Sync`: the full rsync argument list, the exclude patterns, the default dest, and each input that
   must be rejected.
8. `Screenshot`: a successful decode, a non-zero `screencapture`, and base64 that does not decode.
9. `Destroy`: success, a stop failure followed by a successful delete, and a second destroy.
10. `loadState`: a machine that still runs, a machine that no longer runs, a corrupt `state.json`,
    and a run directory that cannot be opened.

### Layer 3: the seven tools over MCP - DONE

Use an in-memory MCP client against `httptest` with the fake tart. This is where the seven tools get
covered without a machine. For each tool test the success path, the error path and the defaults.

| Tool | What the test must prove |
| ---- | ------------------------ |
| `machine_create` | the default image applies, a named image passes through, a tart error becomes a tool error |
| `machine_wait` | the default of 45 s, the cap of 50 s, and each of the three statuses |
| `machine_list` | empty, one machine, and no leak of the private fields |
| `machine_sync` | required fields, the default dest, rounding of `seconds`, and rejection of bad input |
| `machine_exec` | the 600 s default timeout, stdout, stderr, the exit code, and rounding |
| `machine_screenshot` | the result holds both a JPEG and the text metadata, and the PNG path exists |
| `machine_destroy` | `ok: true`, and a second call is an error |

Also test the protocol itself: the tool list holds exactly seven tools, each schema matches the
input struct, and every tool error arrives as `isError` with readable text.

### Layer 4: the real machine

Keep `e2e_test.go` behind the `tart` tag and add the cases that only a real machine can prove: the
booting guard, sync with excludes, reattachment after the manager restarts, a screenshot that
decodes as a real image, and the VM limit error after D1 is corrected.

### Layer 5: races and soak

Run layers 1 to 3 with `go test -race`. Then add a soak test that creates and destroys ten machines
in sequence, and checks that `state.json`, the run directories and `tart list` all return to their
starting state.

### What layers 2 and 3 still need

Layer 2 does not yet cover `waitReady` against a guest agent that never answers, or `loadState`
against a run directory that cannot be opened. Both need the three minute `readyTimeout` to be
configurable, which is a small option in the same style as `WithTartBin`.

`main.go` stays at 0%. `serve` and `main` need either a subprocess test or a split between flag
parsing and the server. The flag parsing is worth a test. The rest is wiring.

### Coverage target

90% of statements for `machine` and `mcpserver`, and 80% for `tart`. The lines that only a
subprocess can reach stay with the layer 4 test. Add the number to `pnpm test` so that a fall in
coverage fails the build.

## 4. Improvement plan, in priority order

### P1, done

1. **Detect a machine that does not start (D1). DONE, issue #1.** `tart.Start` now returns a
   `Process`. `waitReady` watches it and returns the tail of `vm.log`, so the caller gets tart's
   own message. The failure now takes 0.1 s in the test instead of three minutes. The test also
   showed something worse than the original report: with the old code the machine reached `ready`
   in 20 ms although the VM was dead.
2. **Count the machines before a create (D1). DONE, issue #2.** `checkHostCapacity` counts running
   VMs before anything is cloned, and the error names the machines that hold the slots. The limit
   is `-max-machines`, and the default is two.
3. **Test for ssh before a machine is `ready` (D2). DONE, issue #3.** `waitSSH` dials guest port 22
   until it answers. Readiness therefore means usable. The retry inside `Sync` is no longer needed,
   so it was not added. Evidence from the real machine: `bootSeconds` moved from 32 s to 43.9 s,
   and the first `machine_sync` after `ready` took 0.2 s with no failure. The extra 12 s is the
   wait for sshd that used to be a coin toss.

### The review found more

A code review of the corrections found six things. Four mattered.

**The capacity check was a check and then an act.** Four creates that arrived together all passed
a limit with room for one, so the guard did nothing under the concurrency the product is built
for. `Create` now holds `createMu` for its whole length, and a slot counts as held either by a VM
that tart reports running or by one of our own machines that is still booting.
`TestConcurrentCreatesRespectTheHostLimit` failed first with "4 of 4 concurrent creates
succeeded".

**The ssh phase shared the boot budget.** Per-phase timings on a real machine showed the guest
agent taking 112.6 s of the 180 s budget while ssh took 0.0 s. A slower agent would have starved
the ssh phase and produced an error that blamed ssh. Each waiting phase now has its own budget.

**`internal/tart` had no tests.** The `Process` type is the whole mechanism of the first
correction and it was covered only by proxy. It now has six tests of its own, including the log
tail, a silent exit, and concurrent polling under the race detector.

**The capacity error told the caller to do something it could not do.** `machine_destroy` only
accepts a runId the daemon tracks, so naming a foreign VM was useless advice. The message now
names our own machines by runId and says plainly when a slot is held by a VM started outside
greenroom.

The other two were a stale comment of mine that described a race the fix had already removed, and
a fake that emitted an empty VM name where real tart never would. Both are corrected. Making the
fake realistic immediately broke one of my own tests, which had quietly assumed an empty host.

While correcting the first three defects, one more fault appeared. Both wait loops returned a bare
`context deadline exceeded` when the budget ran out. That is the same fault as D1: a message that
names no cause. Both now say what did not answer, the guest agent or ssh, and on which address.

### P2, robustness

4. **`Destroy` removes the machine from the map before it stops the VM.** If stop or delete fails,
   the VM keeps running and no tool can reach it. Remove the entry after the VM is gone.
5. **A failed machine stays in the map and in `state.json`.** `cleanupVM` deletes the VM, but
   `machine_list` still advertises the machine. Decide the contract and apply it.
6. **One bad run directory stops the daemon from starting.** `loadState` returns the error from
   `newRecorder`. Log the machine and continue instead.
7. **`recorder.step` ignores write errors.** The run directory is the evidence of the product. A
   silent loss of evidence is not acceptable. Report the error and count the failures.
8. **No preflight at startup.** Test for `tart` on the PATH, and for the image, when the daemon
   starts. Today a missing binary appears only at the first create.

### P3, contract and hygiene

9. **Confine `dest` (D3). DONE, issue #5.** `guestDest` refuses an absolute `dest` and any `dest`
   that climbs above the guest home.
10. **Require an absolute `source`. DONE, issue #5.** `Sync` refuses a relative `source` and says
    why.
11. **Reserve the screenshot sequence number inside the recorder (D4). DONE, issue #4.** `step` is
    now `begin` plus `complete`. `begin` claims the number under the recorder lock, so a caller
    that must name a file claims its number first. An artifact number is still a step number. The
    race detector is clean and `TestConcurrentScreenshotsGetDistinctFiles` no longer carries a
    skip.
12. **Give each screenshot its own guest path.** `/tmp/greenroom-shot.png` is shared today.
13. **`machine_create` blocks for a `tart pull`.** An absent image makes create take minutes, which
    breaks the documented promise that create returns at once. Pull in the background, or refuse
    with a clear message.
14. **The HTTP endpoint has no authentication.** Anything on loopback can create and drive machines.
    Accept this and write it down, or add a token.

## 5. Order of work

1. Layer 0, the test seam. One option on `NewManager`.
2. Layer 1 and Layer 3. These give the largest coverage for the least code, and Layer 3 covers the
   seven tools that the product sells.
3. P1 defects, each with the test that proves the correction.
4. Layer 2, which is the largest body of tests.
5. P2 and P3 defects.
6. Layer 4 and Layer 5, then put the coverage number in the build.
