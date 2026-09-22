# Build transport: moving a developer's warm project onto a machine

Status: proposed 2026-09-22. Answers "how do we take a build and all of it on someone's
machine and transport it to a VM", and the follow-up that matters more: what does the
developer actually do, and does it feel quick.

Numbering note: the brief asked for `09-build-transport.md`, but `09-image-strategy.md`
already exists. This is `10` so the docs directory keeps one document per number.

Every number marked **[measured]** was taken on the dev host on 2026-09-22: Apple
silicon, macOS 27.0, Tart 2.32.1, guest cloned from `greenroom-base` (macOS 26.6.2,
4 vCPU, Command Line Tools with Swift 6.3.3). Scripts are in `spikes/transport/`.
Anything not marked measured is reasoning or an upstream citation, and says so.

This document is evidence for a tool choice. `docs/09-image-strategy.md` covers the image
itself and is not repeated here.

## The question, and the part of it that decides the product

Source code is small and fast to move. The expensive payload is the build state around it:
DerivedData, SwiftPM checkouts and object files, CocoaPods, `node_modules`, module caches.
Large trees of many small files. Move that well and an agent gets a two-second incremental
build; move it badly and it gets a cold one.

But transport speed alone is the wrong target. There are two different moments:

- **First run for a project.** Hundreds of megabytes have to cross once. Ten seconds is
  fine. Nobody expects the first run to be instant.
- **Every run after, and every edit inside a run.** This is the one the developer feels.
  If a code change takes a second to appear on the machine, the machine feels like part
  of their laptop. If it takes ten, they go and do something else and lose the thread.

So the tool is judged mainly on the second number, and the first number just has to be
tolerable.

## The payload **[measured]**

greenroom's own repo, built warm, is the payload for every run. The same bytes were used
for every mechanism.

