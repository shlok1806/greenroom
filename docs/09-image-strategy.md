# Image strategy: how a machine boots ready

## Decisions (2026-09-21/22)

1. Start from `ghcr.io/cirruslabs/macos-tahoe-base`. Never build the vanilla layer.
2. `greenroom-base` adds: TCC rows for our binaries (macOS 27 user-DB lookup), the
   screen-recording reminder fix, `automationmodetool`, a pinned display, a first-boot
   LaunchDaemon, our ssh key, and a smoke test that fails the build.
3. `greenroom-xcode<N>` adds Xcode from a pre-downloaded `.xip`, simulators, warm caches,
   free-space and runtime assertions. Recipe written, **not built** (needs ~200 GB free).
4. Per-VM identity comes from a read-only **seed disk** read by the first-boot daemon.
   Verified end to end.
5. Per-project dependencies go on a separate attached disk, not a per-project image.
6. No warm start (suspend/resume) until measured on a heavy image.
7. Per-run machines are **plain local clones**, not `clone --stacked`.
8. The daemon runs a **pinned tart** (2.37.0), not whatever is on `PATH`. Done.

How to build and run it: `images/README.md`.

Measured on macOS 27.0 host, Tart 2.32.1 unless marked 2.37.0. Disk figures are `df`
deltas; `du` over-counts shared APFS clone extents.

## 1. Permissions

- MDM cannot grant ScreenCapture or PostEvent ("A profile can't grant access... it can
  only deny it"), and macOS 27 deprecates the PPPC route. Everyone (Cirrus, GitHub runner
  images, Orka, CircleCI) ships SIP off with TCC.db written at build time.
- The Cirrus base already grants Accessibility, ScreenCapture, PostEvent and AppleEvents
  to `sshd-keygen-wrapper`, `osascript` and `tart-guest-agent`. TCC checks the
  responsible process, so anything run over ssh or `tart exec` inherits them.
- Our layer adds: rows for our own binaries (`client_type=1`, named-column `INSERT`), the
  live per-user TCC.db (macOS 27 moved it; find it with `lsof -c tccd`), the
  `ScreenCaptureApprovals.plist` reminder date, and automation mode without auth.
- Screen work must run as a LaunchAgent with `admin` auto-logged in. A LaunchDaemon has no
  WindowServer session.
- TCC fails silently (black frames, `AXIsProcessTrusted() == false`), so the build
  smoke-tests a screenshot, AX trust and a posted event.
- SIP is off. Say so when claiming a result was verified.

## 2. Toolchain layers

```
ghcr.io/cirruslabs/macos-tahoe-base   upstream, SIP off, TCC seeded
  └── greenroom-base                  ours
        └── greenroom-xcode<N>        Xcode, simulators, caches (unbuilt)
```

| Image | Virtual disk | Compressed pull |
| --- | --- | --- |
| `macos-tahoe-vanilla` | 50 GB | 24.0 GB |
| `macos-tahoe-base` | 50 GB | 27.3 GB |
| `macos-tahoe-xcode` | 140 GB | 69.3 GB |

- Registry layers are fixed 512 MiB chunks, not semantic deltas. Base and xcode share 34
  of 263 layers; pulling xcode on top of base still downloads ~53 GB.
- Xcode install: fetch the `.xip` by hand, pass it as a file, `xcodes install --path`. No
  Apple ID in the pipeline.
- Assert at bake time: at least 15 GB free after provisioning; simulator runtimes match a
  checked-in list. Run `xcrun simctl runtime dyld_shared_cache update --all`.

### Why not `--stacked` [2.37.0]

| Operation | Time | Real disk |
| --- | --- | --- |
| `tart clone` (local, 31 GB VM) | 0.027 s | 0 (APFS `clonefile`) |
| `tart clone --stacked`, from cached OCI | 30-58 s | 38 MB (+ ~1 GB once running) |

- `--stacked` refuses a local parent (`requires a remote image`), so every layer must be
  pushed to a registry first.
- It is ~1000x slower per clone, and `Create` holds `createMu` for the clone.
- Its only win is wire savings across many hosts. Revisit with a fleet.
- 2.32.1 cannot read a stacked VM ("VM is missing some of its files").

### Tart gotchas [2.37.0]

