# 0026. Every image carries Xcode, copied from the host's install

Date: 2026-09-25
Status: accepted. Supersedes decision 1 of ADR 0019 ("No Xcode layer ... for now").

## Context

Without Xcode, a machine cannot run `xcodebuild`, XCTest or swift-testing, which rules out
most real Mac projects (issue #130, the agent review behind #123 to #131). ADR 0019 left
Xcode out because a layer seemed to need a hand-downloaded `.xip` (an Apple ID) and about
200 GB of disk.

Both premises changed:
- Xcode 27 moved the platform SDKs and simulator runtimes out of the app. On the host,
  `/Applications/Xcode.app` (27.0, 27A266a) is 3.7 GB. The macOS SDK needed to build and
  test Mac apps is in it; the iOS simulator runtime (about 8 GB, plus a 3 GB dyld cache)
  is a separate download (`docs/12-ios-expansion.md`).
- The host that builds the images, including the CI runner, has Xcode installed. Copying
  that install into the guest needs no Apple ID and no download.

The maintainer decided that every image carries Xcode from now on.

## Decision

1. **Every image `build-image.sh` makes has Xcode**, base and lean alike, and the CI suite's
   image too. There is no Xcode-less variant.
2. **Source: the host's install.** The build copies the host's Xcode (`xcode-select -p`'s
   app, or `-xcode <path>`) into the guest's `/Applications/Xcode.app` with rsync over ssh,
   then in the guest: `xcode-select -s`, `xcodebuild -license accept`,
   `xcodebuild -runFirstLaunch`. The build fails by name if the host has no Xcode. The
   Xcode version is whatever the host has; it is recorded, not pinned.
3. **The guest disk grows before the copy** (`tart set --disk-size`, and the APFS container
   resized in the guest) so builds and DerivedData have room. The disk is sparse, so the
   host pays only for what is written.
4. **The toolchain manifest (ADR 0019 decisions 2 to 4) stays** and now reports Xcode, its
   version and whether XCTest and swift-testing pass. The build fails if Xcode is present
   but either probe fails, so an image never claims a toolchain it does not have.
5. **The dialog gate** (ADR 0018) must pass with Xcode installed: no license, first-launch
   or component prompts on screen after boot or after an `xcodebuild`.
6. **Image names carry a recipe version.** The CI suite names its image after the input
   helper version alone, so a recipe change such as this one would reuse a stale image. The
   name gains a recipe version (a constant bumped with each recipe change), and CI rebuilds
   when either changes.
7. **No simulator runtime yet.** iOS runtimes are added with the iOS work, after its spikes
   (`docs/12-ios-expansion.md`), because of their size and the licence questions there.

## Consequences

- Each image grows by about 4 to 5 GB on the host, plus whatever first launch installs.
  Clones stay cheap (APFS clonefile).
- An image build takes longer: a copy of a few GB and first launch.
- Images follow the host's Xcode. Updating the host's Xcode and rebuilding updates the
  images; two hosts with different Xcode versions build different images. The manifest
  says which.
- Licensing: Apple's Xcode agreement allows running Xcode on Apple-branded computers the
  licensee owns or controls; these guests run on the maintainer's own Mac. Hosting Xcode
  for other people's use is a separate question (`docs/12-ios-expansion.md`, section 6),
  not decided here.
- Agents can now build and test Xcode projects and run XCTest and swift-testing in a
  machine. `machine_exec`'s advice to read `toolchain` first stays.
