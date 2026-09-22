# Build transport: getting a warm project onto a machine

## Recommendation (2026-09-22)

- **Transport:** mutagen two-way sync as a session bound to the run; rsync `-a` stays as
  the one-shot `machine_sync` and the fallback. Blocked on a licence call (below).
- **API shape:** `machine_create` takes a project path and syncs as a fifth boot phase
  with its own budget and timing (option C). The companion shows the phases. A warm
  machine per active project (option B) waits for a fleet.
- **Pin the guest path** `/Users/admin/work/<project>`. **Done** (`GuestWorkDir`).
- **Drop `-z` from rsync.** **Done.**
- Do not use VirtioFS for build trees, offline injection, attached disks, or suspend and
  resume in the per-run path.

Raw numbers and scripts: `spikes/transport/`. Measured on macOS 27.0 host, Tart 2.32.1,
guest from `greenroom-base` (4 vCPU, Swift 6.3.3).

## The number that matters

Not the first sync (paid once, 10 s is fine) but **edit to visible in the guest**, paid on
every change. Under a second feels local; ten seconds loses the developer.

Payload: greenroom's repo built warm, 7,144 files, 714 MiB, 85% under 16 KiB. The Swift
cache was built in the guest (host and guest Swift differ, so a host cache is useless).

| Baseline in the guest | |
| --- | --- |
| Cold boot to ssh usable | 25.7 s |
| Cold `swift build` (warmed VM) | 41.6 s |
| Rebuild after one edit | 1.4-2.1 s |

A cache that transfers correctly turns 41.6 s into ~2 s.

## Results

| Mechanism | Cold | One edit | Cache used |
| --- | --- | --- | --- |
| **Mutagen 0.18.1** | 10.6 s | **0.13-0.18 s**, automatic | yes |
| rsync `-a` | **6.7-8.0 s** | 0.80 s per call | yes |
| rsync `-az` (old `machine_sync`) | 25.5 s | 0.80 s per call | yes |
| Offline injection into stopped clone | 4.3 s | impossible after boot | yes |
| Attached disk image | 9.0 s build + copy | needs restart | only at canonical path |
| VirtioFS `--dir` | 0 s | 0 s | yes, but 45x slower stat, 5.5x slower no-op rebuild |
| Syncthing 2.1.5 | 63.9 s | ~10 s | yes |
| git bundle | 0.3 s | n/a | no cache at all |

**Full loop with mutagen two-way:** edit visible 0.13 s + build 2.01 s + binary back on
host 0.41 s = **2.55 s**, against 1.4-2.1 s locally.

## Gotchas any tool hits

- **SwiftPM caches are keyed to their absolute path.** At another path the build fails
  with `missing required module 'SwiftShims'`, it does not just rebuild.
- **Mutagen rewrites mtimes.** SwiftPM and Go do not care; `make` and Xcode's legacy build
  system might. Test per project type.
- **Go's module cache is mode 0444.** `chmod -R u+w` before deleting or re-syncing over it.
- **`MUTAGEN_DATA_DIRECTORY` must be short** (<104-char socket path) or the daemon times
  out with no explanation. Use something like `~/.greenroom/mutagen`.

## Return path

Mutagen two-way 0.41 s for a 3.4 MB binary; rsync 3.18 s for 251 MB. A read-write disk
image needs the VM stopped, so it is out.

## Warm machines lose

Resume of a suspended VM with a built project: 35.7 s to ssh, then 12.6 s of page-in,
against a 25.7 s cold boot. Cold boot plus a mutagen sync (~36 s) is faster and simpler.

## Costs of mutagen

- **Licence:** official builds since v0.17 include SSPL code. Building without `--sspl` is
  MIT-only. SSPL matters for a hosted product (see ADR 0010). **Needs a decision.**
- Slow release cadence (v0.18.1, 2025-02-24), but alive and Go, so vendorable.
- A second host daemon to supervise.

## Image recommendations

- Bake the mutagen agent (7.9 MB) pinned to the host version. Not for speed (saves
  ~0.3 s) but to remove a per-run install step.
- Pre-create `~/work` as `admin:staff`.
- Bake GitHub `known_hosts`.
- Consider `mdutil -a -i off` and disabling local snapshots (unverified).

## Not tested

Xcode-scale payloads (disk), two machines at once, Syncthing with a lower watch delay,
anything over a real network (where `-z` would help again).

## Open questions

1. Mutagen SSPL: official build, MIT-only build from source, or stay on rsync?
2. How much of the developer's tree may greenroom watch continuously?
3. Prime a project's first run eagerly, spending a machine slot?
