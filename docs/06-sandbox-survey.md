# Survey: how agent sandboxes move code in and evidence out

**Conclusion:** every product uses one or two of three patterns: upload the tree, clone
in the sandbox, or fork a pre-built snapshot. Secrets are always injected at run time,
never baked. Evidence out is the least standard part. Almost nobody accepts a local,
uncommitted working tree, which is greenroom's input. Decisions drawn from this are in
`05-transport.md`.

Collected 2026-09-14 by a research sub-agent; 79 of 81 links checked working. Latency
figures are vendor claims unless listed under "Measured" below.

## Patterns

| Pattern | How | Cold start | Used by |
| --- | --- | --- | --- |
| Upload per run | file-write API, tarball, mount | scales with payload | E2B, Vercel, Cloudflare, Claude Code `--cloud` |
| Clone in sandbox | `git clone` with injected token | scales with repo + network | Codex, Daytona, Cloudflare, Jules, GitHub Actions, Xcode Cloud |
| Fork warm snapshot | image bake or memory snapshot | sub-second (true fork) to minutes (cache resume) | Morph, Vercel `fork`, Cursor Builds, Devin, Modal, E2B templates, Codex cache |

## Products

| Product | Code in | Pre-baked | Secrets | Evidence out |
| --- | --- | --- | --- | --- |
| [E2B](https://e2b.dev/docs/sandbox-template) | `files.write`, git via exec | Dockerfile templates | runtime `env_vars`, not `ENV` | `files.read`, one file at a time |
| [Daytona](https://www.daytona.io/docs/en/git-operations/) | `git.clone` | snapshots from OCI | env vars or Secrets API | file download or `git.push` |
| [Modal](https://modal.com/docs/guide/sandbox-snapshots) | Volumes, git via exec | Images, fs/memory snapshots (memory: 7 days) | `secrets` param | exec output, Volumes |
| [Runloop](https://docs.runloop.ai/docs/devboxes/repo-connect) | Repo Connect (GitHub token) | Blueprints | unverified | presigned download |
| [Morph](https://cloud.morph.so/docs/developers) | exec `git clone` | Infinibranch live snapshots, claims <250 ms | only `MORPH_API_KEY` documented | exec, ssh, HTTP expose |
| [Vercel Sandbox](https://vercel.com/docs/sandbox/working-with-sandbox) | `source`: git, tarball or snapshot | `snapshot()`, `fork()` | env at create | `downloadFile` |
| [Cloudflare](https://developers.cloudflare.com/sandbox/guides/git-workflows/) | `gitCheckout` (removed in 1.0), `writeFile` | Dockerfile container | Worker secrets to env | `readFile` |
| [Fly Machines](https://fly.io/docs/machines/api/machines-resource/) | image only | Docker image | `fly secrets`, build secrets | none built in |
| [Codex cloud](https://learn.chatgpt.com/docs/environments/cloud-environment) | server-side checkout | 12 h container cache | secrets removed before agent phase | diff, PR |
| [Cursor](https://cursor.com/docs/cloud-agent/builds) | own GitHub App | Builds: repo + install in a bootable snapshot | env, runtime (redacted), build-only | PR, signed commits, artifacts to S3 |
| [Devin](https://docs.devin.ai/onboard-devin/repo-setup) | Git integration | Machine Snapshots | env or encrypted secrets | unverified |
| [Jules](https://jules.google/docs/environment/) | fresh VM clone | opt-in snapshot | unverified | unverified |
| [Claude Code web](https://code.claude.com/docs/en/sandbox-environments) | clone, or `--cloud` bundle upload | per session | GitHub token held in a proxy outside the sandbox | unverified |
| [Namespace](https://namespace.so/docs/devbox) (macOS) | `--checkout` clone | Dockerfile; pause/resume | env or vault | git |
| [Cua / Lume](https://github.com/trycua/cua) (macOS) | none documented | OS provisioning only | unverified | ssh |
| [MacStadium Orka](https://docs.macstadium.com/orka/orka3-cli-reference/image-management) | image | Packer golden images | unverified | unverified |
| [EC2 Mac](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-mac-instances.html) | operator's job | AMI; launch 6-20 min | generic AWS | operator's job |
| GitHub Actions macOS | `actions/checkout` | fresh image per job | repo secrets | `upload-artifact` |
| [Xcode Cloud](https://developer.apple.com/documentation/xcode/xcode-cloud) | connected SCM | Apple-managed | per-workflow env | Post-Actions, TestFlight |

Scrapybara (Mac/Windows desktops) shut down 2025-10-15.

## Measured, not vendor claims

- **Boot is cheap; the checkout costs.** ComputeSDK's public benchmark (2026-09-11)
  median time-to-first-command: Daytona 0.35 s, Vercel 0.50 s, Modal 0.89 s, Runloop
  1.14 s, E2B 1.28 s, Cloudflare 5.70 s ([leaderboard](https://www.computesdk.com/benchmarks/sandboxes/burst-tti/)).
- **Matt Rickard, "Clone is the new bottleneck"**: with a precomputed clone plan, a full
  clone takes 184-646 ms, faster than a sandbox boot. The win came from moving work out of
  the per-run path ([post](https://blog.matt-rickard.com/p/clone-is-the-new-bottleneck-for-agents)).
- **GitHub on clone types**: shallow clone of linux is 4x faster than full; avoid shallow
  and treeless fetches; partial clones need the network later
  ([study](https://github.blog/open-source/git/git-clone-a-data-driven-study-on-cloning-behaviors/)).
- **Fly Sprites**: checkpoint ~300 ms, restore under 1 s. Disk state via object storage
  plus metadata, not RAM ([design](https://fly.io/blog/design-and-implementation/)).
- **Firecracker**: resuming the same memory snapshot more than once is considered
  insecure (duplicated RNG seeds, tokens) ([docs](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
  This applies to memory forks, not greenroom's APFS disk clones.
- **CRIU** has no macOS VM equivalent. For macOS, Tart's APFS clone is the only fast
  state-transfer primitive found.

## Not verified

Runloop, Jules, Cua and Orka secrets; Devin git auth and results-out; Morph's <250 ms
and E2B's sub-200 ms (vendor only); Fly memory-snapshot restore times; whether Claude
Code's `--cloud` bundle carries uncommitted work; Docker Sandboxes "workspace sync".
