# Image bench

`bench.py` measures one VM image the way an agent uses it, so image variants can be
compared on data (`docs/image-experiment/`). Throwaway: nothing imports it. Python 3
standard library only.

```sh
export TART_NO_AUTO_PRUNE=1   # a clone must not evict the cached base image
df -h ~                       # a run needs a few GB; use df, not du (du counts APFS clones)
spikes/image-bench/bench.py -image greenroom-lean-a \
  -out docs/image-experiment/raw/greenroom-lean-a.json
```

It builds the daemon from this checkout into `/tmp/greenroom-bench-<image>/grx` and
serves it on `127.0.0.1:7861` with its own `-root`, `-verifier nim` and
`-open-viewer=false`. It never talks to another daemon, never touches a VM it did not
create, and runs its machines one at a time: it waits while the host is at Apple's two-VM
limit and retries a create the limit refused. Flags: `-runs` (3), `-idle-seconds` (600),
`-sample-every` (30), `-latency-reps` (5), `-task-timeout` (900), `-addr`, `-scratch`,
`-env-file`, `-tipsplit`, `-tart`.

## What one run measures

Everything goes through MCP over HTTP (`initialize`, `notifications/initialized`,
`tools/call`), except the idle samples and the window watcher, which use `tart exec`
directly so they add no steps and no activity to the run.

| Phase | Recorded |
| --- | --- |
| Boot | `bootToReadySeconds`: `machine_create` sent until `machine_wait` says ready. The `machine_boot` step's own phases too. |
| Idle | Every 30 s for 10 min: whole-guest CPU busy % (`top`'s second sample), memory used and wired, process count, launchd jobs (user domain, loaded and running; system domain), load. `SettledMean` is the second half. |
| Task | TipSplit (`main.swift` with `var perPerson: Double { tip / Double(people) }`) built with `swiftc -parse-as-library -O`, launched, then the demo task sent to the verifier with `agent_send`. Wall time to the verdict, the verifier's steps, clicks and misclicks, questions, the verdict and whether it is the correct `fail`. |
| Latency | 5 `machine_screenshot` and 5 `machine_ui` calls, client wall time. The first `machine_ui` is timed on its own (`firstUiSeconds`): an image without the current input helper compiles it there. It runs before the task, so the compile never lands in the verifier's time. |
| Stability | `sudo reboot` through `machine_exec`, time until the guest agent answers with a new boot time, then `swiftc` builds and runs a hello world, a screenshot and a UI read work. |
| Popups | Every 10 s from ready to reboot, the guest's on-screen windows (`CGWindowListCopyWindowInfo` through JXA). A window whose owner is not the desktop, the menu bar, the Dock, Finder, Terminal or TipSplit is a popup. The verifier's own text is also searched for words like notification, alert, sign in. |

A misclick is a verifier click whose point is inside no element (containers aside) of the
verifier's latest `machine_ui` tree.
