# greenroom images

The image layer. Decisions and evidence are in
[`docs/09-image-strategy.md`](../docs/09-image-strategy.md). This file is how to run it.

```
ghcr.io/cirruslabs/macos-tahoe-base   upstream, MIT recipes, SIP off, TCC seeded
  └── greenroom-base                  this directory
        └── greenroom-xcode<N>        not written yet
```

We do not build the vanilla layer. It takes one to three hours and its only lasting
output is about 25 blind VNC keystrokes through Setup Assistant that Apple breaks every
point release. Start from Cirrus's base.

## Layout

| Path | What |
| ------------------------------------ | ----------------------------------------------- |
| `greenroom-base.pkr.hcl` | The Packer build |
| `greenroom-xcode.pkr.hcl` | The Xcode layer. Written, never built |
| `data/expected-simulator-runtimes.txt` | Seeded by the first successful Xcode build |
| `scripts/greenroom-tcc.sh` | TCC rows for our binaries, screen-recording fix |
| `scripts/firstboot.sh` | Reads the seed disk, personalizes the VM |
| `scripts/smoke-test.sh` | Fails the build if computer use does not work |
| `data/com.greenroom.firstboot.plist` | LaunchDaemon for the above |
| `make-seed.sh` | Builds a per-VM seed disk |

## Prerequisites

- Apple silicon, macOS 27 or newer
- `tart` 2.36.0 or newer for `clone --stacked`
- `packer`
- About 200 GB free. This is not a rough guess: the dev host had 19 GB and could not
  build the Xcode layer at all.

**Do not `brew upgrade tart`. It does not work.** The `cirruslabs/homebrew-cli` tap is
stale at 2.32.1 and its formula no longer evaluates against current Homebrew
(`Calling depends_on :macos with depends_on macos: is disabled!`). Tart also moved:
`github.com/cirruslabs/tart` no longer resolves and the project is now at
`github.com/openai/tart` (ADR 0010). Install from the release tarball, which is still
signed by Cirrus Labs and published with checksums:

```sh
V=2.37.0
curl -sLO "https://github.com/openai/tart/releases/download/$V/tart.tar.gz"
curl -sL  "https://github.com/openai/tart/releases/download/$V/tart_${V}_checksums.txt" \
  | grep tart.tar.gz | shasum -a 256 -c -      # verify before extracting
mkdir -p ~/.local/tart-$V && tar xzf tart.tar.gz -C ~/.local/tart-$V
~/.local/tart-$V/tart.app/Contents/MacOS/tart --version
```

Installing side-by-side and leaving `/opt/homebrew/bin/tart` alone is the safer move if a
daemon is running: swapping the binary under a daemon that is holding a VM is not
something to do casually. Every argument shape the daemon uses is unchanged in 2.37.0.

The daemon pins this same version and finds it at this same path without any help from
`PATH`: `tart.PinnedVersion` in `apps/daemon/internal/tart` is the single source of truth,
and `apps/daemon/CLAUDE.md` describes the resolution order and the `-tart` flag. Keep the
version here and there in step.

Do this once on the host, or SSH into guests fails with `No route to host` on Sequoia and
later. That is the Local Network privacy permission, not a network fault.

```sh
sudo defaults write com.apple.network.local-network \
  AllowedEthernetLocalNetworkAddresses -array "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16"
# reboot
export TART_NO_AUTO_PRUNE=1   # otherwise a routine clone can evict a 27 GB cache
```

## Build

```sh
tart pull ghcr.io/cirruslabs/macos-tahoe-base:latest     # ~27 GB, ~15 min

packer init  images/greenroom-base.pkr.hcl
packer build -var "ssh_pubkey=$(cat ~/.greenroom/id_ed25519.pub)" \
             images/greenroom-base.pkr.hcl
```

The build fails if the smoke test fails, on purpose. TCC never returns an error, it just
hands back black frames, so an image that builds cleanly can still be broken. Better to
catch it here than mid-run in a customer's PR.

## Run a personalized machine

```sh
tart clone greenroom-base "run-$(uuidgen)"               # 0.03 s, no extra disk

./images/make-seed.sh /tmp/run-123-seed.dmg \
  --hostname gr-run-123 \
  --repo git@github.com:acme/app.git \
  --branch fix-123 \
  --authorized-keys ~/.greenroom/id_ed25519.pub \
  --env /tmp/run-123.env

tart run --no-graphics --suspendable \
         --disk "/tmp/run-123-seed.dmg:ro" run-123

until tart exec run-123 true 2>/dev/null; do sleep 1; done   # 19 to 48 s
```

