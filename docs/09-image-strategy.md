# Image strategy: how a greenroom machine boots ready

Status: proposed 2026-09-21. Answers the "must be baked into the image" risk in
`docs/00-idea.md`, and the question of how a machine becomes this run's machine.

Anything marked **[measured]** was run on the dev host: macOS 27.0 build 26A428, Apple
silicon, against `ghcr.io/cirruslabs/macos-tahoe-base`. Everything else is cited from
upstream source. Measurements from 2026-09-21 and earlier were on Tart 2.32.1; those
marked **[measured 2026-09-21, Tart 2.37.0]** were taken after the upgrade.

The host had 19 GB free, which is why the `greenroom-xcode<N>` layer is written but
unbuilt: it needs roughly 69 GB of pull and 140 GB of virtual disk. Every disk figure
below is a `df` delta on the APFS container, never `du`, which counts shared clone
extents and overstates `~/.tart` by several times.

## The question

`docs/00-idea.md` wants a machine that boots to a snapshot with the toolchain ready.
Three things have to be true at first boot, and they have different answers:

1. Permissions. The agent takes screenshots, reads the accessibility tree and posts
   events, with nobody there to click an approval dialog.
2. Toolchain. Xcode and project dependencies present, caches warm.
3. Identity. Hostname, SSH key, repo, branch, tokens, all different per run.

Items 1 and 2 belong in a published image. Item 3 does not. It changes every run, and a
secret pushed into an image cannot be taken back out.

## 1. Permissions: SIP off, TCC written at build time

MDM cannot do this. Apple's payload schema in `apple/device-management`, file
`com.apple.TCC.configuration-profile-policy.yaml`, says of ScreenCapture: "A profile
can't grant access to the contents; it can only deny it." Same for ListenEvent, Camera
and Microphone. PPPC also needs a user-approved MDM enrolment, since the schema sets
`allowmanualinstall: false`, so `profiles install` will not apply it. On macOS 27 the
PPPC `Accessibility` key is deprecated in favour of a declarative `PermissionDefaults`
that is a default rather than a grant, is user-overridable, shows a consent prompt, and
covers neither ScreenCapture nor PostEvent. Apple is closing this route, not opening it.

So everyone ships a SIP-disabled image with TCC.db written at build time. Cirrus does it
in `scripts/update-tcc-database.sh`, GitHub Actions runner images in
`configure-tccdb-macos.sh`, MacStadium Orka sells `:latest-no-sip` as a product, and
CircleCI's docs say testing macOS apps only works on images with SIP disabled.

The base image already does it. **[measured]** Reading the guest's TCC.db offline from
the host, `ghcr.io/cirruslabs/macos-tahoe-base` ships with `auth_value=2`, meaning
allowed:

```
kTCCServiceAccessibility | /usr/libexec/sshd-keygen-wrapper | 2
kTCCServiceScreenCapture | /usr/libexec/sshd-keygen-wrapper | 2
kTCCServicePostEvent     | /usr/libexec/sshd-keygen-wrapper | 2
kTCCServiceAppleEvents   | /usr/libexec/sshd-keygen-wrapper | 2
  ... the same four for /usr/bin/osascript and .../tart-guest-agent
```

TCC checks the responsible process, which is the ancestor that spawned the caller. So
granting `sshd-keygen-wrapper` means anything greenroom runs over SSH can already take
screenshots and post events. Grant entry points, not the binary that makes the call.
Chasing "which binary needs the permission" is how runner-images issue #8951 ate a week
of someone's time.

greenroom's own layer still has to add four things:

- Rows for greenroom's Swift binary, by realpath, with `client_type=1`. One `INSERT`. The
  base image already has SIP off, so nothing else is needed, and no `csreq` blob either.
