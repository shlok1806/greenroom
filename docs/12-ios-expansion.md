# iOS and iPadOS expansion: technical research

Date: 2026-09-25. Status: research, input for a grilling session. Nothing here is decided.

Builds on `00-idea.md`, `01-plan.md`, `04-landscape.md`, `09-image-strategy.md`, ADR 0012
(AX tree for aiming), ADR 0018 (one image recipe, dialog gate), ADR 0019 (toolchain
manifest) and issue #130 (no Xcode in any image). It does not repeat them.

How the facts were gathered:

- **Local, read-only, on the author's Mac** (Apple M3 Pro, 36 GB, macOS 27.0 build 26A428,
  Xcode 27.0 build 27A266a, tart 2.32.1): `xcrun simctl help <sub>`, `xcrun devicectl ...
  --help`, `xcodebuild -help`, `xcrun xcresulttool help`, `xcrun mcp-server --help`,
  `xcrun simctl runtime list -v`, `du`, the macOS license shipped in Setup Assistant. No VM
  and no simulator was booted. Output quoted below is from these commands on 2026-09-25.
- **Remote primary sources**: Apple developer docs (through their JSON endpoints, because
  the HTML pages are rendered client-side), Apple license PDFs, GitHub repos and issues read
  through the `gh` API, the GHCR registry API for image sizes, vendor docs and pricing pages.
- Every claim has a link or a command. Volatile facts carry "(as of 2026-09-25)". Things I
  could not check are marked **unverified**.

---

## TL;DR

