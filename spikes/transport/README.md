# Transport spike

Raw evidence for `docs/10-build-transport.md`. Run on 2026-09-22, Apple silicon,
macOS 27.0 host, Tart 2.32.1, guest cloned from `greenroom-base` (macOS 26.6.2, 4 vCPU,
Command Line Tools, Swift 6.3.3).

This is a spike. The scripts are the commands that were actually run, kept so the numbers
can be re-taken. They are not a tool and nothing else should import them.

## Before running anything

```sh
export TART_NO_AUTO_PRUNE=1     # a clone must not evict the cached base image
df -h /                         # abort under ~8 GB free
tart list                       # Apple allows two VMs per host, run one
```

`du` lies on this host because it counts shared APFS clone extents. Read disk from `df`.

## Building the payload

```sh
./build-payload.sh /path/to/scratch/payload
```

Copies greenroom's repo, `apps/daemon` and `apps/companion` into a staging tree, then
builds the Go daemon with an isolated `GOMODCACHE`/`GOCACHE` and the Swift companion with
an isolated `--scratch-path`, so nothing is written into paths another track owns.

The `companion/.build` used for the real measurements was built **inside the guest** and
copied back, not built by this script. Host Swift is 6.4/macosx27 and guest Swift is
6.3.3/macosx26, and a cache from one is useless to the other. `build-payload.sh` gives you
a host-built tree; to reproduce the cache-reuse numbers, sync the companion source into a
guest, `swift build` there, and rsync `.build` back over the host-built one.

```sh
./payload-stats.py /path/to/scratch/payload
```

7,144 files, 1,735 directories, 17 symlinks, 748,670,619 bytes (714 MiB), 6,056 files
(84.8%) under 16 KiB totalling 13.9 MiB, mean 102.3 KiB.

## Measurements

| Guest baseline | |
| ------------------------------------ | ------ |
| Cold boot to `tart exec` | 22.6 s |
| Cold boot to ssh usable | 25.7 s |
| Cold `swift build`, fresh VM | 63.0 s |
| Cold `swift build`, warmed VM | 41.6 s |
| Rebuild, one file edited | 1.4 to 2.1 s |
| Rebuild, no change | 0.69 s |

| Cold transport of the payload | |
| ------------------------------------ | ------ |
| rsync `-az` (today's `machine_sync`) | 25.5 s |
| rsync `-a` | 6.7 s, 8.0 s on a repeat |
| Mutagen, agent auto-installed | 10.3 s |
| Mutagen, agent preinstalled | 10.6 s |
| Syncthing 2.1.5 (poll granularity 2 s) | 63.9 s |
| Offline injection into a stopped clone | 4.3 s total: attach 0.29, mount 0.40, ditto 3.19, detach 0.46 |
| `hdiutil create` a UDRW image of the payload | 9.0 s, 855 MB |
| Attached image, copy companion subtree in | 1.19 s for 380 MB / 2,382 files |
| VirtioFS, read the whole tree | 5.6 s |
| VirtioFS, copy to guest local disk | 9.0 s |
| git bundle, copy, clone | 0.06 + 0.12 + 0.14 s, source only |

| Incremental, one source file edited | |
| ------------------------------------ | ------ |
| Mutagen, host edit to guest visible | 0.18, 0.18, 0.15 s (includes ~0.11 s ssh probe) |
| rsync `-az`, per invocation | 0.80 s, 0.72 s with nothing changed |
| Syncthing, default `fsWatcherDelayS=10` | 11.26, 10.01, 9.78 s |

| Build using the transported cache, canonical path | |
| ------------------------------------ | ------ |
| rsync | 1.82 s |
| Mutagen | 2.07 s, then 0.12 s steady |
| Syncthing | 2.00 s, then 0.16 s steady |
| Attached image copied to local disk | 2.09 s |
| Offline injection | 1.94 s |
| Attached image remounted at the canonical path | 3.9 s |
| Attached image at `/Volumes/GRPAYLOAD` | **hard failure**, missing module `SwiftShims` |

| Filesystem tax, same VM, same tree | Local APFS | VirtioFS |
| ------------------------------------ | ---------- | -------- |
| Walk and stat 7,144 files | 0.05 s | 2.26 s |
| Cold `swift build` | 41.6 s | 54.4 s |
| Rebuild, no change | 0.69 s | 3.78 s |

Attached disk image, same walk: 0.15 s.

| Return path | |
| ------------------------------------ | ------ |
| Mutagen two-way, 3.4 MB binary | 0.41 s |
| rsync guest to host, 251 MB / 2,354 files | 3.18 s |
| Read-write image, `tart stop` + host attach | 2.5 s, VM must be stopped |
| VirtioFS | 0 s, already on the host |

| Full loop, Mutagen two-way | |
| ------------------------------------ | ------ |
| Host edit visible in guest | 0.13 s |
| Incremental build in guest | 2.01 s |
| Artifact back on host | 0.41 s |
| **Total** | **2.55 s** |

| Suspend and resume | |
| ------------------------------------ | ------ |
| `tart suspend` | 0.03 s, 1.89 GB `state.vzvmsave` |
| Clone the suspended VM | 0.56 s |
| Resume to ssh usable | 35.7 s, slower than a 25.7 s cold boot |
| Build after resume | 0.73 s compile, 12.6 s wall while memory pages in |

## Things that cost time to find

- A SwiftPM cache is keyed to its absolute path. At the wrong path the build fails with
  `missing required module 'SwiftShims'`, it does not silently rebuild.
- `rsync -z` costs 19 s on this payload over a virtual NIC, and 20 s of host CPU.
- Mutagen rewrites mtimes. SwiftPM and Go do not care; `make` and Xcode's legacy build
  system would.
- The Go module cache is mode 0444, so `rm -rf` over a transported `GOMODCACHE` fails with
  `Permission denied` on every file. `chmod -R u+w` first.
- `MUTAGEN_DATA_DIRECTORY` under a long path exceeds the 104-character Unix socket limit.
  The daemon then fails to start with a connection timeout and no explanation.
- VirtioFS returns `Too many levels of symbolic links` when `ditto` or `cp -a` copies
  extended attributes on a symlink. The symlink itself resolves correctly.
- `hdiutil attach -imagekey diskimage-class=CRawDiskImage -nomount` on a stopped clone's
  `disk.img` mounts `noowners`, so files land as uid 501. That is `admin:staff` in the
  guest, so project files are fine. It is still wrong for `root:wheel` LaunchDaemon plists.

## Cleanup

Every VM created here was a clone of `greenroom-base` named `trackc*` and was deleted.
`greenroom-base` and the two dated VMs were not touched.
