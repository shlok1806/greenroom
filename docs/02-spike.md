# M0 spike results

Date: 2026-09-11 to 2026-09-12. Host: Apple silicon Mac, macOS 26.7, 36 GB RAM.
Guest: `ghcr.io/cirruslabs/macos-tahoe-base:latest` (macOS 26.6.2), Tart 2.32.1.
No code was written; everything below was done with the Tart CLI by hand.

## Numbers

| Step                                            | Measured                         |
| ----------------------------------------------- | -------------------------------- |
| `tart pull` of the base image (27 GB compressed)| 15 min                           |
| Disk used by the pulled image                   | 28 GB (90 GB free before, 62 after) |
| `tart clone` of the image                       | 0.07 s, no extra disk (APFS clone) |
| Boot to `tart ip` answering                     | 13 s                             |
| Boot to `tart exec` answering                   | 32 s                             |
| `tart exec` round trip, first call after boot   | 0.5 to 0.9 s                     |
| `tart exec` round trip, warm                    | 50 to 70 ms                      |
| `screencapture` inside the guest, 1024x768 PNG  | 0.7 s                            |
| Transfer that PNG back via `tart exec` + base64 | 0.3 s                            |
| Same via `scp`                                  | 0.44 s                           |
| `rsync` this repo into the guest, first time    | 0.49 s                           |
| `rsync` again, no changes                       | 0.31 s                           |
| Launch TextEdit and screenshot it               | 5.6 s including a 3 s sleep      |
| `tart suspend` of an 8 GB VM                    | returns instantly, finishes in about 90 s |
| Resume from suspend                             | **failed**, see below            |

## What works out of the box

- **`tart exec` needs no network or keys.** The Cirrus image ships the Tart guest
  agent as a launchd agent for `admin`. Commands, screenshots and small file transfers
  can all go through it. ssh is only needed for rsync.
- **The guest is a logged-in desktop.** `admin` owns the console, a Terminal is open
  from image build, screen resolution is 1024x768. `screencapture` produced a real
  screenshot of the desktop on the first call.
- **Screen capture and accessibility are already granted.** SIP is disabled in the
  image and the TCC database already holds `ScreenCapture`, `Accessibility`,
  `PostEvent` and `Microphone` for `tart-guest-agent` with `auth_value = 2`.
- **Toolchain in the base image:** Command Line Tools, Swift 6.3.3, git, node, brew.
  No Xcode, so Swift Package Manager builds work but `xcodebuild` on a project does not.
- **ssh with a key works** after appending the host public key to
  `~/.ssh/authorized_keys` through `tart exec`. rsync over it is sub-second for a
  small repo.

## What does not, and what to do about it

- **macOS still prompts for screen recording.** After the first screenshot the guest
  showed the macOS periodic "tart-guest-agent is requesting to bypass the system
  private window picker" reminder. The permission is granted; this is the reminder
  dialog macOS 15+ shows for screen-recording apps. It sits on top of everything and
  will appear in screenshots. Fix in the image: silence the reminder in
  `~/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist`
  or the equivalent for this macOS, and verify by taking several screenshots over
  time. To be done when building the greenroom base image.
- **Automation (AppleEvents) is not granted.** `osascript` targeting System Events
  hangs on a permission prompt; the TCC row for `kTCCServiceAppleEvents` has
  `auth_value = 0`. Fix in the image: insert the row with the System Events bundle id
  as the indirect object, which SIP being off allows. Needed for M2, not M1.
- **Modal prompts block the agent's view.** Both prompts above stayed on screen until
  killed with `pkill -x UserNotificationCenter`. The daemon should never rely on
  prompts being absent; the image must be prompt-free and a screenshot with an
  unexpected dialog is a bug to surface, not hide.
- **Suspend and resume is broken here.** `tart suspend` wrote a 3 GB `state.vzvmsave`,
  but `tart run` failed to restore twice: `VZErrorDomain Code=12` with "invalid
  argument" then "permission denied". Possibly the macOS 26.7 host, possibly running
  Tart from a detached non-interactive process. Not worth more spike time: a cold
  boot to a working shell is 32 s, which is acceptable for v0. Warm machines, if
  wanted later, come from keeping one booted clone idle rather than from suspend.
  Track upstream before building on it.
- **Disk.** The Xcode images are about 69 GB compressed and do not fit next to the
  host's Xcode with 61 GB free. Options: free space on this Mac, or build an image
  with only the Command Line Tools plus the iOS simulator runtime, or accept base-only
  until a Mac with more disk hosts the daemon.

## Implications for the daemon

1. Use `tart exec` as the primary channel. Open one exec per call is fine at 50 to
   70 ms warm; the first call after boot is slow because the agent is starting.
   Poll `tart exec <vm> true` to detect readiness rather than `tart ip`.
2. Transfer screenshots through exec with base64 for v0. It is within 0.15 s of scp
   and needs no key. Switch to ssh if images get large or exec proves flaky.
3. Sync with rsync over ssh; install the daemon's key through exec at first boot.
4. Budget about 35 s from `machine.create` to a usable machine. Report progress to
   the agent rather than blocking silently.
5. Build a greenroom base image from the Cirrus base: silence the screen-recording
   reminder, grant AppleEvents, install the ssh key, optionally set a larger display.
   Snapshot that as the image every run clones. This is an M1 task, not M2.
6. The base image is enough for M1's "build, launch, screenshot" loop using a Swift
   Package Manager app. Xcode comes when disk allows.
