# Transport: how to move work into a machine, and evidence out

Status: research and measurements. No decision yet. This document is for the boundary between
M1 and M2. It is written in ASD-STE100 Simplified Technical English.

The measurements are from 2026-09-14 on this Mac. The host has 36 GB of memory and macOS 26.7.
Tart is version 2.32.1. The guest image is `ghcr.io/cirruslabs/macos-tahoe-base:latest`. These
numbers extend `docs/02-spike.md`. The scripts are not in the repository. They are in
`~/.greenroom/spikes/transport/`, with their raw output.

The daemon has one transport today. `machine_sync` runs rsync over ssh from a host directory
([`manager.go`](../apps/daemon/internal/machine/manager.go)). It works. The measurements show
that it is a good answer for one of the five payloads. It is the wrong answer for the other
four.

## 1. Five payloads, not one

The request to move the context and the files is five different problems.

| Payload             | Size          | Rate of change | Same for each run | Sensitive |
| ------------------- | ------------- | -------------- | ----------------- | --------- |
| Toolchain           | 10 to 70 GB   | weekly         | yes               | no        |
| Repository and dependencies | 100 MB to GBs | daily  | mostly            | sometimes |
| Run delta           | KBs to MBs    | each run       | no                | no        |
| Agent context       | KBs           | each run       | no                | no        |
| Secrets             | bytes         | each run       | no                | **yes**   |
| Evidence (outbound) | KBs to GBs    | continuous     | no                | sometimes |

Do not select one channel and send all five payloads through it. The toolchain must not cross
the network on each run. A secret must not go to disk at all.

## 2. The channels that the machine has

A Tart guest gives more than ssh. Section 3 gives the measurements for each channel.

| Channel | Available | Needs network | Needs key | Configure at |
| ------- | --------- | ------------- | --------- | ------------ |
| Guest agent RPC (`tart exec`, gRPC over vsock) | 28 s after start | no | no | any time |
| ssh, rsync, scp | after DHCP and key installation | yes | yes | any time |
| virtiofs share (`tart run --dir`) | after boot | no | no | **boot** |
| Attached disk (`tart run --disk`: file, block device or NBD URL) | after boot | no | no | **boot** |
| Guest to host HTTP through the NAT gateway | after DHCP | yes | no | any time |
| OCI image and APFS clone (`tart pull`, `tart clone`) | before boot | no | no | **image build** |

Two channels accept configuration only at boot. This is a constraint on the tools.
`machine_create` must know about shares and disks, because nothing can add one later.

### The guest agent channel gives more than the CLI shows

`tart exec` is a small client for a gRPC service. The guest agent listens on vsock port 8080.
Tart connects that port to a unix socket on the host at `~/.tart/vms/<name>/control.sock`. The
service has three calls: `Exec`, `Signal` and `ResolveIP`. `Exec` is a bidirectional stream.
The `Exec` request holds more fields than the CLI makes available: an env map, a workdir, a
user, a detach flag, and streamed stdin.

