# 0013. Screen-capture approvals: dated in 3024, written every boot, checked before captures

Date: 2026-09-22
Status: accepted

## Context

On macOS 15 and later the guest covers its own screen with `"tart-guest-agent" is
requesting to bypass the system private window picker and directly access your screen
and audio`. It is not TCC: the TCC rows are granted and the capture works under it. It
lands in every screenshot and frame, takes focus, and stacks.

The alert comes from replayd, which keeps `ScreenCaptureApprovals.plist` in the user's
group container: one record per capturing client, keyed by the responsible process's
resolved executable path, or an app's bundle URL (`file:///.../App.app/`). Every
screenshot and the live helper run through `tart exec`, so the client is
tart-guest-agent; anything over ssh is `/usr/libexec/sshd-keygen-wrapper`.

Two fixes were written in parallel, and a first merge kept a five-key record with
`LastUsed` set to now and skipped any record whose hint date was already 3024. An
adversarial test on 26.6.2 then measured what replayd actually does:

- The alert is decided by `kScreenCaptureApprovalLastUsed` alone: missing, or more than
  30 days old, and the next capture alerts. `kScreenCaptureApprovalLastAlerted` is not
  consulted.
- `kScreenCapturePrivacyHintDate` schedules the monthly "... is accessing your screen"
  banner; a past date shows it.
- replayd sets LastUsed to now on every capture, and resets a record after 30 days
  without one, or after a guest clock jump.
- A record of these three dates is enough. The claim that replayd drops a record missing
  any of five keys was false.
- replayd caches the file and writes its copy back. Every restart while a record is stale
  raises one more alert.

So the skip was exactly wrong for a baked image: its record says 3024 but its LastUsed is
as old as the image, and the first capture a month later alerts.

Measured here while fixing it:

- After replayd is killed, every capture fails with "could not create image from
  display" until launchd has it back (2 to 4 s; up to 7 s when it is killed again within
  seconds, which launchd throttles).
- Killing replayd also stops every ScreenCaptureKit session, so the live helper
  (ADR 0011) exits.

## Decision

1. One script, `captureApprovalsScript` in `machine/capturealert.go`, with modes. It
   writes with `defaults write ... -dict ... -date`, because a bundle-URL key contains
   `:`, PlistBuddy's path separator. `images/scripts/greenroom-tcc.sh` writes the same
   records for the Packer image.
2. `write`, at every boot before ready and in `prepare-image`, is unconditional: for the
   resolved tart-guest-agent path and sshd-keygen-wrapper, LastUsed, LastAlerted and the
   hint date all in 3024. replayd is held with SIGSTOP across the write, LastUsed is read
   back, then replayd is killed, kickstarted and waited for (up to 10 s), so the next
   capture does not fail. At boot a failure is recorded as `captureAlertError` and is not
   fatal; in `prepare-image` it is fatal.
3. `check` only reads. It exits 0 if both records are current, 3 if one is missing, its
   hint date is not 3024, or its LastUsed is more than a week old. `ensureCaptureApproval`
   runs it before a screenshot, a frame and the start of a live stream, never during one,
   at most once a minute per machine by the wall clock (Go's monotonic clock stops while
   the host sleeps, which is when records age). Only a 3 leads to a `write`.
4. A write that kills replayd first ends a running live stream with a reason; viewers
   reconnect to a fresh helper, as the companion already does after any drop. A current
   record never touches replayd, so normal operation never interrupts the stream.
5. `machine_approve_capture {runId, app}` writes the same record for an app under test
   that captures the screen itself, keyed by its bundle URL, and records a step. It ends a
   running live stream the same way. It takes the same per-machine lock as the check and
   write, so two writers never overlap (one's kill during the other's write could lose a
   record to replayd's cached write-back despite the read-back).

## Consequences

- A fresh machine, a baked image of any age, and a guest whose clock jumped all capture
  without the alert. The check costs one `tart exec` a minute while something captures.
- A rewrite interrupts the live stream for a few seconds. It happens only when a record
  was reset, or on `machine_approve_capture`.
- An app approved with `machine_approve_capture` is not re-checked: if its record ages
  out (30 days without capturing, or a clock jump), the call must be repeated. A bare
  executable outside an `.app` cannot be approved this way.
- This depends on replayd's private file format and behaviour as measured on 26.6.2. A
  macOS update can change either; the read-back makes a renamed key fail loudly at boot
  (`captureAlertError`) rather than silently.
