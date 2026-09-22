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
| `scripts/greenroom-tcc.sh` | TCC rows for our binaries, screen-recording fix |
| `scripts/firstboot.sh` | Reads the seed disk, personalizes the VM |
| `scripts/smoke-test.sh` | Fails the build if computer use does not work |
| `data/com.greenroom.firstboot.plist` | LaunchDaemon for the above |
| `make-seed.sh` | Builds a per-VM seed disk |

## Prerequisites

- Apple silicon, macOS 27 or newer
- `tart` 2.36.0 or newer for `clone --stacked`. The dev host was on 2.32.1, so
  `brew upgrade tart`.
- `packer`
- About 200 GB free

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
tart clone greenroom-base "run-$(uuidgen)"               # 0.074 s, no extra disk

./images/make-seed.sh /tmp/run-123-seed.dmg \
  --hostname gr-run-123 \
  --repo git@github.com:acme/app.git \
  --branch fix-123 \
  --authorized-keys ~/.greenroom/id_ed25519.pub \
  --env /tmp/run-123.env

tart run --no-graphics --suspendable \
         --disk "/tmp/run-123-seed.dmg:ro" run-123

until tart exec run-123 true 2>/dev/null; do sleep 1; done   # ~15 s
```

Poll `tart exec`, not `tart ip`. `tart ip` answers at about 13 s, but the guest agent is
not up until about 32 s on a cold image.

Always pass `--suspendable`. Without it `tart suspend` looks like it worked, writes a
3 GB state file, and the resume then fails with `VZErrorDomain Code=12`, including for
the original VM. There is no error at suspend time.

## Things that will bite

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
