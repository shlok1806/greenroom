# Transport: work in, evidence out

**Conclusion:** keep rsync over ssh for the working-tree delta, use the guest agent
channel (vsock) for everything small and sensitive, and put bulk (toolchain, repo, build
cache) in an image or snapshot rather than sending it per run. Never put a secret in an
image or a command line. The build-cache follow-up is `10-build-transport.md`.

Measured 2026-09-14 on a 36 GB macOS 26.7 host, Tart 2.32.1, `macos-tahoe-base`.

Note: this doc assumed the verifier would be headless Claude Code inside the guest
(ADR 0003). ADR 0005 moved the verifier onto the host, so the "run Claude Code in the
guest" advice below is on hold.

## Five payloads, five answers

| Payload | Size | Changes | Where it should travel |
| --- | --- | --- | --- |
| Toolchain | 10-70 GB | weekly | image |
| Repo + dependencies + build cache | 100 MB-GBs | daily | snapshot or attached disk, then delta |
| Run delta | KB-MB | per run | rsync |
| Agent context (task, config) | KB | per run | guest agent channel |
| Secrets | bytes | per run | guest agent channel, never disk or image |
| Evidence out | KB-GB | continuous | guest agent channel; share for large files |

## Channels

| Channel | Ready | Network | Key | Set at |
| --- | --- | --- | --- | --- |
| Guest agent gRPC (`tart exec`, vsock) | ~28 s after start | no | no | any time |
| ssh / rsync / scp | after DHCP + key | yes | yes | any time |
| virtiofs (`tart run --dir`) | boot | no | no | boot only |
| Attached disk (`tart run --disk`) | boot | no | no | boot only |
| Guest to host HTTP via NAT (192.168.64.1) | after DHCP | yes | no | any time |
| Image / APFS clone | before boot | no | no | image build |

The guest agent's gRPC `Exec` (over `~/.tart/vms/<name>/control.sock`) supports env,
workdir, user, detach and streamed stdin, which the `tart exec` CLI does not expose.
Calling it directly would keep secrets out of host `ps`. It is not a public interface, and
Tart is now OpenAI's (ADR 0010).

## Measurements

5,000 small files (~50 MB) and one 512 MB file.

| Into the guest | 5,000 files | 512 MB |
| --- | --- | --- |
| rsync over ssh, first | **1.18 s** | 2.22 s |
| rsync, no change / one file changed | **0.35 s** | - |
| tar through guest agent stdin | 3.22 s | 2.19 s |
| guest curl from host HTTP | - | **0.69 s** |
| copy from virtiofs share | 15.49 s | 0.40 s |

| On a virtiofs share | |
| --- | --- |
| `git status` | 5.87 s |
| `wc -l` on 5,000 files | 2.36 s (0.63 s on guest disk) |

| Out of the guest | |
| --- | --- |
| 3 MB PNG via guest agent base64 | 0.13 s |
| 3 MB PNG via rsync | 0.18 s |

Takeaways:

- virtiofs is fast per byte, slow per file. Never build or run git on it.
- rsync's cost is round trips, not bytes: 0.35 s for a small delta.
- The guest agent channel works before an IP exists and under any network policy.
- An APFS clone is free (0.11 s, no disk). Anything already in an image costs nothing.

## Options considered

| Option | Verdict |
| --- | --- |
| A. rsync per run | Current. Best delta path. Needs IP and key. Takes a host path. |
| B. Snapshot with repo + cache, then delta | Biggest win for real builds. Needs refresh and delete rules. |
| C. git (clone, bundle, fetch) | Carries no uncommitted work and no build cache. |
| D. virtiofs share of the tree | Only for one large read-only input or large outputs. |
| E. tar over guest agent | Works pre-network; not incremental. |
| F. Guest pulls from daemon HTTP | Fastest bulk, but opens a socket to untrusted guests; impossible under Softnet. |
| G. Continuous two-way sync | Re-examined in `10-build-transport.md` (mutagen). |
| H. Attached disk / NBD | Only if project inputs outgrow snapshots. |

Every shipped product (E2B, Cursor, Codex) uses a snapshot plus a delta (option B). See
`06-sandbox-survey.md`.

## Secrets

1. Never in an image or snapshot. Layers are copyable and immutable.
2. Never in a command string (visible in host `ps`). Use gRPC env or stdin.
3. Short-lived, fetched at point of use. The daemon pushes a fresh token over vsock on a
   timer; an `apiKeyHelper` in the guest only reads it.
4. Keep the long-lived credential outside the machine (proxy pattern, like Claude Code on
   the web).
5. Redaction protects the model, not the machine. Anything in the VM is readable by what
   the agent runs.

Tart's default NAT does not isolate VMs from each other. Softnet does, but needs root and
blocks guest-to-host. vsock has no neighbours. Decide egress policy before any machine
holds a real token.

## Recommended order

1. Talk gRPC to `control.sock` instead of spawning `tart exec`.
2. Add `machine_put` over the guest agent for manifests, config, keys, small files.
3. Keep rsync for the delta, but make the tool take content, not a host path, so a hosted
   daemon can use the same API.
4. Build per-project snapshots or dependency disks.
5. Keep screenshots on the guest agent channel.
6. Turn on `--net-softnet` before a second concurrent machine holds a credential.

## Open questions

1. Does a run write back to the host tree, or return a patch/PR?
2. Where does the build cache live between runs: per project or per branch?
3. Does a machine get internet access?
4. Do we build on Tart internals (`control.sock`, the proto) now that OpenAI owns Tart?
