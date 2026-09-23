# images

Packer recipes and guest scripts for greenroom's VM images. Why it is built this way:
[`docs/09-image-strategy.md`](../docs/09-image-strategy.md).

```
ghcr.io/cirruslabs/macos-tahoe-base   upstream, SIP off, TCC seeded
  └── greenroom-base                  greenroom-base.pkr.hcl
        └── greenroom-xcode<N>        greenroom-xcode.pkr.hcl (written, never built)
```

Note: `apps/daemon/scripts/build-image.sh` also makes a VM named `greenroom-base`, with
the input helper and ssh key baked in but none of the TCC, display or first-boot work
below. The two are not merged yet. The daemon's CI and `install.sh` expect the
`build-image.sh` one.

## Layout

| Path | What |
| --- | --- |
| `greenroom-base.pkr.hcl` | Base layer build |
| `greenroom-xcode.pkr.hcl` | Xcode layer build |
| `scripts/greenroom-tcc.sh` | TCC rows for our binaries, screen-capture alert fix (replayd approvals), desktop preferences |
| `scripts/firstboot.sh` | Reads the seed disk, personalizes the VM |
| `scripts/smoke-test.sh` | Fails the build if screenshots or input do not work |
| `data/com.greenroom.firstboot.plist` | LaunchDaemon for `firstboot.sh` |
| `data/expected-simulator-runtimes.txt` | Empty until the first Xcode build fills it |
| `make-seed.sh` | Builds a per-VM seed disk |

## Prerequisites

- Apple silicon, macOS 27 or newer.
- Tart 2.37.0 from the signed release, not Homebrew (install steps in
  `apps/daemon/CLAUDE.md`). Keep it in step with `tart.PinnedVersion`.
- Packer: `brew install hashicorp/tap/packer`.
- Disk: the pulled base is ~28 GB; the Xcode layer needs ~200 GB free.
- `export TART_NO_AUTO_PRUNE=1`, or a clone can evict the 27 GB cached base.
- If ssh to a guest fails with `No route to host`, allow local network for Ethernet
  ranges, then reboot:

  ```sh
  sudo defaults write com.apple.network.local-network \
    AllowedEthernetLocalNetworkAddresses -array "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16"
  ```

## Build the base layer

```sh
tart pull ghcr.io/cirruslabs/macos-tahoe-base:latest     # ~27 GB, ~15 min
packer init  images/greenroom-base.pkr.hcl
packer build -var "ssh_pubkey=$(cat ~/.greenroom/id_ed25519.pub)" images/greenroom-base.pkr.hcl
```

Variables: `base_image`, `vm_name` (default `greenroom-base`), `ssh_pubkey`, `display`
(default `1512x982`). The build fails if the smoke test fails, on purpose: TCC never
errors, it just returns black frames.

## Run a personalized machine

```sh
tart clone greenroom-base run-123
./images/make-seed.sh /tmp/run-123-seed.dmg \
  --hostname gr-run-123 --repo git@github.com:acme/app.git --branch fix-123 \
  --authorized-keys ~/.greenroom/id_ed25519.pub --env /tmp/run-123.env
tart run --no-graphics --suspendable --disk "/tmp/run-123-seed.dmg:ro" run-123
until tart exec run-123 true 2>/dev/null; do sleep 1; done   # 19-48 s
```

Poll `tart exec`, not `tart ip` (IP answers ~20 s before the guest agent).

| In the guest | What |
| --- | --- |
| `/var/log/greenroom-firstboot.log` | the run, line by line |
| `/var/db/greenroom-firstboot.json` | `status` (`ok`, `failed`, `no-seed`), values, failed steps |
| `/var/db/.greenroom-personalized` | written only on full success, so failures retry next boot |

No seed disk is fine: the VM stays generic.

## Build the Xcode layer

Download the `.xip` by hand (no Apple ID in the pipeline) to
`~/XcodesCache/Xcode_<version>.xip`, then:

```sh
packer init  images/greenroom-xcode.pkr.hcl
packer build -var "xcode_version=26.1" \
             -var "xip_path=$HOME/XcodesCache/Xcode_26.1.xip" \
             images/greenroom-xcode.pkr.hcl
```

Produces `greenroom-xcode26`. The first build prints the simulator runtimes it found and
stops; check them into `data/expected-simulator-runtimes.txt`, and later builds assert
against it.

## Gotchas

- **`sync` before `tart stop`.** Otherwise files written just before are gone on next
  boot. `Bootstrap failed: 5` often means the plist is missing, not mis-owned.
- **Always `--suspendable`** if you might suspend. Without it resume fails with
  `VZErrorDomain Code=12`, with no error at suspend time.
- **No secrets in images.** Pushed layers are immutable. Use the seed disk or a per-run
  read-only `--dir`.
- **No offline disk edits.** Files land as uid 501 and launchd rejects non-`root:wheel`
  plists. Keep `hdiutil attach -imagekey diskimage-class=CRawDiskImage` for debugging.
- **Two VMs per host.** An image build takes a slot.
- **SIP is off.** Required for TCC writes.
- **Screen work runs as a LaunchAgent**, never a LaunchDaemon.
- To grant a new greenroom binary TCC, add its realpath to `GREENROOM_BINARIES` in
  `scripts/greenroom-tcc.sh`. Anything run over ssh or `tart exec` already inherits the
  base grants.
