# Image experiment: variant A (`greenroom-lean-a`) against the base image

Measured 2026-09-23 on one Apple silicon host (macOS 27.0, tart 2.37.0), guest macOS
26.6.2 (25G83), with `spikes/image-bench/bench.py` on a daemon built from this repo
(commits `f065ada` for `greenroom-base`, `55be80b` for the other two). Three fresh
machines per image, one at a time. Raw data: [`raw/`](raw/). Decisions: [`decision-log.md`](decision-log.md).

## Images

| Image | Built by | Allocated size | What it is |
| --- | --- | --- | --- |
| `greenroom-base` | `build-image.sh`, 2026-09-20 | 31.2 GB | The image the local daemon serves. Stale: input helper v2, older provisioning. |
| `greenroom-base-v5` | `build-image.sh` in the VM suite CI, 2026-09-23 | 32.6 GB | Same script and helper (v5) as A, without `-lean`. The like-for-like control. |
| `greenroom-lean-a` | `build-image.sh -lean -name greenroom-lean-a` | 32.6 GB | Variant A. |

All three are local clones of `ghcr.io/cirruslabs/macos-tahoe-base`, so their blocks are
shared: A adds no measurable size (0.03 GB over base-v5) and costs nothing to clone.

## Results

Median of three runs, each run in brackets. Change is A against the first column.

### A against the like-for-like control (`greenroom-base-v5`)

| Metric | greenroom-base-v5 | greenroom-lean-a | Change |
| --- | --- | --- | --- |
| Boot to ready (s) | **26.4** (25.0, 26.4, 27.9) | **20.5** (42.5\*, 20.5, 20.5) | -22% |
| Idle CPU busy, 10 min mean (%) | **15.0** (16.0, 13.8, 15.0) | **16.9** (49.4\*, 16.9, 12.8) | no difference |
| Idle CPU busy, last 5 min (%) | **14.7** (18.1, 14.2, 14.7) | **17.2** (55.4\*, 17.2, 12.0) | no difference |
| Idle memory used, last 5 min (MB) | **7940** (7940, 7887, 7987) | **7750** (7672, 7840, 7750) | -2% |
| Idle processes | **587** (573, 587, 592) | **483** (483, 479, 484) | -18% |
| launchd jobs, user domain | **497** | **420** | -16% |
| launchd jobs running, user domain | **227** (227, 222, 228) | **163** (163, 163, 167) | -28% |
| launchd jobs, system domain | 415 | 415 | 0 |
| Task wall time (s), clean runs only | 406.6 (1 run, 1 outage) | 251.9 (1 run) | inconclusive |
| Task verifier steps, clean runs only | 11 | 11 | 0 |
| Task misclicks | 0, 0, 0 | 0, 0, 0 | 0 |
| Verdict (correct is fail) | fail, none (model), none (model) | none (model), fail, fail | every verdict correct |
| First `machine_ui` (s) | **0.17** | **0.18** | 0 |
| `machine_screenshot` median (s) | **0.165** (0.168, 0.163, 0.165) | **0.243** (0.247, 0.243, 0.171) | noise, see below |
| `machine_ui` median (s) | **0.082** (0.082, 0.085, 0.080) | **0.198** (0.198, 0.233, 0.085) | noise, see below |
| Popups, banners, notifications seen | 0, 0, 0 | 0, 0, 0 | 0 |
| Desktop widgets on screen | 3, 3, 3 | 0, 0, 0 | gone |
| Survives `sudo reboot`, `swiftc` works | yes, yes, yes | yes, yes, yes | same |
| Reboot: guest agent back (s) | **30.8** | **30.5** | 0 |

### A against the image the daemon serves today (`greenroom-base`)

| Metric | greenroom-base | greenroom-lean-a | Change |
| --- | --- | --- | --- |
| Boot to ready (s) | **37.9** (37.9, 29.2, 48.4\*) | **20.5** | -46% |
| Idle CPU busy, 10 min mean (%) | **25.7** (25.7\*, 24.9, 49.2\*) | **16.9** | -34% |
| Idle processes | **588** | **483** | -18% |
| launchd jobs running, user domain | **223** | **163** | -27% |
| Task wall time (s), clean runs only | 231.4 (1 run) | 251.9 (1 run) | inconclusive |
| Task verifier steps | 11, 11, 12 | 11 (clean run) | 0 |
| First `machine_ui` (s) | **14.79** | **0.18** | helper already baked |
| Popups seen | 0, 0, 0 | 0, 0, 0 | 0 |
| Desktop widgets on screen | 3, 3, 3 | 0, 0, 0 | gone |
| Reboot: guest agent back (s) | **58.8** (37.2, 58.8, 71.6) | **30.5** | -48% |
| Survives reboot, `swiftc` works | yes, yes, yes | yes, yes, yes | same |

