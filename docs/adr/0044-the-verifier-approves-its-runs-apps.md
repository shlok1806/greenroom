# 0044. The verifier approves its run's apps with its own call

Date: 2026-10-01
Status: accepted

Extends ADR 0038 (`machine_approve_control`, the TCC grant for an app built at run time). Fixes
issue #269.

## Context

ADR 0038 point 2 made approving an app under test an explicit call, `machine_approve_control`,
and named "the verifier and the Companion" as its callers. Only the MCP tool was built. The
verifier's own tool set (`internal/verifier/tools.go`) never got it, so when a task needs an
app built in the run to be scripted or controlled, the verifier has two ways out, both bad.
Seen in run `20260929-024102-a0820939cd8d7aab` while verifying PR #268:

- It hand-writes TCC.db rows through `machine_exec`. A row is easy to get wrong (the client's
  resolved path, its client type, both the system and the user database, tccd's cache), and a
  wrong one leaves the next `osascript` blocked on the prompt until `machine_exec`'s timeout,
  which reads as a slow or broken app, not as a missing grant.
- It succeeds only when the coder approved the app before handing it over, which the coder has
  no reason to know it must do.

Two fixes were open (the issue names both):

- **(a)** Give the verifier its own approve call.
- **(b)** Have the daemon approve an app automatically when the verifier or the coder launches
  one from `work/`.

## Decision

### 1. The verifier gets `machine_approve_control`, scoped to its run's own apps

Option (a). The verifier's tool list gains `machine_approve_control {app}`, the same name and
argument as the MCP tool, running the same guest script (`guest/tccgrant.sh`) through
`Manager.ApproveControlInHome`. The step is recorded `by: verifier`. The manual brain gets the
same call as `approve <app>`.

Option (b) is rejected:

- The daemon cannot see a launch. Apps start through arbitrary shell (`open`, `./App`, `xcodebuild
  test`, a script the coder wrote, an app launching another), so "on launch" means parsing
  commands or running a guest watcher. ADR 0038 already rejected the watcher: the Apple Event
  that raises the prompt can come from the same command that launches the app, so a reaction to
  the launch has no guaranteed window before it. A grant only prevents the next prompt; it never
  answers one on screen.
- Approving on sight is wider than asked. It would grant Accessibility, screen recording, the
  camera and the microphone to anything that appears under `work/`, with no step a person can
  read saying who decided it. An explicit call is a recorded decision.
- It would be a second mechanism next to a call that already works, with its own timing bugs,
  for no case the call does not cover.

An explicit call has none of these problems: the caller knows when the build finished and what
it produced, the grant happens before the first control attempt, and the step is evidence.

### 2. The verifier's scope: its own run, and only what resolves under the guest home

The verifier's call takes no `runId`: it acts on the run whose conversation it is serving,
like every other verifier tool, so it cannot reach another run's machine.

Within that guest it approves only an `.app` bundle whose resolved path, and whose resolved
main executable, lie under the guest home. That is where everything a run puts on the machine
lands: `machine_sync` (`~/work/<name>`), a checkout, a `swiftc` or SwiftPM build, and Xcode's
`~/Library/Developer/Xcode/DerivedData`. Pre-installed apps already have their Apple Events rows
from the image (ADR 0038 point 1), so the verifier never needs to approve one, and granting a
system app the camera or Accessibility would change the machine under test in a way no task
asked for. Both paths are checked after `realpath`, in the guest, so a link in the home to an
installed app, or a bundle whose executable is a link to `/bin/sh`, is refused too: the grants
are keyed to the executable, and either would otherwise hand them to a system binary.

The coder's MCP tool keeps the unscoped grant. The coder is the user's agent and may test an
installer that puts its app in `/Applications`.

This is a statement of intent, not a security boundary. The guest is the run's own disposable
machine, and the verifier's `machine_exec` can already run anything in it as the admin user with
passwordless `sudo`. What contains the verifier is the VM (ADR 0003); this scope keeps its
ordinary path narrow and its grants explainable.

### 3. The verifier's `machine_exec` refuses a command that names TCC.db

A verifier command containing `TCC.db` (any case) is not run. The tool result says to call
`machine_approve_control` for an app it built, and to ask the coder or the human for any other
permission change. The system prompt carries the same rule next to the approval rule: approve
an app built or synced in this run once, after it is built and before launching, scripting or
clicking it, and never write TCC.db yourself.

The prompt alone is not enough: the small reasoning model ignores rules it does not need at the
moment, and the refusal arrives exactly when it reaches for the wrong tool. Reads are refused as
well as writes, because the substring cannot tell them apart and the verifier has no task that
needs TCC.db's contents; the approval's own result says what was granted. Like point 2, this is
guidance and not a boundary: a determined command can name the file without the substring. It
costs no step, like every other refusal before a tool runs, and counts toward the repeated
failure rule (issue #125).

## Measured

On a clone of `greenroom-base-v10-r4` (recipe 4), with an accessory app built by `swiftc`
under `~/work` and running:

- `osascript -e 'tell application id "<its id>" to count windows'` through `machine_exec` raised
  the prompt (a `UserNotificationCenter` window at layer 8) and blocked until the guest killed it
  at 60 s, exit 124: the issue's stall.
- After the verifier's scoped approval, the same command answered `0` at once.
- The scoped call refused `/System/Applications/Calculator.app` as outside the guest home.
- `tell application id "<its id>" to get name`, with no grant at all, answered at once: AppleScript
  resolves an application's name from its bundle and sends no Apple Event. A check that uses
  `get name` proves nothing about a grant. ADR 0038 point 5's Calculator exercise in the image
  gate uses it, so it cannot fail for a missing Calculator row; that is a follow-up for the
  gate, not changed here.

`TestEndToEndVerifierApprovesARunBuiltApp` (`apps/daemon/e2e_approve_test.go`) is that run.

## Consequences

- A verifier task that builds an app and drives it no longer depends on the coder approving it
  first, and no longer burns a five-minute `machine_exec` timeout on a prompt it caused itself.
- The verifier's ordinary path cannot grant TCC services to anything but what its run built.
- The MCP tool and the verifier's call share one guest script; the scope is its second argument
  (`any`, the default, or `home`), so a change to what is granted changes both.
- A task that genuinely needs a different TCC state (testing an app's permission-denied path,
  say) cannot be set up by the verifier through `machine_exec`; it asks. That is the right owner
  for a decision about what the machine under test permits.
- The verifier still waits out `machine_exec`'s full timeout on a prompt it did not cause (an
  app that triggers one by itself). ADR 0038 point 3's desktop look covers `machine_exec_wait`
  for MCP callers only; extending it to the verifier's blocking exec is separate work.