Poll `tart exec`, not `tart ip`. `tart ip` answers at about 13 s, but the guest agent is
not up until about 32 s on a cold image.

This path has been run end to end and works: the seed mounts at `/Volumes/GRSEED`, `:ro`
is enforced, the hostname is set, `authorized_keys` and `run.env` land as `admin:staff`
mode 600, and the repo clones as `admin:staff`. Two files tell you what happened:

| Path in the guest | What |
| --------------------------------- | -------------------------------------------- |
| `/var/log/greenroom-firstboot.log` | The run, line by line |
| `/var/db/greenroom-firstboot.json` | `status`, the values applied, `failed` steps |

`status` is `ok`, `failed` or `no-seed`. The stamp at
`/var/db/.greenroom-personalized` is written **only** when every step succeeded, so a VM
whose clone failed retries on its next boot instead of reporting itself as ready. Booting
with no seed disk at all is not a failure: the VM stays generic and can still be
personalized later.

Always pass `--suspendable`. Without it `tart suspend` looks like it worked, writes a
3 GB state file, and the resume then fails with `VZErrorDomain Code=12`, including for
the original VM. There is no error at suspend time.

## Build the Xcode layer

Not yet built, and it needs about 200 GB free plus a `.xip` that only a person can
fetch. Apple ID credentials never go in the pipeline, so download the `.xip` once by
hand from https://developer.apple.com/download/all/?q=Xcode and keep it at
`~/XcodesCache/Xcode_<version>.xip`.

```sh
packer init  images/greenroom-xcode.pkr.hcl
packer build -var "xcode_version=26.1" \
             -var "xip_path=$HOME/XcodesCache/Xcode_26.1.xip" \
             images/greenroom-xcode.pkr.hcl
```

`xcode_version=26.1` produces `greenroom-xcode26`: a point release is not a new lineage.
The build asserts free space before and after, that the simulator runtimes match
`data/expected-simulator-runtimes.txt`, and that the base image's smoke test still
passes with Xcode on top. That expected-runtimes file ships **empty on purpose**, because
a guessed list would fail every build for a reason that is not real. The first build
prints the runtimes it found and stops so a person can check them in; after that the diff
is a real assertion.

Note that `--stacked` cannot stack on a local VM, so both layers have to be pushed to a
registry before anything can stack on them. See `docs/09-image-strategy.md`.

## Things that will bite

**`sync` before you stop a VM.** Writing files into a guest and then running `tart stop`
does not flush the guest's dirty pages, and the files are simply gone on the next boot.
This cost a debugging cycle while proving the first-boot daemon: the LaunchDaemon plist
and its script vanished, and `launchctl bootstrap` then reported
`Bootstrap failed: 5: Input/output error`, which reads like the ownership problem
described below but was not. The file was not there at all. `apps/daemon/CLAUDE.md`
records the same failure for `PrepareGuest`. Always `sync`, and when you see
`Bootstrap failed: 5`, check the file exists before suspecting ownership.

Secrets never go in the image. Layers are content-addressed and immutable, so a pushed
secret cannot be taken back out. Secrets go on the seed disk or a per-run
`--dir=...:ro` mount.

Do not mutate disk images offline. It works, since the guest Data volume is unencrypted
and mounts read-write on the host, but without host `sudo` the mount is `noowners`, files
land as uid 501, and launchd refuses a LaunchDaemon plist that is not `root:wheel`. Keep
`hdiutil attach -imagekey diskimage-class=CRawDiskImage` for debugging a VM that will not
boot, which is what it is good for.

Two VMs per host, enforced by the framework. An image build takes one slot.

The image has SIP disabled. TCC writes need it and there is no alternative, since MDM
cannot grant Screen Recording. Say so rather than let a reviewer discover it.

Anything that touches the screen runs as a LaunchAgent, not a LaunchDaemon. A daemon has
no Aqua session and nothing to capture.

## Adding greenroom's computer-use binary (M2)

Add its realpath to `GREENROOM_BINARIES` in `scripts/greenroom-tcc.sh` and install it as
a LaunchAgent. TCC checks the responsible process, so anything run over SSH already
inherits the base image's grants. These rows are for binaries started some other way.
