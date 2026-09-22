# M0 spike

**Result: Tart is a good enough base for v0.** A cloned VM runs a command 32 s after
boot, `tart exec` needs no network or keys, and screenshots work on the first try. The
image needs work (prompts, disk), and suspend/resume failed.

Done by hand with the Tart CLI, 2026-09-11 to 12. Host: Apple silicon, macOS 26.7, 36 GB
RAM. Guest: `ghcr.io/cirruslabs/macos-tahoe-base:latest` (macOS 26.6.2), Tart 2.32.1.

## Numbers

| Step | Measured |
| --- | --- |
| `tart pull` base image (27 GB compressed) | 15 min, 28 GB on disk |
| `tart clone` | 0.07 s, no extra disk (APFS clone) |
| Boot to `tart ip` | 13 s |
| Boot to `tart exec` | 32 s |
| `tart exec` round trip | 0.5-0.9 s first call, 50-70 ms warm |
| `screencapture` 1024x768 PNG in guest | 0.7 s |
| PNG back via `tart exec` + base64 | 0.3 s (scp: 0.44 s) |
| rsync this repo in | 0.49 s first, 0.31 s no change |
| `tart suspend` of 8 GB VM | ~90 s; resume **failed** |

## What works out of the box

- `tart exec` through the guest agent: no network, no keys. ssh is only needed for rsync.
- The guest is a logged-in desktop (`admin`, 1024x768).
- SIP is off and TCC already grants ScreenCapture, Accessibility, PostEvent, Microphone to
  `tart-guest-agent`.
- Toolchain: Command Line Tools, Swift 6.3.3, git, node, brew. No Xcode.
- ssh works after appending a key to `authorized_keys` via `tart exec`.

## What does not

- **Screen-recording reminder** dialog appears and lands in screenshots. Fix in the image
  (`ScreenCaptureApprovals.plist`).
- **AppleEvents not granted**: `osascript` to System Events hangs on a prompt. Fix in the
  image.
- **Modal prompts** block the view until killed. The image must be prompt-free.
- **Suspend/resume** failed with `VZErrorDomain Code=12`. Later traced to the VM not being
  started with `--suspendable` (`09-image-strategy.md`).
- **Disk**: the Xcode image (~69 GB compressed) did not fit.

## What the daemon took from this

1. `tart exec` is the main channel. Detect readiness by polling `tart exec <vm> true`, not
   `tart ip`.
2. Screenshots come back through exec as base64.
3. Sync is rsync over ssh; the key goes in through exec at boot.
4. Budget ~35 s from create to usable, and report progress rather than block.
5. Build a greenroom base image: silence the reminder, grant AppleEvents, bake the key.