- Runtime lookup of the per-user TCC database. macOS 27 moved it to
  `/private/var/containers/Data/ProtectedSystem/<uuid>/.../TCC.db`. Writing to the old
  `~/Library/...` path creates an empty database there and the grants go nowhere, with no
  error. Find the live one with `sudo lsof -c tccd -Fn` and fail on zero or more than one
  match. Copy Cirrus's `resolve_user_tcc_database()` rather than writing it again.
- The Sequoia and later screen-recording reminder, which is not TCC. It lives in
  `~/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist`,
  fires monthly, takes focus and lands in screenshots. GitHub's runner images write a
  year-3024 date to stop it. Also prefer ScreenCaptureKit over the older
  `CGWindowListCreateImage` call, which is what usually triggers it.
- `automationmodetool enable-automationmode-without-authentication`, driven by `expect`,
  or Apple Events prompts for a password.

Use named-column `INSERT`, never positional. The `access` table gained four columns in
Sonoma, which is why runner-images issues #8214 and #9529 exist.

Anything that touches the screen has to run as a LaunchAgent, not a LaunchDaemon. A
daemon gets no Aqua session and no WindowServer connection, so there is nothing to
capture and no session to post events into. That makes auto-login for `admin` a
requirement rather than a convenience. A resumed VM sitting on the login window looks
exactly like a TCC failure at the screenshot layer.

The build has to smoke-test this. TCC never returns an error. A missing grant gives you
a black frame and `AXIsProcessTrusted() == false`. So the build takes a screenshot and
checks it is not a uniform frame, checks `AXIsProcessTrusted()`, posts a CGEvent and
checks it landed, and fails otherwise. Silent no-ops are how TCC provisioning rots, and
finding out mid-run in a customer's PR is expensive.

One cost to be honest about: the image has SIP disabled. A reviewer can fairly ask what
"we verified it" means on such a machine. SIP-off affects kext loading, debugger attach
and filesystem protection, not normal app behaviour, but say so up front rather than let
someone find it.

## 2. Toolchain: three published layers, joined by plain local clones

> **Decided 2026-09-22: greenroom does not use `tart clone --stacked`.** The measurements
> below are what settled it. A plain local clone already costs 0.027 s and zero disk
> through APFS `clonefile(2)`, while a stacked clone takes 30 to 58 s and needs its
> parent pushed to a registry first. Stacked clones only pay off across several hosts
> pulling the same lineage, which is a fleet problem greenroom does not have yet. The
> rest of this section keeps the evidence so the decision can be revisited when there is
> a fleet.


```
ghcr.io/cirruslabs/macos-tahoe-base     upstream, MIT recipes, SIP off, TCC seeded
  └── greenroom-base                    our TCC rows, guest binary, display, firstboot daemon
        └── greenroom-xcode<N>          Xcode, simulators, warm caches
```

Each stage is its own `packer build` starting from the previous VM through
`vm_base_name`, so the IPSW is only ever restored once.

Do not build the vanilla layer. It takes one to three hours and its only lasting output
is about 25 blind VNC keystrokes through Setup Assistant that Apple breaks every point
release. macOS 26.4 added an "Age Range" pane and turned the Apple ID skip button into a
menu, which hung builds until issues #341 and #342 were fixed. Only build it if we need
a macOS version Cirrus does not publish.

Sizes, from GHCR manifest annotations:

| Image | Virtual disk | Compressed pull |
| --------------------- | ------------ | --------------- |
| `macos-tahoe-vanilla` | 50 GB | 24.0 GB |
| `macos-tahoe-base` | 50 GB | 27.3 GB |
| `macos-tahoe-xcode` | 140 GB | 69.3 GB |
| `macos-runner:tahoe` | 520 GB | 189.2 GB |

The "image size 50GB+" line in `docs/00-idea.md` is low by about 1.4x compressed and
2.8x uncompressed once Xcode is in.

