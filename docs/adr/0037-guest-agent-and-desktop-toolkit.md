# 0037. A Greenroom agent in the guest, on one channel, drives the desktop by reference

Date: 2026-09-27
Status: proposed. Design and evidence: `docs/21-verifier-desktop-toolkit.md`. Umbrella issue
#206. Builds on ADRs 0009, 0011, 0012, 0017, 0024, 0027 to 0029 and daemon ADRs 0002 to 0004;
changes how 0012's tree is produced and aimed at. (0035 and 0036 are reserved for redesign
PRs in flight.)

## Context

The verifier's main cost is now navigating the desktop, not judging. Over every verification
run recorded so far (Era B, Sep 22 to 27: 21 runs, 835 verifier calls, 198 minutes; docs/21
section 1):

- 39% of verifier time was wasted, and **32% went to navigation and desktop-tooling
  failures**: tool errors and timeouts 31.1 min (describer failures, a 320 s helper install,
  exec deadlines, lost transport), misaimed clicks 10.0, covered controls 4.1, app launch 3.5,
  re-looks 3.4, window focus 2.7, drags 2.6 (16 of 16 raw drags wasted), clipped text 2.5,
  wrong scroll area 1.8 (57% of all scrolls wasted).
- Eight rework rounds, where a coding agent sent the verifier back over navigation, cost about
  160 more calls and 40 minutes. All three check-level false passes of the dogfooding session
  (docs/19) came from what `machine_ui` shows: covered controls look clickable (#189), clipped
  text looks complete and nested scroll areas are invisible (#190).
- About 40% of verifier time is waiting for a vision model to describe a screenshot, often for
  text the tree could have given exactly.
- About half of all calls are `machine_ui` reads made only to see whether the last input did
  anything (#192).

The tools cannot express what is needed: an element id is an index into one read; a click goes
to a center whatever covers it; scroll posts a wheel event wherever the pointer is; nothing
waits; menus, dialogs, windows and apps are reached through `osascript` and `open`. And every
action is one or more `tart exec` calls (about four per verified action, plus 30 a minute for
recorder frames), each a new process and AX connection, and each leaking one descriptor in
`tart run` for the life of the VM (daemon ADR 0002, #186).

Prior art agrees on the fix (docs/21 section 3). Playwright (Apache-2.0) made browser
automation reliable with refs that survive snapshots, actionability checks with auto-retry
(visible, stable, enabled, receives events by hit test), settle-then-report, `wait_for` and
`expect`, and modal states named in every response. cua-driver and Peekaboo (MIT) carry the
same ideas to macOS AX, and add honest action results (delivery is not effect; never retry a
mutating action blindly). Desktop Commander (MIT) has no GUI control and injects remotely
switched `[SYSTEM INSTRUCTION]` text into tool results, which a verifier cannot accept.

## Decision

1. **A long-lived Greenroom agent in the guest.** The input helper gains an `--agent` mode
   (helper version 9 and up). The daemon starts it through **one long-lived `tart exec -i`
   per machine** and speaks a framed protocol on its stdin and stdout, extending the `--serve`
   format (`[type u8][length u32 BE][payload]`) with request, response, blob, cancel, event,
   ping, pause and stream frames. Every desktop operation, and the recorder's frames, go over
   this channel. The agent's responsible process stays tart-guest-agent, so it inherits the
   Accessibility, PostEvent and ScreenCapture grants and replayd approvals the image already
   has: no new TCC rows. It listens on nothing, so no process in the guest can borrow its
   permissions. It exits when stdin closes.
   Rejected: one exec per action (the #186 leak, fragile), a LaunchAgent with a TCP listener
   (a confused deputy for guest code, new TCC rows per build, and the daemon does not dial
   guests), ssh (wrong responsible process and session), tart's control socket directly (same
   leak, not a public interface).
2. **The daemon stays the only MCP server.** A `guestagent` package owns the connection
   (heartbeat every 2 s, reconnect with backoff, deadlines on every request, at most 45 s),
   and a `desktop` package owns tool semantics: per-reader refs, the lease, step recording and
   evidence. No MCP server runs in the guest.
3. **Perception is a snapshot with stable refs.** `machine_snapshot` replaces `machine_ui`:
   refs (`e17`) stay the same while the element lives, fail closed when it is gone, and are
   scoped per reader. Each element carries states and **visibility from real hit-testing**
   (`covered by <ref>`, `offscreen in <scroll ref>`, `clipped`, `not drawn`); scroll containers
   are elements with positions; text is never cut without a marker; modal and foreign windows
   (sheets, alerts, menus, system prompts, notifications) head every snapshot as `attention`.
   `machine_find` searches the tree; `machine_screenshot` crops to a ref and asks the
   describer a question; OCR merges text for non-AX content (wave 3).
4. **Actions are by ref, with actionability and auto-wait.** Attached, visible (scrolled into
   view when needed), enabled, stable, receives the event at a hit-tested point, frontmost, not
   behind a modal; retried with backoff to a 5 s default. The input is never repeated by the
   tool. Each action returns its effect in the same result (`changed` with a diff, `no change`,
   `unverifiable`, `refused` with the reason), which replaces the separate effect read.
   Pointer events stay the default (ADR 0012's reason holds); AX actions and value setting are
   explicit and recorded as such.
5. **Waiting and assertions are tools.** `machine_wait_for` (AXObserver plus a 150 ms poll, at
   most 40 s) and `machine_expect`, whose recorded observation is evidence a verdict cites
   under ADR 0024.
6. **Build in four waves** (docs/21 section 8; #212, #213, #214, #215): (1) agent, channel, snapshot, find, press,
   type, set value, key, scroll, wait_for, expect, screenshot crops, and a navigation fixture
   app and bench tier; (2) menus, select, dialogs and system prompts, file dialogs, windows,
   apps, open, drag, clipboard; (3) logs, crashes, processes and hangs, OCR, set-of-marks,
   before and after crops, the live stream on the same channel; (4) the verifier switches
   over, with an evidence-contract ADR, measured on the bench and a dogfooding session. Waves
   land behind a daemon flag until wave 4.
7. **Tool results carry only guest observations and text from this repository.** Nothing
   fetched, nothing remotely switchable. Screen text is quoted as data; secure fields are
   never read back.

## Consequences

- One `tart exec` per machine for desktop work instead of about four per verified action and
  30 a minute for frames; the #186 leak stops mattering for desktop control even before tart
  fixes it.
- Actionability, waiting and diffing run in the guest in milliseconds instead of as model
  steps; the verifier's calls per checked action drop from three to two, and polling loops to
  one call. Targets per wave are in docs/21 section 9; the false pass rate may not rise.
- A channel is a new single point of failure. It is guarded by heartbeats, per-request
  deadlines, an agent watchdog that reports a stalled WindowServer (#187), reconnects, and
  until wave 4 a read-only degraded path through one-shot execs. Inputs never fall back
  silently.
- A daemon restart ends the agent; the next daemon reconnects with one exec and refs from
  before report themselves stale.
- The helper grows from one file to a multi-file Swift program compiled in the guest; the
  compile (about 60 to 90 s expected) is paid once per image by `prepare-image`, which bumps
  `imageRecipeVersion` once. The dialog gate gains an agent smoke test.
- The verifier's prompt loses its coordinate-first rules; coordinate input remains for content
  with no ref and needs a stated reason.
- ADR 0012's tree becomes the snapshot; its per-reader scoping and pointer-first rule carry
  over, its `uiStep` guard is replaced by stable refs, and its rejection of set-of-marks for
  aiming stands (marks are drawn from refs for the describer and the Companion only).
