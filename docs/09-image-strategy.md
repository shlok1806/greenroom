# Image strategy: how a greenroom machine boots ready

Status: proposed 2026-09-21. Answers the "must be baked into the image" risk in
`docs/00-idea.md`, and the question of how a machine becomes this run's machine.

Anything marked **[measured]** was run on the dev host: macOS 27.0 build 26A428, Apple
silicon, Tart 2.32.1, against `ghcr.io/cirruslabs/macos-tahoe-base`. Everything else is
cited from upstream source.

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

## 2. Toolchain: three published layers, joined by stacked clones

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
macOS 27 with DiskImageKit. It keeps a private ASIF overlay over a shared read-only base,
so pulling a sibling only downloads immutable files not already cached. The dev host runs
Tart 2.32.1 and needs upgrading **[measured]**. Keep lineages shallow, since each parent
overlay costs assembly time at run. Note this pulls against ADR 0010: 2.36.0 converts to
Apache-2.0 in 2028.

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
   and the free-space and runtime assertions.
4. Per-VM identity comes from a read-only seed disk read by the baked-in first-boot
   daemon, not from offline image mutation.
5. Per-project dependencies come from a separate attached disk, not a fourth image.
6. Warm start waits until someone measures it on a heavy image.

## Open

- Does resume beat cold boot on an Xcode image with a logged-in GUI session? The
  warm-machines plan depends on this and nobody has measured it.
- Does `--stacked` make the project layer thin on the wire in practice, and is pinning
  Tart 2.36.0 acceptable under ADR 0010?
- Do offline writes survive boot as reliably as reads? Only reads and one hostname
  injection were checked.
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