Registry layers are not semantic deltas. Tart cuts the disk into fixed 512 MiB
offset-aligned chunks and gzips each one as a layer, so only byte-identical aligned
regions dedupe. Base and xcode share 34 of 263 layers. Pulling `xcode` when you already
have `base` still downloads about 53 GB. One extra `brew install` shifts file offsets and
invalidates chunks well past the bytes it changed.

`tart clone --stacked` fixes this. It shipped in Tart 2.36.0 on 2026-08-25 and needs
macOS 27 with DiskImageKit. It keeps a private ASIF overlay over a shared read-only base.
Note this pulls against ADR 0010: 2.36.0 converts to Apache-2.0 in 2028.

### `--stacked` only stacks on a registry image **[measured 2026-09-21, Tart 2.37.0]**

This is the correction that matters most to the plan above, and it was found by running
it rather than reading about it:

```
$ tart clone --stacked greenroom-base m-stacked
Error: --stacked requires a remote image
```

A stacked clone's parent must be an OCI image in the local cache, never a local VM. So
the three-layer picture at the top of this section cannot be joined by local stacked
clones. `greenroom-base` and `greenroom-xcode<N>` have to be **pushed to a registry** and
pulled before anything can stack on them. A Packer build's local output is a dead end for
stacking; it is only a thing to push.

That has a consequence worth stating plainly: because layers are immutable and public
once pushed, and because a stacked child needs its parent in a registry, the rule that no
secret may ever enter an image stops being a matter of hygiene and becomes structural.

Measured, host at 19 GB free, all figures from `df` deltas on the APFS container. Never
`du`: it counts shared clone extents and reports `~/.tart` at 122 GB when the real
occupancy is a fraction of that.

| Operation | Time | Real disk |
| ------------------------------------------- | ------- | --------- |
| `tart clone` (local, 31 GB VM) | 0.027 s | 0 MB, APFS `clonefile(2)` |
| `tart clone --stacked` from cached OCI, 1st | 57.8 s | 38 MB |
| `tart clone --stacked` from cached OCI, 2nd | 30.7 s | 38 MB |
| `tart clone --stacked` from cached OCI, 3rd | 29.5 s | 38 MB |

The 38 MB is a 4 MB `overlay.asif` plus a 32 MB `nvram.bin`. The stacked VM directory
holds `overlay.asif` and the 96-layer `manifest.json` instead of a 47 GB `disk.img`.

Two things follow, and they point in opposite directions:

- On disk at rest, `--stacked` wins nothing here. A plain local clone already costs zero
  because of `clonefile(2)`. The overlay's value is that it is *thin in absolute terms*
  rather than thin *relative to a parent it must be on the same volume as*.
- In time, `--stacked` is about a thousand times slower per clone: 30 s against 0.027 s.
  That is a real problem for the daemon, because `Create` holds `createMu` for its whole
  duration (see `apps/daemon/CLAUDE.md`). Serializing a 0.1 s clone costs nothing;
  serializing a 30 s one means the second concurrent `machine_create` waits half a minute
  before it even starts booting. If greenroom adopts stacked clones for per-run machines,
  the clone has to come out from under that lock first.

`tart set <vm> --disk-size` on a stacked overlay **works** **[measured]**. After
`--disk-size 100` the guest saw a 100.0 GB `disk0` and a 99.5 GB APFS container, with
93 GiB on `/`, while the host-side overlay was still only 745 MB after one boot. A booted,
personalized VM that had been through two reboots had a 1.1 GB overlay. So roughly
**1 GB of real disk per running machine** is the number to plan with, against 50 GB of
apparent disk.

What is still unmeasured is the claim this section opened with: that pulling a sibling
image downloads only what is not already cached. Testing it needs a second real image in
a registry, and the Xcode layer that would be the sibling could not be built on this host
(19 GB free against roughly 69 GB of pull and 140 GB of virtual disk). Treat the wire
saving as unverified.

### Tart is not installable from Homebrew any more **[measured 2026-09-21]**

`brew upgrade tart` does not work, and the instruction to run it was wrong:

```
Error: cirruslabs/cli/tart: Calling `depends_on :macos` with `depends_on macos:`
is disabled!
```

The `cirruslabs/homebrew-cli` tap is stale at tart 2.32.1 and its formula no longer
evaluates against current Homebrew, because it carries `depends_on :macos` twice. The tap
has not been updated since the move described in ADR 0010: `github.com/cirruslabs/tart`
no longer resolves and the project lives at `github.com/openai/tart`, where the latest
release is **2.37.0**.

Install from the release tarball instead, which is still signed by Cirrus Labs
(`Developer ID Application: Cirrus Labs, Inc. (9M2P8L4D89)`) and published with a
checksums file. On the dev host 2.37.0 was installed side-by-side under
`~/.local/tart-2.37.0/` and `/opt/homebrew/bin/tart` was deliberately left at 2.32.1, so
the user's running daemon was not swapped underneath while it held a VM.

Every `tart` argument shape the daemon uses is unchanged in 2.37.0, so
`apps/daemon/internal/tart` does not break on the upgrade: `clone`, `delete`, `stop`,
`ip`, `list --format json`, `run <name> <mode>` and `exec` all take the same arguments.

### `tart exec -t` needs a terminal on the *host* **[measured 2026-09-21]**

`tart exec` supports `-i` (attach stdin) and `-t` (allocate a remote pty), in 2.32.1 as
well as 2.37.0, and that is how a guest command gets a real terminal without any
guest-side helper to install or version. It is what interactive sessions need, because
`xcodebuild` and most test runners branch on `isatty()`.

There is a trap in it, and it cost a failed end-to-end run to find. `-t` reads the
terminal size from **tart's own stdin** and does not degrade when there is not one. It
crashes:

```
$ printf '' | tart exec -i -t <vm> tty
tart/Exec.swift:87: Fatal error: 'try!' expression unexpectedly raised an error:
failed to get terminal size: Inappropriate ioctl for device
```

A daemon's stdin is never a terminal, so `-t` invoked from one produces a session that is
dead on arrival. The fix is for the caller to allocate a **host-side pty** and hand tart
the slave. With that, it works, verified against a booted VM:

```
$ pty.spawn([tart, "exec", "-i", "-t", vm, "sh", "-c", "tty; test -t 0; test -t 1"])
/dev/ttys001
STDIN_TTY
STDOUT_TTY
```

Two further notes for anyone implementing this: set the window size explicitly on that
pty, since a 0x0 one is what crashes tart and a small one makes build output wrap oddly;
and reading a pty master on macOS returns `EIO` when the child exits, which is a normal
end of session rather than a failure.

### Old tart cannot read a stacked VM **[measured 2026-09-21]**

A VM created by `clone --stacked` has `overlay.asif` and no `disk.img`, and 2.32.1 does
not know what that is:

```
$ /opt/homebrew/bin/tart exec -i -t m-pty tty      # 2.32.1, VM made by 2.37.0
VM is missing some of its files (config.json, disk.img or nvram.bin)
```

So the daemon and the `tart` on `PATH` have to move together the moment stacked clones
are used. A side-by-side install is fine for measuring, but it is not a way to run two
tart versions against one set of VMs.

Installing Xcode needs no Apple ID in the build. `xcodes` wants credentials and 2FA has
no unattended path, so neither Cirrus nor GitHub authenticates during the build. Both
fetch the `.xip` once, out of band, and pass it in as a file. Cirrus keeps
`~/XcodesCache/Xcode_<v>.xip` on the build host, file-provisions it, then runs
`xcodes install --path`. Do that. Apple ID credentials never go in the pipeline.

Two assertions worth copying, because both failures otherwise show up at agent time:

- Free space floor. Run `df -m` after provisioning and fail under 15 GB.
- Simulator runtimes diffed against a checked-in expected list, failing on drift.