- Homebrew tap is dead at 2.32.1. Install from the signed release (`apps/daemon/CLAUDE.md`).
- `tart exec -t` crashes without a terminal on the **host** side. Give it a host pty with
  a non-zero window size. `EIO` on the master means the child exited.

## 3. Identity: seed disk

- The guest Data volume is not encrypted and can be mounted and edited offline, but
  without host `sudo` files land as uid 501 and launchd rejects non-`root:wheel`
  LaunchDaemon plists (`Bootstrap failed: 5`). Do not use offline edits.
- Instead: `hdiutil create -volname GRSEED -format UDRW -fs HFS+ ...` then
  `tart run --disk seed.dmg:ro`. Mounts at `/Volumes/GRSEED`, read-only enforced, 1.9 MB,
  under a second. UDRO (compressed) is rejected by Tart.
- The first-boot LaunchDaemon (in the image as `root:wheel`) polls for the seed, sets
  hostname, `authorized_keys`, `run.env`, clones the repo, writes
  `/var/db/greenroom-firstboot.json` and stamps `/var/db/.greenroom-personalized` only on
  full success.
- Verified end to end. The one real failure: files written right before `tart stop` were
  gone after reboot. **Always `sync` before stopping a guest.**
- Bugs fixed in `firstboot.sh`: `git clone` could hang on prompts (now
  `GIT_TERMINAL_PROMPT=0`, `BatchMode=yes`); a failed clone still stamped the VM;
  `sudo -u admin` kept root's `HOME`.

## Warm machines

| Operation | Plain clone | Stacked (2.37.0) |
| --- | --- | --- |
| Cold boot | 15 s to ssh | 29 s to exec |
| `tart suspend` | ~2 s, 3.3 GB state | ~0 s, 1.5 GB state |
| Resume | 14 s | 19 s |

- A clone of a suspended VM keeps its MAC address, so only one descendant per snapshot can
  run at once.
- `--suspendable` is required at `tart run`. Without it suspend looks fine and resume fails
  with `VZErrorDomain Code=12`.
- `10-build-transport.md` measured resume slower than cold boot with a built project.
  Not worth it on the base image.
- The two-VM cap is enforced by the framework. An image build uses one slot.

## Per-project dependencies

Put SwiftPM, DerivedData, Pods, `node_modules` on an attached disk versioned by lockfile
hash (`tart run --disk=...:ro`), 1-20 GB. Do not use virtiofs (`--dir`) for build caches;
many-small-file latency dominates.

## Secrets

- Bake only public material (Apple CAs, GitHub `known_hosts`).
- Per run: seed disk or a read-only `--dir` mount, deleted after the run.
- Never `tart push` a VM that has held a secret.
- `codesign` needs `security set-key-partition-list -S apple-tool:,apple:,codesign: ...`,
  not just an unlocked keychain.

## Display and coordinates

- Base image is 1024x768. Pin it: `tart set <vm> --display 1512x982pt --no-display-refit`.
- `screencapture` returns pixels; `CGEventPost` takes points. The daemon hides this: callers
  send fractions 0 to 1 and the manager converts (`apps/daemon/CLAUDE.md`). This doc
  originally proposed pixel coordinates; the fraction design replaced it.

## Open

- Does resume beat cold boot on an Xcode image with a GUI session?
- Does `--stacked` save wire bytes between sibling images in practice?
- How do we explain "verified on a SIP-disabled machine" to a reviewer?

## Sources

- [apple/device-management PPPC schema](https://github.com/apple/device-management/blob/release/mdm/profiles/com.apple.TCC.configuration-profile-policy.yaml)
- [cirruslabs/macos-image-templates](https://github.com/cirruslabs/macos-image-templates)
- [actions/runner-images configure-tccdb-macos.sh](https://github.com/actions/runner-images/blob/main/images/macos/scripts/build/configure-tccdb-macos.sh)
- [Tart FAQ](https://tart.run/faq/), [Packer builder for Tart](https://developer.hashicorp.com/packer/integrations/cirruslabs/tart/latest/components/builder/tart)
- Tart issues [725](https://github.com/openai/tart/issues/725) (seed volumes),
  [803](https://github.com/openai/tart/issues/803) (`--suspendable`),
  [1161](https://github.com/openai/tart/issues/1161) (`--dir` vs `--disk`)
