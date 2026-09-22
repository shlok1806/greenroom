# Transport spike

Scripts and raw numbers behind `docs/10-build-transport.md` (conclusions live there).
Run 2026-09-22: macOS 27.0 host, Tart 2.32.1, guest cloned from `greenroom-base`
(macOS 26.6.2, 4 vCPU, Swift 6.3.3). Throwaway: nothing should import these.

## Before running

```sh
export TART_NO_AUTO_PRUNE=1   # a clone must not evict the cached base image
df -h /                       # stop under ~8 GB free; use df, not du (du counts APFS clones)
tart list                     # two VMs per host; run one
```

## Payload

```sh
./build-payload.sh /path/to/scratch/payload   # repo + daemon + companion, isolated caches
./payload-stats.py /path/to/scratch/payload
```

7,144 files, 1,735 dirs, 17 symlinks, 714 MiB, 84.8% of files under 16 KiB.

`build-payload.sh` builds the Swift cache on the host. The measured runs used a cache
built in the guest (host Swift 6.4 caches are useless to guest Swift 6.3.3): sync the
companion into a guest, `swift build` there, rsync `.build` back.

## Numbers

| Guest baseline | |
| --- | --- |
| Cold boot to `tart exec` / ssh | 22.6 s / 25.7 s |
| Cold `swift build`, fresh / warmed VM | 63.0 s / 41.6 s |
| Rebuild, one edit / no change | 1.4-2.1 s / 0.69 s |

| Cold transport | |
| --- | --- |
| rsync `-az` | 25.5 s |
| rsync `-a` | 6.7 s (8.0 s repeat) |
| Mutagen, agent auto-installed / preinstalled | 10.3 s / 10.6 s |
| Syncthing 2.1.5 | 63.9 s |
| Offline injection | 4.3 s (attach 0.29, mount 0.40, ditto 3.19, detach 0.46) |
| `hdiutil create` UDRW image | 9.0 s, 855 MB |
| Attached image, copy companion in | 1.19 s (380 MB, 2,382 files) |
| VirtioFS read whole tree / copy to local | 5.6 s / 9.0 s |
| git bundle + copy + clone | 0.06 + 0.12 + 0.14 s, source only |

| One edit | |
| --- | --- |
| Mutagen | 0.18, 0.18, 0.15 s (incl. ~0.11 s ssh probe) |
| rsync per call | 0.80 s (0.72 s no change) |
| Syncthing (`fsWatcherDelayS=10`) | 11.26, 10.01, 9.78 s |

| Build on transported cache, canonical path | |
| --- | --- |
| rsync / Mutagen / Syncthing | 1.82 s / 2.07 s then 0.12 s / 2.00 s then 0.16 s |
| Attached image copied local / remounted at canonical path | 2.09 s / 3.9 s |
| Offline injection | 1.94 s |
| Attached image at `/Volumes/GRPAYLOAD` | fails: missing module `SwiftShims` |

| Filesystem tax | Local APFS | VirtioFS |
| --- | --- | --- |
| Stat 7,144 files | 0.05 s | 2.26 s (attached image: 0.15 s) |
| Cold `swift build` | 41.6 s | 54.4 s |
| No-op rebuild | 0.69 s | 3.78 s |

| Return path | |
| --- | --- |
| Mutagen two-way, 3.4 MB | 0.41 s |
| rsync, 251 MB / 2,354 files | 3.18 s |
| Read-write image | 2.5 s, VM must be stopped |

Full mutagen loop (edit visible + build + binary back): 0.13 + 2.01 + 0.41 = **2.55 s**.

| Suspend / resume | |
| --- | --- |
| `tart suspend` | 0.03 s, 1.89 GB state |
| Clone suspended VM | 0.56 s |
| Resume to ssh | 35.7 s (cold boot: 25.7 s) |
| First build after resume | 12.6 s wall while pages load |

## Gotchas found

- VirtioFS returns `Too many levels of symbolic links` when `ditto`/`cp -a` copies xattrs
  on a symlink.
- The rest (SwiftPM path keying, mtimes, Go cache 0444, mutagen socket length) are in
  `docs/10-build-transport.md`.

All VMs made here were `trackc*` clones and were deleted.
