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

- Daemon with 22 MCP tools: `machine_create`, `_wait`, `_list`, `_sync`, `_exec`,
  `_exec_wait`, `_approve_capture`, `_screenshot`, `_destroy`, `_ui`, `_click`, `_type`,
  `_key`, `_scroll`, `_input`, `_session_start`, `_session_send`, `_session_read`,
  `_session_close`, `agent_send`, `agent_wait`, `agent_transcript`.
- Verifier on NVIDIA NIM or in `manual` mode.
- Companion app: runs, transcript, steps, screen player, live H.264 screen (ADR 0011),
  take control.
- Packer recipe for `greenroom-base` (TCC, first-boot seed disk) and an unbuilt
  `greenroom-xcode` recipe.

## Next steps

In rough priority order.

1. **Build and launch a real Mac app end to end.** The last M1 item. Needs a SwiftPM app
   first (fits the base image), then Xcode.
2. **One `greenroom-base`.** `images/greenroom-base.pkr.hcl` (TCC, firstboot, display) and
   `apps/daemon/scripts/build-image.sh` (input helper, ssh key) both produce a VM named
   `greenroom-base` with different contents. Merge them.
3. **Build `greenroom-xcode`.** Needs ~200 GB free and a hand-downloaded `.xip`
   (`09-image-strategy.md`).
4. **Sync as a boot phase.** `machine_create` takes a project path and reports ready once
   synced (`10-build-transport.md`, option C). Decide on mutagen (SSPL licence question)
   versus rsync.
5. **Robustness left from `07-test-plan.md`:** `Destroy` forgets the machine before the
   VM is gone; recorder ignores write errors; one bad run directory stops `loadState`;
   all screenshots share `/tmp/greenroom-shot.png` in the guest; no auth on the HTTP
   endpoint.
6. **Tart behind an interface** (ADR 0010). Today `internal/tart` is the only caller, but
   `Manager` holds a concrete `*tart.Client`.
7. Open issues: #7 (GPU crash dialog in graphics mode; graphics retired by ADR 0016,
   so it can close as won't fix), #22 (nothing installs `packer`),
   #23 (transcript wastes a wide window; layout decided in companion ADR 0004, not built
   yet).

Not planned: fleet, multi-host, PR posting, warm machines (resume is not faster than cold
boot on the base image; see `10-build-transport.md`).
