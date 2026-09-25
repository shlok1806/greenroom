# 11. Testing greenroom across two Macs

One Mac plays the cloud: it runs the daemon, Tart and the machines. The other Mac, a
friend's laptop, runs only Claude Code and the Companion and reaches the first through
`https://greenroom.shlokthakkar.com`. Why and how it is built: ADR 0021.

```
friend's Mac                          Cloudflare                 host Mac (the "cloud")
Claude Code                                                       cloudflared
  -> greenroom connect  --HTTPS + token-->  tunnel  ------------> 127.0.0.1:7777 greenroom daemon
Greenroom Companion     --HTTPS + token-->                           -> Tart macOS VMs
```

## On the host Mac (yours)

### Once

1. Make the token and name the public host. This writes `GREENROOM_TOKEN` and
   `GREENROOM_PUBLIC_HOST` into the repo's `.env`, then reloads the daemon so it reads them:

   ```sh
   scripts/remote/host.sh token
   apps/daemon/scripts/install.sh
   ```

2. Create the tunnel and its DNS name (needs `cloudflared tunnel login` done already):

   ```sh
   scripts/remote/host.sh tunnel-setup
   ```

3. Build what the friend downloads (the Companion app, the `greenroom` binary, the
   installer) into `~/.greenroom/dist/`. Run it again after pulling new code:

   ```sh
   scripts/remote/host.sh dist
   ```

### Every test session

1. Open the tunnel in a terminal and leave it running. Closing it takes your Mac off the
   internet again.

   ```sh
   scripts/remote/host.sh tunnel
   ```

2. In another terminal, check it from the outside:

   ```sh
   scripts/remote/host.sh check
   ```

3. Send your friend the install command with the token (`scripts/remote/host.sh token --show`
   prints it). Send it privately: anyone with the token can drive your machines.

## On your friend's Mac

Needs: Apple silicon, macOS 15 or later, Claude Code installed and logged in.

1. Paste this into Terminal, with the token you were sent:

   ```sh
   curl -fsSL https://greenroom.shlokthakkar.com/install.sh | sh -s -- <token>
   ```

   It installs the Greenroom Companion app and the small `greenroom` client, saves the
   address and token in `~/.greenroom/client.json`, checks it can reach the host, adds
   greenroom to Claude Code, and opens the Companion. Nothing else: no VM, no Xcode, no Go.

2. Open Claude Code in any project folder and ask, for example:

   > Use greenroom to create a machine, sync this project into it, build it and take a
   > screenshot.

   Claude Code starts a macOS machine on the host, uploads the project folder (including
   edits that are not committed), builds it there and shows the screen. Files it copies
   back with `machine_pull` and each screenshot's PNG land on your Mac, under
   `~/.greenroom/connect/runs/<runId>/` unless it names another folder (ADR 0022). The Companion
   shows the run, its steps and the live screen, and "take control" lets you click in it.

3. When done, ask Claude Code to destroy the machine, or press destroy in the Companion.

Running the installer again updates everything in place (for example after the host
rebuilt `dist` or rotated the token).

## When something is wrong

| What you see | Why | Fix |
| --- | --- | --- |
| Installer: `could not reach` / connection error | The host's tunnel is not running | Host: `scripts/remote/host.sh tunnel` |
| Installer or Companion: `unauthorized` / token rejected | Token typo, or the host rotated it | Re-run the installer with the current token |
| Companion: `Host must be a loopback address or the configured public host` | The host's `.env` has no or a different `GREENROOM_PUBLIC_HOST` | Host: `scripts/remote/host.sh token`, then `apps/daemon/scripts/install.sh` |
| `machine_create`: capacity error | Apple allows two macOS VMs per host | Destroy an old machine first |
| Claude Code has no greenroom tools | `claude` was not found during install | `claude mcp add -s user greenroom -- ~/.greenroom/bin/greenroom connect` |

## Uninstall (friend's Mac)

```sh
claude mcp remove greenroom -s user
rm -rf "/Applications/Greenroom Companion.app" ~/.greenroom
```

## Afterwards (host)

Stop the tunnel (Ctrl-C). To cut off the token for good, `scripts/remote/host.sh token --rotate`
and reinstall the daemon.