Most of this second table is the stale image, not the lean profile: `greenroom-base`
predates helper v5 (its first UI read compiles the helper for about 15 s) and the current
provisioning. Read the first table for what `-lean` itself buys.

\* The run shared the host with another VM. `greenroom-base` run 3 idled while PR #38's VM
suite (the self-hosted runner is this host) booted its own machine, and run 1 overlapped
PR #37's suite. `greenroom-lean-a` run 1 overlapped the no-mistakes pipeline's real-guest
build of the lean image. Each shows 2 to 4 times the CPU of its sibling runs. They are
kept in the medians and the raw data, not dropped.

The verifier model (NVIDIA NIM) was down for long stretches: timeouts, 404, 429 and 500.
The bench resends the task after the verifier gives up (up to 3 times, 120 s apart,
recorded under `task.modelOutages`); "none (model)" is a run with no verdict after that.
Of nine tasks only three got a verdict without resends (`greenroom-base` run 1,
`greenroom-lean-a` run 3, and `greenroom-base-v5` run 1 after one slow turn), so task time
is compared on those alone and is inconclusive: model latency dominates it and the image
does not show through. Steps, misclicks and verdicts agree wherever there is a verdict:
11 steps, no misclicks, the correct fail every time. A verdict that came after resends
counts only the last send's steps (2 and 4), which is why the table uses clean runs.

Latency: A's run 3 (0.171 s screenshot, 0.085 s UI read) matches base-v5; its runs 1 and 2
were slower on both calls. With three samples and host noise that is not a difference
either way.

## What the screen looks like

- [`screenshots/lean-a-after-idle.jpg`](screenshots/lean-a-after-idle.jpg): A after 10 minutes idle.
  Seven apps in the Dock, no widgets.
- [`screenshots/base-v5-after-idle.jpg`](screenshots/base-v5-after-idle.jpg): the control after 10
  minutes idle. Full Dock; calendar, weather and Photos widgets behind the window.
- [`screenshots/base-desktop-with-control-dialog.jpg`](screenshots/base-desktop-with-control-dialog.jpg):
  the positive control for the popup detector (a dialog and a notification raised on
  purpose). The watcher saw the dialog as `osascript` and the widgets as Notification
  Center windows, so "0 popups" above means none appeared, not that none could be seen.
- [`screenshots/lean-a-desktop.jpg`](screenshots/lean-a-desktop.jpg): A after a reboot.

Both images restore a Terminal window over the desktop at every login (decision log,
follow-ups).

## Verified settings in A

`guest/lean.sh` reads every setting back and fails the build naming the check. Verified on
a real guest twice: the build of `greenroom-lean-a`, and a fresh build of the final
reviewed script (`leanx-verify`, `lean: ok`, then deleted). On a scratch clone, after a reboot none of the
disabled user agents was running and the Dock kept its seven apps. Software Update's
automatic check is the one setting that does not hold: macOS 26's softwareupdated ignores
`--schedule off` and deletes `AutomaticCheckEnabled` within seconds, so it is written best
effort and not read back (downloads, installs and App Store updates are off and read back;
the update notification agent is disabled).

## Reproduce

```sh
export TART_NO_AUTO_PRUNE=1
apps/daemon/scripts/build-image.sh -lean -name greenroom-lean-a
spikes/image-bench/bench.py -image greenroom-base-v5 -out docs/image-experiment/raw/greenroom-base-v5.json
spikes/image-bench/bench.py -image greenroom-lean-a  -out docs/image-experiment/raw/greenroom-lean-a.json
spikes/image-bench/table.py docs/image-experiment/raw/greenroom-base-v5.json docs/image-experiment/raw/greenroom-lean-a.json
```

`raw/*.pass1.json` is a first pass in which the verifier model failed three of six tasks
before the bench could resend. It is kept as the evidence for that change, not used above.
