# 0047. A prompt stops the verifier's command

Date: 2026-10-01
Status: accepted

Extends ADR 0038 point 3 (the desktop look while a command runs) and ADR 0044 (the verifier's
`machine_approve_control`). Keeps ADR 0018 point 4 (greenroom never answers or closes a dialog).
Fixes issue #283.

## Context

ADR 0038 point 3 made `machine_exec` look at the screen when a command is still running after
its wait, so an MCP caller learns a prompt is blocking it. The verifier's `machine_exec` never
returns "still running": it blocks on the whole command (`Manager.ExecAs`, a 5 minute timeout),
so the look never runs for it. ADR 0044 named this as separate work.

Reproduced on a clone of `greenroom-base-v10-r4` with the verifier's manual brain, with an
accessory app built by `swiftc` under `~/work` and not approved:
`osascript -e 'tell application id "<its id>" to count windows'` raised `"tart-guest-agent" wants
access to control "PromptCheck"` (a `UserNotificationCenter` window at layer 8). The verifier's
call came back after 121 s with `PromptCheck got an error: AppleEvent timed out. (-1712)` and
nothing about a prompt. osascript's own Apple Event timeout (2 minutes) ended it first; a command
that waits without such a timeout waits for the exec's 5 minutes.

Measured on the same guest, the facts the decision rests on:

- The prompt is the target app's request to tccd, not the sender's. Killing the osascript that
  caused it, or quitting the app it names, does not take it off screen. It stays until someone
  answers it or it times out, 120 s after it appeared.
- When it times out, tccd writes a denial (`auth_value` 0) for that client and app into the user
  TCC.db. An approval written while the prompt was up (`machine_approve_control`) is overwritten
  by it, and the next Apple Event prompts again. An approval written after the prompt is gone
  takes: the same osascript then answered `0` at once.
- While a prompt is up, a second request for the same app waits behind it.
- `machine_ui` cannot read the prompt (issue #223), but System Events can: `value of every static
  text of every window` of the process drawing it returns its text, naming the app that asked and
  the app it wants. The image grants tart-guest-agent Apple Events to System Events (ADR 0038
  point 1).

## Decision

### 1. One look, two callers

The look ADR 0038 point 3 added is one function both exec paths call (`lookAtDesktop`). It reads
every on-screen window as before, and the desktop report gains `prompts`: each unexpected window
at a modal panel or alert level (layers 8 to 19) drawn by a process that is not a regular app
(not one of the report's running apps), Notification Center's banners excepted. The look then
reads, by the drawing process's pid, its executable's path (`ps`) and the window's text (through
System Events), and keeps only the windows macOS itself draws: an executable under `/System`, or
under `/usr` but not `/usr/local`, on the sealed system volume. Every process that draws a TCC,
Gatekeeper, authorization or crash prompt lives there (`UserNotificationCenter`,
`CoreServicesUIAgent`, `SecurityAgent`), and an app built, synced or installed in a run never
does. So a regular app's own alert, an accessory or menu bar app's own alert (issue #223's apps,
which are not regular apps either), a menu (layer 101) and a floating panel (layer 3) are not
prompts; each stays an unexpected window in the report. The read is best effort: when it fails
the window is kept as a prompt, by owner and frame, since missing a real prompt costs a command
its whole timeout.

### 2. The verifier's exec looks while it waits, and stops the command on a new prompt

The verifier's `machine_exec` and the manual brain's `run` go through `Manager.ExecWatched`.
While the command runs it looks every 15 s (`WithPromptLook`). On a prompt no earlier stop
reported, it stops the command and returns its result with `stoppedForPrompt` and `desktop`,
which the step record keeps. The verifier reads that greenroom ended the command because of the
prompt, what the prompt says, and what to do: wait until the prompt has timed out (a `sleep` of
the remaining time, then a screenshot), call `machine_approve_control` for an app it built, and
run the command again; for anything else, `ask`, quoting the prompt.

The command is stopped, not left running, because:

- The verifier cannot come back for it. It has no `machine_exec_wait`, and adding one would
  give the small model a polling loop to manage for no gain.
- Leaving it changes nothing about the prompt (it outlives the command, measured above) and
  leaves a second copy of the command running when the verifier runs it again after approving,
  which, for osascript, also queues a second request behind the first prompt.
- What the command printed so far comes back in the result, as at a timeout.

The stop is the guest's: the wrapper's argv now ends with the execId
(`/bin/sh -s greenroom-exec <secs> <execId>`), the wrapper names its temp dir by it
(`/tmp/greenroom-exec.<execId>.XXXXXX`) and writes there the pid of its login zsh. A script reads
that pid and sends TERM to the zsh's process group (the same group the timeout watchdog signals,
so the command's children go too), then KILL after 5 s, then the host cancels its own tart exec.
The wrapper prints the output so far and exits 143. The stop goes by that recorded pid, never by a
process name, so a command that `exec`s another program is stopped too, and it signals only that
group, never the wrapper's or the guest agent's. It refuses an execId that is not hex.

A prompt stops one command. The verifier's wait for it to go (`sleep`), or anything else it runs
while it is up, is not stopped for the same prompt: a prompt is keyed by its process and its text,
and forgotten once a look no longer finds it, so the same request raised again later stops a
command again. A prompt is remembered only when its stop did end the command: a command that
ended on its own while the look ran returns its own result, and the next command the prompt
blocks is stopped and told. A look that fails changes nothing.

Neither path answers, clicks or closes the prompt (ADR 0018 point 4). The decision to allow an
app stays an explicit, recorded call (ADR 0044).

### 3. The MCP path does not stop commands

`machine_exec` and `machine_exec_wait` for MCP callers keep ADR 0038's behaviour: a command still
running after the wait is reported with `desktop`, now including `prompts` and their text, and
keeps running. That caller can come back with `machine_exec_wait` and decide, and a person may
answer the prompt in the Companion. Both paths share the detection and the report; they differ
only in whether the caller can return for the command. The tool description now says that an
approval made while a prompt is up is undone when it times out.

## Consequences

- A verifier command blocked on a prompt comes back in about 15 s instead of 2 to 5 minutes, and
  says which app prompted. Measured on the same clone after the change: stopped after 16 s with
  the prompt's text; a `sleep 115` while the prompt was up ran to its end; after the approval the
  same osascript answered `0`.
- Every verifier command longer than 15 s pays one screen read per 15 s, and a text read only
  while a prompt is on screen. A finding equal to the last one is not logged or sent again.
- A verifier command is not stopped for an app's own alert, whether the app is a regular one or
  an accessory or menu bar app (the alert's executable is not part of macOS). Such an alert never
  times out, so telling the verifier to wait for it would loop. A prompt drawn by a third-party
  process outside the system volume is missed the same way; none of the prompts this ADR is about
  are.
- `promptLifetime` (2 minutes) is measured for the Apple Events prompt. Other TCC prompts may
  live longer; the verifier's instructions say to check with a screenshot and wait again.
- The extra argv field shows in the guest's `ps` (a short hex id). A `pgrep -f` through
  `machine_exec` still cannot match its own wrapper: the command is still not in any argv.