Also run `xcrun simctl runtime dyld_shared_cache update --all` at bake time, or
`update_dyld_sim_shared_cache` takes a core for minutes after every boot, which is the
CPU the agent wanted.

## 3. Identity: seed volume, not image surgery

This one was tested rather than reasoned about.

Offline mutation works. **[measured]** The guest's Data volume reports `Sealed: No,
FileVault: No`. It is not encrypted, which contradicts the common claim that Apple
silicon always encrypts it. That claim holds for physical internal storage wired to the
AES engine. A VM's `disk.img` is a file on the host and the guest has no Secure Enclave.
So this works:

```sh
hdiutil attach -imagekey diskimage-class=CRawDiskImage -nomount disk.img
diskutil mount disk12s5     # Data
```

`/Users`, `/Library`, `/private`, `/etc` and `/usr/local` are all writable, since they
are firmlinks onto Data and the sealed System volume never needs touching. Injecting a
hostname with `plutil` into `preferences.plist` survived a boot.

It still should not be the mechanism. Without host `sudo` the volume mounts `noowners`,
files land as uid 501, and launchd refuses any LaunchDaemon plist not owned by
`root:wheel`. What you get is `Bootstrap failed: 5: Input/output error` and no hint that
ownership was the cause **[measured]**. A provisioning step that fails silently is the
worst kind.

The seed volume avoids all of it, and works. **[measured]**

```sh
hdiutil create -volname GRSEED -srcfolder seed/ -format UDRW -fs HFS+ -layout NONE seed.dmg
tart run --no-graphics --disk "$PWD/seed.dmg:ro" <vm>
```

The guest mounts it at `/Volumes/GRSEED`, the contents are readable, `:ro` is enforced,
and the image is 1.9 MB and builds in under a second. `-format UDRO` fails: Tart rejects
compressed images with "Invalid disk image. The disk image format is not recognized."

The first-boot LaunchDaemon goes into the base image once, as `root:wheel`, installed by
a process that has root inside the guest. Per VM we then only produce data. That means no
host sudo, no `hdiutil attach`, no mount and detach race, no risk to the base image, and
a per-VM artifact that is a JSON file we can log, diff and attach to the run record.
That last part is what `00-idea.md` wants from evidence.

`diskarbitrationd` mounts asynchronously, so a `RunAtLoad` daemon can start before the
seed is there. Poll for the file.

There is no cloud-init for macOS guests. The NoCloud seed ISO is the maintainers'
answer for Linux guests only. On macOS we supply the agent, which is the baked-in daemon
above.

### Measured timings, and what they say about warm machines

| Operation | Time | Note |
| ----------------------------- | -------- | --------------------------------- |
| `tart clone` (31 GB) | 0.074 s | APFS `clonefile(2)`, no extra disk |
| Cold boot to SSH ready | 15 s | |
| `tart suspend` | ~2 s | writes 3.3 GB `state.vzvmsave` |
| Clone a suspended VM | 0.039 s | state file copied too |
| Resume the clone to SSH ready | 14 s | |

`tart suspend` buys one second and costs a concurrency limit. Tart keeps the MAC address
when cloning a suspended VM, because regenerating it invalidates the snapshot. So N
clones of one snapshot share a MAC, collide on the NAT network and break `tart ip`. It
fails closed, since `tart run` refuses a VM whose MAC matches a running one, but you get
one running descendant per suspended snapshot.

This contradicts the warm-machines assumption in `docs/03-tech-stack.md`. On the base
image that assumption has no measurement behind it. It may hold once the image carries
Xcode and a logged-in GUI session, which is where resume should start to pay, but measure
it there before anything depends on it.

### Suspend and resume on a stacked VM **[measured 2026-09-21, Tart 2.37.0]**

Re-measured on a stacked clone of the base image, started with `--suspendable`. Timings
are to `tart exec` answering, which is the guest agent being up, not merely an IP:

| Operation | Time | Cost |
| ----------------------------------- | ----- | ------------------------- |
| Cold boot, stacked VM | 29 s | |
| `tart suspend` | ~0 s | 1.5 GB `state.vzvmsave` |
| Resume to guest agent ready | 19 s | |

So suspend and resume do work on a stacked overlay, which was not obvious beforehand, and
the state file sits alongside `overlay.asif` in the VM directory. Resume saved 10 s
against a cold boot of the same VM, rather than the 1 s measured earlier on a plain clone.

This still does not settle the warm-start question. This is the light base image with no
logged-in GUI session and no Xcode, and 10 s is not enough on its own to accept a 1.5 GB
per-snapshot cost and the one-running-descendant-per-snapshot limit described above. The
measurement that decides it is still the one on a heavy image, and it is still not done,
because the Xcode image could not be built here.

`--suspendable` is required and its absence fails silently. **[measured]** Without it,
`tart suspend` looks like it worked and writes a 2.9 GB state file, then the resume fails
with `VZErrorDomain Code=12 "invalid argument"`, including for the original VM. This is
probably what `docs/02-spike.md` hit, so re-run that before trusting its conclusion. Any
greenroom code path that suspends should check the VM started with `--suspendable`.

The two-VM cap comes from the framework, not from licensing **[measured]**: `The number
of VMs exceeds the system limit`. An image build takes one of the two slots, so the
daemon has to count image builds as machine slots.

## Per-project dependencies

Do not bake an image per project. At 69 GB compressed and one to three hours per build,
every base-OS or Xcode refresh multiplies by the number of customers. Publish the
project's dependency cache as its own disk instead, covering SwiftPM, DerivedData,
CocoaPods, node_modules, Gradle and the checked-out repo, then attach it:

```sh
tart run vm --disk="ghcr.io/greenroom/deps-<project>:<lockfile-sha>:ro"
```

`--disk` takes a disk image file, a block device, a remote VM name, or an NBD URL. That
is 1 to 20 GB, rebuilds in minutes, and versions per lockfile hash. DerivedData matters
most: an agent verifying a one-line fix should get an incremental build, not a cold one.

Use `--dir`, which is VirtioFS, for the working tree and for evidence coming back out.
Do not use it for a hot build cache. Many-small-files latency dominates SwiftPM and
DerivedData builds.

## Secrets

Image holds the public toolchain. Run holds the secrets. Every CI provider works this
way and greenroom has no reason to differ.

- Bake only public material: Apple WWDR and DeveloperID CAs, GitHub `known_hosts`.
- Per run, mount `--dir="secrets:~/.greenroom/runs/$ID:ro"` and delete it when the run
  ends.
- Never `tart push` a VM that has had a secret in its keychain or `~/.ssh`. Layers are
  content-addressed and immutable, so you cannot un-publish one.
- `codesign` needs the keychain partition list, not just an unlock. `security
  unlock-keychain` on its own still fails with `errSecInternalComponent`. The fix is
  `security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$PW" "$KC"`,
  and leaving out `codesign:` is the most common macOS CI failure there is.

