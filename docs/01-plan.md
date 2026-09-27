# Plan

Scope is ADR 0003: one developer, one Claude Code session, one machine at a time on their
own Mac. Fleet, hosting and PR posting are deferred.

## Status (2026-09-22)

| Milestone | What | Status |
| --- | --- | --- |
| M0 | Spike: Tart by hand, measure boot, exec, screenshot (`02-spike.md`) | done |
| M1 | Machine tools over MCP | done, except a real GUI app built from a synced repo |
| M1.5 | Conversation per run, verifier, companion app (ADRs 0005-0007, `08-...`) | done |
| M2 | Computer use: click, type, key, scroll, drag for verifier, coder and human (ADR 0009) | done (accessibility tree for aiming: ADR 0012) |
| - | Run recording as a timelapse (ADR 0008) | done |
| - | Interactive pty sessions (`machine_session_*`) | done |
| - | Pinned tart 2.37.0, `greenroom-base` with the input helper baked in | done |

What exists today:

- Daemon with 25 MCP tools: `machine_create`, `_wait`, `_list`, `_sync`, `_pull`, `_exec`,
  `_exec_wait`, `_approve_capture`, `_screenshot`, `_destroy`, `_ui`, `_click`, `_type`,
  `_key`, `_scroll`, `_input`, `_session_start`, `_session_send`, `_session_read`,
  `_session_close`, `agent_send`, `agent_wait`, `agent_transcript`, `run_finish`,
  `run_report` (ADR 0034).
- Verifier on NVIDIA NIM or in `manual` mode.
- Companion app: runs, transcript, steps, screen player, live H.264 screen (ADR 0011),
  take control.
- One image recipe, `apps/daemon/scripts/build-image.sh`: base and lean layers, a
  toolchain manifest, and a dialog gate that fails the build on any prompt or stray app
  (ADR 0018, ADR 0019). Every image carries the host's Xcode, so `xcodebuild`, XCTest and
  swift-testing work in a machine (ADR 0026).

## Next steps

In rough priority order.

1. **Build and launch a real Mac app end to end.** The last M1 item. Xcode is in every
   image now (ADR 0026), so an Xcode project works as well as a SwiftPM app.
2. (Done: Xcode in every image, copied from the host, ADR 0026. No simulator runtime yet;
   that comes with the iOS work, `docs/12-ios-expansion.md`.)
3. (Done: one image recipe, ADR 0018.)
4. **Sync as a boot phase.** `machine_create` takes a project path and reports ready once
   synced (`10-build-transport.md`, option C). Decide on mutagen (SSPL licence question)
   versus rsync.
5. **Robustness left from `07-test-plan.md`:** `Destroy` forgets the machine before the
   VM is gone; recorder ignores write errors; one bad run directory stops `loadState`;
   all screenshots share `/tmp/greenroom-shot.png` in the guest; no auth on the HTTP
   endpoint.
6. **Tart behind an interface** (ADR 0010). Today `internal/tart` is the only caller, but
   `Manager` holds a concrete `*tart.Client`.
7. Open issues: #23 (transcript wastes a wide window; layout decided in companion ADR 0004,
   not built yet).

Not planned: fleet, multi-host, PR posting, warm machines (resume is not faster than cold
boot on the base image; see `10-build-transport.md`).