| | |
| ------------------------- | --------------------------------- |
| Files | 7,144 |
| Directories | 1,735 |
| Symbolic links | 17 (mutagen's scan, which counts symlinked directories; 2 are file-level) |
| Total size | 748,670,619 bytes (714 MiB) |
| Files under 16 KiB | 6,056 (84.8%), 13.9 MiB between them |
| Mean file size | 102.3 KiB |

Composition:

| Component | Files | Size |
| ---------------------------------- | ----- | ------ |
| `repo/` (worktree, `node_modules`) | 560 | 226 MB |
| `companion/` source + `.build` | 2,382 | 380 MB |
| `daemon/` source | 61 | 12 MB |
| Go module cache | 1,626 | 69 MB |
| Go build cache | 2,515 | 181 MB |

The `companion/.build` tree was built **inside the guest** and copied back out, not built
on the host. That matters: the host runs Swift 6.4 targeting macosx27 and the guest runs
Swift 6.3.3 targeting macosx26, and a cache from one is worthless to the other. Using a
guest-built cache is what makes "did the transported cache actually get used" a real
question rather than a foregone failure.

Honest limit on scale: 7,144 files is a modest tree. A real iOS app's DerivedData plus
SwiftPM checkouts runs into the tens or hundreds of thousands. The per-file costs below
scale with file count, so the gaps between mechanisms widen at real scale rather than
narrow. Disk headroom on this host (13 GB used of 21 GB free at the start) ruled out a
bigger payload.

### Build baselines in the guest **[measured]**

These are the yardsticks. Everything else is compared against them.

| | Wall clock |
| ------------------------------------------- | ---------- |
| Cold boot of a fresh clone to `tart exec` | 22.6 s |
| Cold boot to ssh usable | 25.7 s |
| Cold `swift build`, fresh VM | 63.0 s |
| Cold `swift build`, warmed VM (control) | 41.6 s |
| Rebuild, one source file edited | 1.4 to 2.1 s |
| Rebuild, nothing changed | 0.69 s |

A transported cache that works turns 41.6 s into about 2 s. That is the whole prize.

## The race

Cold transport is the full 714 MiB / 7,144 files. Incremental is one source file edited
and the time until the guest can see the change. All **[measured]**.

| Mechanism | Cold transport | Incremental | Build uses the cache | Guest daemon |
| ------------------------------ | -------------- | ------------------ | ------------------------ | ------------ |
| **Mutagen 0.18.1** | **10.6 s** | **0.13 to 0.18 s** | yes, 2.07 s then 0.12 s | 7.9 MB agent, auto-installed |
| rsync `-a` over ssh | 6.7 to 8.0 s | 0.80 s per call | yes, 1.82 s | none |
| rsync `-az` (today's `machine_sync`) | 25.5 s | 0.80 s per call | yes | none |
| Offline injection into a stopped clone | 4.3 s | impossible | yes, 1.94 s | none |
| Attached disk image, read-only | 9.0 s to build image, 2.3 s to copy in | needs a VM restart | yes, at the right path | none |
| Attached disk image, read-write | same | needs a VM restart | yes, at the right path | none |
| VirtioFS (`tart run --dir`) | 0 s, nothing is copied | 0 s, shared | yes, but every build pays a tax | none |
| Syncthing 2.1.5 | 63.9 s | 9.8 to 11.4 s | yes, 2.00 s then 0.16 s | 27.8 MB, paired per VM |
| git bundle + clone | 0.3 s | n/a | **no cache at all** | none |

Reading of the table:

**Mutagen wins on the number that decides the feel.** A host edit is visible in the guest
in 0.13 s, measured three times at 0.18, 0.18 and 0.15 s, and those figures include one
ssh round trip of about 0.11 s used to observe it. The real propagation is under 100 ms.
Nothing is invoked: mutagen watches both ends and pushes continuously. rsync's 0.80 s is
respectable but it is 0.80 s *per invocation*, and somebody has to decide to invoke it.

**Mutagen's cold sync is 1.4x slower than plain rsync and that is the price.** 10.6 s
against 6.7 s on a 714 MiB payload. Paid once per run.

**`-z` is costing `machine_sync` 19 seconds.** rsync with `-az` took 25.5 s; the identical
transfer with `-a` took 6.7 s, a 3.8x difference, and `-az` burned 20 s of host CPU doing
it. Compression pays for itself over a slow network. Over a virtual NIC to a VM on the same
machine it is pure tax. This is a one-character fix worth making regardless of what else
changes.

**Syncthing is the wrong shape for this.** 63.9 s cold, and about 10 s to propagate an
edit. The 10 s is `fsWatcherDelayS`, which defaults to 10 and is documented as tunable
down to 1; I could not set it from the v2 CLI in the time available, so treat "1 s at
best" as an upstream claim rather than measured. Even at 1 s it is an order of magnitude
behind mutagen. The deeper problem is the model: two daemons, device IDs, and a mutual
pairing step per machine. For a VM that lives twenty minutes that is a lot of ceremony,
and setting it up by script took me far longer than every other mechanism combined.

**git transports the cheap part.** The whole repo bundled is 6.7 MB, and bundle plus copy
plus clone is 0.3 s. It also carries 145 tracked files and zero build cache. It is a good
way to get a branch onto a machine and not a build transport. Worth keeping in mind for
"agent needs a clean checkout of a branch", which is a different job.

**VirtioFS is free to attach and expensive to build on.** No copy happens, so cold
transport is zero. The cost shows up in every build afterwards. Same VM, same tree:

| | Local APFS | VirtioFS | Ratio |
| ------------------------------ | ---------- | -------- | ----- |
| Walk and stat 7,144 files | 0.05 s | 2.26 s | 45x |
| Cold `swift build` | 41.6 s | 54.4 s | 1.31x |
| Rebuild, nothing changed | 0.69 s | 3.78 s | 5.5x |

The metadata number is the one that scales badly. 2.26 s to stat 7,144 files becomes half
a minute at DerivedData scale, and an incremental build stats everything. The same walk
over an attached disk image took 0.15 s, 15x faster than VirtioFS, because the guest is
talking to a block device and a local APFS driver rather than to the host over a
filesystem protocol. VirtioFS is right for handing evidence back out and wrong for a hot
build tree, which is what `docs/09-image-strategy.md` already said; this puts numbers on it.

**Offline injection is the fastest way to fill a machine, and it can only be done once.**
Attaching a stopped clone's `disk.img` on the host takes 0.29 s, mounting the Data volume
0.40 s, writing the whole payload 3.19 s, detaching 0.46 s: 4.3 s in total, faster than
anything else, and the VM then boots normally and builds in 1.94 s off the injected cache.
The ownership trap named in `docs/09-image-strategy.md` does not bite here, because the
volume mounts `noowners` and files land as uid 501, which is exactly what `admin` is in
the guest; `id` inside the guest confirmed `uid=501(admin) gid=20(staff)` and the injected
files read back as `admin:staff` **[measured]**. That trap is real for `root:wheel`
LaunchDaemon plists and not for project files in a user's home.

It is also a dead end for the thing that matters. Injection only works while the VM is
stopped. It cannot carry a single edit made after boot, which is the entire inner loop.
It is a cold-start accelerator, not a transport.

## Three findings that will bite whatever tool is chosen

### The guest path is part of the cache

A SwiftPM build cache is keyed to the absolute path it was built at. Transport the same
bytes to a different path and the build does not merely miss the cache, it fails outright
**[measured]**:

```
error: precompiled file '/Users/admin/work/rsync-noz/companion/.build/.../SwiftShims-....pcm'
was compiled with module cache path '/Users/admin/work/companion/.build/.../',
but the path is currently '/Users/admin/work/rsync-noz/companion/.build/.../'
error: missing required module 'SwiftShims'
```

The same tree at `/Users/admin/work/companion`, where it was built, rebuilt in 1.82 s. This
reproduced through every mechanism, including the attached disk image mounted at
`/Volumes/GRPAYLOAD`. For the disk image there is a clean fix: mount it at the canonical
path instead, `sudo diskutil mount -mountPoint /Users/admin/work /dev/disk2s1`, after which
the build succeeded in 3.9 s **[measured]**.

So greenroom must pin one guest path and never vary it. `/Users/admin/work/<project>` is
the obvious choice, and `machine_sync` already defaults to `~/work/<basename>`. Make that a
documented invariant rather than a default, and make the daemon refuse a destination that
would move a cache.

### Mutagen does not preserve modification times, and SwiftPM does not care

Mutagen rewrote every mtime to sync time **[measured]**: host `Package.swift` at
1790021668, guest copy at 1790052725. The build worked anyway, 2.07 s and then 0.12 s
steady, because llbuild keys off its own build database and content hashes rather than raw
mtime comparison. Go's build cache is content-addressed and would also be fine.

Anything that compares mtimes directly would not be. `make`, Xcode's legacy build system,
and some codegen scripts are the obvious risks. This is the single thing to test first when
a new project type is onboarded, and it is a reason to keep rsync available as a fallback:
rsync `-a` preserves mtimes and mutagen does not.

### The Go module cache is read-only and breaks cleanup

Go writes its module cache mode 0444. Every attempt to `rm -rf` or re-sync over a
transported `GOMODCACHE` failed with `Permission denied`, across thousands of files
**[measured]**. Any sync tool that has to replace or delete files there needs
`chmod -R u+w` first. Cheap to handle, silent and confusing if not.

## The return path

Getting the artifact back out.

| Mechanism | Time | Works while the VM runs |
| ------------------------------------- | ------ | ----------------------- |
| Mutagen two-way, 3.4 MB binary | 0.41 s | yes |
| rsync guest to host, 251 MB / 2,354 files | 3.18 s | yes |
| VirtioFS | 0 s, already on the host | yes |
| Read-write disk image | 2.5 s, plus the VM must stop | **no** |

The read-write image works. The guest mounted it, built into it, and after `tart stop` the
host attached it and read the binary back in 2.5 s **[measured]**. But the VM has to be
stopped, because host and guest cannot both hold an APFS volume read-write. That rules it
out for anything live, which is most of what greenroom does: an agent wants to look at a
screenshot and a log while the machine is still running.

Mutagen's two-way mode is the good answer because it is the same mechanism in both
directions, there is nothing extra to run, and the artifact appears on the host before the
developer has finished reading the build output.

**The full developer loop, measured end to end** with a two-way mutagen session:

| Step | Time |
| ------------------------------------- | ------ |
| Host edit visible in the guest | 0.13 s |
| Incremental `swift build` in the guest | 2.01 s |
| Rebuilt binary back on the host | 0.41 s |
| **Total, edit to artifact** | **2.55 s** |

2.55 s on a remote machine, against 1.4 to 2.1 s for the same rebuild locally. The machine
is close enough to free that a developer stops thinking about it. That is the bar, and one
tool clears it.

## Warm machines do not help here **[measured]**

Re-measuring the suspend and resume path from `docs/09-image-strategy.md`, with a VM that
already had the project built:

| | |
| ------------------------------- | ------- |
| `tart suspend` | 0.03 s, writes a 1.89 GB `state.vzvmsave` |
| Clone the suspended VM | 0.56 s |
| Resume the clone to ssh usable | 35.7 s |
| Cold boot a fresh clone to ssh usable | 25.7 s |
| Build after resume | 0.73 s compile, 12.6 s wall while memory pages back in |

Resume was **slower than a cold boot**, 35.7 s against 25.7 s, and then cost another 12 s
of page-in before the first build ran at full speed. On this image, suspend and resume buys
nothing and costs the one-running-descendant-per-snapshot limit described in
`docs/09-image-strategy.md`. That may change on an image carrying Xcode and a logged-in GUI
session, which is where resume should start to pay, but it is not true today and nothing
should be built on the assumption.

The practical consequence is good news: a cold boot plus a mutagen sync is about 36 s to a
fully warm machine, which is quicker than resuming a snapshot and far simpler.

## Recommendation: what `machine_sync` becomes

**Ship Mutagen as the transport, keep rsync as the seed and the fallback.**

1. **Replace `machine_sync` with a session, not a copy.** The daemon starts a mutagen
   sync session when a machine becomes ready and terminates it when the run ends. The
   MCP tool stops being "copy these bytes now" and becomes "this machine is bound to this
   project directory". `machine_sync` stays as an explicit one-shot for callers that want
   one, implemented as rsync, because a one-shot copy is still the right primitive for
   pushing a fixture or pulling a log.

2. **Drop `-z` from the rsync path today.** `-az` to `-a` takes the cold copy from 25.5 s
   to 6.7 s **[measured]**. This is a one-line change in
   `apps/daemon/internal/machine/manager.go` and is worth making whether or not mutagen
   lands. Track A owns that file; this is a recommendation, not a change.

3. **Pin the guest path.** One canonical location, `/Users/admin/work/<project>`, treated
   as an invariant. A build cache that lands anywhere else is not a slow build, it is a
   failed one.

4. **Bake the mutagen agent into `greenroom-base`.** Recommendation for Track A, detailed
   below. Be honest about why: it is not speed.

5. **Do not use VirtioFS for the build tree.** Keep `--dir` for evidence coming out, where
   its zero-copy property is exactly right and its metadata cost does not matter.

6. **Do not use offline injection, attached disk images, or suspend and resume** in the
   per-run path. Injection is genuinely the fastest cold fill at 4.3 s, and it is still the
   wrong choice, because it cannot carry an edit and the inner loop is the product. Keep
   `hdiutil attach -imagekey diskimage-class=CRawDiskImage` for debugging a VM that will
   not boot, which is what `images/README.md` already says.

### What this costs, said plainly

- **Licence.** Mutagen is MIT except for a `sspl/` directory under the Server Side Public
  License, and from v0.17 onward the **official release builds include the SSPL code**.
  Building without the `--sspl` flag yields an MIT-only binary. SSPL is not an OSI-approved
  licence and it is a copyleft-for-service licence, which matters for the hosted product
  in `docs/00-idea.md` and pulls against ADR 0010 the same way pinning Tart 2.36.0 does.
  **This is a decision for the user, not for me:** ship the official build, build MIT-only
  from source, or stay on rsync. My reading is that building MIT-only from source is
  straightforward and removes the question, but it adds a build step to greenroom's own
  release pipeline.
- **Release cadence.** v0.18.1 was released 2025-02-24, about nineteen months ago. The
  repository is alive, with the most recent commit on 2026-04-22 and 4.4k stars, but this
  is a slow-moving dependency with a small maintainer base. It was Docker Desktop's file
  sync, so it has been load-tested by a lot of people, and the Go codebase means greenroom
  could vendor it if it ever stalled.
- **A daemon on the host.** Mutagen runs a background daemon. greenroom already runs one,
  so this is a second process to supervise, not a new category of problem. One wrinkle
  found the hard way: `MUTAGEN_DATA_DIRECTORY` under a long path exceeds the 104-character
  Unix socket limit, and the daemon then fails to start with a connection timeout rather
  than an explanatory error **[measured]**. The daemon should set a short data directory
  explicitly, for example `~/.greenroom/mutagen`.
- **mtimes are not preserved.** Fine for SwiftPM and Go, a risk for `make` and Xcode's
  legacy build system. Test it per project type and keep the rsync fallback.

## Recommendations for `images/` (Track A owns these, I did not edit them)

- **Bake the mutagen agent.** The darwin/arm64 agent is a single 7.9 MB Mach-O binary that
  lives at `~/.mutagen/agents/<version>/mutagen-agent`. Be clear about the reason: auto
  install cost about 0.3 s here (10.3 s with install against 10.6 s without, which is
  inside the noise) **[measured]**, so this is not a speed optimisation. It is worth doing
  because it removes a per-run step that can fail, removes the need for the 97 MB
  multi-platform agent bundle on the host, and lets a machine sync without the host
  reaching the network. Version-pin it to the host's mutagen version; a mismatch makes
  mutagen reinstall, which is a correct but silent fallback.
- **Pre-create `~/work` owned by `admin:staff`**, so the canonical path exists before the
  first sync and cannot be created with the wrong owner.
- **Bake GitHub `known_hosts`**, as `docs/09-image-strategy.md` already recommends.
- **`sudo mdutil -a -i off`** and disabling Time Machine local snapshots. Not verified in
  this spike. Spotlight indexing a freshly transported 700 MB tree of object files is pure
  waste, and local snapshots pin deleted blocks on a guest volume with 17 GB free. Both
  look clearly worth doing; both need a boot-survival check before anyone claims they work.

## What I could not test here, and why

- **A payload at real Xcode scale.** The host had 13 GB used of 21 GB free and the brief
  set an 8 GB floor. Everything measured points the same way at larger file counts, with
  the per-file mechanisms pulling further ahead of VirtioFS, but that is reasoning and not
  measurement.
- **Two machines at once.** Apple's two-VM limit was in force and Track A held the other
  slot, so every experiment ran one VM at a time. The one-running-descendant-per-suspended-
  snapshot constraint is quoted from `docs/09-image-strategy.md` and was not re-verified.
- **Syncthing with `fsWatcherDelayS` lowered.** The v2 CLI rejected every spelling I tried
  for that key. The 10 s default is measured; the 1 s floor is an upstream claim.
- **Mutagen over a real network.** Everything here ran over a virtual NIC to a VM on the
  same machine, where bandwidth is effectively free and latency is about 0.1 ms. The
  ordering of these tools would change for a Mac mini across a LAN, and `-z` would start
  earning its keep again. The hosted fleet in `docs/00-idea.md` will need this re-measured.

## The developer experience

The plumbing above only matters if the developer never has to think about it. Three shapes,
with the same engine underneath.

Common to all three: the **first run for a project is slow and that is fine**. Boot is
25.7 s, the first full sync is 10.6 s, and if the project has never been built on a
greenroom machine the first build is 41.6 s or, for a real app, minutes. Call it a minute
for greenroom's own repo. **Every run after that should be a cold boot plus a differential
sync**, which measured at about 36 s to a machine that rebuilds in two seconds. The product
is that gap. It should be visible to the developer: tell them the first run is priming and
then show them how much faster the second one was.

### Option A: pick a project in the companion and press go

The companion lists projects. The developer picks one, presses go, and watches a machine
come up with a progress line that names what is happening: booting, syncing, ready. When
the machine is ready they get a terminal, or they hand it to an agent.

Good: obvious, discoverable, nothing to learn, and the companion already exists as the
place a developer looks at runs. Drag a folder onto the window to add a project, which is
the same gesture and needs no extra design.

Less good: it is still a thing you go and do. The developer switches to another app,
chooses, waits.

### Option B: greenroom follows the repo you are in

The daemon knows the developer's active project, from the editor or the shell or simply
the last repo that changed. It keeps one machine warm and bound to it. When an agent asks
for a machine, the project is already there. When the developer switches project,
greenroom re-binds and starts the first sync in the background while they are still
reading the diff.

Good: the best possible version of the number that matters. The sync has already happened
by the time anybody asks. This is the only option where "every run after the first" feels
genuinely instant rather than merely fast.

Less good: it burns a machine slot continuously, and there are only two per host. It also
guesses, and a wrong guess is worse than no guess. And it keeps a mutagen session live
against the developer's working tree all day, which means a stray `rm -rf` on either side
propagates. One-way-safe mode and mutagen's ignore rules contain that, but the failure mode
is severe enough to need saying out loud.

### Option C: no explicit step at all, sync is part of asking for a machine

`machine_create` takes a project path. The daemon boots, syncs, and does not report ready
until the tree is there, which is one call and one wait for the caller. The developer never
issues a sync command because there isn't one.

Good: the smallest API, nothing to forget, and it matches the shape `machine_sync` already
has in `apps/daemon`. An agent calling over MCP gets exactly one concept.

Less good: `machine_create` is already asynchronous because boot exceeds the MCP timeout,
and folding a 10 s sync into it makes ready mean more things and fail in more ways. The
boot phase timings invariant in `apps/daemon/CLAUDE.md` exists precisely so a slow boot
names its cause; a sync phase needs the same treatment.

### What I would ship

**Option C now, Option B later, with Option A as the way a human sees it.**

Option C is the correct default because it removes the sync step from the API rather than
making it nicer, and it is a small change to a code path that already handles a long
asynchronous boot with per-phase timings. Add `sync` as a fifth boot phase with its own
budget and its own recorded duration, so a slow sync names itself instead of looking like
a slow boot.

Option A is how the human sees the same thing: the companion lists projects and shows the
phases going past. It is presentation over Option C, not a separate mechanism.

Option B is the one that makes greenroom feel magic, and it should wait until the fleet
exists. On a two-slot laptop, spending a slot on a machine nobody asked for is the wrong
trade. Once a run can be routed to a rented Mac mini, a warm bound machine per active
project costs nothing the developer can feel, and the first-run penalty disappears
entirely because priming happened while they were writing the code.

## Open questions for the user

1. **Mutagen's SSPL component.** Ship the official build, build MIT-only from source, or
   stay on rsync and accept a per-invocation sync. This is a licensing call, not a
   technical one, and it is yours.
2. **How much of the developer's tree is greenroom allowed to watch?** Option B keeps a
   live session against a working tree. That is a trust question before it is a
   performance one.
3. **Should the first run for a project be primed eagerly?** Priming on project open makes
   the first `verify` feel like every other one, and costs a machine slot for something
   nobody asked for yet.

## Sources

- [mutagen-io/mutagen](https://github.com/mutagen-io/mutagen), release v0.18.1
  (2025-02-24), latest commit 2026-04-22, `LICENSE.md` on MIT and the `sspl/` directory
- [Syncthing](https://github.com/syncthing/syncthing), release v2.1.5 (2026-09-08), MPL-2.0
- `docs/09-image-strategy.md` for the image layering, the seed volume, the ownership trap
  and the suspended-clone MAC constraint
- `apps/daemon/internal/machine/manager.go`, `Sync`, for today's `rsync -az` behaviour
- `spikes/transport/` for the scripts behind every number above