## Display and coordinates

The base image defaults to 1024x768 **[measured]**, too small to verify an app. Pin it:
`tart set <vm> --display 1512x982pt --no-display-refit`. The `pt` suffix is what controls
Retina. Turn display-refit off, since a display that resizes mid-run invalidates cached
coordinates. Only one display is supported.

`screencapture` returns physical pixels. `CGEventPost` takes logical points. On a HiDPI
guest those differ by the backing scale factor. Let the guest-side Swift CLI own that
conversion and keep pixel coordinates off the model's side of the API: `screenshot`
returns `{image, width_px, height_px, scale}`, and `click` takes coordinates in the same
pixel space as the image it was handed and divides internally. Read the scale at runtime
and never hardcode 2. A vision model looking at a 2x screenshot will report pixel
coordinates from that image, and making the caller remember a scale factor produces
clicks that land wrong about half the time, which is hard to debug from a run report.

Record the resolution in the run manifest. A screenshot whose resolution you cannot
reconstruct is not evidence.

## Decision

1. Start from `ghcr.io/cirruslabs/macos-tahoe-base`. Do not build vanilla.
2. `greenroom-base` adds TCC rows for our guest binary with the macOS 27 user-DB lookup,
   the screen-recording reminder fix, `automationmodetool`, a pinned display, the
   first-boot LaunchDaemon, our SSH key, and a smoke test that fails the build.