These fields are important for secrets. The `tart exec` CLI has no `--env` option. Today a
token must go into a file in the guest, or into the command string. A token in the command
string is visible in `ps` output on the host for the length of the call. The daemon can connect
to `control.sock` directly. Use Go stubs from the guest agent file
[`proto/v1/agent.proto`](https://github.com/cirruslabs/tart-guest-agent). This keeps the token
out of the process arguments. It also removes one subprocess for each call.

Caution: Tart moved from `cirruslabs/tart` to [`openai/tart`](https://github.com/openai/tart)
after the acquisition in April 2026 (`docs/04-landscape.md`). Neither the socket path nor the
proto file is a documented public interface. Use of them is a risk, because we do not control
that repository. The risk is small if this code stays behind the `internal/tart` package.

## 3. Measurements

The test data has two parts. The first part is 5,000 small files in 100 directories, about
50 MB in total. This is the shape of a source tree. The second part is one 512 MB file of
incompressible data. This is the shape of a build artifact.

### Into the machine

| Path                                                    | 5,000 small files | 512 MB file |
| ------------------------------------------------------- | ----------------- | ----------- |
| rsync over ssh, first run                                | **1.18 s**        | 2.22 s      |
| rsync over ssh, no changes                               | **0.35 s**        | -           |
| rsync over ssh, one file changed                         | **0.35 s**        | -           |
| tar through the guest agent stdin                        | 3.22 s            | 2.19 s      |
| curl from the guest to an HTTP server on the host        | not measured      | **0.69 s**  |
| copy from a virtiofs `--dir` share to the guest disk     | **15.49 s**       | 0.40 s      |

### Work directly on a virtiofs share

| Operation                             | On the share | On the guest disk |
| ------------------------------------- | ------------ | ----------------- |
| `git status` on the test repository   | 5.87 s       | -                 |
| `wc -l` on all 5,000 files            | 2.36 s       | 0.63 s            |
| Write 100 files of 500 KB             | 7.06 s       | not measured      |
| Write one 200 MB file                 | 0.21 s       | -                 |

### Out of the machine

| Path                                              | Measured |
| ------------------------------------------------- | -------- |
| `screencapture` to the guest disk                  | 0.56 s   |
| `screencapture` directly to a writable share       | 0.68 s   |
| 3 MB PNG out through the guest agent as base64     | 0.13 s   |
| 3 MB PNG out through rsync                         | 0.18 s   |

### What the measurements show

1. **virtiofs is fast for each byte and slow for each file.** A sequential read gives 1.3 GB/s.
   A 200 MB write takes 0.21 s. Operations on many files are much slower. A copy of 5,000 files
   from the share takes 15.5 s. rsync sends the same tree into the guest in 1.18 s, which is
   thirteen times faster. `git status` on the share takes 5.9 s. Do not build on a share. Do not
   run git on a share. Use a share for one large input file, or for one large output artifact.
2. **rsync over ssh is the best channel for a delta.** A sync with no changes takes 0.35 s. A
   sync after one file changes also takes 0.35 s. The cost is the round trips, not the bytes.
3. **The guest agent channel is fast enough to be the default.** It streams at 234 MB/s. It
   sends the 5,000 file tree in 3.2 s. It needs no key, no network and no DHCP. It is the only
   channel that works before the machine has an IP address. It is also the only channel that a
   network policy cannot stop.
4. **The guest can reach the host at the NAT gateway address 192.168.64.1.** This is confirmed.
   An HTTP server on the host sent the 512 MB file to the guest at about 740 MB/s. This is three
   times faster than rsync, because this path has no ssh encryption. The daemon runs an HTTP
   server already. It binds to 127.0.0.1 today.
5. **An APFS clone is free.** A clone of the 33 GB base image takes 0.11 s and uses no more
   disk. Content that is already in a snapshot arrives at a new machine at no cost.

## 4. The options

### A. rsync over ssh for each run (the current method)

Method: install the daemon public key through the guest agent at first boot. Then run `rsync -a`
from a host path.

Advantages: the fastest delta path. It is incremental. It works in both directions. We have the
dependency already.

Disadvantages: the guest must have an IP address and a key, so this cannot run during boot. It
needs network access, which is in conflict with `--net-host` isolation. It sends the full tree
the first time. The tool takes a **host path**, and a host path has no meaning when the fleet is
not the caller Mac. Section 7 explains this.

### B. Put the bulk in a snapshot and send only the delta

Method: keep one stopped VM for each project. That VM holds the repository, the dependencies and
a warm build cache. `machine_create` clones that VM instead of the base image. The transport for
each run then carries only the content that the snapshot does not have.

Advantages: the clone takes 0.11 s and uses no more disk. The full repository and the build cache
therefore arrive at no cost. This removes the first-build cost. For a real Xcode project the
first build is the largest cost, not the 1.2 s of rsync.

Disadvantages: a snapshot becomes old, so it needs a refresh rule, a name and a deletion rule.
Each snapshot uses disk as the machine writes to it. A repository inside an image is also a
repository that a reader can extract from that image. This is important for the hosted product.

There is one warning from Firecracker, where this method is older. The Firecracker maintainers
consider a second resume from the same state to be unsafe. Every child gets an exact copy of the
same state. This includes random number seeds, the entropy pool and any token in memory
([snapshot support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
This applies to a memory snapshot. It does not apply to the disk clone in this option. Our spike
also found that `tart suspend` does not work (`docs/02-spike.md`). Read that Firecracker page
first if anybody looks at memory-level forks later.

This option has the largest effect on the product measure that matters. That measure is the time
from "verify this branch" to "here is the screenshot".

### C. Use git

Method: there are three variants. The guest clones from GitHub with a short-lived token. Or the
host makes a `git bundle` of the branch and sends it through the guest agent channel. Or the
guest fetches from a git daemon on the host through the NAT gateway.

Advantages: git sends the exact delta. The guest gets real history for `git diff`. The result of
the run is a commit. A bundle through the guest agent channel needs no network and no key.

Disadvantages: a clone from GitHub needs internet access and a credential inside the machine.
Section 5 says to avoid a credential inside the machine. Also, git cannot carry an uncommitted
working tree. An uncommitted working tree is most of what a developer wants to verify.

Do not use `--depth 1` automatically if the guest clones. GitHub reports that a shallow clone of
Linux is four times faster than a full clone. GitHub also recommends against a shallow fetch, and
against a fetch from a treeless partial clone
([data-driven study](https://github.blog/open-source/git/git-clone-a-data-driven-study-on-cloning-behaviors/)).
A partial clone needs the network during later operations. A machine with restricted internet
access can therefore stop and wait for a remote that it cannot reach. That failure is difficult
to find inside a closed VM.

### D. virtiofs share of the working tree

Method: start the machine with `tart run --dir=work:<path>[:ro]`. The guest sees
`/Volumes/My Shared Files/work`.

Advantages: no copy and no stale data. It needs no key and no network. The guest sees each host
edit immediately.

Disadvantages: the measurements in section 3. Tart also fixes the mount at boot. The mount path
contains spaces, and some tools fail on a path with spaces. The uid and gid mapping is
approximate. A writable share gives the guest direct access to the host file system. This is bad
for a machine that exists to hold an unsupervised agent. Keep this option for two jobs only: one
large read-only input, and large artifacts out.

### E. tar through the guest agent

Method: stream a tar file into `Exec` stdin and extract it in the guest. Reverse the direction
for output.

Advantages: it works 28 s after boot. It needs no IP address, no key and no network. No network
policy can stop it. With direct gRPC it also carries env and workdir.

Disadvantages: it is not incremental, so a second sync sends everything again. It is three times
slower than rsync on small files. We must also write the retry, resume and progress code that
rsync has already.

### F. HTTP from the guest to the daemon

Method: bind the daemon HTTP server to the vmnet interface and to loopback. Give the guest a
bearer token for the run. The guest then downloads a tar file, or a list of files by hash.

Advantages: the fastest bulk path in the measurements. It needs no ssh key. The same design works
when the daemon is a cloud API instead of a process on the user Mac.

Disadvantages: this puts a listening socket on a network that an unsupervised agent can reach.
Other machines on the same host can reach that network too, as section 5 explains. The token
therefore needs authentication and a scope limited to one run. The token must also get into the
guest, which needs the guest agent channel again. Softnet is the only true isolation that Tart
offers, and Softnet makes the host unreachable from the guest. This option and machine isolation
are therefore mutually exclusive.

### G. Continuous sync in both directions

Method: run mutagen or unison between the host and the guest for the length of the run.

Advantages: the developer edits files on the host and the machine follows. This is good for an
interactive loop.

Disadvantages: it adds a daemon and a conflict model. It also adds a failure mode: the build
output of the agent can overwrite the developer tree. Do not use this for v0.

### H. Attached disk or NBD

Method: build a disk image on the host that holds the input. Attach it with `--disk=path:ro`. Or
serve a block device over NBD. Not measured.

Advantages: block-level speed. There is no per-file virtiofs cost. The guest cannot write through
a read-only attachment.

Disadvantages: the image build costs as much as a copy. Tart fixes the attachment at boot. The
guest must also mount the disk. This option is of interest only if the inputs of a project become
too large for a snapshot.

## 5. Context and secrets are a separate problem

The files are the easy half. The agent inside the machine needs three more things before it can
work:

- the run manifest: the task, the branch, the definition of done, and the location for evidence
- the agent configuration: system prompt, `CLAUDE.md`, skills, MCP configuration and settings
- credentials: an Anthropic credential for the agent, and a GitHub token if the agent must push

### Claude Code has explicit injection points. Use them.

In v0 the verifier is headless Claude Code inside the machine (ADR 0003). We do not need to
invent a context format. The headless documentation recommends `--bare` for scripted calls and
SDK calls. Bare mode disables automatic discovery of hooks, skills, commands, subagents, plugins,
MCP servers and memory. It takes each item as an explicit flag instead:
`--append-system-prompt-file`, `--settings`, `--mcp-config`, `--agents` and `--add-dir`. Bare mode
also never reads OAuth credentials from the system keychain. A machine therefore cannot inherit a
login that we did not intend to give it
([headless documentation](https://code.claude.com/docs/en/headless)).

The context payload is therefore a few small files and one command line. The guest agent channel
is good at exactly this. Three points need attention in the design:

- **Do not use transcripts as the primary mechanism.** `--resume` accepts an absolute path to a
  `.jsonl` file, so a session can move between machines. But Anthropic documents the transcript
  format as internal, and it changes between versions. Transcripts are also unencrypted on disk
  ([sessions documentation](https://code.claude.com/docs/en/sessions)). Put the task in the prompt
  and in the system prompt. Use `--output-format stream-json` for structured progress out.
- **A denied permission does not fail a headless run.** The exit code stays 0. The only record is
  the `permission_denials` array in the JSON output. A check for "did the verification happen"
  must read that array, not the exit code.
- **`CLAUDE_CONFIG_DIR` moves the full configuration directory.** This includes the credential
  fallback file. It is a clean method to keep the agent state for one run in one place.

### Secrets: five rules

1. **Never put a secret in an image or a snapshot.** A snapshot is a file that a person can copy.
   `tart suspend` also writes guest memory to disk. A credential in the greenroom base image is a
   credential in every machine, including the machines that outlive the run. Note that
   `claude setup-token` makes a token that is valid for **one year**. That is the worst possible
   secret to put in an image
   ([authentication documentation](https://code.claude.com/docs/en/authentication)).
2. **Never put a secret in a command string.** `machine_exec` takes a shell command, and we give
   that command to `tart exec`. A secret in that command is in the host process arguments for the
   length of the call. Use the env map on the direct gRPC path, or use stdin.
3. **Make each secret short-lived, and get it at the point of use.** The documented Claude Code
   mechanism is `apiKeyHelper`. Claude Code runs that script again every five minutes to get a
   new key. This fits greenroom, with one problem. Under Softnet the guest cannot call the daemon,
   so the helper cannot get anything by itself. Reverse the direction. The daemon pushes a new
   short-lived token into the guest through the guest agent channel on a timer. The helper then
   only reads the file. The machine never holds a credential for more than one refresh interval
   after the run.
4. **Keep the credential outside the machine when this is possible.** Claude Code on the web keeps
   the user GitHub token in a proxy outside the sandbox. That proxy issues scoped credentials into
   the sandbox ([sandbox environments](https://code.claude.com/docs/en/sandbox-environments)). The
   daemon is in the correct position to do the same for us.
5. **Redaction protects the model, not the machine.** Cursor cloud agents separate three kinds of
   value: plain environment variables, runtime secrets that the model cannot see, and build secrets
   that never enter the running environment. Cursor states that a process in the same VM can still
   read a redacted secret
   ([Cursor documentation](https://cursor.com/docs/cloud-agent/security-network)). Anything that we
   put in the machine is readable by anything that the agent runs.

### The network is not a safe channel between machines

Cirrus Labs wrote Tart. They also wrote [Softnet](https://github.com/cirruslabs/softnet), because
the default NAT and bridged networking in Tart do not isolate one VM from another. Softnet learns
the DHCP address of each VM. It then drops all traffic that does not match that address. Two
properties are important here. Softnet needs passwordless sudo or a setuid root binary. Softnet
also makes the host unreachable from the guest, which makes option F impossible. We do not use
`--net-softnet` today.

This is the strongest argument in this document for the vsock channel. A credential that crosses
ssh, or HTTP to the gateway, is on a network that another machine on the same host can possibly
read. The product exists to run several machines at the same time. vsock is point to point between
the host and one guest, and it has no neighbors.

There is also a product question. Can the machine reach the internet at all? Tart offers
`--net-host` for a host-only network. Softnet offers allow lists and block lists. A machine that
cannot reach GitHub cannot leak the token. It also cannot clone. Every file must then come through
the host, which makes option C worse and option B better. Cursor names prompt injection as the
path for data theft, and this is why a wildcard egress rule is dangerous for an autonomous agent.
Decide the egress policy before the first machine holds a real token.

## 6. Other products: three of them, one answer

Every product that has shipped this uses a snapshot plus a delta.

**E2B** splits the problem in two. A template holds the base image, the dependencies and even a
running start command. A file system API (`files.write`, `files.read`) puts the data for each run
in. A pause saves both the file system and the memory. The E2B rule is to put the identical
content in the template, and to upload the content that changes. E2B also says not to put large
data in the template, because the build layers become slow
([E2B template documentation](https://e2b.dev/docs/sandbox-template)).

**Cursor cloud agents** go further and put the repository itself in the snapshot. A background
Build clones the repositories, runs the install script and makes a bootable snapshot. Each agent
starts from that prepared disk. The agent then checks out the requested branch. A Build becomes
stale after a threshold, and the default threshold is 24 hours. After that an agent pulls the
current default branch at start
([Cursor cloud agent documentation](https://cursor.com/docs/cloud-agent/builds)).

**Codex cloud** uses the same shape. It has a setup script and a container cache. It takes the
snapshot at environment creation, or after a cache miss. A maintenance script refreshes it
([Codex cloud environments](https://developers.openai.com/codex/cloud/environments)).

`docs/06-sandbox-survey.md` holds the full survey of more than twenty products, with citations.
Four points from it are important here.

**Option B is the common answer, not a new idea.** The parts that we would otherwise invent
already have known shapes. Those parts are the staleness threshold, the install script and the
recorded commit for each repository.

**Boot time is not the number to improve.** Modal states that the delay that a user feels is the
delay between "started" and "ready". That delay is the clone, the dependency install and the
server start. It is not the hypervisor
([Modal](https://modal.com/blog/unpacking-sandbox-startup-latency)). Our measurements agree. We
use 28 s for boot and 1.2 s for sync. An Xcode build then takes minutes. A snapshot with a warm
build cache improves the part that is slow.

**No product sends a diff as the evidence out.** Each product uses a `git push` to a temporary
branch, often with a pull request after it, or a file download API.

**Almost no product accepts a local working tree as input.** E2B, Cursor and Codex all start from
a branch on a remote. Their delta for each run is a checkout on a disk that holds the history
already. The input for greenroom is the Mac in front of the user. There is one exception. Claude
Code on the web clones from GitHub when the user starts a session in the browser. But a start from
the CLI with `--cloud` uploads the local repository as a bundle instead
([sandbox environments](https://code.claude.com/docs/en/sandbox-environments)). That is the closest
published design to our problem. Check whether that bundle carries uncommitted work before we
assume that we are alone here.

## 7. The decision that outlives v0

`machine_sync(source: "/Users/shlok/projects/app")` has meaning only while the daemon runs on the
same Mac as the files. The hosted shape in `docs/00-idea.md` is different. The caller agent is on
the caller machine, and the fleet is in another place. A host path then has no meaning.

Define the tools in terms of content, not host paths. The caller gives bytes, or a list of
hashes. The daemon then selects one of three methods:

1. an rsync from a local directory
2. an upload to a cloud API
3. a cache hit against a snapshot that holds those hashes already

The local implementation stays rsync, and nothing becomes slower. If we do not make this change now,
every caller encodes the assumption that greenroom is local. This includes the verification skill.

## 8. Recommended order of work

1. **Connect to `control.sock` with gRPC** in place of the `tart exec` subprocess. This gives
   access to env, workdir and detach. It keeps secrets out of the host process arguments. It also
   costs less for each call.
2. **Add `machine_put` on the guest agent channel** for run manifests, configuration, keys and
   small files. It works before the machine has an IP address, and under any network policy. It is
   therefore the one path that is always available.
3. **Keep rsync for the delta.** Change the tool to take content in place of a host path, as
   section 7 explains.
4. **Build project snapshots**, with a refresh rule and a deletion rule. This is the largest gain.
   It also needs the most design.
5. **Send evidence out on the guest agent channel** for screenshots. `machine_screenshot` does
   this already. Add a writable share only if the recordings become large.
6. **Run Claude Code in the guest in bare mode.** Give it an explicit system prompt file, settings
   and MCP configuration through `machine_put`. Use an `apiKeyHelper` that reads a short-lived
   token, and let the daemon refresh that token on the guest agent channel. Do not store a
   credential.
7. **Use `--net-softnet`** as soon as more than one machine runs at the same time, and before any
   machine holds a real credential. Accept the two costs: passwordless sudo, and the loss of
   option F.

## 9. Open questions

1. Must the working tree be **writable back to the host**? Or does a run return a patch and a pull
   request? No other document answers this.
2. Where does the **build cache** stay between runs? Is it per project or per branch? This is the
   same question as "what goes in a snapshot".
3. Does a machine get **internet access**? The answer selects the cheapest transport. It is also
   the difference between "an agent ran here" and "an agent ran here and could steal data".
4. Does the caller want to **watch the tree during the run** (option G)? Or is submit and collect
   enough? ADR 0003 specifies one session, which indicates submit and collect.
5. Do we depend on Tart internals (`control.sock` and the guest agent proto) now that Tart is an
   OpenAI repository? Or do we stay on the documented CLI and pay the cost?