1. **Xcode 27 (GA 2026-09-14) moved the ground.** Simulator.app is gone; `DeviceHub.app`
   (`com.apple.dt.Devices`) replaces it and also mirrors and drives *physical* devices.
   `devicectl` now lists and drives simulators too (orientation, screenshots, biometrics,
   VoiceOver). Xcode ships a headless MCP server whose `DeviceInteractionSynthesize` tool
   taps, swipes, types and returns a screenshot plus a UI hierarchy with frames. Anything
   written against Simulator.app (including Claude Code Desktop's pane) broke.
2. **The simulator runs in a macOS VM.** GitHub's arm64 macOS runners are
   Virtualization.framework VMs and run simulators every day; Cirrus ships `macos-*-xcode`
   Tart images with runtimes and a pre-built dyld cache. Known VM-specific failure:
   `Timeout waiting for screen surfaces` on screenshots under CPU contention. The guest GPU
   reports an old Metal family (Apple 5 era) unless a host default is flipped.
3. **The accessibility tree exists, but not through a public API.** Three sources give
   element frames for aiming: (a) idb / AXe, via CoreSimulator's accessibility bridge
   (`AccessibilityPlatformTranslation`, private), fast (tens of ms); (b) Xcode 27's MCP
   `DeviceInteractionSynthesize`, Apple-supported, richer (web text, widgets, status bar)
   but slow (0.2-0.9 s per read, 3.3-5.7 s per input, one session per simulator);
   (c) XCUITest snapshots (`XCUIElementSnapshot`), public but needs a runner app built and
   installed. Greenroom's existing `machine_ui` over the Device Hub window is a fourth,
   unmeasured, candidate.
4. **`simctl` has no tap, no rotate, no gestures.** Input needs idb / AXe (HID events into
   the simulator, private SimulatorKit), XCUITest, Xcode's MCP, or clicking the Device Hub
   window through macOS input (what Greenroom does today). Xcode 27 made mouse input on a
   simulator *hybrid*: a click is a direct touch, but a scroll wheel is a `UIEvent.scroll`,
   not a swipe, and two-finger touch is unsupported.
5. **Image cost.** Cirrus `macos-tahoe-xcode:27`: 140 GB virtual, **62.1 GB** compressed
   pull, 263 layers. `macos-golden-gate-xcode:27` (macOS 27): **68.9 GB** pull, ASIF disk.
   Both carry four simulator platforms, Android SDK, Flutter, fastlane, CocoaPods, Codex
   and Claude Code. A Greenroom layer with Xcode 27 plus one arm64 iOS runtime is estimated
   at about +21 GB on disk (Xcode.app 9.6 GB logical, one iOS runtime about 7.9 GB, its
   dyld cache about 3.3 GB), measured on the host, not in a guest.
6. **The dyld shared cache is keyed by the host OS build** (local path
   `/Library/Developer/CoreSimulator/Caches/dyld/26A428/`, 9.9 GB for three runtimes). A
   baked cache survives only while the guest macOS build stays fixed, which Greenroom
   already guarantees by disabling Software Update (ADR 0018).
7. **Real devices into a VM became possible on paper in macOS 27**:
   `VZUSBPassthroughDevice` (new, macOS 27.0) plus a restricted entitlement. UTM shipped it
   on 2026-09-18, tested with a flash drive only. Tart has not (issue #139 open since
   2022). iOS 27 devices can also pair with Device Hub over the network. Both are spikes,
   not facts, for iPhones in a Tart guest.
8. **Licensing.** Local-first (the developer's own Mac) is squarely inside both licenses.
   A hosted fleet needs the macOS SLA section 3 lease (24-hour minimum, sole use, advance
   notice to Apple) and the lessee must accept the Xcode agreement, whose section 2.7 bars
   hosting Xcode "over a network where [it] could be run or used by multiple computers at
   the same time". The macOS SLA also says you "may not use the Apple Software to run any
   Apple operating system software, including iOS ... in virtual operating system
   environments" except as 2B allows; whether a simulator in a VM is that is a question for
   counsel. macOS 27's SLA adds "except as otherwise provided in writing ... by an
   authorized representative of Apple" to the VM and lease clauses.
9. **Nobody does the whole job.** Xcode 27's own agent tools, Claude Code Desktop, AXe,
   MobileBuildMCP (formerly XcodeBuildMCP), mobile-mcp and Maestro MCP all drive a
   simulator on the developer's own Mac. None gives a disposable machine, an independent
   verifier, a recorded run and human takeover in the same machine. Device farms have real
   devices but no agent loop that proves a change.
10. **Recommended v1**: an opt-in `greenroom-xcode27` layer (Xcode 27, one iOS 27 arm64
    runtime, baked dyld cache, pinned AXe), simulators booted with `simctl`, input and AX
    tree through AXe behind a driver interface, Xcode MCP as a second tree for
    cross-checks, Device Hub window visible for the live stream and human takeover, and
    evidence as AX trees, `simctl io` screenshots and video, and `.xcresult` summaries.
    Real devices through a device-farm partner, later.

---

## 0. What changed in 2026 that matters

| Change | Source |
| --- | --- |
| Xcode 27.0 released 2026-09-14 (build 27A266a, iOS SDK 27.0 24A430). Requires macOS Tahoe 26.6 or later and Apple silicon only. | [xcodereleases data](https://xcodereleases.com/data.json); [Xcode 27 release notes](https://developer.apple.com/documentation/xcode-release-notes/xcode-27-release-notes): "Xcode 27 requires a Mac running macOS Tahoe 26.6 or later." and "Xcode 27 will only install and run on Apple silicon Macs." |
| Simulator.app removed, replaced by `Xcode.app/Contents/Applications/DeviceHub.app`, bundle id `com.apple.dt.Devices`, version 27.0 | local: `ls /Applications/Xcode.app/Contents/Applications` and `defaults read .../DeviceHub.app/Contents/Info.plist CFBundleIdentifier`; breakage reports: [wix/Detox#4978](https://github.com/wix/Detox/issues/4978), [callstackincubator/rock#730](https://github.com/callstackincubator/rock/issues/730) |
| `devicectl` lists simulators (`Reality: simulated`) and gains `orientation`, `capture`, `simulate biometrics`, `settings voiceover` | local: `xcrun devicectl list devices`, `xcrun devicectl device --help` |
| Xcode MCP server can run headless (`sudo xcrun mcp-server enable`), agents "can now boot simulators, install and launch apps, synthesize touch events, and capture screenshots to verify UI behavior" | [Xcode 27 release notes](https://developer.apple.com/documentation/xcode-release-notes/xcode-27-release-notes) (175179787, 181836944); local `xcrun mcp-server --help` |
| Device Hub mirrors and accepts input for physical iPhones and iPads; pairs iOS 27 devices over the network | [Interacting with your app in Device Hub](https://developer.apple.com/documentation/xcode/interacting-with-your-app-in-device-hub); release notes (179418483) |
| macOS 27 adds USB passthrough to Virtualization.framework | [VZUSBPassthroughDevice](https://developer.apple.com/documentation/virtualization/vzusbpassthroughdevice) (introduced macOS 27.0) |
| XcodeBuildMCP moved to Sentry and was renamed MobileBuildMCP (the old URL redirects) | `gh api repos/getsentry/XcodeBuildMCP` returns `getsentry/MobileBuildMCP` (as of 2026-09-25) |
| Claude Code Desktop's iOS Simulator pane does not work with Xcode 27 | [code.claude.com docs](https://code.claude.com/docs/en/desktop-ios-simulator): "The pane doesn't yet work with Xcode 27, which replaces the Simulator app with Device Hub." (as of 2026-09-25) |

Consequence for Greenroom: build for Xcode 27 only. Anything that assumes Simulator.app
(window titles, `open -a Simulator`, `com.apple.iphonesimulator` defaults) is legacy.

---

## 1. Xcode inside a Tart macOS VM

### 1.1 Requirements

- Xcode 27 needs macOS 26.6+ (above). Greenroom's current guest is macOS 26.6.2
  (`docs/09-image-strategy.md` measurements), so Xcode 27 installs on today's
  `greenroom-lean-a` without a base change. Re-check with `tart exec <vm> sw_vers` before
  building.
- Cirrus builds Xcode images with 4 vCPU and 8 GB RAM (`cpu_count = 4`, `memory_gb = 8` in
  [templates/xcode.pkr.hcl](https://github.com/cirruslabs/macos-image-templates/blob/main/templates/xcode.pkr.hcl)),
  and the pushed VM config says the same (`"memorySize":8589934592,"cpuCount":4`, config
  blob of `ghcr.io/cirruslabs/macos-golden-gate-xcode:27`, read 2026-09-25).
- GitHub's standard arm64 macOS runner, which runs simulators, has 3 vCPU (M1), 7 GB RAM,
  14 GB storage ([GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners), as of 2026-09-25).
  That is a floor that works for CI, not a recommendation for an interactive machine.

### 1.2 Install paths

| Path | What it does | Pros | Cons |
| --- | --- | --- | --- |
| Pre-downloaded `.xip`, `xcodes install --path` | Cirrus: `sudo xcodes install ${version} --experimental-unxip --path /Users/admin/Downloads/Xcode_${version}.xip --select --empty-trash`, then moves it to `/Applications/Xcode_${version}.app` and `xcode-select -s` ([xcode.pkr.hcl](https://github.com/cirruslabs/macos-image-templates/blob/main/templates/xcode.pkr.hcl)) | No Apple ID in the pipeline, repeatable, already Greenroom's plan (`09-...` section 2) | Someone fetches the `.xip` by hand from `download.developer.apple.com` (URL form in [xcodereleases data](https://xcodereleases.com/data.json)); size of the Xcode 27 `.xip` **unverified** |
| `xcodes install <version>` (downloads) | Signs in to Apple Developer and downloads | Automatable | Needs Apple ID credentials and 2FA in the build; unfit for CI images (**unverified** for current xcodes) |
| Cirrus prebuilt `ghcr.io/cirruslabs/macos-{tahoe,golden-gate}-xcode:27` | Pull and clone | Zero build work, rebuilt per Xcode release | 62-69 GB pull, carries Android/Flutter/etc., Cirrus now owned by OpenAI (ADR 0010), licence of the image contents is Apple's |
| Build our own layer on `greenroom-lean-a` | ADR 0018 recipe plus an Xcode step | Only what we need; dialog gate applies | We own the recipe and its rebuild cadence |

### 1.3 Cirrus `macos-*-xcode` images

What they contain, from [templates/xcode.pkr.hcl](https://github.com/cirruslabs/macos-image-templates/blob/main/templates/xcode.pkr.hcl) (main, read 2026-09-25):

- Codex, Claude Code and Amazon Q (`brew install codex`, `brew install --cask claude-code`, `brew install --cask amazon-q`), the GitHub Actions runner, `/Users/runner` symlink.
- OpenJDK 17, Android command-line tools, platform-tools, `platforms;android-36`, build-tools 36, NDK 28.
- Xcode via `xcodes`, then `xcodebuild -downloadPlatform iOS`, `xcodebuild -runFirstLaunch`,
  then `xcodebuild -downloadAllPlatforms` (iOS, tvOS, watchOS, visionOS), plus
  `xcodebuild -downloadComponent MetalToolchain` (release workflow sets
  `XCODE_COMPONENTS: '"MetalToolchain"'`).
- libimobiledevice, ideviceinstaller, ios-deploy, carthage, xcbeautify, swiftformat,
  swiftlint, swiftgen, tuist, fastlane, cocoapods, xcpretty, Flutter stable,
  `wix/brew/applesimutils`, ImageMagick, GraphicsMagick.
- Apple WWDR G3 and Developer ID G2 CA certificates.
- A free-space assertion (`disk_free_mb` default 15000) and an optional expected-runtimes diff.
- `sudo launchctl unload -w /System/Library/LaunchDaemons/com.apple.apsd.plist` ("causes high
  CPU usage after boot").
- `xcrun simctl runtime dyld_shared_cache update --all || sleep 180`, "to avoid wasting CPU
  cycles after boot".

Sizes, from the GHCR manifests (`https://ghcr.io/v2/cirruslabs/<name>/manifests/<tag>`, sum
of layer sizes, anonymous token, read 2026-09-25):

| Image | Layers | Compressed pull | `uncompressed-disk-size` annotation | Uploaded |
| --- | ---: | ---: | ---: | --- |
| `macos-tahoe-base:latest` | 96 | 27.3 GB | 50.0 GB | 2026-09-05 |
| `macos-tahoe-xcode:27` | 263 | 62.1 GB | 140.0 GB | 2026-09-22 07:10Z |
| `macos-golden-gate-base:latest` (macOS 27) | 78 | 33.8 GB | 40.6 GB | 2026-09-21 |
| `macos-golden-gate-xcode:27` | 162 | 68.9 GB | 85.4 GB | 2026-09-22 09:43Z |

The golden-gate VM config says `"diskFormat":"asif"` (Apple Sparse Image Format), which
likely explains why its annotation is the used size rather than the 140 GB nominal disk.
**Unverified**: whether Tart 2.37 on a macOS 26 host can run an ASIF image.

Cadence: "Once a new version of Xcode is released ... This generally happens the next
weekend after a release" and some images are rebuilt monthly
([README](https://github.com/cirruslabs/macos-image-templates#release-cadence)). Evidence:
Xcode 27 GA on 2026-09-14, release `27` published 2026-09-22, images uploaded the same day.
The two images were built serially (`max-parallel: 1`) about 2.5 hours apart, inside a 180
minute job timeout ([release.yml](https://github.com/cirruslabs/macos-image-templates/blob/main/.github/workflows/release.yml)).
Their multi-Xcode `macos-runner` images use 240, 380 and 520 GB disks.

Expected runtimes in the macOS 27 image: iOS 27.0 (24A434), tvOS 27.0, watchOS 27.0,
visionOS 27.0 ([expected.golden-gate.runtimes.txt](https://github.com/cirruslabs/macos-image-templates/blob/main/data/expected.golden-gate.runtimes.txt)).
The Tahoe image carries nine iOS runtimes from 18.6 to 27.0
([expected.tahoe.runtimes.txt](https://github.com/cirruslabs/macos-image-templates/blob/main/data/expected.tahoe.runtimes.txt)).

### 1.4 First launch and license

From `xcodebuild -help` (Xcode 27, local):

```
-license                 show the Xcode and SDK license agreements
-checkFirstLaunchStatus  Check if any First Launch tasks need to be performed
-runFirstLaunch          install packages and agree to the license. Add an
                         `-checkForNewerComponents` flag to check for any additional
                         Xcode Device support components.
```

Recipe (in the guest, as `admin` with passwordless sudo, which the image has):

```sh
sudo xcode-select -s /Applications/Xcode_27.app/Contents/Developer
sudo xcodebuild -license accept
sudo xcodebuild -runFirstLaunch          # installs MobileDevice etc., agrees to the license
xcodebuild -checkFirstLaunchStatus; echo $?   # 0 means nothing left (observed locally: rc=0)
sudo DevToolsSecurity -enable            # debugger without an auth prompt (unverified on 27)
sudo dseditgroup -o edit -a admin -t user _developer   # same purpose (unverified on 27)
```

The dialog gate (ADR 0018) must then pass with Xcode installed. Unknowns to check in the
gate: an Xcode "components" sheet on first GUI launch, a Device Hub first-run window, and
Coding Intelligence onboarding. None of these should appear if nothing launches Xcode.app,
but `DeviceInteraction*` tools start "Xcode's tool service" (section 3.5).

Update (2026-09-25, ADR 0026): every greenroom image now carries the host's Xcode and runs
this recipe at build time (`apps/daemon/internal/machine/guest/xcode.sh`), plus
`sudo DevToolsSecurity -enable` and the user in `_developer`. Measured in a 26.6.2 guest with
Xcode 27.0 (27A266a): `-checkFirstLaunchStatus` exits 69 before `-runFirstLaunch` and 0
after; `-runFirstLaunch` takes about 17 s; `DevToolsSecurity -status` is "disabled" on the
Cirrus base until enabled. The dialog gate builds with `xcodebuild` in a fresh clone and
found no prompt. Opening Xcode.app itself still shows two sheets, "External Agent Access"
and "What's New in Xcode" (`images/README.md`, Gotchas); the gate does not open the app.

### 1.5 Simulator runtimes

Commands (`xcodebuild -help` and `xcrun simctl help runtime`, local; Apple:
[Downloading and installing additional Xcode components](https://developer.apple.com/documentation/xcode/downloading-and-installing-additional-xcode-components)):

```sh
xcodebuild -downloadPlatform iOS                       # arm64 variant on Apple silicon
xcodebuild -downloadPlatform iOS -buildVersion 27.0 -exportPath /tmp/rt   # download only
xcodebuild -importPlatform "/tmp/rt/iOS_27.0_Simulator_Runtime.dmg"        # install elsewhere
xcrun simctl runtime add /tmp/rt/<runtime>.dmg          # same, lower level
xcrun simctl runtime list -v                            # size, state, mount path
xcrun simctl runtime delete --notUsedSinceDays 0 --dry-run
xcrun simctl runtime dyld_shared_cache update --all     # build caches now, not at first boot
xcodebuild -downloadComponent MetalToolchain            # only if the project compiles .metal
```

Apple: "By default, Xcode downloads a variant based on your Mac computer's architecture ...
Otherwise, Xcode downloads an Apple silicon variant to save disk space." Cirrus runs
`-downloadPlatform` inside Packer with no Apple ID, which is evidence it needs no sign-in.

Measured locally (`xcrun simctl runtime list -v`, 2026-09-25): iOS 26.2, 26.4 and 26.5
runtimes are "Patchable Cryptex Disk Image", "Supported Architectures: arm64", 7.8 to 7.9 GB
each, stored under `/System/Library/AssetsV2/com_apple_MobileAsset_iOSSimulatorRuntime/...`
and mounted at `/Library/Developer/CoreSimulator/Volumes/iOS_<build>`. Size of the iOS 27.0
runtime **unverified** (none installed on this host).

**dyld shared cache**: `/Library/Developer/CoreSimulator/Caches/dyld/26A428` is 9.9 GB for
three runtimes (local `du`, 26A428 is the host macOS build). Keyed by host build means a
guest OS update invalidates it and the first simulator boot rebuilds it (Cirrus: "wait for
the update_dyld_sim_shared_cache process ... to avoid wasting CPU cycles after boot").
Greenroom disables Software Update (ADR 0018), so a baked cache stays valid for the image's
life. Bake it last, after any OS change.

Interface Builder no longer needs a simulator to compile: Xcode 27 "Introducing a new
Interface Builder compilation mode, `toolchain`, for UIKit ... allows compiling IB documents
without the need to download a simulator, which is especially useful for build servers"
(release notes 114401122). A build-only image could skip runtimes entirely.

### 1.6 Disk and RAM budget (estimate)

| Item | Size | Evidence |
| --- | ---: | --- |
| Xcode 27.app | 9.6 GB logical, 3.7 GB allocated | local `du -sh -A` vs `du -sh` (APFS compression; clone/copy may decompress, **unverified** in a guest) |
| One iOS arm64 runtime | about 7.9 GB | local, iOS 26.x; 27.0 unverified |
| Its dyld cache | about 3.3 GB | local, 9.9 GB / 3 |
| Default device set, first-launch packages, caches | 1-3 GB | **unverified** |
| MetalToolchain (optional) | unknown | **unverified** |
| **Layer total** | **about +21 to +24 GB on disk** | sum; compressed pull delta probably 12-16 GB since the runtime dmg is already compressed (**unverified**) |

Compare Cirrus: golden-gate base to xcode adds 44.8 GB used and 35.1 GB compressed, for four
platforms, Android and Flutter.

Host view: two VMs (the licence and framework cap) each with a 21 GB larger clone is free
(APFS `clonefile`, `09-...` "Why not --stacked"), so only the image itself costs disk.

RAM: **unverified** per simulator. Proposal: 12 GB and 6 vCPU for an iOS machine, measured
in spike S3 (section 9). Two such VMs on a 36 GB host leave 12 GB for macOS and the daemon.

### 1.7 Keeping the image small

- One Xcode, one iOS runtime (the one the SDK prefers: `xcrun simctl runtime match list`).
  No tvOS, watchOS, visionOS, Rosetta/universal variants, no Android, no Flutter.
- arm64 runtime variant only (default on Apple silicon).
- `xcrun simctl delete all`, then create exactly the devices the tools offer (say one
  iPhone and one iPad), so the device set is fixed and named.
- Bake the dyld cache (saves minutes and CPU at first boot) but only for the kept runtime.
- `xcodebuild -checkFirstLaunchStatus` must be 0 and `xcrun simctl runtime list -j` must
  match a checked-in list (Cirrus's `expected_runtimes_file` pattern).
- No DerivedData in the image; per-project caches go on the attached dependency disk
  (`09-...` "Per-project dependencies").
- Documentation downloads: Xcode 27 has no separate doc sets to strip that I could find;
  **unverified**.

### 1.8 Build and pull times

| Measurement | Value | Source |
| --- | --- | --- |
| Cirrus Xcode image build + push, one macOS version | about 2.5 h wall clock (two images uploaded 07:10Z and 09:43Z, serial) | GHCR `upload-time` annotations |
| Cirrus job timeout | 180 min | [release.yml](https://github.com/cirruslabs/macos-image-templates/blob/main/.github/workflows/release.yml) |
| Pull of a 62-69 GB image | **unverified**; at 1 Gbit/s about 9 min wire time, plus decompression | arithmetic |
| Greenroom Xcode layer build | **unverified**; spike S1 | - |

---

## 2. The iOS Simulator inside a macOS VM on Apple silicon

### 2.1 Does it work

Yes, with caveats. Evidence:

- GitHub's arm64 macOS runners are VMs ("Nested-virtualization is not supported due to the
  limitation of Apple's Virtualization Framework",
  [docs](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)) and
  their images ship simulator runtimes with a policy of "only three major.minor versions of
  platform tools and simulator runtimes" per Xcode ([runner-images](https://github.com/actions/runner-images)).
  Their issue tracker is full of simulator runs (for example
  [#13830](https://github.com/actions/runner-images/issues/13830),
  [#12777](https://github.com/actions/runner-images/issues/12777)).
- Cirrus's Tart Xcode images install runtimes, check them against a list and pre-build the
  dyld cache (section 1.3). They are built to run `xcodebuild test` in Tart.
- No nested virtualization is needed: a simulator is a set of processes on the Mac under
  its own `launchd_sim`, not a VM. `simctl boot --disabledJob=<job>` ("Disables the given
  launchd job") and `simctl spawn` ("Spawn a process ... on a device") show the model
  (local `xcrun simctl help boot|spawn`).

### 2.2 GPU and Metal

- The guest GPU is paravirtualized. Cua measured stock Tahoe VMs reporting "Apple 5-era
  family, 32 KB of maximum threadgroup memory, and SIMD-group matrix support as
  unavailable" ([Cua blog, 2026-08-11](https://github.com/trycua/cua/blob/main/blog/gpu-passthrough-macos-vms.md)).
- Host default that lifts the restriction, "before starting the VM":
  `defaults write com.apple.gpusw.ParavirtualizedGraphics ForceUnrestrictedDeviceFeatureLevel -bool true`
  ([macos-image-templates README](https://github.com/cirruslabs/macos-image-templates#metal-capabilities)).
- Cirrus base images carry a process-scoped Metal capability shim
  (`/usr/local/lib/TartMetalCapabilities.dylib`, via `DYLD_INSERT_LIBRARIES`), off by
  default, "experimental and version-sensitive"
  ([README](https://github.com/cirruslabs/macos-image-templates/blob/main/data/tart-metal-capabilities/README.md)).
  It is for compute workloads; whether the simulator's renderer or an app under test in the
  simulator benefits is **unverified** and injection into Apple-signed simulator processes
  is unlikely to work.
- Practical meaning: ordinary UIKit/SwiftUI apps render (CI proves it). Apps that probe
  `MTLDevice.supportsFamily(.apple7+)` or use Metal 4 features may take a different path
  in a VM than on a Mac or device. Report the guest's Metal family in the toolchain
  manifest so the verifier can say so.
- Greenroom boots with `--no-graphics` (ADR 0016); the VM still has its paravirtualized
  display, which is what the live stream captures. Simulator rendering under `--no-graphics`
  is **unverified** (spike S2).

### 2.3 Known limits in VMs

| Limit | Evidence | Status |
| --- | --- | --- |
| `simctl io ... screenshot` fails with "Timeout waiting for screen surfaces", intermittently, apparently under CPU load from parallel jobs | [runner-images#13830](https://github.com/actions/runner-images/issues/13830) (2026-03-20, macos-26 arm64) | open there |
| Slow first boot while the dyld cache builds | Cirrus template comment and `sleep 180` | fixed by baking |
| `apsd` high CPU after boot | Cirrus unloads it | note: `apsd` is the push daemon; unloading it may break simulator APNs (next row) |
| Remote (APNs) notifications in the simulator need "macOS 13 on Mac computers with Apple silicon or T2", Sandbox environment | [Xcode 14 release notes](https://developer.apple.com/documentation/xcode-release-notes/xcode-14-release-notes) | in a VM **unverified**; `simctl push` works without APNs |
| No USB devices in a Tart guest (so no physical iPhone) | Tart [#139](https://github.com/openai/tart/issues/139) open | see section 5.3 |
| Two macOS VMs per host | framework and SLA (`09-...`, section 6) | fixed |
| iCloud sign-in, Apple Pay, Sign in with Apple inside a VM's simulator | none found | **unverified**; plausible failures tied to device identity |
| Hardware features absent in any simulator | Apple: "some hardware-specific features might not be available ... To test the feature itself, run your code on a physical device" ([Running your app on simulated or physical devices](https://developer.apple.com/documentation/xcode/running-your-app-on-simulated-or-physical-devices)) | Apple no longer lists them on this page |

### 2.4 How many simulators per VM

No primary number found. Data points:

- Claude Code Desktop runs up to 4 per session on a developer Mac
  ([docs](https://code.claude.com/docs/en/desktop-ios-simulator)).
- `xcodebuild` has `-parallel-testing-worker-count` and
  `-maximum-concurrent-test-simulator-destinations` (local `xcodebuild -help`); parallel
  testing clones simulators, and Xcode 27 notes that such clones "may not be visible in
  Device Hub but are still actively running tests" (176809181).
- Memory per booted iOS 27 simulator with one app: **unverified**; spike S3 measures it.

For v1 assume one simulator per machine (matches ADR 0003, one agent one machine), two
at most (iPhone plus iPad).

---

## 3. Driving a simulator for an agent

### 3.1 Capability matrix

Legend: Y yes, N no, P partial. "Device Hub + macOS input" is Greenroom's current
`machine_click`/`machine_ui` pointed at the Device Hub window.

| Capability | `simctl` | `devicectl` (27) | idb / AXe | Xcode MCP | XCUITest (WDA, Maestro, Appium) | Device Hub + macOS input |
| --- | --- | --- | --- | --- | --- | --- |
| Create, boot, erase, clone | Y | P (reset) | N | P (boots) | N | P (menus) |
| Install, launch, terminate | Y | Y (`process launch/terminate`) | Y | Y (build+install+run) | P | N |
| Screenshot | Y `io screenshot` | Y `capture screenshot` | Y | Y (every call) | Y | Y (guest screencapture) |
| Video | Y `io recordVideo` | Y `capture screen-record` | Y (AXe `record-video`, `stream-video`) | N | P | Y (run recording) |
| Tap at point | N | N | Y | Y (`t x y`) | Y | Y (click) |
| Tap element by id/label | N | N | Y (AXe `--id`, `--label`) | N (coordinates) | Y | via `machine_ui` ids |
| Swipe, drag, long press | N | N | Y | Y | Y | P (drag; scroll wheel is not a swipe) |
| Pinch, rotate (two fingers) | N | N | N (no multi-touch in AXe's command set) | **unverified** | Y (`pinch`, `rotate` in XCUIElement) | P (trackpad pinch becomes `UIEvent.transform`, not touches) |
| Type text | N (`pbcopy` then paste) | P (`pasteboard`) | Y (HID keyboard) | Y | Y | Y |
| Hardware buttons | N | N | Y (home, lock, side, siri, apple-pay) | Y | P (`XCUIDevice.press`) | Y (bezel, Controls menu) |
| Rotation | N | Y `orientation set/rotate` | N | Y ("Change device orientation") | Y (`XCUIDevice.orientation`) | Y (menu) |
| Deep link | Y `openurl` | Y `process openURL` | Y | P | Y | N |
| Push notification | Y `push` | N | N | N | N | N |
| Location | Y `location set/start/run` | Y `simulate location` | N | N | N | Y (menu) |
| Privacy grants | Y `privacy grant` | N | N | N | P | N |
| Status bar override | Y `status_bar override` | Y `simulate statusBar` | N | N | N | N |
| Appearance, content size, contrast | Y `ui` | Y `settings appearance` | N | N | N | Y |
| Biometrics (Face ID) | N | Y `simulate biometrics --success/--failure` | N | N | N | Y (menu) |
| VoiceOver | N | Y `settings voiceover` | N | N | Y (`XCUIVoiceOverService`, iOS 27) | Y |
| Logs | Y `spawn <dev> log stream` | P | Y | Y (`GetConsoleOutput`) | N | N |
| **AX tree with frames** | N | N | **Y** (JSON, points) | **Y** (text, frame + hitPoint) | **Y** (`XCUIElementSnapshot`) | **unverified** (section 3.7) |
| Works on physical devices | N | Y | P (idb: yes; AXe: simulator) | Y | Y | Y (iOS 18+) |
| Uses private Apple API | N | N | Y | N | N | N |

Sources: local help output for `simctl` and `devicectl`; AXe
[cli-quick-reference](https://github.com/cameroncooke/AXe/blob/main/Skills/CLI/axe/references/cli-quick-reference.md);
Xcode MCP schema ([tools-headless-27.2.json](https://github.com/artemnovichkov/xcode-tools-docs));
Apple [XCUIElementSnapshot](https://developer.apple.com/documentation/xcuiautomation/xcuielementsnapshot),
[XCUIVoiceOverService](https://developer.apple.com/documentation/xcuiautomation/xcuivoiceoverservice) (iOS 27.0);
[Interacting with your app in Device Hub](https://developer.apple.com/documentation/xcode/interacting-with-your-app-in-device-hub).

### 3.2 `simctl` (Xcode 27), verified subcommands

Local `xcrun simctl help` lists: `addmedia appinfo boot clone create delete diagnose erase
get_app_container getenv icloud_sync install install_app_data io keychain launch list
listapps location logverbose openurl pair pair_activate pbcopy pbpaste pbsync
personalization privacy push reboot rename runtime shutdown spawn status_bar terminate ui
uninstall unpair upgrade`. No tap, no input, no rotate.

The ones an agent needs, with Xcode 27 syntax:

```sh
UDID=$(xcrun simctl create "gr-iphone" "iPhone 17 Pro" com.apple.CoreSimulator.SimRuntime.iOS-27-0)
xcrun simctl boot "$UDID"
xcrun simctl bootstatus "$UDID" -b          # waits for boot; hidden from the list but `simctl help bootstatus` documents -b, -c, -d
xcrun simctl install "$UDID" path/to/App.app
SIMCTL_CHILD_MY_FLAG=1 xcrun simctl launch --terminate-running-process \
    --stdout=/tmp/app.out --stderr=/tmp/app.err "$UDID" com.example.app -arg1
xcrun simctl terminate "$UDID" com.example.app
xcrun simctl io "$UDID" screenshot --type=png --mask=black shot.png   # "-" for stdout
xcrun simctl io "$UDID" recordVideo --codec=h264 --force run.mov       # SIGINT to stop; waits for "Recording started" on stderr
xcrun simctl io "$UDID" screenConfig geometry 1179x2556@3             # new-ish: screen geometry
xcrun simctl openurl "$UDID" "myapp://route?x=1"
xcrun simctl push "$UDID" com.example.app payload.json                 # aps key, <= 4096 bytes
xcrun simctl location "$UDID" set 37.3349,-122.0090
xcrun simctl privacy "$UDID" grant photos com.example.app              # "can mask bugs" (Apple's warning)
xcrun simctl status_bar "$UDID" override --time 9:41 --batteryState charged --batteryLevel 100 --wifiBars 3 --cellularBars 4
xcrun simctl ui "$UDID" appearance dark
xcrun simctl ui "$UDID" content_size accessibility-large
xcrun simctl keychain "$UDID" add-root-cert mitm-ca.pem
xcrun simctl pbcopy "$UDID" < text.txt
xcrun simctl spawn "$UDID" log stream --level debug --predicate 'subsystem == "com.example.app"'
xcrun simctl get_app_container "$UDID" com.example.app data
xcrun simctl erase "$UDID"
```

Notes from the help text: `launch --arch` needs "runtime version 26 or newer";
`privacy` services are `all calendar contacts-limited contacts location location-always
photos-add photos media-library microphone motion reminders siri` (no camera, no
notifications); `push` supports "Only application remote push notifications ... VoIP,
Complication, File Provider, and other types are not supported".

### 3.3 `devicectl` on simulators (new in Xcode 27)

Local `xcrun devicectl list devices` shows the host's simulators with `Reality: simulated`.
Relevant subcommands and help (local):

```
devicectl device orientation set --device <udid> <portrait|portraitUpsideDown|landscapeLeft|landscapeRight|faceUp|faceDown>
devicectl device orientation rotate --device <udid> <direction>
devicectl device capture screenshot --device <udid> --destination <path> [--display-unique-id <id>]
devicectl device capture screen-record ...
devicectl device simulate biometrics --device <udid> --success | --failure
devicectl device simulate location ... / simulate statusBar ...
devicectl device settings voiceover --device <udid> [--enable] [--disable]
devicectl device settings appearance | audio | biometrics | reset
devicectl device process launch | terminate | openURL | signal | sendMemoryWarning
devicectl device info displays | processes | apps | lockState
-j/--json-output -        # JSON on stdout, "versioned and will remain stable across releases"
```

This closes simctl's rotation gap with a public tool. Behaviour on a booted simulator is
**unverified** (no simulator was booted); spike S4.

### 3.4 idb and AXe (private frameworks, fastest)

- **idb** ([facebook/idb](https://github.com/facebook/idb), MIT): very active, last release
  v1.6.2 on 2026-09-23, commits on 2026-09-25 touching `--filter interactable`, CoreDevice
  streams and "DTUHID digitizer transport". Input goes through
  `FBSimulatorControl/HID/SimulatorIndigoHID*.swift` (the simulator's Indigo HID channel);
  the tree comes from `PrivateHeaders/AccessibilityPlatformTranslation/AXPTranslator.h`
  (Apple's private iOS-to-macOS accessibility translator) (repo tree, 2026-09-25).
- **AXe** ([cameroncooke/AXe](https://github.com/cameroncooke/AXe), MIT, v1.8.0
  2026-07-20): a single CLI that "builds IDB from the immutable fork revision configured in
  `scripts/build.sh`". v1.8.0 "Added Xcode 27 and iOS 27 simulator automation through Device
  Hub without requiring Simulator.app", validated on "Xcode 27 Beta 3 (build `27A5218g`)",
  not the 27.0 GA build (**risk**). Commands: `describe-ui [--point x,y]` (JSON),
  `tap -x -y | --id | --label [--tap-style automatic|physical|simulator]`, `swipe`,
  `drag --steps`, `touch --down/--up`, `gesture` presets (scroll and edge swipes), `slider`,
  `type` (`--stdin`, `--file`), `key <HID code>`, `key-sequence`, `key-combo`,
  `button home|lock|side-button|siri|apple-pay`, `batch` with `--wait-timeout`,
  `screenshot`, `record-video`, `stream-video --format mjpeg|ffmpeg`
  ([quick reference](https://github.com/cameroncooke/AXe/blob/main/Skills/CLI/axe/references/cli-quick-reference.md)).
  No pinch or rotate gesture in the list. v1.7.0 fixed taps landing wrong in landscape by
  detecting orientation itself ([CHANGELOG](https://github.com/cameroncooke/AXe/blob/main/CHANGELOG.md)).
- **Latency** (third party, host not VM): a SimMirror benchmark on 2026-09-17, macOS
  26.6.2, iOS 27.0 on Xcode 27.0, measured idb snapshot p50/p95 59/78 ms (16 elements),
  input p50/p95 1.0/558 ms (occasional half-second stall), tap-to-screen-change p50 328 ms,
  `simctl` screenshot p50 490 ms vs idb 8 ms
  ([sim-mirror connectors.md](https://github.com/AndrewKochulab/sim-mirror/blob/main/docs/connectors.md)).
  Low-trust source (2 stars); re-measure in S5.
- **Gaps**: idb's tree "leaves out a web page's text and links, a widget's text and the
  status bar; Xcode 27's UI hierarchy has them" (same source). WKWebView content is the
  obvious hole for hybrid apps.
- **TCC**: Claude Code Desktop says its simulator pane "doesn't need the macOS Accessibility
  and Screen Recording permissions that computer use requires"
  ([docs](https://code.claude.com/docs/en/desktop-ios-simulator)). Whether AXe needs
  Accessibility for `describe-ui` in a guest is **unverified**; the Greenroom image grants it
  to tart-guest-agent anyway (`09-...` section 1).

### 3.5 Xcode 27's MCP server (Apple-supported)

- Enable headless: `sudo xcrun mcp-server enable` (and for unattended machines
  `sudo xcrun mcp-server enable --unsafe-always-allow-all-agents`, "not a recommended
  configuration for at-desk use"); register `xcrun mcpbridge` as a stdio MCP server
  ([release notes 181836944](https://developer.apple.com/documentation/xcode-release-notes/xcode-27-release-notes);
  local `xcrun mcp-server --help`, which also shows `allow-folder`, `approve`, `status`,
  `show-logs`). Local status on this host: `Permission: disabled`, `mcp-server: not running`.
- 53 tools in the 27.2 headless schema
  ([xcode-tools-docs](https://github.com/artemnovichkov/xcode-tools-docs), `tools-headless-27.2.json`).
  The device ones, quoted:
  - `DeviceInteractionStartSession {deviceIdentifier, sessionIdentifier}`: "finds and (if
    necessary) boots a desired device". "Keeping the session open is expensive".
  - `DeviceInteractionStartWorkspaceSession`, `DeviceInteractionInstallAndRun`
    ("Builds, installs, and starts the current application").
  - `DeviceInteractionSynthesize {interactSessionKey, interactionCommand, activationBundleId}`:
    "Synthesizes device events (tap, swipe, type, etc.) on a physical device or simulator
    and captures the resulting state ... it captures a screenshot and UI hierarchy,
    returning paths to both files ... Change device orientation ... Always use positions
    based on the most recent hierarchy dump."
  - `DeviceInteractionEndSession`.
- Command grammar is not documented; the schema's only example is `'t 100 200'`. A blog
  shows `w` (wait) and `sender keyboard kbd [text]` and a hierarchy entry
  `Button frame: {{346.0, 66.0}, {36.0, 36.0}} label: "Add Book" hitPoint: {364.0, 84.0}`
  ([artemnovichkov.com, 2026-08-13](https://artemnovichkov.com/blog/headless-xcode-from-prompt-to-simulator-with-mcp)).
  Coordinates look like iOS points. Full grammar **unverified** (spike S6).
- Latency and limits (third party, host): session open 0.05-1.8 s, a read 0.2-0.9 s, a tap
  answers after 3.3 s and a button after 5.7 s because each waits for the screen to settle,
  one session per simulator, approval per program that "outlasts Xcode restarting", no
  window opened ([sim-mirror](https://github.com/AndrewKochulab/sim-mirror/blob/main/docs/connectors.md)).
- Fit for Greenroom: good as a **reader** (richer tree, Apple-maintained, also works on
  physical devices), poor as the **hands** (seconds per action, one session per device
  shared with any other agent in Xcode). Licence note in section 6 about Apple models.

### 3.6 XCUITest-based drivers

- Apple's public path: a UI test bundle drives the app through XCTest;
  `XCUIElementSnapshot` is "a snapshot of an element's attributes and descendant user
  interface hierarchy" ([docs](https://developer.apple.com/documentation/xcuiautomation/xcuielementsnapshot)),
  with frames. Xcode 27 adds `XCUIVoiceOverService` and a test-plan setting for how app
  crashes during UI tests are treated (release notes 175858549, 168107814).
- **WebDriverAgent** ([appium/WebDriverAgent](https://github.com/appium/WebDriverAgent),
  v16.12.10, 2026-09-21): an XCTest runner that serves WebDriver over HTTP; `/source`
  returns the tree. Used by Appium's XCUITest driver (v12.13.2, 2026-09-23) and by
  mobile-mcp for physical iOS devices.
- **Maestro** ([mobile-dev-inc/Maestro](https://github.com/mobile-dev-inc/Maestro), cli-2.10.0,
  2026-08-31): its iOS driver is an XCTest runner (`maestro-ios-xctest-runner/`), ships
  `maestro mcp`; "Physical iOS devices are not yet supported."
- Costs: the runner app must be built for the exact Xcode/iOS pair and installed per
  simulator (tens of seconds, **unverified**), it runs inside the test process model (an
  app crash ends the session), and on a device it needs signing. Upside: public API, same
  on simulator and device, and it is what customers' own UI tests use.

### 3.7 Clicking the Device Hub window through macOS input (today's path)

- Greenroom posts CGEvents in the guest and reads the frontmost app's AX tree (ADR 0012,
  `apps/daemon/CLAUDE.md` "UI tree"). With Device Hub frontmost this works without new
  code: the verifier clicks inside the device screen.
- **Semantics changed in Xcode 27**: "clicking with a mouse or trackpad produces a
  simulated finger touch of type `UITouch.TouchType.direct`", but "When scrolling with a
  pointing device, `UIEvent.EventType.scroll` is emitted. When pinching or rotating with a
  trackpad, `UIEvent.EventType.transform` is emitted ... This hybrid behavior has been added
  for ease of use for Device Hub, and does not reflect functionality available on physical
  devices" (release notes 48372360). So `machine_scroll` on a simulator is not a swipe;
  a drag is. Also "Device Hub doesn't currently support sending a two-finger touch"
  (169537162), "The Controls->Shake menu item does not work" (171282777), and "Keyboard
  and mouse inputs to simulators for OS versions before iOS 18.0 ... are not accepted"
  (181945323).
- **Coordinate mapping**: a click at guest point `(gx, gy)` lands at iOS point
  `((gx - sx) / sw * W, (gy - sy) / sh * H)` where `(sx, sy, sw, sh)` is the device screen's
  rect inside the Device Hub window (global guest points) and `W x H` the iOS screen size in
  points. Getting `(sx, sy, sw, sh)` needs the Device Hub AX tree (the screen view's frame)
  or window geometry plus the zoom level; Device Hub has "Zoom to Fit" and "Physical Size"
  modes ([Configuring the environment](https://developer.apple.com/documentation/xcode/configuring-the-environment-of-a-simulated-device)).
  The bezel and rounded corners make the edges unsafe. With the guest display at 1024x768
  (Cirrus default) or Greenroom's pinned 1512x982 pt, an iPhone screen is drawn well below
  1:1, so small controls are few guest points wide.
- **Does the guest's macOS AX tree include the iOS app's elements?** Historically the
  simulator window exposed translated iOS elements to macOS accessibility clients (that is
  what `AXPTranslator` is for). A third-party project reports it works with real control
  rects but returns stale `frame=None` placeholders at times
  ([SyncTek-LLC/simdrive#185](https://github.com/SyncTek-LLC/simdrive/pull/185), Simulator.app,
  not Device Hub). For Device Hub **unverified**; spike S7 is cheap and decisive, because
  if it works, `machine_ui` already returns iOS elements in guest coordinates and the
  existing click path aims correctly.
- Hardware keyboard: Device Hub has a "Simulate Hardware Keyboard" menu item, fixed in 27
  (178196115). Typing through `machine_type` goes through it; with it off, text goes
  through the on-screen keyboard only via taps.

### 3.8 Which approach gives an AX tree with frames for aiming

Ranked for Greenroom's verifier, which today aims from a tree and clicks element centers:

1. **idb / AXe `describe-ui`**: JSON with frames in iOS points, about 60 ms, selectors by
   id and label, works headless. Private API; pinned binary per Xcode; misses web content.
2. **Xcode MCP hierarchy**: frame plus `hitPoint`, includes web text, widgets, status bar,
   Apple-supported, works on devices. Slow, one session per device, approval model,
   undocumented text format.
3. **Guest macOS AX of the Device Hub window** (if S7 passes): zero new code, coordinates
   already in the guest's space, same code path as Mac apps. Unknown completeness.
4. **XCUITest snapshot** (WDA, Maestro): public and device-portable, but needs a runner app
   per Xcode/iOS and a long-lived session.

The same "never aim from a picture when a tree exists" rule (ADR 0012) carries over.

---

## 4. Build and test

### 4.1 `xcodebuild` for simulator destinations

```sh
xcodebuild -list -json -project App.xcodeproj
xcodebuild -showdestinations -scheme App -project App.xcodeproj
xcodebuild build -scheme App -project App.xcodeproj \
  -destination "platform=iOS Simulator,id=$UDID" \
  -derivedDataPath /Volumes/deps/DerivedData \
  CODE_SIGNING_ALLOWED=NO
APP=$(find /Volumes/deps/DerivedData/Build/Products/Debug-iphonesimulator -maxdepth 1 -name '*.app' | head -1)
xcrun simctl install "$UDID" "$APP"

# tests, split so a re-run skips the build
xcodebuild build-for-testing -scheme App -destination "platform=iOS Simulator,id=$UDID" -derivedDataPath DD
xcodebuild test-without-building -xctestrun DD/Build/Products/*.xctestrun \
  -destination "platform=iOS Simulator,id=$UDID" \
  -resultBundlePath /tmp/run.xcresult \
  -parallel-testing-enabled NO            # so the one simulator stays the one on screen
```

Flags confirmed in local `xcodebuild -help`: `-destination`, `-destination-timeout`,
`-resultBundlePath`, `-resultBundleVersion 3 [default]`, `-parallel-testing-enabled`,
`-parallel-testing-worker-count`, `-maximum-concurrent-test-simulator-destinations`,
`-allowProvisioningUpdates`, `-allowProvisioningDeviceRegistration`. `CODE_SIGNING_ALLOWED=NO`
for simulator builds is common practice rather than an Apple-documented flag here
(**unverified** as a statement of policy); simulator builds need no provisioning profile.

swift-testing and XCTest both run in simulators from `xcodebuild test`. Xcode 27 surfaces
cross-framework assertion misuse as runtime warnings and fixes swift-testing issue
attribution on background threads (release notes 170335449, 169036231). Plain `swift test`
in an image with Xcode resolves issue #44/#130's "no such module 'XCTest'".

### 4.2 DerivedData and caching

- Put `-derivedDataPath` and SwiftPM's `-clonedSourcePackagesDirPath` on the per-project
  attached disk (`09-...`), not virtiofs (`--dir`), which is slow for many small files
  (Tart [#1161](https://github.com/openai/tart/issues/1161), cited in `09-...`).
- A warm DerivedData keyed by lockfile hash and Xcode build number; a different Xcode
  build invalidates it.
- Build time numbers in a Greenroom guest: **unverified** (spike S1). A 2025 GitHub report
  of a build going from 15 to 40+ minutes after an Xcode upgrade on hosted runners
  ([runner-images#13096](https://github.com/actions/runner-images/issues/13096)) is a
  reminder that VM builds are sensitive to CPU count.

### 4.3 Result bundles as evidence

Local `xcrun xcresulttool help` (version 25115):

```sh
xcrun xcresulttool get test-results summary --path run.xcresult --compact   # pass/fail counts, failures, devices
xcrun xcresulttool get test-results tests --path run.xcresult
xcrun xcresulttool get test-results test-details --test-id "<id>" --path run.xcresult
xcrun xcresulttool get test-results activities --test-id "<id>" --path run.xcresult
xcrun xcresulttool get build-results --path run.xcresult
xcrun xcresulttool get log --type console --path run.xcresult
xcrun xcresulttool export attachments --path run.xcresult --output-path out/   # screenshots from UI tests
xcrun xcresulttool export coverage --path run.xcresult --output-path out/
```

`get object` and `graph` are deprecated in favour of these (help text). The summary JSON is
the natural evidence item: machine-produced, not agent-claimed. `--compact` ("Use compact
JSON formatting") and `--schema` exist on `get test-results summary` (local help).

### 4.4 Signing: simulator vs device

| | Simulator | Physical device |
| --- | --- | --- |
| Apple Developer Program | not needed | needed (a free team works for personal devices, with limits: **unverified**) |
| Certificate + provisioning profile | no | yes; the device UDID must be registered |
| Automatic signing in CI | n/a | `-allowProvisioningUpdates` with an App Store Connect key (`-authenticationKeyPath/-ID/-IssuerID`, local help) |
| Keychain in the guest | no | `security set-key-partition-list -S apple-tool:,apple:,codesign:` (`09-...` Secrets) |
| Xcode SLA | "Use Provisioning Profiles to install Your Applications onto a reasonable, limited number of Authorized Test Units solely for use by You and/or Your Authorized Developers" (2.2B) | |

For Greenroom this is the cleanest line: simulators need no customer secrets in the VM.

---

## 5. Real devices

### 5.1 What needs a device

Apple's current wording is generic: "some hardware-specific features might not be
available ... run your code on a physical device" and simulators "don't replicate the
performance or features of a physical device"
([Running your app on simulated or physical devices](https://developer.apple.com/documentation/xcode/running-your-app-on-simulated-or-physical-devices)).
Commonly cited device-only areas (from general knowledge, **unverified** against a current
Apple list): camera capture, Bluetooth (CoreBluetooth), NFC, most motion sensors and
barometer, ARKit world tracking, true performance and thermal behaviour, cellular, carrier
and Wallet/Apple Pay provisioning, some background execution timing. APNs itself works in
the simulator on Apple silicon hosts (section 2.3), and `simctl push` needs no APNs.

### 5.2 `devicectl` against devices

Same CLI as section 3.3: `list devices`, `device install app`, `device process launch`,
`device capture screenshot`, `device info details`, `manage pair`, JSON output that is
"versioned and will remain stable across releases" (local help). Device Hub can also show a
physical device's screen and send it input (iOS 18+), and Xcode MCP's
`DeviceInteractionSynthesize` works "on a physical device or simulator". So an agent loop
on a real iPhone attached to a Mac exists in Apple's own tools now.

### 5.3 USB passthrough into VMs

- macOS 27 adds `VZUSBPassthroughDevice` and `VZUSBPassthroughDeviceConfiguration`: "an
  abstraction of a USB device that is connected to the system and makes the USB device
  accessible to a VZVirtualMachine by capturing it" ([Apple docs](https://developer.apple.com/documentation/virtualization/vzusbpassthroughdeviceconfiguration), introduced macOS 27.0).
- UTM merged support on 2026-09-18: devices are those "the user assigned to UTM in the
  system 'Virtual Machine Accessories' menu bar item" via AccessoryAccess.framework, and it
  "Requires the `com.apple.developer.accessory-access.usb` entitlement ... and macOS 27".
  Tested only "with a USB flash drive on Linux and macOS Apple VMs"
  ([utmapp/UTM#7877](https://github.com/utmapp/UTM/pull/7877)).
- Tart: "Passthrough USB devices" is open since 2022 ([#139](https://github.com/openai/tart/issues/139),
  last comment 2026-09-19 pointing at UTM); Tart 2.38.0 (2026-09-24) added "an option to
  disable USB accessories" ([release](https://github.com/openai/tart/releases/tag/2.38.0)),
  which hints at the new accessory plumbing but is not passthrough.
- **Unverified**: that an iPhone captured by a macOS guest pairs with the guest's
  CoreDevice, that the entitlement is granted to a third-party VM manager like Greenroom's
  daemon, and whether the user-assignment step can be automated.
- Alternative without USB: iOS 27 network pairing with Device Hub ("Pair Nearby Device")
  (179418483). A guest on Tart's default NAT network probably cannot see the device's
  Bonjour; `tart run --net-bridged <if>` might. **Unverified**.

### 5.4 Provisioning in a hosted model

Every customer would bring their own Apple team, certificates and device registrations;
the Xcode SLA limits provisioned devices to "a reasonable, limited number of Authorized
Test Units" owned or controlled by the licensee (2.2B). Greenroom-owned shared test phones
signed by customers' teams run against that and against the macOS lease rules (section 6).
This is why the device-farm partner path is attractive.

### 5.5 Device farms as a partner path (pricing as of 2026-09-25)

| Service | iOS real devices | Test types | Agent path | Pricing pointer |
| --- | --- | --- | --- | --- |
| BrowserStack App Automate / App Live | yes | Appium, XCUITest, manual live sessions | Official [MCP server](https://github.com/browserstack/mcp-server) (46 tools, active 2026-09-25): `runAppLiveSession`, `takeAppScreenshot`, `runAppTestsOnBrowserStack`, `getFailureLogs`, `fetchAutomationScreenshots`; Appium upload `api-cloud.browserstack.com/app-automate/upload` ([docs](https://www.browserstack.com/docs/app-automate/appium/getting-started/java)) | App Automate Desktop & Mobile from $175/month per parallel, annual ([pricing](https://www.browserstack.com/pricing?product=app-automate)) |
| AWS Device Farm | yes, public and private devices, remote access | XCUITest (`.ipa` or `.zip` with `.xctestrun`/test plans), Appium, custom environment; test insights from `.xcresult` ([XCTest UI guide](https://docs.aws.amazon.com/devicefarm/latest/developerguide/test-types-ios-xctest-ui.html)) | AWS API/CLI (`aws devicefarm get-job`) | $0.17 per device minute, 1,000 free trial minutes, unmetered slots from $250/month, private devices from $200/month ([pricing](https://aws.amazon.com/device-farm/pricing/)) |
| Firebase Test Lab | yes, "real physical iOS devices hosted in a Google data center" | XCTest (incl. XCUITest), Robo for iOS, Game Loop ([docs](https://firebase.google.com/docs/test-lab/ios/get-started)) | `gcloud firebase test ios run` | $5 per physical device hour, Blaze plan free 30 min/day physical ([pricing](https://firebase.google.com/docs/test-lab/usage-quotas-pricing)) |
| Sauce Labs | yes (Real Device Cloud); virtual cloud lists "mobile emulators & simulators" | Appium, XCUITest | [sauce-api-mcp](https://github.com/saucelabs/sauce-api-mcp) (10 stars, API model) | Real Device Cloud $199/month annual per parallel; Virtual $149/month ([pricing](https://saucelabs.com/pricing)) |

How a Greenroom agent would use one: build a device `.ipa` and an XCUITest runner in the
Greenroom VM (needs the customer's signing, or the farm's resigning), upload, run, pull
video, logs and the `.xcresult` back as evidence. Interactive agent driving (tap, read tree)
is possible only through Appium/WDA sessions (BrowserStack, Sauce, AWS remote access), at
network latency. Robo (Firebase) is exploration, not verification of a specific change.

---

## 6. Licensing and legal

Not legal advice. Quotes are exact.

### 6.1 Xcode and Apple SDKs Agreement (EA2002, 06/08/2026)

Source: [apple.com/legal/sla/docs/xcode.pdf](https://www.apple.com/legal/sla/docs/xcode.pdf), fetched 2026-09-25.

- Header: "USE OF APPLE SOFTWARE IS GOVERNED BY THIS AGREEMENT AND IS AUTHORIZED ONLY FOR
  EXECUTION ON AN APPLE-BRANDED PRODUCT RUNNING MACOS."
- 2.2: "Apple hereby grants You ... a limited, non-exclusive, personal, revocable,
  non-sublicensable, non-transferable, and internal use license to: A. Install a reasonable
  number of copies of the Apple Software on Apple-branded computers that are owned or
  controlled by You to be used internally by You or Your Authorized Developers only as
  follows: (i) You may use the Xcode Developer Tools to test and develop application and
  other software; ... (iii) You may use the Apple SDKs (excluding the macOS SDK) solely to
  test and develop Applications that are specifically for use with the applicable
  Apple-branded products for which the SDK is targeted".
- 2.2C: "You will not ... (2) use output generated from an Apple model to train, fine-tune,
  or improve another artificial intelligence model, and (3) programmatically access or use
  Apple models through Apple Software or Services except as expressly permitted."
- 2.7: "This Agreement does not allow the Apple Software or Services to be made available
  over a network where they could be run or used by multiple computers at the same time,
  unless otherwise expressly permitted in writing by Apple. Further, unless otherwise
  expressly permitted by Apple in writing, You agree not to rent, lease, lend, upload to or
  host on any website or server, sell, redistribute, or sublicense the Apple Software and
  Apple Services, in whole or in part, or to enable others to do so."

### 6.2 macOS Software License Agreement

The macOS 27 text on this Mac (`/System/Library/CoreServices/Setup Assistant.app/Contents/Resources/en.lproj/OSXSoftwareLicense.rtf`,
"SOFTWARE LICENSE AGREEMENT FOR macOS") and the published
[macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf):

- 2B(iii), macOS 27: "except as otherwise provided in writing, signed, or issued by an
  authorized representative of Apple, to install, use and run up to two (2) additional
  copies or instances of the Apple Software ... within virtual operating system environments
  on each Apple-branded computer you own or control that is already running the Apple
  Software, for purposes of: (a) software development; (b) testing during software
  development; (c) using macOS Server; or (d) personal, non-commercial use." The Tahoe text
  lacks the "except as otherwise provided in writing" opening; it is new in 27.
- After 2B: "Except as expressly permitted in Section 3, the grant set forth in Section
  2B(iii) above does not permit you to use the virtualized copies or instances of the Apple
  Software in connection with service bureau, time-sharing, terminal sharing, relay service
  or other similar types of services. Except as expressly permitted in this Section 2B, you
  may not use the Apple Software to run any Apple operating system software, including iOS,
  iPadOS, watchOS or tvOS, in virtual operating system environments on Mac Computer(s)."
- 3A (Leasing for Permitted Developer Services): lease allowed if "(i) the leased Apple
  Software must be used for the sole purpose of providing Permitted Developer Services ...
  (ii) each lease period must be for a minimum period of twenty-four (24) consecutive hours;
  (iii) during the lease period, the End User Lessee must have sole and exclusive use and
  control of the Apple Software and the Apple-branded hardware on which it is installed ...
  (iv) prior to using the Apple Software, the End User Lessee must review and agree to be
  bound by the terms of this License and the terms applicable to any software preinstalled
  on the Apple Software, including, but not limited to Apple's Xcode developer software".
  "Permitted Developer Services means continuous integration services, including but not
  limited to software development, building software from source, automated testing during
  software development, and running necessary developer tools". "Each Lessor must provide
  Apple with advance notice prior to leasing". macOS 27 also opens 3A with "except as
  otherwise provided in writing, signed, or issued by an authorized representative of Apple".
- 3D: "either a Lessor or a Lessee (but not both) may install, use and run additional copies
  or instances of the Apple Software within virtual operating system environments in
  accordance with Section 2B(iii)".

### 6.3 What this means for Greenroom iOS

| Model | Reading | Confidence |
| --- | --- | --- |
| Local daemon on the developer's own Mac, their own Xcode licence, VMs for testing during development | Inside 2B(iii)(a)/(b) and Xcode 2.2A. Same as today's Mac-app use. | high |
| Customer brings their own Mac (self-hosted fleet) | Same as local; the customer is the licensee. | high |
| Greenroom-hosted Macs leased per customer | Only under section 3: 24-hour minimum, one lessee with sole use of the host, advance notice to Apple, lessee accepts macOS and Xcode terms. Per-minute, multi-tenant VMs on one host do not fit "sole and exclusive use". | high that constraints apply; business fit is poor |
| Offering "simulators as a service" (agents from many customers driving simulators on shared hosts) | Collides with Xcode 2.7 ("made available over a network where they could be run or used by multiple computers at the same time", "host on any website or server") and the macOS "service bureau, time-sharing" bar. | needs counsel |
| iOS Simulator inside a macOS VM at all | The "may not use the Apple Software to run any Apple operating system software, including iOS ... in virtual operating system environments" sentence is about the macOS SLA grant. A simulator runtime is Apple Software under the Xcode agreement, run as macOS processes. The industry (GitHub, Cirrus, MacStadium) does it openly. | needs counsel; low practical risk for local use |
| Xcode MCP tools that call Apple models (documentation semantic search, Apple specialists) | 2.2C(3) bars programmatic access to Apple models except as permitted; `DeviceInteraction*` do not look model-backed. Avoid wiring Greenroom to model-backed Xcode tools. | medium |
| Device farms | Operate under their own Apple arrangements (**unverified**); Greenroom would be their customer. | medium |

Questions for counsel: (1) Is a per-run VM on a leased host for one customer's 24-hour
lease a compliant Greenroom-hosted offering? (2) Does the new "except as otherwise provided
in writing" language point to a program Apple runs for agent or cloud vendors? (3) Is an
agent-driven simulator "testing during software development" when the agent, not a person,
is the user? (4) Section 2.7's network clause vs a hosted MCP endpoint that returns
screenshots and trees from Xcode-run simulators.

---

## 7. Who does this for iOS agents today, and what they lack

| Product (as of 2026-09-25) | What it does | Runs where | Lacks for Greenroom's job |
| --- | --- | --- | --- |
| **Xcode 27 Coding Intelligence + MCP** | Agents build, run, tap, read hierarchy, run tests, read crashes; headless mode; "Coding Intelligence now includes a new security layer that monitors and controls filesystem access by coding agents" (178289431) | developer's Mac | No isolation per agent, no independent verifier, no recording, seconds per action |
| **Claude Code Desktop iOS Simulator pane** | Live stream, Claude taps and reads screen, up to 4 sims per session, per-device consent | developer's Mac, local sessions only | Not Xcode 27 yet; not in cloud/SSH sessions; `requireCoworkFullVmSandbox` disables it; no verifier or evidence bundle ([docs](https://code.claude.com/docs/en/desktop-ios-simulator)) |
| **MobileBuildMCP** (ex-XcodeBuildMCP, Sentry, MIT, v2.7.1 2026-09-23, 6.4k stars) | Build, test, device and simulator workflows; `ui-automation` workflow: `snapshot_ui`, `wait_for_ui`, `batch`, `tap`, `touch`, `long_press`, `swipe`, `drag`, `gesture`, `button`, `key_press`, `key_sequence`, `type_text`, `screenshot`, backed by a bundled AXe (`scripts/bundle-axe.sh`) | developer's Mac | Same host contention; telemetry to Sentry; no isolation or evidence ([repo](https://github.com/getsentry/MobileBuildMCP)) |
| **AXe** (MIT, v1.8.0) | CLI for HID input and AX tree | any Mac | A tool, not a product |
| **idb** (Meta, MIT, v1.6.2) | Companion + CLI for simulators and devices | any Mac | A tool |
| **mobile-mcp** (Apache-2.0, 1.0.4, 7k stars) | "drives apps from the native accessibility tree", iOS simulators via `mobilecli`, physical iOS via WDA ([repo](https://github.com/mobile-next/mobile-mcp)) | developer's Mac | No isolation, no verifier |
| **Maestro MCP** (Apache-2.0) | Agent inspects, taps, asserts; YAML flows | developer's Mac or Maestro Cloud | No physical iOS; no per-change isolation or evidence to PR |
| **ios-simulator-mcp** (MIT, v2.1.0) | MCP over idb | developer's Mac | Tool only |
| **sim-mirror** (2 stars) | Browser/agent mirror, merges idb and Xcode trees | developer's Mac | Hobby-scale |
| **Appium XCUITest driver / WDA** | WebDriver for iOS | anywhere with Xcode | Test framework, not an agent machine |
| **Cua** (26k stars) | macOS VMs (Lume), cua-driver computer use, cloud desktops | Apple silicon, cloud | No iOS-specific support found in repo tree (search for `ios`/`simulat` paths, 2026-09-25) |
| **Devin on Namespace Macs** | Xcode, Simulator, computer use on M4 Pro / M5 Max hosts (`04-landscape.md`) | Namespace cloud | Tied to Devin; lease model unknown |
| **Device farms** (section 5.5) | Real devices, test runs, MCP for BrowserStack | vendor cloud | No build-in-VM plus verifier loop; interactive latency |

The gap Greenroom fills is the same as for Mac apps: a disposable machine per change, a
verifier that is not the coder, a recorded run and evidence, and human takeover in the
same machine. On iOS there is one more: none of the local tools protects the developer's
own Mac from N agents booting N simulators.

---

## 8. Recommended architecture for Greenroom iOS v1

### 8.1 Scope

Simulators only, local daemon only (ADR 0003 scope), iPhone and iPad, Xcode 27 only.
Real devices and hosting deferred.

### 8.2 Image: `greenroom-xcode27`

- Built by `build-image.sh` on top of `greenroom-lean-a` (macOS 26.6.2 guest; meets Xcode
  27's 26.6 floor), never a default (issue #130). Revisit a macOS 27 base when the Cirrus
  golden-gate base is adopted for Mac work too.
- Steps, in order: copy `Xcode_27.xip` in; `xcodes install 27 --experimental-unxip --path
  ... --select --empty-trash`; `xcodebuild -license accept`; `-runFirstLaunch`;
  `-downloadPlatform iOS -buildVersion 27.0`; `simctl delete all`; create fixed devices
  (`gr-iphone` iPhone 17 Pro, `gr-ipad` iPad Pro 13-inch (M5)); install a pinned AXe build;
  `DevToolsSecurity -enable`; `simctl runtime dyld_shared_cache update --all` last; then
  `toolchain.sh` extended (below); then the dialog gate.
- Read-backs that fail the build by name: `xcodebuild -checkFirstLaunchStatus` is 0;
  `simctl runtime list -j` equals the checked-in list; the two devices exist; a smoke boot
  of `gr-iphone`, `simctl io screenshot` non-uniform, `axe describe-ui` returns at least one
  element on the home screen, shutdown.
- `toolchain.json` gains `xcode {version, build, path}`, `simRuntimes [...]`,
  `simDevices [...]`, `axe {version}`, `metalFamily`. The daemon still does not interpret it
  (ADR 0019).
- Machine size for this image: 6 vCPU, 12 GB (to be confirmed by S3).

### 8.3 Runtime model

- `machine_create {image: "greenroom-xcode27"}` (issue #130's image selection).
- The daemon boots the machine as today; the simulator is booted on demand by a new tool,
  not at machine boot (boot costs RAM and CPU; many runs will only build and unit-test).
- Device Hub: launch it with the booted device visible, sized to fit, so that (a) the live
  stream and run recording show the device, (b) a human can take over through the existing
  lease (ADR 0009) with pointer semantics that match Xcode 27's documented behaviour. The
  dialog gate's allowlist learns "Device Hub with one device window" as expected state for
  this image only after a device boot.

### 8.4 Driver

- New Go package `internal/sim` behind an interface, run through `tart exec` like the input
  helper:

  ```go
  type SimDriver interface {
      Boot(ctx, device) (UDID, error)
      Install(ctx, udid, appPath) error
      Launch(ctx, udid, bundleID string, args []string, env map[string]string) error
      Terminate(ctx, udid, bundleID) error
      Screenshot(ctx, udid) (png []byte, screen Size, err error)
      UI(ctx, udid) (UITree, error)          // frames in iOS points
      Input(ctx, udid, []SimAction) error    // tap, swipe, drag, type, key, button
      Orientation(ctx, udid, o) error        // devicectl
      Env(ctx, udid, EnvChange) error        // openurl, push, location, privacy, status bar, appearance
      Logs(ctx, udid, predicate) (Stream, error)
  }
  ```

- v1 implementation: `simctl` and `devicectl` for lifecycle and environment, AXe for input
  and tree. A second `UI` implementation over Xcode's MCP (`mcpbridge`) for cross-checks and
  web content, enabled with `mcp-server enable --unsafe-always-allow-all-agents` at image
  build time (a VM, not a desk; section 3.5).
- Pin AXe by commit and verify it against Xcode 27.0 GA in the image smoke test. Keep the
  option to vendor idb_companion directly if AXe lags Xcode releases.

### 8.5 AX tree source and aiming

- `UI` returns elements with frames in iOS points; the manager converts to fractions of the
  device screen (0 to 1), as ADR 0009/0012 do for the Mac screen, and keeps ids per reader.
  Outline line format stays `[12] Button label="Add Book" center (0.93, 0.09) size ...`.
- `machine_click {element}` for a device target aims at the element center and posts a HID
  tap through the driver (a touch, not a pointer click). Rejected alternative: clicking the
  Device Hub window from the tree, because scroll and gestures would be pointer events
  (section 3.7) and the verifier would verify less than a finger does, the same reason ADR
  0012 rejected `AXPress`.
- Fallbacks in order: Xcode MCP hierarchy (merged for web views), then vision positions.

### 8.6 Tool surface (options for the grilling)

- **Option A, extend existing tools with a target**: `machine_ui {target:"device:gr-iphone"}`,
  `machine_click {target, element}`, `machine_type {target}`, `machine_screenshot {target}`,
  plus new `machine_device_boot`, `machine_device_install_launch`, `machine_device_env`
  (openurl, push, location, privacy, status bar, appearance, orientation). One mental model.
- **Option B, a `device_*` family**: `device_boot`, `device_install`, `device_launch`,
  `device_ui`, `device_tap`, `device_swipe`, `device_type`, `device_button`, `device_env`,
  `device_screenshot`, `device_logs`. Clearer semantics (touch vs click), more tools.

### 8.7 Evidence

Every device step records: the action, the AX tree before and after (as ADR 0012 steps),
a `simctl io screenshot` PNG in device pixels, and the run recording already captures
Device Hub. Add: `simctl io recordVideo --codec=h264` per verification segment, a
`simctl spawn ... log stream` excerpt filtered to the app's subsystem, and for test runs the
`xcresulttool get test-results summary` JSON plus exported failure attachments. Use
`status_bar override --time 9:41 ...` before screenshots that go into a PR, so diffs are
stable.

### 8.8 Real devices, later

Partner path first (section 5.5), triggered by an explicit "needs a device" verdict from
the verifier. Track Tart USB passthrough and iOS 27 network pairing (spikes S9, S10).

---

## 9. Spikes, with exact commands

All on the author's Mac, macOS 27 host, tart pinned per `apps/daemon/CLAUDE.md`.

**S1. Build an Xcode 27 layer and measure it.**

```sh
tart clone greenroom-lean-a gr-xcode-spike
tart set gr-xcode-spike --disk-size 90 --cpu 6 --memory 12288
tart run gr-xcode-spike --no-graphics &
# in guest: grow the APFS container (Tart FAQ disk resizing), then:
tart exec gr-xcode-spike sw_vers
time tart exec gr-xcode-spike sudo xcodes install 27 --experimental-unxip \
  --path /Users/admin/Downloads/Xcode_27.xip --select --empty-trash
tart exec gr-xcode-spike sudo xcodebuild -license accept
time tart exec gr-xcode-spike sudo xcodebuild -runFirstLaunch
time tart exec gr-xcode-spike xcodebuild -downloadPlatform iOS -buildVersion 27.0
time tart exec gr-xcode-spike xcrun simctl runtime dyld_shared_cache update --all
tart exec gr-xcode-spike df -h /; tart exec gr-xcode-spike xcrun simctl runtime list -v
tart stop gr-xcode-spike && du -sh ~/.tart/vms/gr-xcode-spike
```

Record: wall time per step, `df` delta, runtime size, dyld cache size, compressed size
after `tart push` to a local registry (optional).

**S2. Simulator boots and renders in a `--no-graphics` guest.**

```sh
U=$(tart exec gr-xcode-spike xcrun simctl create gr-iphone "iPhone 17 Pro" com.apple.CoreSimulator.SimRuntime.iOS-27-0)
time tart exec gr-xcode-spike xcrun simctl boot $U
time tart exec gr-xcode-spike xcrun simctl bootstatus $U -b
tart exec gr-xcode-spike xcrun simctl io $U screenshot /tmp/s.png   # repeat 50x, count "screen surfaces" timeouts
tart exec gr-xcode-spike open -a /Applications/Xcode_27.app/Contents/Applications/DeviceHub.app
```

Pass: non-uniform screenshot, boot under 60 s after the dyld cache is baked, zero timeouts
in 50 captures with an idle guest, then again during an `xcodebuild` of a sample app.

**S3. RAM and CPU per simulator.** `tart exec ... vm_stat`, `top -l 1 -o mem`, and
`ps -axo rss,comm | sort -nr | head` before boot, after boot, after launching a sample
SwiftUI app, with one and two simulators. Also compare 8 GB vs 12 GB guests for build time.

**S4. `devicectl` drives a simulator.**

```sh
tart exec gr-xcode-spike xcrun devicectl list devices
tart exec gr-xcode-spike xcrun devicectl device orientation set --device $U landscapeLeft
tart exec gr-xcode-spike xcrun devicectl device capture screenshot --device $U --destination /tmp/d.png
tart exec gr-xcode-spike xcrun devicectl device simulate biometrics --device $U --success
```

**S5. AXe on Xcode 27.0 GA in the guest.**

```sh
tart exec gr-xcode-spike brew install cameroncooke/axe/axe
tart exec gr-xcode-spike axe describe-ui --udid $U > tree.json
tart exec gr-xcode-spike axe tap --label "Settings" --udid $U
tart exec gr-xcode-spike axe swipe --start-x 200 --start-y 700 --end-x 200 --end-y 200 --udid $U
tart exec gr-xcode-spike axe type 'hello' --udid $U
for i in $(seq 50); do /usr/bin/time -p tart exec gr-xcode-spike axe describe-ui --udid $U >/dev/null; done 2>&1 | grep real
```

Record: works or not on 27A266a, p50/p95 of `describe-ui` and `tap` through `tart exec`,
whether it needs Device Hub running, whether it needs the Accessibility grant.

**S6. Xcode MCP headless in the guest.**

```sh
tart exec gr-xcode-spike sudo xcrun mcp-server enable --unsafe-always-allow-all-agents
tart exec gr-xcode-spike xcrun mcp-server status
# drive mcpbridge over stdio with a JSON-RPC client: initialize, tools/list,
# DeviceInteractionStartSession {deviceIdentifier: U, sessionIdentifier: "Spike"},
# DeviceInteractionSynthesize {interactionCommand: ""} then "t 100 200",
# read the returned hierarchy file, DeviceInteractionEndSession
```

Record: the hierarchy file format, the command grammar (try `t`, `s`/swipe, `w`, keyboard
forms, button names), latency, any GUI prompt the dialog gate would catch.

**S7. Guest macOS AX tree over Device Hub (zero-code path).** With an app launched and
Device Hub frontmost: `greenroom machine_ui {app:"DeviceHub"}` through the daemon, or
`tart exec ... ~/.greenroom/bin/greenroom-input-<v> --ui-base64 <base64 of {"app":"com.apple.dt.Devices","limit":500}>`.
Pass: the iOS app's buttons appear with labels and frames that match `axe describe-ui`
after the mapping in section 3.7.

**S8. Build and test a sample app end to end.** A SwiftUI app with an XCTest unit target,
a swift-testing target and an XCUITest target: `xcodebuild build-for-testing`,
`test-without-building -resultBundlePath`, `xcresulttool get test-results summary`. Measure
cold and warm DerivedData on the attached disk vs the boot disk.

**S9. USB passthrough of an iPhone (macOS 27 host).** Outside Tart first: build a minimal
Swift VZ harness with `VZUSBPassthroughDeviceConfiguration`, sign with the
`com.apple.developer.accessory-access.usb` entitlement (find out whether a Developer ID
can carry it), assign the phone in "Virtual Machine Accessories", then in the guest
`xcrun devicectl list devices` and `devicectl manage pair`.

**S10. Network pairing from a bridged guest.** `tart run gr-xcode-spike --net-bridged en0`,
then Device Hub "Pair Nearby Device" with an iOS 27 phone on the same network, then
`devicectl list devices` in the guest.

**S11. APNs in a guest simulator.** Register for remote notifications in a sample app on
the guest simulator, send through the APNs sandbox; separately confirm `simctl push`.

**S12. Two machines, two simulators.** Both VM slots running an iOS machine each, a build
in one and UI driving in the other; count screenshot timeouts and input stalls.

---

## 10. Risks

| Risk | Why | Mitigation |
| --- | --- | --- |
| Private-API drivers break on each Xcode release | idb and AXe use SimulatorKit Indigo HID and AXPTranslator; AXe's Xcode 27 support was validated on beta 3 | Pin by Xcode build, smoke test in the image build, Xcode MCP as a supported fallback reader, idb as a second source |
| Apple keeps churning the simulator surface | Simulator.app to Device Hub in one release broke Detox, rock, Claude Code's pane | Depend on `simctl`/`devicectl` JSON first, the driver interface second, never on window titles |
| Image size and pull time | +21-24 GB estimated, 62-69 GB if we reused Cirrus | Own lean layer, one runtime, attached dependency disk |
| Slow or flaky simulators in a VM | "Timeout waiting for screen surfaces" under CPU load; Metal family reported as Apple 5 era | Bigger VM for the Xcode image, retry capture with a bound, S2/S12 numbers, report Metal family in the manifest |
| Pointer semantics differ from touch | Xcode 27 hybrid input: scroll is not a swipe | Agents use HID touch; humans use Device Hub; say which in the step |
| AX tree gaps | idb misses web text, widgets, status bar; custom-drawn views have no tree | Merge Xcode's hierarchy on demand, vision fallback, verifier says when it aimed without a tree |
| Legal, hosted | Xcode 2.7 network and hosting clauses; macOS lease 24 h and sole use; the "iOS in virtual environments" sentence | Local and self-hosted only until counsel answers section 6.3 questions |
| Xcode MCP permission model | `--unsafe-always-allow-all-agents` is a global switch in the guest | Acceptable in a disposable VM with no customer secrets; never on a host |
| Real-device demand arrives early | Camera, Bluetooth, performance | Device-farm partner; USB passthrough spike |
| Customer projects need older Xcode | One Xcode per image | Image per Xcode major; `toolchain` tells the agent what it got |
| OpenAI owns Tart and Cirrus images (ADR 0010) | Base images and Tart roadmap | Already behind a driver interface plan (`01-plan.md` step 6) |

---

## 11. Open questions for the grilling

1. Option A or B for the tool surface (section 8.6)? Does "click" on a device mean a touch?
2. Is a HID touch from AXe "the event path a user takes" enough for the verifier, given
   ADR 0012 rejected `AXPress` for skipping it?
3. Boot the simulator at machine boot (ready faster) or on demand (cheaper)? Does warm
   start (suspend/resume, `09-...`) become worth it with a booted simulator?
4. Build Greenroom's own layer, or start from `macos-tahoe-xcode:27` and strip it?
5. One Xcode per image, or the Cirrus `macos-runner` pattern with several?
6. Vendor idb_companion (Meta, MIT) directly, or depend on AXe's packaging of it?
7. Accept Xcode MCP's `--unsafe-always-allow-all-agents` inside the guest?
8. Where does human takeover input go: Device Hub pointer (documented Apple behaviour) or a
   companion-side touch surface that sends HID touches through the driver?
9. Evidence to the PR: which subset (xcresult summary, 3 screenshots, a video, AX excerpt)?
10. Is there a customer segment that needs real devices in v1, or is simulator-only enough
    to find out?
11. Hosted: ask Apple (the new "except as otherwise provided in writing" clause) before or
    after the Tart licence opinion (ADR 0010)?

---

## Sources

Local commands (2026-09-25, macOS 27.0 26A428, Xcode 27.0 27A266a): `xcrun simctl help`
and `help <sub>` for io, privacy, push, ui, status_bar, location, runtime, launch, keychain,
openurl, boot, create, erase, clone; `xcrun simctl runtime list -v`; `xcrun devicectl --help`,
`device --help`, `list devices`, `device orientation set --help`; `xcodebuild -help`,
`-checkFirstLaunchStatus`, `-showComponent MetalToolchain`; `xcrun xcresulttool help`;
`xcrun mcp-server --help` and `status`; `du` on Xcode.app and the CoreSimulator dyld cache;
`defaults read` on DeviceHub.app; the macOS licence RTF in Setup Assistant; `tart set --help`,
`tart run --help`.

Apple:
[Xcode 27 release notes](https://developer.apple.com/documentation/xcode-release-notes/xcode-27-release-notes),
[Xcode 14 release notes](https://developer.apple.com/documentation/xcode-release-notes/xcode-14-release-notes),
[Downloading and installing additional Xcode components](https://developer.apple.com/documentation/xcode/downloading-and-installing-additional-xcode-components),
[Device Hub](https://developer.apple.com/documentation/xcode/device-hub),
[Interacting with your app in Device Hub](https://developer.apple.com/documentation/xcode/interacting-with-your-app-in-device-hub),
[Configuring the environment of a simulated device](https://developer.apple.com/documentation/xcode/configuring-the-environment-of-a-simulated-device),
[Running your app on simulated or physical devices](https://developer.apple.com/documentation/xcode/running-your-app-on-simulated-or-physical-devices),
[XCUIElementSnapshot](https://developer.apple.com/documentation/xcuiautomation/xcuielementsnapshot),
[XCUIVoiceOverService](https://developer.apple.com/documentation/xcuiautomation/xcuivoiceoverservice),
[VZUSBPassthroughDevice](https://developer.apple.com/documentation/virtualization/vzusbpassthroughdevice),
[VZUSBPassthroughDeviceConfiguration](https://developer.apple.com/documentation/virtualization/vzusbpassthroughdeviceconfiguration),
[VZXHCIControllerConfiguration](https://developer.apple.com/documentation/virtualization/vzxhcicontrollerconfiguration),
[Xcode and Apple SDKs Agreement](https://www.apple.com/legal/sla/docs/xcode.pdf),
[macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf).

Tart and Cirrus:
[cirruslabs/macos-image-templates](https://github.com/cirruslabs/macos-image-templates)
(README, `templates/xcode.pkr.hcl`, `.github/workflows/release.yml`, `data/expected.*.runtimes.txt`,
`data/tart-metal-capabilities/README.md`), GHCR manifests for `macos-{tahoe,golden-gate}-{base,xcode}`,
[openai/tart releases](https://github.com/openai/tart/releases),
[Tart #139](https://github.com/openai/tart/issues/139), [Tart FAQ](https://tart.run/faq/),
[Cua GPU blog](https://github.com/trycua/cua/blob/main/blog/gpu-passthrough-macos-vms.md).

GitHub runners:
[GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners),
[actions/runner-images](https://github.com/actions/runner-images) and issues
[#13830](https://github.com/actions/runner-images/issues/13830),
[#14404](https://github.com/actions/runner-images/issues/14404),
[#12777](https://github.com/actions/runner-images/issues/12777),
[#13096](https://github.com/actions/runner-images/issues/13096),
[configure-xcode.sh](https://github.com/actions/runner-images/blob/main/images/macos/scripts/build/configure-xcode.sh).

Drivers and products:
[facebook/idb](https://github.com/facebook/idb),
[cameroncooke/AXe](https://github.com/cameroncooke/AXe) (README, CHANGELOG, CLI reference, `DescribeUI.swift`),
[getsentry/MobileBuildMCP](https://github.com/getsentry/MobileBuildMCP) (`manifests/workflows/ui-automation.yaml`),
[artemnovichkov/xcode-tools-docs](https://github.com/artemnovichkov/xcode-tools-docs),
[Headless Xcode blog](https://artemnovichkov.com/blog/headless-xcode-from-prompt-to-simulator-with-mcp),
[Xcode 27 MCP tool list gist](https://gist.github.com/ennbou/586d173c355d31a395f332f5747d90d4),
[AndrewKochulab/sim-mirror](https://github.com/AndrewKochulab/sim-mirror) (`docs/connectors.md`, PR #31),
[SyncTek-LLC/simdrive#185](https://github.com/SyncTek-LLC/simdrive/pull/185),
[appium/WebDriverAgent](https://github.com/appium/WebDriverAgent),
[appium/appium-xcuitest-driver](https://github.com/appium/appium-xcuitest-driver),
[mobile-dev-inc/Maestro](https://github.com/mobile-dev-inc/Maestro),
[mobile-next/mobile-mcp](https://github.com/mobile-next/mobile-mcp),
[joshuayoes/ios-simulator-mcp](https://github.com/joshuayoes/ios-simulator-mcp),
[trycua/cua](https://github.com/trycua/cua),
[utmapp/UTM#7877](https://github.com/utmapp/UTM/pull/7877),
[Claude Code Desktop iOS Simulator](https://code.claude.com/docs/en/desktop-ios-simulator),
[wix/Detox#4978](https://github.com/wix/Detox/issues/4978),
[callstackincubator/rock#730](https://github.com/callstackincubator/rock/issues/730),
[xcodereleases data](https://xcodereleases.com/data.json).

Device farms:
[BrowserStack MCP server](https://github.com/browserstack/mcp-server),
[BrowserStack App Automate Appium docs](https://www.browserstack.com/docs/app-automate/appium/getting-started/java),
[BrowserStack pricing](https://www.browserstack.com/pricing?product=app-automate),
[AWS Device Farm pricing](https://aws.amazon.com/device-farm/pricing/),
[AWS Device Farm XCTest UI](https://docs.aws.amazon.com/devicefarm/latest/developerguide/test-types-ios-xctest-ui.html),
[Firebase Test Lab iOS](https://firebase.google.com/docs/test-lab/ios/get-started),
[Firebase Test Lab pricing](https://firebase.google.com/docs/test-lab/usage-quotas-pricing),
[Sauce Labs pricing](https://saucelabs.com/pricing),
[saucelabs/sauce-api-mcp](https://github.com/saucelabs/sauce-api-mcp).