3. `greenroom-xcode<N>` adds Xcode from a pre-fetched `.xip`, simulators, warm caches,
   and the free-space and runtime assertions. Written as
   `images/greenroom-xcode.pkr.hcl` and **not yet built**: it needs roughly 200 GB free
   and a `.xip` a person has to download by hand.
4. Per-VM identity comes from a read-only seed disk read by the baked-in first-boot
   daemon, not from offline image mutation. **Verified end to end.**
5. Per-project dependencies come from a separate attached disk, not a fourth image.
6. Warm start waits until someone measures it on a heavy image.
7. **Per-run machines are plain local clones. Stacked clones are not adopted.** Decided
   2026-09-22 on the measurements in section 2: a local clone is 0.027 s and free, a
   stacked clone is 30 to 58 s and costs about 1 GB of overlay per running machine.
   `Create` holds `createMu` for the whole clone, so a 30 s clone would stall a second
   `machine_create` for half a minute. Stacked clones also require pushing both greenroom
   layers to a registry, since `--stacked` refuses a local VM.

   Revisit when there is more than one host. The saving stacked clones offer is on the
   wire between hosts pulling one lineage, and that saving is still unmeasured because it
   needs a second real image in a registry. Two things would have to change first: the
   clone would have to come out from under `createMu`, and the daemon would have to move
   to Tart 2.37.0, since 2.32.1 cannot read a stacked VM at all.

8. **The daemon calls a pinned `tart`, not whatever is on `PATH`.** Decided 2026-09-22.
   Homebrew cannot deliver a current Tart: the `cirruslabs/homebrew-cli` tap is abandoned
   at 2.32.1 and its formula no longer evaluates. Version drift between the host and the
   daemon is a real failure mode, shown by 2.32.1 being unable to read a stacked VM, so
   the version the daemon expects is recorded in the repo and invoked by absolute path.

## The first-boot daemon, run for real **[measured 2026-09-21]**

Until now the seed-volume path had been reasoned about and never executed. It has now
run in a booted VM, end to end, and it works. What follows is what it took.

The daemon and its plist were installed inside a running guest exactly as the Packer
build installs them, `root:wheel`, the VM was stopped, and it was restarted with
`--disk seed.dmg:ro`. Verified after boot:

- the seed mounts at `/Volumes/GRSEED` and `:ro` is enforced
  (`touch` → `Read-only file system`)
- `plutil -extract <key> raw -o -` reads the JSON correctly. This had been flagged as
  suspect and is not: it returns the bare value for every key.
- `ComputerName`, `LocalHostName` and `HostName` are all set
- `authorized_keys` and `run.env` land as `admin:staff` mode 600
- the repo clones as `admin:staff` and `git log` works in it as `admin`
- the stamp at `/var/db/.greenroom-personalized` is written `root:wheel`

**The one real failure was not in the script.** The first attempt produced no log at all,
and `launchctl bootstrap` returned the exact error this document warns about,
`Bootstrap failed: 5: Input/output error`. The cause was not ownership. The plist and the
script were **simply gone after the reboot**: `tart stop` immediately after writing them
did not flush the guest's dirty pages. Adding `sync` before the stop fixed it completely.

