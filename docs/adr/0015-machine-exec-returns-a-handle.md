# 0015. machine_exec waits at most 50 s, then returns a handle; its output is bounded

Date: 2026-09-23
Status: accepted

## Context

**A long command looked like a broken tool** (issue #39). `machine_exec` held the MCP
call open until the guest command exited, up to its 600 s default timeout, and wrote
nothing to the response before that. Claude Code gives up on a tool call after 60 s
with `The operation timed out.` A cold `swift build` takes about 70 s, so the most
common first step of a session failed: the agent lost the exit code and the output,
while the run record said the step succeeded. `machine_wait`, `agent_wait` and
`machine_session_read` already stop at 50 s (`maxWait`) for this reason.

Keeping the call open was considered first:

- The go-sdk (v1.7.0) streamable HTTP server sends a POST's SSE headers and first bytes
  only with the first JSON-RPC message on that stream. There is no API to send a
  keepalive comment on a request's stream.
- Progress notifications are such a message, but the spec lets a server send them only
  for a request that carried a `progressToken`, and whether one resets a client's timer
  is up to the client. A server cannot rely on either.
- Writing our own SSE comments under the SDK, through a wrapped `ResponseWriter`, would
  bet on how each client times a call (first byte or whole response) and on SDK
  internals that are not ours.

**Output was unbounded** (issue #29). `tart exec` output went into unbounded buffers and
then, whole, into the tool result twice (structured content and its JSON text). 10 MiB
of output became a 25 MB response; 200 MiB grew the daemon to 1.4 GB.

## Decision

1. `machine_exec` starts the command and waits at most `waitSeconds` (default 45, max
   50, like `machine_wait`). A command that finishes in time returns as before, plus
   `execId` and `running: false`. One still going returns `running: true`, an `execId`
   and no `exitCode`, and keeps running.
2. `machine_exec_wait {runId, execId, waitSeconds}` waits the same way and returns the
   same shape. It can be called again after the command finished; a machine keeps its
   last 32 finished commands. Like `machine_wait` it records no step of its own.
3. The command belongs to the machine, not to the call (`machine/execjob.go`). It runs
   under a context detached from the request, until it exits, its guest timeout (ADR
   0014) or the machine goes (`detachLocked` cancels it). Its step number is claimed when
   it starts and its record, with the full duration and the result, is written when it
   ends, so the `step` the first call returns is the step the result lands in.
4. Each of stdout and stderr keeps its first 8 KiB and last 24 KiB
   (`machine.ExecHeadLimit`, `ExecTailLimit`), with a marker line where bytes were left
   out, never inside a UTF-8 character. `stdoutBytes`/`stderrBytes` give the full sizes
   and `stdoutTruncated`/`stderrTruncated` say when bytes were cut. The daemon never
   holds more: `tart.Client.ExecTo` streams into the bounded writer. The tool
   description states the limits.
5. `Manager.Exec` (the verifier) keeps its blocking signature on top of the same job,
   and a caller that gives up still ends the host `tart exec`.

## Consequences

- An agent may need two calls for a long command instead of one; the description says
  so and points at `machine_session_*` for a build to watch or a program to type into.
- A command no caller ever collects still finishes and is recorded.
- More than 32 KiB of a stream is not recoverable from the result. An agent that wants
  more writes the output to a file in the guest and reads parts of it.
- The whole output still crosses `tart exec` before the host drops the middle, so a
  command that prints hundreds of MB still takes the time to move it. Bounding it in
  the guest wrapper would need a size trailer in the output protocol; not done.
