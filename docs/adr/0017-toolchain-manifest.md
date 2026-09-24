# 0017. The image reports its toolchain; the daemon passes it on

Date: 2026-09-24
Status: accepted

## Context

The default images have the Command Line Tools and no Xcode. `swift test` fails there
with `no such module 'XCTest'` and, without extra search paths, `no such module
'Testing'` (issue #44). An agent learns this only by trying, and one then deleted the
project's own XCTest file in the guest so its new tests could pass, and reported the run
green.

An Xcode layer would fix the toolchain, but it needs a hand-downloaded `.xip` and about
200 GB of disk. Adding the CLT frameworks to the search paths is a hack that breaks with
the next CLT and still leaves XCTest out. Hard-coding "no Xcode" in the daemon would be
wrong for any other image the daemon is pointed at.

## Decision

1. No Xcode layer, and no CLT search-path patches, for now.
2. `prepare-image` measures the toolchain at build time and writes
   `/usr/local/greenroom/toolchain.json` (`guest/toolchain.sh`): whether Xcode is present
   and where, the active developer directory, whether an XCTest package and a
   swift-testing package each build and pass with a plain `swift test`, `swift --version`
   and the CLT package version. Measured by running them, not inferred from paths.
3. At boot the daemon reads that file and returns it as the machine's `toolchain`, which
   `machine_wait` returns. The daemon does not interpret it. An image without the file
   reports `{"known": false}`.
4. `machine_exec`'s description tells agents to check `toolchain` from `machine_wait`
   before running tests, and never to delete or exclude a project's existing tests to get
   a green run.

## Consequences

- An image build takes about a minute longer (two small `swift test` runs).
- The manifest is as true as the image. A toolchain installed in a machine after boot is
  not in it.
- The Xcode question stays open (issue #44), for when there is disk for it.