This is the same failure `apps/daemon/CLAUDE.md` records for `PrepareGuest`, hit again
from a different direction, which makes it a property of Tart and the guest rather than a
quirk of one script. Anything that writes into a guest and then stops it must `sync`
first. Note also that `Bootstrap failed: 5` means "launchd could not load this job" and
says nothing about *why*: here it meant the file did not exist. Check that the file is
still there before believing it is an ownership problem.

Three bugs in `firstboot.sh` were found by reading it and fixed:

- **`git clone` could hang forever.** A LaunchDaemon has no terminal, so a private repo
  over https blocks on a username prompt and an unknown host key blocks on the yes/no
  question, with nothing to answer either. Fixed with `GIT_TERMINAL_PROMPT=0` and
  `BatchMode=yes`. Confirmed by running it against an inaccessible repo: it now fails in
  seconds with `terminal prompts disabled` instead of blocking until the run times out.
- **A failed clone still marked the VM personalized.** The script has no `set -e`, so the
  unconditional `touch` at the end ran anyway and the next boot skipped personalization.
  Now the stamp is written only when every step succeeded, so a failure retries.
- **`sudo -u admin` kept root's `HOME`**, so git read `/var/root/.gitconfig`.

The script also now writes `/var/db/greenroom-firstboot.json` with a status, the values
it applied and a list of failed steps. That is the per-VM artifact this document asked
for: something the daemon can read and attach to the run record. Verified in both
directions, `{"status":"ok",...,"failed":[]}` and
`{"status":"failed",...,"failed":["clone"]}`, and in the failing case the hostname and
keys were still applied and no stamp was written.

There is no `timeout` binary on macOS, so there is deliberately no wall-clock cap on the
clone. Closing the prompts removes the hang; a slow clone is just slow, and the run has
its own timeout above this.

## Open

- Does resume beat cold boot on an Xcode image with a logged-in GUI session? Still open,
  and still the question the warm-machines plan depends on. Resume now measures 19 s
  against a 29 s cold boot on the *light* stacked base image, which is suggestive and not
  sufficient. The Xcode image could not be built on this host.
- Does `--stacked` make the project layer thin on the wire in practice? Still open, and
  it now needs both layers pushed to a registry, because a stacked clone cannot have a
  local VM as its parent.
- What does a 30 s stacked clone do to `machine_create`, which holds `createMu` for the
  whole clone? This is a new question the measurement created.
- Do offline writes survive boot as reliably as reads? Still only reads and one hostname
  injection were checked, and the seed volume means we no longer need the answer.
- How do we describe "verified on a SIP-disabled machine" to a reviewer?

## Sources

- [apple/device-management, PPPC payload schema](https://github.com/apple/device-management/blob/release/mdm/profiles/com.apple.TCC.configuration-profile-policy.yaml)
- [cirruslabs/macos-image-templates](https://github.com/cirruslabs/macos-image-templates),
  `templates/{base,xcode,vanilla-tahoe,disable-sip}.pkr.hcl`, `scripts/update-tcc-database.sh`
- [actions/runner-images, configure-tccdb-macos.sh](https://github.com/actions/runner-images/blob/main/images/macos/scripts/build/configure-tccdb-macos.sh)
- [Tart FAQ](https://tart.run/faq/), stacked disk images, `~/.tart` layout, pruning
- [Tart 2.36.0 release](https://github.com/cirruslabs/tart/releases/tag/2.36.0)
- [Packer builder for Tart](https://developer.hashicorp.com/packer/integrations/cirruslabs/tart/latest/components/builder/tart)
- [Apple, Volume encryption with FileVault](https://support.apple.com/guide/security/volume-encryption-with-filevault-sec4c6dc1b6e/web)
- [XcodesOrg/xcodes](https://github.com/XcodesOrg/xcodes)
- Tart issues [725](https://github.com/openai/tart/issues/725) on seed volumes,
  [803](https://github.com/openai/tart/issues/803) on `--suspendable`,
  [1161](https://github.com/openai/tart/issues/1161) on `--dir` versus `--disk`
