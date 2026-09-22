# 0009. A human can take the machine's mouse and keyboard, under a lease the run records

Date: 2026-09-20
Status: accepted (implemented)

## Context

ADR 0007 gave the companion a Screen tab and one rule: **the app does not drive**. It
watches, it speaks, and it may intervene in two ways that the conversation records (a
screenshot, a destroy). That was right for a tab that showed a picture.

It stops being right the first time a person watches a run go wrong. The verifier is
stuck on a dialog it does not understand, or the app under test needs a password typed
once, or the reviewer wants to click through the thing themselves before accepting a
verdict. Today the only answers are to send a message and hope the verifier does it, or
to destroy the machine. The picture is right there and the mouse does nothing.

Browserbase made the same move for browsers: a live view you can click in, next to the
agent that is driving. The reason it works is not the clicking, it is that the session is
one session. The agent and the person are looking at the same screen and taking turns on
the same machine, rather than the person opening their own copy.

greenroom already has what that needs. The daemon captures a frame every couple of
seconds (ADR 0008) and pushes it down the event stream, so the picture is live without
anything new. The guest has the permissions: the image grants `Accessibility` and
`PostEvent` to the Tart guest agent (`docs/02-spike.md`), which is what posting an event
into the guest's own session requires. What is missing is a way in, and a rule about who
is holding the mouse.

VNC was the obvious alternative, and it is what ADR 0007 expected to arrive eventually.
It is rejected for now, not forever: `--vnc-experimental` is blocked by issue #7 (a
graphics-enabled VM restarts its GPU), it is a second channel into the machine that the
daemon cannot see or record, and a VNC session leaves nothing in the run's evidence. A
click that no one can review afterwards is the opposite of what this product sells.

## Decision

**A person may drive a machine from the companion, through the daemon, under a lease.**

**The lease.** `Manager` holds at most one `Control` per machine: a holder (`human`
today, the seat rather than the person), when it was taken, when it expires and how many
actions it has posted. Taking it a second time from the same seat renews it; from another
seat it is refused. It expires after a minute of silence and every posted batch renews it.

The lease is not ceremony. It is what makes the handover visible: the daemon writes
"human took control of the screen" into the run's conversation once when it is taken and
"human gave the screen back after N actions" when it is given back, so the coding agent
learns on its next `agent_wait` that someone else was driving its machine and roughly how
much they did. It is also what stops two hands reaching for one mouse, which is a state
nothing can recover from, and what guarantees a machine is not left believing a person is
at its keyboard when the app that held it has crashed.

**The way in.** Three routes on the existing API, beside the ones ADR 0007 lists:

```
POST   /api/runs/{id}/control   { ttlSeconds? }  take or renew; answers the lease and the screen size
DELETE /api/runs/{id}/control                    give it back
POST   /api/runs/{id}/input     { actions: [...] }
```

An action is one of `move`, `click`, `down`, `up`, `scroll`, `type`, `key`, `sleep`.

**Coordinates are fractions of the screen, never pixels.** The app is looking at a frame
scaled to whatever size the window happens to be, and the frame itself is a resized JPEG;
it cannot know the guest's resolution and must not have to. It sends `0.25, 0.5` and the
daemon, which does know, multiplies. This is also what lets the same click mean the same
thing when the window is resized, when the frame cap changes, and on a Retina guest.

**The events are posted by a helper compiled inside the guest.** `CGEvent` at the HID tap
is the only way to move a real pointer on macOS, and it has to be posted from a process in
the guest's own login session. The daemon carries the helper's source
(`internal/machine/guest/input.swift`), writes it into the machine as base64 and compiles
it there once per machine, then calls it with one base64 argument per batch. Nothing the
person types ever reaches a shell. `osascript` and System Events were rejected: the spike
found AppleEvents is not granted in the image and prompts for it, while `PostEvent`
already is.

**A batch is one step.** A drag is a press, some moves and a release; a typed word is one
`type`. They go in one request and are recorded as one line in `steps.jsonl`
(`machine_input`), with the actions as its input. The evidence says what the person did,
at a grain a reviewer can read, and the frames on either side of it show what it did to
the screen.

**The screen speeds up while a person is holding it.** A recording for a reviewer can
afford to be two seconds behind; a hand on a mouse cannot, because the picture is the only
feedback there is. While a lease is live the frame recorder captures every 500 ms, and it
goes back to `-frame-interval` when the lease ends.

**What the companion does with it.** The Screen tab gains a "Take control" switch. With it
on the picture is pinned to the newest frame, an AppKit layer over the image turns real
mouse and keyboard events into fractions and actions, a red badge says "You have control",
and the lease is given back when the switch goes off, when the tab or the run changes, when
the machine stops being ready, and when the app quits.

## Consequences

**ADR 0007's "the app does not drive" is narrowed, not dropped.** The rule it was
protecting was never "the app must be powerless"; it was "the app must not change what
the coding agent believes about the machine without telling it". Creating machines,
syncing code and running commands stay out for exactly that reason. Driving the screen is
in because the lease makes it the loudest thing in the transcript rather than the
quietest: a handover message, a step per batch, and frames showing the result.

**The verifier does not have this yet.** `Manager.Input` takes a holder and the lease
mechanism is already general, so `machine_click` and `machine_type` as MCP tools are the
same call with a different seat. That is M2's own work and this ADR does not do it; what
it does is make sure that when the verifier gets a mouse, it cannot take one a person is
already holding.

**A machine needs a Swift toolchain.** The Cirrus base image has one. A machine without it
answers the first attempt to take control with "swiftc is not installed in this machine"
rather than failing silently, and the greenroom base image should ship the helper
pre-built so the first click does not wait for a compile.

**Control is not authentication.** The daemon listens on loopback and so does the app;
"human" is a seat, not a person, and a second companion window on the same host is the
same seat. That is the same trade ADR 0007 made, and it changes when the listener does.

**A lease does not survive the daemon.** State reloaded from `state.json` drops it: the
app that held it did not survive either, and whoever is driving has to take it again.
