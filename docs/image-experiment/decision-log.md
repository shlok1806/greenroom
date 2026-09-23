# Image experiment: decision log

Every decision about greenroom's VM image variants, newest last, each with its evidence.
Results: [`report.md`](report.md). Raw numbers: [`raw/`](raw/). Harness:
[`spikes/image-bench/`](../../spikes/image-bench/).

## 2026-09-23: decisions by the user

1. Build lean variants of the image and decide between them by measured data, not by
   opinion.
2. Metrics, the same for every variant: boot-to-ready seconds and image size; 10 minutes
   of idle guest CPU, memory, process count and launchd job count; a verifier task on the
   TipSplit app (wall time, steps, misclicks, whether the verdict is the correct fail,
   any popup, banner or notification seen); screenshot and `machine_ui` latency;
   stability (survives `sudo reboot`, `swiftc` works).
3. Variant A (`greenroom-lean-a`) keeps every app on disk and hides the rest: same
   upstream base and provisioning as `greenroom-base`, the other apps out of the Dock and
   Launchpad with their user-level agents disabled, and widgets, notifications, Siri,
   Spotlight indexing, media analysis, iCloud and Apple Account prompts, Software Update,
   Time Machine prompts, Setup Assistant and What's New, and Game Center off. SIP, the
   authenticated root and the sealed system volume stay unchanged.
4. A physical-deletion variant B is planned separately. It is compared against A, and B
   wins only if it is at least 15% better on boot, idle CPU or task time, with no
   regression on popups or stability.
5. Keep only the core apps visible: Finder, Terminal, System Settings, Safari, TextEdit,
   Preview, Activity Monitor, Console and the Xcode command-line tools.
6. A feature agent follows on the winning image.
7. The process is data driven, and every decision is logged here with its evidence.
8. The work lands as stacked PRs.

## 2026-09-23: decisions while building and measuring variant A

Evidence for each is in [`report.md`](report.md) and [`raw/`](raw/) unless noted.

9. **The lean profile is a script in the image build path, not boot.**
   `build-image.sh -lean` runs `prepare-image -lean`, which runs `guest/lean.sh` after the
   standard `PrepareGuest`, so A has everything `greenroom-base` has. Boot applies nothing
   lean; the image carries it. Merged as PR #38.
10. **Only the Data volume is touched.** Preferences and `launchctl disable` in the
    user's `gui/<uid>` domain (persisted in `/private/var/db/com.apple.xpc.launchd`). No
    app is deleted; SIP, the authenticated root and the sealed volume are unchanged, so
    every app still launches by path.
11. **Every setting is read back, and a failed check fails the build by name.** The first
    draft ran under zsh, which does not word-split a variable: it disabled one bogus label
    made of all 73 names joined by newlines, and a substring read-back passed it. After a
    reboot the agents were all running. The script now runs under `/bin/sh` and matches
    labels exactly; `lean_test.go` runs the real script under stub `defaults`,
    `launchctl` and `sudo`, including a launchd that forgets a disable and a key that does
    not read back.
12. **Launchpad cannot be trimmed in macOS 26.** Tahoe replaced Launchpad with
    `Apps.app`, which lists every app on the sealed volume and has no per-app hide (no
    preference, no hidden-app key in the binary). Hiding apps there needs the sealed volume,
    which decision 3 rules out, so `Apps.app` left the Dock instead. Variant B (deletion)
    is where this can change.
13. **Banners and widgets go off by disabling `com.apple.notificationcenterui.agent`**,
    plus WindowManager's widget-hide keys. Evidence: 3 widgets on screen in every base run,
    0 in every A run. Cost, accepted: an app under test cannot show a notification banner
    in A, so a verifier cannot check one there.
14. **Software Update's automatic check is best effort.** On macOS 26 softwareupdated
    ignores `--schedule off` and deletes `AutomaticCheckEnabled` within seconds (found by
    the no-mistakes test step on a real guest). Disabling the system daemon was not in
    scope, so the key is written without a read-back. Downloads, installs, App Store
    updates and the user-level update notification agent are off and read back.
15. **The like-for-like control is `greenroom-base-v5`, reported beside `greenroom-base`.**
    `greenroom-base` is stale: built 2026-09-20 with input helper v2 (its first UI read
    compiles the helper for about 15 s) and older provisioning. `greenroom-base-v5` is the
    VM suite's build of the same `build-image.sh` and helper as A, without `-lean`. The
    difference A makes is A against base-v5; against `greenroom-base` it looks larger than
    it is.
16. **A verifier model outage is not an image result.** NIM timed out, returned 404, 429
    and 500 for long stretches; in the first pass three of six tasks got no verdict. The
    bench now resends after the verifier gives up and records each outage. Task time is
    compared only on runs without an outage (one per image), and stays inconclusive: model
    latency dominates it. Every verdict that did arrive was the correct fail, with 11 steps
    and no misclicks, on every image.
17. **Samples that shared the host with another VM are kept and marked, not dropped.** The
    self-hosted CI runner is this host; its VM suite and the pipeline's real-guest build
    overlapped three runs, which show 2 to 4 times the idle CPU of their siblings.
18. **The benchmarked `greenroom-lean-a` is kept although its script predates review.** The
    review added read-backs, a settle wait and a best-effort Software Update write; the
    settings written are the same. The final script was verified by a fresh build
    (`leanx-verify`, `lean: ok`, deleted after).
19. **Result: A beats the control on boot and background load, not on idle CPU or task
    time.** Against `greenroom-base-v5`: boot to ready 20.5 s against 26.4 s (-22%),
    idle processes -18%, running user agents -28%, widgets gone, memory -2%, image size and
    reboot time equal, no popups in either, both survive a reboot with `swiftc` working.
    Idle CPU is the same within noise (12.0 to 17.2% against 14.2 to 18.1% in uncontended
    runs), and task time is inconclusive. A is the current winner over the standard image.
20. **The bar for variant B**, by decision 4: B wins only if it is at least 15% better
    than A's medians here on boot to ready (20.5 s, so 17.4 s or less), idle CPU (16.9%,
    so 14.4% or less) or task time (251.9 s on its clean run, so 214 s or less), with no
    popups and every stability check passing. Idle CPU and task time need more runs, or a
    quiet host and a reliable model, before they can decide anything.

### Follow-ups for the feature agent

- Both images reopen a Terminal window over the desktop at every login (the Cirrus base's
  saved Terminal session; see `screenshots/lean-a-after-idle.jpg` and
  `screenshots/base-v5-after-idle.jpg`). The verifier then sees Terminal in its app list.
  Clearing Terminal's saved state in the image is a small fix; it changes the image, so
  it needs its own measurement.
- `greenroom-base`, the image the local daemon serves, is stale (helper v2). Rebuilding it
  with the current `build-image.sh` gives most of the difference in the second table of
  the report with no lean profile at all.
