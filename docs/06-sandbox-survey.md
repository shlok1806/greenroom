# Survey: how other agent sandboxes move code in and evidence out

Status: research draft, 2026-09-14. This document supports `docs/05-transport.md`, which holds
the greenroom decisions. This document is the raw survey of more than twenty products.

A research sub-agent collected this material. A person did not write it. Each claim has a link.
Each unconfirmed claim has the mark UNVERIFIED. The last section lists what the agent could not
verify. I checked all 81 links. Two links do not work, so treat those two claims as unsourced:
`orkadocs.macstadium.com/v2.4.0/docs/existing-images-upload-management` and
`vercel.com/docs/rest-api/sdk/sandboxes/create-a-sandbox`. The other 79 links work.

Read the section "Measured numbers, not vendor claims" before you use any other latency number
in this document. The vendors report the other numbers themselves.

Note on style: `docs/05-transport.md` is written in ASD-STE100 Simplified Technical English. The
product sections below are not. They keep the words of the sub-agent that wrote them.

## Summary

Every agent-sandbox and cloud-dev product answers "how does the workspace get into the
box" with one of three patterns, sometimes two at once: **upload the working tree**
(an HTTP file-write API, a tarball, or a bind/volume mount), **clone the repo fresh
inside the sandbox** (git clone with an injected token, once per run), or **fork a
pre-warmed snapshot** that already has the repo checked out and dependencies installed
(a container/VM image bake, or a true memory/hypervisor snapshot). Secrets are
consistently kept out of the base image and injected at run time as scoped env vars,
distinct from anything baked into a Dockerfile or golden image. Getting evidence back
out is the least standardized part of the landscape: some products expose a generic
file-download API, some run git push/PR-creation with a platform-owned credential, and
several general-purpose sandbox primitives (Fly Machines, Cloudflare Sandboxes) don't
address it at all, leaving it to the calling application. Below is what each product's
own docs, blogs or source say, with inline citations, followed by a synthesis of the
three patterns and a running list of what could not be verified.

## Sandbox / cloud-dev infra

### E2B

1. **Transport:** file-write API, `sandbox.files.write(path, content)`, for individual
   files; browser/unauthenticated clients get pre-signed upload URLs when the sandbox is
   created with `secure: true` ([Upload data to sandbox](https://e2b.dev/docs/filesystem/upload)).
   A repo would be pulled in by running `git clone` via the exec API, or baked into a
   template image ahead of time.
2. **Baked ahead, via Sandbox Templates.** Templates are Dockerfiles (Debian-based
   images only, conventionally `e2b.Dockerfile`), built with `e2b template build`, and
   sandboxes boot from the resulting template image in under a second when pre-warmed
   ([Sandbox templates](https://e2b.dev/docs/sandbox-template)).
3. **Secrets:** the docs explicitly recommend passing secrets as runtime `env_vars` at
   `Sandbox.create()` rather than baking them into the Dockerfile's `ENV`, because
   anything in `ENV` persists in the template image and is visible to anyone who spins
   up a sandbox from it ([Sandbox templates](https://e2b.dev/docs/sandbox-template)).
   A separate `E2B_ACCESS_TOKEN` authenticates template builds, distinct from the
   `E2B_API_KEY` used to create sandboxes.
4. **Cold start / large repos:** E2B runs on Firecracker microVMs and targets
   sub-200ms sandbox initialization; guidance elsewhere in the docs warns against
   baking large datasets, models, or compiled binaries into the Dockerfile because it
   slows builds and balloons the template image ([Sandbox templates](https://e2b.dev/docs/sandbox-template)).
   For files over 100MB, the docs flag egress cost and timeout risk and suggest S3
   mounting instead of the sandbox's own filesystem ([Upload/download docs](https://e2b.dev/docs/quickstart/upload-download-files)).
   Sub-200ms is E2B's own framing; I could not find a first-party E2B blog post with an
   independently reproduced benchmark, so treat the exact number as a vendor claim.
5. **Results out:** `sandbox.files.read(path)` downloads one file at a time; there is
   no documented bulk/archive download, and the docs note they're working on a better
   multi-file solution ([Upload/download docs](https://e2b.dev/docs/quickstart/upload-download-files)).
   The sandbox filesystem is destroyed with the sandbox, so anything not read out (or
   exported to persistent storage) before that point is lost.

### Daytona

1. **Transport:** `sandbox.git.clone(repoUrl, path)` - a first-class SDK method, git
   clone with credentials passed per-call, not a wrapped shell command
   ([Git Operations](https://www.daytona.io/docs/en/git-operations/)).
2. **Both, combined.** Snapshots are sandbox templates built from Docker/OCI images
   (via the dashboard, Python/TS/Go SDK, or from a git repository if credentials are
   provided) to give a consistent base with dependencies pre-installed; the
   recommended pattern is to pre-bake slow dependency installs into the snapshot, then
   `git.clone()` the actual repo at runtime against that snapshot
   ([Snapshots](https://www.daytona.io/docs/en/snapshots/)).
3. **Secrets:** two tiers - plain environment variables (readable by anything inside
   the sandbox, including the agent itself, via `env`) versus a Secrets API (SDK
   0.192.0+) that propagates within seconds but only to processes spawned after the
   update; already-running processes keep the old environment
   ([Secrets](https://www.daytona.io/docs/en/secrets/)).
4. **Cold start / large repos:** UNVERIFIED - no first-party latency numbers or
   monorepo/LFS guidance surfaced in the docs pages reviewed.
5. **Results out:** `fs.downloadFile`/`downloadFiles` for pulling artifacts straight
   back to the local machine, or the git module (`sandbox.git.add`, commit, then
   `sandbox.git.push` with a username/token per push) when the result should land as a
   commit or PR; the docs frame the choice explicitly as "use file download for
   artifacts, use git for anything that should trigger CI or land in history"
   ([File System Operations](https://www.daytona.io/docs/en/file-system-operations/)).

### Modal Sandboxes

1. **Transport:** Volumes, mounted at `Sandbox.create()` time as a path → Volume
   mapping, with sub-path mounting to scope one Volume to one session's directory; the
   repo itself would typically arrive via `sandbox.exec("git", "clone", ...)` against a
   mounted Volume ([Volumes](https://modal.com/docs/guide/volumes), [Sandboxes](https://modal.com/docs/guide/sandboxes)).
2. **Baked ahead, via Modal Images** (Dockerfile-equivalent), separate from runtime
   Volumes. Modal additionally supports true filesystem/memory snapshotting:
   `snapshot_filesystem()` returns a new Image usable to spawn another Sandbox with the
   same filesystem, and `mount_image`/`unmount_image` can attach a directory snapshot
   into a running sandbox ([Snapshots](https://modal.com/docs/guide/sandbox-snapshots)).
   Memory snapshots expire after 7 days; filesystem snapshots are recommended for
   longer persistence. A sandbox cannot be snapshotted while an `exec` is running, and
   background processes started via `exec` are not restored after a snapshot restore
   (same page).
3. **Secrets:** a dedicated `secrets` parameter (list of Modal `Secret` objects)
   distinct from the plain `env` dict, injected as environment variables at sandbox
   creation ([modal.Sandbox reference](https://modal.com/docs/reference/modal.Sandbox), [Secrets](https://modal.com/docs/guide/secrets)).
4. **Cold start / large repos:** UNVERIFIED for numbers; Volumes v2 support an
   explicit `sync` commit mid-execution so writes are durable before the sandbox ends,
   which the docs frame as useful for long-running sandboxes rather than cold-start
   optimization ([Volumes](https://modal.com/docs/guide/volumes)).
5. **Results out:** exec output via `ContainerProcess`, or reading back from a mounted
   Volume once `sync` has committed it; `snapshot_filesystem()` doubles as a
   results-out path when the "result" is meant to seed another sandbox rather than
   leave the platform.

### Runloop

1. **Transport:** "Repo Connect" - the platform is handed a GitHub token, then an AI
   step analyzes the repo's structure, dependencies and build process and auto-derives
   a Blueprint, including any services declared in the repo's GitHub Actions workflow
   files ([Repo Connect](https://docs.runloop.ai/docs/devboxes/repo-connect)).
2. **Baked ahead into Blueprints** - Dockerfile-equivalent definitions built via the
   `runloop.blueprint` SDK manager, with a documented recommendation to manually SSH
   into a Devbox and verify install commands before committing them to a Blueprint
   ([Devbox Blueprints](https://docs.runloop.ai/docs/devboxes/blueprints)). Devboxes
   and snapshots layer on top of a built Blueprint.
3. **Secrets:** UNVERIFIED - not documented on the pages reviewed.
4. **Cold start / large repos:** UNVERIFIED - not found.
5. **Results out:** a binary-file-download endpoint that returns a pre-signed URL
   ([Download binary file contents](https://docs.runloop.ai/api-reference/devbox/download-binary-file-contents-from-devbox-filesystem)).
   There is no documented diff API; the practical pattern shown is running `git diff`
   via the devbox exec/shell API and downloading its output
   ([Read and Write Files on Devbox](https://docs.runloop.ai/docs/devboxes/files)).

### Morph

1. **Transport:** devboxes start from a named template (`morphcloud devbox template
   list` / `start`), or a raw VM instance starts from a base image id
   (`image_id="morphvm-minimal"`); no git-clone-into-instance or file-upload primitive
   is documented on Morph's own developer page - a repo would be pulled via
   `instance.exec("git clone ...")` ([Welcome to Morph Cloud](https://cloud.morph.so/docs/developers)).
2. **This is the flagship "fork a warm snapshot" product.** "Infinibranch" is Morph's
   own name for snapshotting and branching a running instance: `instance.snapshot()`
   captures live process state - including a background process mid-run, which resumes
   exactly where it left off after restore - and forking from that snapshot is
   described as "near-zero overhead branching" versus a "full clone required" on a
   traditional VM, with **native support for unlimited parallel branches**
   ([Welcome to Morph Cloud](https://cloud.morph.so/docs/developers)). This is the
   direct primary source for the "infinite branching" pitch named in the research brief.
3. **Secrets:** the only documented environment variable is `MORPH_API_KEY`, which
   authenticates the SDK/CLI to Morph's control plane - this is client-side auth, not a
   documented mechanism for injecting application secrets into the guest. UNVERIFIED
   how app-level secrets (git tokens, API keys for the agent's own tools) get into a
   Morph instance.
4. **Cold start:** Morph's own docs claim Infinibranch can "snapshot, branch, and
   restore entire computational environments in under 250ms," contrasted with "2–3
   minutes for typical VMs" ([Welcome to Morph Cloud](https://cloud.morph.so/docs/developers)).
   These are Morph's own marketing-page numbers, not an independently reproduced
   benchmark - treat as a vendor claim, not a controlled measurement. Large-repo
   handling is not addressed.
5. **Results out:** `instance.exec(...)` returns captured stdout; SSH access
   (`morphcloud devbox ssh`); HTTP port exposure with a shareable preview URL scoped to
   the org (`morphcloud devbox expose-http`); EFS volumes can be mounted across hosts
   for shared file access ([Welcome to Morph Cloud](https://cloud.morph.so/docs/developers)).
   No documented single-file download API or PR-automation primitive.

## General-purpose sandbox platforms

### Vercel Sandbox

1. **Transport - all three patterns are first-class `source` types on
   `Sandbox.create()`:** `type: "git"` (with optional shallow `depth` and a
   `revision`), `type: "tarball"` (a URL), and `type: "snapshot"` (a `snapshotId`)
   ([Create a sandbox](https://vercel.com/docs/rest-api/sdk/sandboxes/create-a-sandbox)).
   A `writeFiles()` API also writes individual files directly, independent of how the
   sandbox was created ([Working with Sandbox](https://vercel.com/docs/sandbox/working-with-sandbox)).
2. **Both, and directly documents the fork pattern.** `sandbox.snapshot()` captures the
   current filesystem state explicitly, and persistent sandboxes also snapshot
   automatically on stop. `Sandbox.fork({ sourceSandbox })` spawns a new sandbox seeded
   from the source's latest snapshot without the caller tracking snapshot IDs manually
   ([Working with Sandbox](https://vercel.com/docs/sandbox/working-with-sandbox)) - this
   is a second directly-documented primary source for the snapshot-fork pattern.
3. **Secrets:** env vars are set once at `Sandbox.create()` and inherited by every
   subsequent command, overridable per command
   ([Vercel Sandbox now accepts environment variables at creation](https://vercel.com/changelog/vercel-sandbox-now-accepts-environment-variables-at-creation)).
   Private-repo auth is a GitHub personal access token or GitHub App installation token
   passed inline on the git `source` object
   ([Using private GitHub repositories with Vercel Sandbox](https://vercel.com/kb/guide/sandbox-private-github-repositories)).
4. **Cold start / cost:** no explicit millisecond figure found in the docs pages
   fetched (UNVERIFIED for a hard number). Concrete platform limits: default timeout 5
   minutes, up to 24 hours on Pro/Enterprise; 64GB ephemeral NVMe per sandbox from a
   custom image (32GB on deprecated runtimes); snapshots expire 30 days after last use
   by default ([Vercel Sandbox pricing](https://vercel.com/docs/sandbox/pricing)).
   Data a sandbox *downloads* from the internet - including git repositories - is
   explicitly free; only outbound traffic the sandbox sends is billable (same page).
5. **Results out:** `sandbox.downloadFile()` pulls a single file back to the caller's
   machine; snapshot/fork is also usable as a hand-off mechanism between sandboxes.
   There is no built-in "open a PR" primitive in the SDK itself - that's left to
   whatever runs inside the sandbox (e.g., `gh pr create` via a token the caller
   supplied).

### Cloudflare Sandboxes (`@cloudflare/sandbox` SDK)

1. **Transport:** on the stable SDK, `sandbox.gitCheckout(repoUrl, { branch, targetDir,
   depth })` is a dedicated helper that shallow-clones a repo; on the newer 1.0 preview
   (`@cloudflare/sandbox@next`), `gitCheckout` was removed and git must be run directly
   via the shell/exec API instead ([Work with Git](https://developers.cloudflare.com/sandbox/guides/git-workflows/),
   [Migrate to 1.0 preview](https://developers.cloudflare.com/sandbox/1-0-preview/migrate/)).
   A file-write API (`sandbox.writeFile`) exists independently.
2. **Baked ahead, per environment.** The sandbox is a Cloudflare Container, defined by
   a Dockerfile referenced in `wrangler.jsonc` (`[[containers]] image = "./Dockerfile"`);
   architecturally, a Durable Object provides persistent identity and lifecycle
   management over the Container, which is the actual compute runtime
   ([Architecture](https://developers.cloudflare.com/sandbox/concepts/architecture/)).
   First build is 2–3 minutes; subsequent builds are cached and much faster
   ([Getting started](https://developers.cloudflare.com/sandbox/get-started/)).
3. **Secrets:** `wrangler secret put` sets Worker-level secrets, which the Worker then
   forwards into the container as scoped environment variables (shown in the Codex
   tutorial passing a restricted `CODEX_API_KEY` into the container)
   ([Run Codex with Cloudflare Containers](https://developers.cloudflare.com/sandbox/tutorials/openai-agents-api/)).
   For private-repo git access, the docs show embedding a token directly in the clone
   URL but explicitly warn this puts the raw credential inside the sandbox; a cleaner
   pattern in the Artifacts example converts a short-lived write token into an
   authenticated git remote injected via `sandbox.setEnvVars({ ARTIFACTS_GIT_REMOTE:
   ... })` rather than putting the token in the URL
   ([Sandbox SDK + Artifacts](https://developers.cloudflare.com/artifacts/examples/sandbox-sdk-artifacts/)).
4. **Cold start / large repos:** only the container-build cold start (2–3 minutes
   first time, cached after) is documented; no steady-state per-sandbox-instance
   cold-start figure or large-repo guidance was found.
5. **Results out:** `sandbox.readFile()` reads one file back into the Worker, which can
   then return it in an HTTP response; no documented bulk export, archive download, or
   PR-automation primitive at the SDK level.

### Fly.io Machines

1. **Transport:** the Machines API's only required field is `config.image` - a
   container registry reference - so the "transport" for a workspace is baking it into
   the image, or having the container's own entrypoint pull code (e.g., `git clone`) on
   boot; there is no platform-level file-upload or workspace-sync primitive
   ([Machines resource](https://fly.io/docs/machines/api/machines-resource/), [Machines
   API reference](https://docs.machines.dev/)).
2. **Baked ahead**, via a normal Docker build pushed to a registry (`fly deploy` or a
   direct registry push), then referenced by `config.image` on machine creation. The
   API supports cloning an existing Machine's config to keep instances in sync
   ([Managing Machines with the API](https://fly.io/docs/machines/guides-examples/managing-machines-with-the-api/)),
   though the exact mechanics of that clone (config-only vs. disk-state) are UNVERIFIED
   from the page reviewed.
3. **Secrets:** `fly secrets set NAME=VALUE` sets encrypted, deploy-time env vars;
   `fly secrets import` bulk-loads from a dotenv file; a separate Build Secrets
   mechanism scopes credentials to the Docker build phase only, so they're never
   present in the running Machine's environment ([fly secrets set](https://fly.io/docs/flyctl/secrets-set/),
   [Build Secrets](https://fly.io/docs/apps/build-secrets/)). A Machines API access
   token can itself be stored as a secret (`fly secrets set FLY_API_TOKEN=...`).
4. **Cold start / large repos:** Fly's own Firecracker explainer states you can
   checkpoint an environment before executing arbitrary code and restore a clean state
   afterward, and that Firecracker's minimal device model is what makes long-lived,
   restorable snapshots practical rather than a full boot per task
   ([What is Firecracker VM?](https://fly.io/learn/firecracker-vm/)). I could not find
   a dedicated first-party Fly.io *engineering blog* post with reproducible
   snapshot/restore latency numbers (their "Volume Expansion and Snapshot Restores"
   post is about disk volumes, not VM memory snapshots) - flag as UNVERIFIED for hard
   numbers. Large-repo handling is not addressed at the platform level; it's delegated
   to the app's own Dockerfile.
5. **Results out:** no built-in artifact or PR mechanism - Fly Machines is
   infrastructure-primitive, not an agent product, so results leave however the app
   inside the Machine is built to expose them (HTTP endpoint, logs, or its own git push
   using an injected secret).

## Agent products

### OpenAI Codex cloud

1. **Transport:** "Codex creates a container and checks out the repo at the selected
   branch or commit SHA" ([Cloud environments](https://learn.chatgpt.com/docs/environments/cloud-environment)) - git clone, server-side.
2. **Both.** Container state is cached for up to 12 hours to speed up new chats and
   follow-ups within the same environment; on a cache hit the cached container is
   resumed and the chat's branch is checked out, with an optional maintenance script
   run "when the setup script ran on an older commit and dependencies need updating."
   Cache invalidates automatically if the setup script, maintenance script, env vars,
   or secrets change, or manually via a "Reset cache" control; on Business/Enterprise
   plans the cache is shared across everyone with access to the environment (same
   page). Repository conventions live in `AGENTS.md`, with directory-scoped
   `AGENTS.override.md` for team-specific rules ([Custom instructions with AGENTS.md](https://developers.openai.com/codex/guides/agents-md)).
3. **Secrets:** plain environment variables persist for the whole chat (setup script
   and agent phase both). Secrets carry an additional encryption layer, are decrypted
   only for task execution, and are explicitly **removed before the agent phase
   starts** - available to the setup script but not to the agent itself
   ([Cloud environments](https://learn.chatgpt.com/docs/environments/cloud-environment)).
4. **Cold start / large repos:** no monorepo, large-binary, or git LFS guidance found.
   Internet access during the agent run is separately configurable and documented, with
   guidance to keep it as limited as possible for common package managers ([Agent
   internet access](https://developers.openai.com/codex/cloud/internet-access/)); the
   original 2025 Codex launch described internet access as disabled during task
   execution by default, and current docs describe a more granular, configurable model
   - flagging that as a real change over time rather than a one-time UNVERIFIED gap.
5. **Results out:** the agent presents a diff of changed files, from which the user can
   ask follow-up questions or open a PR ([Cloud environments](https://learn.chatgpt.com/docs/environments/cloud-environment)).
   A known issue: the "Create PR" button in the Codex Cloud UI bypasses `gh pr create`,
   so PR templates and closing keywords aren't applied and the PR isn't auto-linked to
   an issue unless edited manually - the CLI, by contrast, drives `gh` directly and
   gets that linking for free ([openai/codex#6750](https://github.com/openai/codex/issues/6750)).

### Cursor Cloud Agents (formerly Background Agents)

1. **Transport:** git access comes from **Cursor's own GitHub App** with a read-write
   grant, not a user-supplied token - "Grant read-write privileges to our GitHub app
   for repos you want to edit" ([Secrets & Network](https://cursor.com/docs/cloud-agent/security-network)).
2. **Baked ahead, via "Builds."** A Build is a bootable snapshot Cursor prepares ahead
   of agent runs, defined by `.cursor/environment.json` (install command, optional
   Dockerfile, start commands, terminals); building it clones every configured repo,
   runs install, and creates a fresh bootable snapshot, recording the commit used per
   repo. Cursor keeps the latest successful Build ready so agents start fast; an unused
   snapshot is deleted automatically after 90 days ([Cloud Agent Builds](https://cursor.com/docs/cloud-agent/builds), [Cloud Agents](https://cursor.com/docs/cloud-agent)).
3. **Secrets - three explicit tiers** ([Secrets & Network](https://cursor.com/docs/cloud-agent/security-network)):
   plain **Environment Variables** (low sensitivity, fully visible to the agent);
   **Runtime Secrets** (still real env vars, but scrubbed to `[REDACTED]` from tool
   call results, chat transcript, commits and commit messages - an agent using a
   terminal directly can still read the raw value); and **Build Secrets** (Docker
   build-phase only, via `RUN --mount=type=secret,id=X,env=X`, never present in the
   running agent's environment - the documented pattern for private registry
   credentials).
4. **Cold start:** Builds exist specifically so agents "boot fast" from a cached
   snapshot rather than cloning and installing from scratch every run - no millisecond
   figure given, but the mechanism is explicit. Network egress has three modes (allow
   all / default + allowlist / allowlist only), and even under the strictest mode a
   narrow set of IPs stays reachable for git traffic across GitHub, GitLab, Azure
   DevOps and Bitbucket (same page).
5. **Results out:** PRs are opened through the same GitHub App grant; every commit is
   signed with an HSM-backed Ed25519 key, producing a Verified badge on GitHub/GitLab
   automatically, which the docs note satisfies signed-commit branch-protection rules
   with no extra configuration. Screenshots, videos and log artifacts are uploaded to
   an S3 bucket (`cloud-agent-artifacts.s3.us-east-1.amazonaws.com`) and surfaced on
   the PR; the docs warn against wildcarding that host in an allowlist since it
   "creates an exfiltration path" for a prompt-injected agent (same page).

### Devin (Cognition Labs)

1. **Transport:** repos are connected under Settings > Connections
   (GitHub/GitLab/Bitbucket/Azure DevOps); enterprise orgs add a second layer via
   Enterprise Integrations plus per-org Repository Permissions - "Devin needs
   repository access through the Git integration before it can clone or build"
   ([Classic configuration](https://docs.devin.ai/onboard-devin/repo-setup)). The exact
   underlying auth mechanism (GitHub App vs. OAuth vs. deploy key) is not spelled out
   on that page - UNVERIFIED.
2. **Baked ahead, into Machine Snapshots** - "a frozen, bootable image the session
   starts from," pre-loaded with cloned repos, installed tools and resolved
   dependencies. Each session boots a *fresh copy* of the snapshot; session changes are
   discarded and do not persist back to the snapshot; cloning and dependency
   installation happen once, at snapshot build time, not per session (same page). A
   release-notes tip: have Devin run setup interactively in VSCode, then snapshot, so
   future sessions inherit the result ([Release Notes](https://docs.devin.ai/release-notes)).
3. **Secrets:** two tiers on the blueprint - non-sensitive values as plain `env` fields
   or `$ENVRC`-style shared values, versus sensitive values entered in the blueprint
   editor's encrypted Secrets tab and referenced as `$VARIABLE_NAME`; both become
   environment variables at build time and at session runtime. The docs note Devin can
   auto-detect a repo's toolchain but "cannot discover credentials" - those are always
   supplied manually ([Classic configuration](https://docs.devin.ai/onboard-devin/repo-setup)).
4. **Cold start / large repos:** UNVERIFIED on the pages reviewed.
5. **Results out:** UNVERIFIED for the general product - no PR/push mechanism is
   documented on the repo-setup page. For **macOS specifically**, Devin's Namespace
   "Outposts" integration is already documented in this repo's own landscape research:
   Devin's inference/planning loop stays in Cognition's cloud while command execution,
   file edits, and repository access happen on a Namespace-provisioned Devbox, with the
   Devbox worker opening an outbound connection back to Devin's cloud
   ([Namespace: Giving Devin a fast computer](https://namespace.so/blog/devin-outposts-devboxes),
   already cited in [docs/04-landscape.md](./04-landscape.md)).

### Google Jules

1. **Transport:** each task runs in a fresh, secure, short-lived VM; Jules "clones the
   repository, installs dependencies, and runs tests" - no clone-depth, protocol or
   auth detail is given ([Environment setup](https://jules.google/docs/environment/)).
2. **Snapshot mechanism, opt-in and manual.** A "Run Snapshot" button executes the
   configured setup script and, on success, captures an environment snapshot that
   future Jules tasks against that repo reuse - explicitly pitched as valuable "for
   complex environments with long setup times." For simple projects, Jules can infer
   setup without a script at all, reading `agents.md`/`readme.md` for hints (same
   page). Long-running processes like dev servers or watch scripts are explicitly
   **not supported** in setup scripts.
3. **Secrets:** UNVERIFIED - not addressed on the environment-setup docs page.
4. **Cold start / large repos:** UNVERIFIED - no numbers or monorepo/LFS guidance
   found; only general advice to "keep setup lightweight and fast."
5. **Results out:** UNVERIFIED on the pages reviewed - the environment-setup doc's
   scope stops at environment preparation and says nothing about PR creation, git
   push, or branch/commit conventions.

## Claude Code cloud sandboxing, and a sunset product

### Claude Code on the web / Anthropic sandboxing

1. **Transport:** each web session runs in an isolated, Anthropic-managed VM. A
   network proxy enforces a default allowlist, and a **separate proxy holds the user's
   GitHub token outside the sandbox**, issuing scoped credentials into the sandbox for
   repository access rather than handing over the raw token
   ([Choose a sandbox environment](https://code.claude.com/docs/en/sandbox-environments)).
   Launching from the CLI with `--cloud` lets Claude Code bundle and upload the local
   repository instead of requiring a connected GitHub account (same page, "Send local
   repositories without GitHub") - i.e., Claude Code on web supports both clone-in-
   sandbox and upload-per-run, selected by how you launch it.
2. **Per-session, not image-baked** for the hosted product; organizations can instead
   route sessions to a **self-hosted environment** running on their own infrastructure
   (same doc, linked but not independently fetched in this pass).
3. **Secrets:** the scoped-credential proxy design described above is the documented
   mechanism - the raw GitHub token never enters the sandbox.
4. **Separately, a local/self-managed sandboxing layer exists** for any Claude Code
   session (not just the hosted web product): the built-in sandboxed Bash tool and the
   open-sourced `@anthropic-ai/sandbox-runtime` enforce OS-level filesystem and network
   restrictions - Seatbelt on macOS, bubblewrap on Linux/WSL2 - and can wrap a
   container or VM for defense in depth
   ([Choose a sandbox environment](https://code.claude.com/docs/en/sandbox-environments),
   [Configure the sandboxed Bash tool](https://code.claude.com/docs/en/sandboxing),
   [Engineering: sandboxing](https://anthropic.com/engineering/claude-code-sandboxing),
   [anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)).
   Independent testing cited in search results found filesystem writes blocked at the
   syscall level and network isolation enforced via a localhost proxy checking an
   allowlist - relevant to greenroom as prior art for layering OS-level enforcement
   *inside* a VM, on top of the VM boundary itself. The same doc mentions **Docker
   Sandboxes** (a third-party product, `docs.docker.com/ai/sandboxes`) as a microVM
   option with its own Docker daemon and "workspace sync" as a named feature - worth
   independently investigating later, since "workspace sync" as a first-class concept
   is directly on-topic for greenroom; not independently fetched in this pass.
5. **Results out:** UNVERIFIED beyond the scoped-credential git-push path implied by
   the GitHub-token-proxy design - no dedicated PR-automation or artifact-export
   mechanism was found described on the pages fetched.

### Scrapybara (sunset - historical only)

Scrapybara announced on X that its virtual desktop service would be sunset as of
October 15, 2025: no new VM creation, all existing VMs halted and deleted, and users
told to back up data locally ([@scrapybara](https://x.com/scrapybara/status/1971655785869726110)).
Its docs (`docs.scrapybara.com`) are still reachable as of this research
(2026-09-14) and describe a "virtual desktop infrastructure for computer use agents"
with Ubuntu and Browser instance types, a claimed sub-1-second instance startup, and
named protocols for Filesystem, Environment Variables, Code Execution and Browser, plus
an "Authenticated sessions" feature to save and reuse browser auth state across
instances ([Welcome to Scrapybara](https://docs.scrapybara.com/introduction)). The
introduction page does not itself spell out an upload/clone mechanism for a git
repo or working tree, and the individual protocol subpages
(`/protocols/file`, `/protocols/env`) were not fetched in this pass - flag as
UNVERIFIED for exact API mechanics. The product is decommissioned, so this is useful
only as a historical data point, not as an actionable integration target.

## macOS-specific infrastructure

### Namespace devboxes (macOS)

1. **Transport:** two documented paths. The CLI/dashboard flow,
   `devbox create --checkout=github.com/org/repo`, clones a repo into the Devbox at
   creation time ([Devboxes](https://namespace.so/docs/devbox)). Separately, a
   Blueprint's Repository field can be configured so "Devboxes can be configured to
   automatically clone a GitHub repository when they are created"
   ([Devbox Blueprints](https://namespace.so/docs/devbox/blueprint)). Either path
   requires connecting GitHub integration first.
2. **Environment is a Dockerfile, not a repo-baked snapshot.** "One Dockerfile defines
   the environment for your entire team, including AI agents," built with Namespace's
   remote Docker builders and pushed to a built-in registry (`nscr.io`); the separate
   "base image" blueprint field is mainly for OS/toolchain pinning - for macOS, the
   macOS and Xcode version ([Devboxes](https://namespace.so/docs/devbox), [Devbox
   Blueprints](https://namespace.so/docs/devbox/blueprint)). No explicit VM-snapshot
   mechanism is documented; persistence instead comes from pause/resume - an idle
   Devbox sleeps without billing and "resumes in seconds" with its filesystem intact.
3. **Secrets:** two blueprint-level channels - plain Environment Variables (values
   stored in plain text) and a managed Secrets vault
   (`cloud.namespace.so/workspace/vars`) referenced from the blueprint ([Devbox
   Blueprints](https://namespace.so/docs/devbox/blueprint)).
4. **Cold start:** only figure given is "resumes in seconds" from a paused/sleeping
   Devbox - not a cold-boot number. macOS guests run on Apple M4 Pro/M5 Max hardware
   per Namespace's own writeup on the Devin integration ([Namespace: Giving Devin a
   fast computer](https://namespace.so/blog/devin-outposts-devboxes), already cited in
   [docs/04-landscape.md](./04-landscape.md)). Large-repo handling isn't addressed;
   Tailscale support lets a Devbox reach a self-hosted git server as an alternative to
   public GitHub, useful for large private monorepos behind a VPN.
5. **Results out:** no dedicated export API documented - the docs frame git (commits,
   branches, pull requests) as the return path, the same channel work arrives through.

### Cua / Lume

1. **Transport:** the open-source local tool, Lume, builds VMs locally from an Apple
   restore image (IPSW) with unattended setup
   (`lume create --ipsw ... --unattended tahoe`) - the README documents no
   repo-into-VM mechanism at all, only guest OS provisioning
   ([lume README](https://github.com/trycua/cua/blob/main/libs/lume/README.md)). For
   Cua's hosted Cloud Sandbox, the documented flow is: create an instance via
   dashboard/API, then drive it over the Computer SDK
   (`pip install cua-computer`, `Computer(provider_type="cloud", ...)`) using
   screenshot/click/type computer-use primitives, or the newer
   `Sandbox.ephemeral(Image.macos())` API - no git-clone or file-upload primitive
   surfaced in the docs reviewed ([Quickstart](https://docs.cua.ai/docs/quickstart-devs)).
2. Lume's `sequoia`/`tahoe` presets configure the guest (create a `lume` user, enable
   SSH, autologin, disable sleep) - this is OS provisioning, not workspace baking.
   Default SSH credentials are `lume`/`lume` (README).
3. **Secrets:** UNVERIFIED for both Lume and Cua Cloud Sandbox - no injection
   mechanism documented in the material reviewed.
4. **Cold start / large repos:** UNVERIFIED - no numbers found. One documented
   maturity signal directly relevant to what greenroom is itself building on Tart: the
   README says the `tahoe` preset is "E2E verified" from a local IPSW, while `sequoia`
   "can still present an Accessibility step" during first-boot Setup Assistant, tracked
   as an open issue (`trycua/cua#2155`) - i.e., automated unattended macOS provisioning
   is acknowledged to still be rough at the edges even in a project built specifically
   for this.
5. **Results out:** SSH is the only documented access path in the Lume README; no
   file-download or PR-automation API was found. Separately, an open GitHub issue
   ([trycua/cua#3584](https://github.com/trycua/cua/issues/3584)) reports that Cua's
   own cloud CLI/MCP commands (`cua do switch cloud`, the `sandbox_*`/`computer_*` MCP
   tools) currently error against a newer Fleet-based backend because they still call a
   legacy `/v1/vms` API - evidence the hosted cloud product's docs and CLI are
   presently inconsistent mid-migration.

### MacStadium / Orka

1. **Transport:** a VM is created from an Orka "image" containing the macOS OS, apps
   and configuration; images are uploaded as raw or qcow2 files (not OVA) or pulled
   from MacStadium's own remote image repository via `orka image list-remote`
   ([Manage macOS VM images](https://docs.macstadium.com/orka/orka3-cli-reference/image-management), [Base Images](https://orkadocs.macstadium.com/v2.4.0/docs/existing-images-upload-management)).
2. **Baked ahead**, via the Orka Packer plugin
   (`packer-plugin-macstadium-orka`), which takes a source image and runs additional
   provisioning steps to produce a "golden" base image; the general workflow is
   generate/upload/pull an image → use it to create VMs → `save`/`commit` a running,
   restarted VM back into a new base image so settings like SSH persist
   ([Packer plugin config](https://github.com/macstadium/packer-plugin-macstadium-orka/blob/main/docs/builders/config.mdx)).
3. **Secrets:** UNVERIFIED - not found in the pages reviewed; plausibly baked into the
   golden image or injected via Packer provisioners, but not confirmed by a primary
   source.
4. **Cold start / large repos:** UNVERIFIED - not documented in the pages reviewed.
5. **Results out:** UNVERIFIED - Orka is VM infrastructure, not an agent-results
   product; presumably SSH/ARD plus whatever CI tooling is layered on top (e.g.
   Jenkins). Bitrise's own docs reference "Allocating a dedicated host for Mac
   instances," but that describes Bitrise's own AWS EC2 Mac setup, not Orka - a
   separate product, noted here only to avoid conflating the two.

### AWS EC2 Mac instances

1. **Transport:** none built in - a Mac instance boots from an AMI, and getting a
   workspace onto it (SSH, ARD, S3 copy, git clone) is entirely up to the operator's
   own tooling ([Amazon EC2 Mac instances](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-mac-instances.html)).
2. **Baked ahead, via AMI.** AWS-vended AMIs are EC2-optimized and pre-loaded with the
   AWS CLI, CloudWatch Agent, and `ec2-macos-init`; the current AMI ID is looked up via
   SSM Parameter Store, e.g.
   `aws ssm get-parameter --name /aws/service/ec2-macos/sonoma/arm64_mac/latest/image_id`;
   guidance recommends launching fresh from the current AMI rather than reusing a
   customized instance when you don't need to preserve state
   ([Launch an EC2 Mac instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/mac-instance-launch.html)).
3. **Secrets:** not documented as Mac-instance-specific; presumably standard AWS
   mechanisms (IAM role, Secrets Manager, SSM Parameter Store) apply, but nothing
   Mac-specific was found - UNVERIFIED for anything beyond generic AWS practice.
4. **Cold start / cost:** launch time is "roughly 6 to 20 minutes" for AWS-vended AMIs
   on both x86 and Apple silicon Mac instances
   ([Amazon EC2 Mac instances](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-mac-instances.html)).
   AWS does not manage or support the internal SSD on Apple hardware and strongly
   recommends EBS volumes instead, with EBS hotplug supported (same page). The
   Dedicated Host requirement and its 24-hour minimum allocation trace back to Apple's
   own SLA, already documented in this repo's [docs/04-landscape.md](./04-landscape.md).
5. **Results out:** not product-specific - whatever the operator's own tooling does
   (S3 upload, git push, SSH pull); there is no EC2-Mac-specific artifact or PR API.

### GitHub Actions macOS runners

1. **Transport:** `actions/checkout` clones the repo into `$GITHUB_WORKSPACE` on the
   runner using the workflow's built-in token or a supplied PAT - the standard,
   well-established GitHub Actions checkout flow.
2. **Fresh every run, not baked with the repo.** Each job gets a fresh hosted-runner VM
   from a macOS runner image (`actions/runner-images`); GitHub does not offer a
   persistent per-workspace snapshot at the hosted-runner level - every job starts from
   the stock runner image and checks out fresh.
3. **Secrets:** GitHub Actions' standard encrypted repo/org secrets are exposed as env
   vars to the job - this is well-established GitHub Actions behavior, not
   independently re-verified against a specific docs page in this research pass, so
   flag as high-confidence general knowledge rather than a freshly cited primary
   source.
4. **Cold start / large repos:** no first-party numbers were pulled in this pass;
   `actions/checkout` is generally known to support shallow clones via `fetch-depth`,
   but that specific detail was not freshly verified against docs.github.com in this
   session - flag as UNVERIFIED-this-session rather than contested.
5. **Results out:** `actions/upload-artifact` and `actions/download-artifact` are the
   documented artifact path, retained 90 days by default and configurable
   ([actions/upload-artifact](https://github.com/actions/upload-artifact), [Downloading
   workflow artifacts](https://docs.github.com/en/actions/managing-workflow-runs/downloading-workflow-artifacts)).
   `download-artifact@v4` with no `name` downloads every artifact from a run into its
   own directory; the `gh run download` CLI is the command-line equivalent. GitHub's
   own changelog documents recent support for uploading/downloading non-zipped
   artifacts ([GitHub Changelog, 2026-02-26](https://github.blog/changelog/2026-02-26-github-actions-now-supports-uploading-and-downloading-non-zipped-artifacts/)).

### Xcode Cloud

1. **Transport:** Apple's CI integrates directly with the connected source-control
   provider - GitHub, GitLab, Bitbucket, or Apple's own git hosting - and start
   conditions include branch changes, PR changes, tag changes, a schedule, or a VCS
   webhook from providers like GitHub or Bitbucket
   ([Xcode Cloud documentation](https://developer.apple.com/documentation/xcode/xcode-cloud)).
2. **Not a per-run baked image in the way CI competitors describe it** - Apple manages
   the macOS/Xcode toolchain server-side and does not publish build-machine
   provisioning or caching internals; workflows can add additional repositories for
   shared dependencies. UNVERIFIED beyond that framing - Apple does not document the
   underlying VM/snapshot mechanics.
3. **Secrets:** environment variables are configurable per workflow, referenced in
   third-party tutorials covering custom build scripts, but a primary Apple doc
   specifically on Xcode Cloud environment-variable handling was not independently
   fetched in this pass - flag as partially unverified.
4. **Cold start / large repos:** UNVERIFIED - Apple does not publish this.
5. **Results out:** Post-Actions run after the build/test/archive Actions complete
   (e.g., TestFlight distribution, Slack notification); Apple's own WWDC guidance for
   external-tester distribution recommends selecting a single branch as the start
   condition and a "Clean" environment for consistency
   ([Customize your advanced Xcode Cloud workflows, WWDC21](https://developer.apple.com/videos/play/wwdc2021/10269/)).
   Once an Archive Action runs, the build appears in App Store Connect under
   TestFlight → Builds within a few minutes.

## Synthesis: the three transport patterns

| Pattern | Transport | Cold start | Best for | Notable adopters |
| --- | --- | --- | --- | --- |
| **Upload per run** | HTTP file-write API, tarball, or bind/volume mount | Fast for small deltas; scales with payload size, not repo history | Small workspaces, untrusted/AI-generated code, one-shot execution, no git provider available | E2B `files.write` ([docs](https://e2b.dev/docs/filesystem/upload)); Vercel Sandbox `tarball`/`writeFiles` source ([docs](https://vercel.com/docs/sandbox/working-with-sandbox)); Cloudflare Sandbox `writeFile` ([docs](https://developers.cloudflare.com/sandbox/get-started/)); Claude Code `--cloud` bundle-and-upload path ([docs](https://code.claude.com/docs/en/sandbox-environments)) |
| **Clone in sandbox** | `git clone` with an injected token, run fresh every session/job | Dominated by repo size and network; no persistent state carried forward unless separately cached | Untrusted or short-lived agent runs, CI jobs, products that want a verifiably clean checkout every time | Codex cloud ([docs](https://learn.chatgpt.com/docs/environments/cloud-environment)); Daytona `git.clone` ([docs](https://www.daytona.io/docs/en/git-operations/)); Cloudflare `gitCheckout` ([docs](https://developers.cloudflare.com/sandbox/guides/git-workflows/)); Jules ([docs](https://jules.google/docs/environment/)); GitHub Actions `actions/checkout`; Xcode Cloud ([docs](https://developer.apple.com/documentation/xcode/xcode-cloud)) |
| **Fork a warm snapshot** | Container/VM image bake ahead of time, or a true memory/hypervisor snapshot forked per run | Sub-second to a few seconds when true memory-snapshot forking is used; minutes when it's really "cached container resume" rather than a real fork | Repeated runs against the same repo, parallel agent branches/exploration, latency-sensitive interactive use | Morph Infinibranch, `<250ms` vs "2-3 minutes for typical VMs" ([docs](https://cloud.morph.so/docs/developers)); Vercel Sandbox `Sandbox.fork()` seeded from latest snapshot ([docs](https://vercel.com/docs/sandbox/working-with-sandbox)); Cursor Cloud Agent Builds, a pre-warmed bootable snapshot ([docs](https://cursor.com/docs/cloud-agent/builds)); Devin Machine Snapshots, "a frozen, bootable image the session starts from" ([docs](https://docs.devin.ai/onboard-devin/repo-setup)); Modal filesystem/memory snapshots ([docs](https://modal.com/docs/guide/sandbox-snapshots)); E2B templates, boot in under a second when pre-warmed ([docs](https://e2b.dev/docs/sandbox-template)); Codex cloud's 12-hour container cache (a weaker, resume-not-fork variant) ([docs](https://learn.chatgpt.com/docs/environments/cloud-environment)) |

### Measured numbers, not vendor claims

Almost every latency figure in the product sections above is vendor self-reported. Four
sources in this survey are reproducible or first-hand, and they are the ones worth
anchoring on.

**Sandbox boot is already cheap; the checkout is what costs.** ComputeSDK runs a public,
GitHub-Actions-scheduled benchmark defining time-to-interactive as "the elapsed time from
calling `compute.sandbox.create()` to the first successful `runCommand()` inside the
sandbox," 100 iterations per provider per run on 4 vCPU / 16 GB in Northern Virginia. The
run dated September 11, 2026 gives median TTI of 0.35 s for Daytona, 0.50 s for Vercel,
0.89 s for Modal, 1.14 s for Runloop, 1.28 s for E2B and 5.70 s for Cloudflare
([Burst TTI leaderboard](https://www.computesdk.com/benchmarks/sandboxes/burst-tti/),
[source repo](https://github.com/computesdk/benchmarks)). Medians are not strictly
comparable across the table because success rates differ (Sandbox0 5%, Microsandbox 55%,
Hopx 0%, where a 0.00 s median reflects no successful runs rather than speed). ComputeSDK
also runs a heavier "Dax" suite that times a real cold clone plus install plus typecheck
of the opencode repo phase by phase, and a majority of the 30 providers tested fail it
outright, mostly for lacking curl, root/sudo or a package manager
([Dax benchmark](https://www.computesdk.com/benchmarks/sandboxes/dax/)).

**Matt Rickard's "Clone is the new bottleneck for agents" (June 12, 2026)** is the
clearest first-hand writeup of the exact tradeoff in this doc's title. His argument: TTI
stops at the moment the sandbox can run a command, which excludes pulling the code down,
and for many repos that pull costs "several sandbox boots." Benchmarking with hyperfine,
n=10 per lane with a discarded warmup, against four mirrored repos (hello-world 4 KiB;
nanoid 0.4 MiB / 5,756 objects; claude-code 12.6 MiB; vite 25.0 MiB / 114,514 objects),
he reports that with a clone plan precomputed outside the timed window a full clone
materializes in a median 184 to 646 ms, i.e. faster than the sandbox boots
([blog.matt-rickard.com](https://blog.matt-rickard.com/p/clone-is-the-new-bottleneck-for-agents),
[r2d4/corigin-benchmarks](https://github.com/r2d4/corigin-benchmarks)). The stated cost is
a custom read-side protocol replacing `git clone`, slower pushes (that is when the
acceleration artifacts get precomputed), and no support for shallow, blobless, treeless or
single-branch variants. Directly relevant to greenroom: the win came from moving work
*out* of the per-run critical path, not from a faster network.

**If you do clone per run, GitHub's own guidance is explicit about which clone.** For
workflows that "need to do a single clone and delete the repository immediately, shallow
clones are a good option," while "if you need the commit history in your build, then a
treeless partial clone might work better for you than a full clone"; shallow clones are
not recommended for developers, and "always use a full fetch instead of a shallow fetch"
([Get up to speed with partial clone and shallow clone](https://github.blog/open-source/git/get-up-to-speed-with-partial-clone-and-shallow-clone/)).
GitHub's companion data-driven study puts numbers on it: "a shallow clone of
`torvalds/linux` is four times faster than a full clone, while a treeless clone is only
twice as fast and a blobless clone is only 1.5 times as fast" (5 m full, 2.4 m treeless,
3 m blobless), and "shallow clone of all the three different repositories consumes the
lowest amount of total and Git CPU per clone." The catch is on the fetch side, where
treeless is far worse than blobless (on linux, 400 ms vs 140 ms CPU per fetch and 1250 ms
vs 500 ms to reset), leading the authors to "strongly recommend against shallow fetches
and fetching from treeless partial clones"
([Git clone: a data-driven study on cloning behaviors](https://github.blog/open-source/git/git-clone-a-data-driven-study-on-cloning-behaviors/)).
The hazard worth carrying into a sandbox design: partial clones are online-first, so a
sandbox with restricted egress can stall or fail when the promisor remote is unreachable.

**Fly.io Sprites is the closest shipped analogue to what greenroom wants, and it is
filesystem-only.** Checkpoints "take about 300ms to create," restore reconnects "in well
under a second," and a cold-start request to a Sprite URL "will typically take less than
1s" ([Checkpoint & Restore](https://fly.io/sprites/checkpoint-restore/)). The reason it is
that fast is architectural rather than hypervisor-level: root storage is S3-compatible
object storage via "a very hacked-up JuiceFS, rewritten with a SQLite metadata backend,"
splitting data into immutable chunks plus a metadata map, with a sparse 100 GB NVMe volume
per Sprite acting as "a dm-cache-like" chunk cache, so checkpoint and restore "merely
shuffle metadata around" ([The Design and Implementation of Sprites](https://fly.io/blog/design-and-implementation/)).
Note what this means: Fly's checkpoints capture disk state, not RAM, and Fly's own docs
describe restoring one Sprite to its own earlier state rather than forking many children
from one checkpoint. Their writeup on an agent destroying its own toolchain reports a
restore back to a working path in about nine seconds, and argues that copy-on-write makes
checkpointing before every risky step cheap enough to be reflexive
([Building Agents That Don't Break Themselves](https://fly.io/blog/building-agents-that-dont-break-themselves/)).

Underlying-technology sources for the snapshot-fork pattern, independent of any one
vendor's marketing:

- **Firecracker's own snapshot support** documents full snapshot/restore, including
  page-fault handling on resume and explicit caveats - cgroups v1 causes high
  snapshot-restoration latency (v2 "strongly recommended"), a resumed guest's network
  connection state is not guaranteed to survive, and diff snapshots are still in
  developer preview pending `guest_memfd` integration
  ([snapshot-support.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md),
  [handling-page-faults-on-snapshot-resume.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)).
  This is the microVM-snapshot mechanism E2B, Vercel Sandbox and (per Fly's own
  explainer) Fly Machines all build on. Two details matter for anyone designing a
  fork-a-warm-snapshot system. First, resume is fast by construction: the memory file is
  mapped `MAP_PRIVATE` rather than read in up front, giving "runtime on-demand loading of
  memory pages" and so "very fast snapshot loading times, but comes with the cost of
  having to keep the guest memory file around" for the resumed VM's whole lifetime, with
  an optional `Uffd` backend handing page faults to a userspace process instead of the
  kernel. Second, and more sharply: Firecracker's maintainers "consider resuming execution
  from the same state more than once insecure," because duplicated guest state "can
  include identifiers, random numbers and random number seeds, the guest OS entropy pool,
  as well as cryptographic tokens." The VMGenID device mitigates this only partially, by
  exposing a fresh 16-byte identifier on resume that a modern Linux guest uses to reseed
  its PRNG, and the doc is explicit that other state "will still be replicated across
  multiple microVMs resumed from the same snapshot." Guest network connectivity "is not
  guaranteed to be preserved after resume," and the guest wall clock resumes from the
  moment of snapshot creation and needs correcting guest-side. In other words the
  "infinite branching" pitch is a genuine performance win with real correctness and
  security homework attached, and no vendor marketing page in this survey mentions that
  homework.
- **CRIU** (Checkpoint/Restore In Userspace) is the general Linux process-snapshot
  mechanism referenced as an alternative or complement to hypervisor-level snapshots:
  it attaches via `PTRACE_SEIZE`, injects parasitic code to dump memory pages into
  image files, and captures open files, credentials, registers and task state
  ([Marcus Folkesson, Checkpoint-restore in Linux](https://www.marcusfolkesson.se/blog/checkpoint-restore/)).
  A directly on-topic benchmark: restoring a VNC server plus an Eclipse IDE from a
  CRIU checkpoint dropped startup time from roughly 29 seconds to about 1.5 seconds
  versus cold init ([bex.co, Kubernetes checkpoint/restore for agent sandboxes](https://bex.co/blog/2026/08/08/kubernetes-checkpoint-restore-criu-agent-sandbox-costs)).
  CRIU has real constraints relevant to an agent sandbox: it always checkpoints and
  restores a process tree together with all its children, the restored PID must not
  already be in use, and library versions must match exactly between source and
  destination systems ([Red Hat, CRIU documentation](https://access.redhat.com/articles/2455211)).
- No CRIU-for-macOS-VM equivalent was found - CRIU is Linux-process-level, not
  applicable to a full Virtualization.framework guest the way Tart's own APFS-based
  clone-and-suspend model is. This is a genuine gap: **the primary source for
  fast macOS VM state transfer in this whole survey remains Tart's own clone/suspend
  mechanism** (already the basis for greenroom's own design in
  [docs/03-tech-stack.md](./03-tech-stack.md)), not any of the Linux/Firecracker
  snapshot literature above.

## Could not verify / open questions

- **Runloop:** secrets/credential injection mechanism, and any cold-start or
  large-repo guidance - not found in the docs pages reviewed.
- **Morph:** how application-level secrets (as opposed to the `MORPH_API_KEY` client
  credential) get into a running instance or devbox; whether the "<250ms" and "2–3
  minutes" figures are independently reproducible or purely a vendor marketing claim.
- **Devin:** the exact underlying git-auth protocol (GitHub App vs. OAuth vs. deploy
  key); any documented PR-creation or git-push mechanism from inside a session.
- **Jules:** secrets/credential injection; cold-start numbers; large-repo/monorepo/LFS
  guidance; the results-out mechanism (PR creation, diff, git push) - none were found
  on the environment-setup docs page, despite Jules being a widely used product.
- **Scrapybara:** the specific file/env protocol mechanics (`/protocols/file`,
  `/protocols/env`) were not fetched - only the introduction page was reviewed. The
  product is sunset, so this is low priority to chase further.
- **MacStadium/Orka:** secrets injection and results-out mechanism - not documented in
  the image-management and Packer-plugin pages reviewed.
- **Cua/Lume:** secrets injection for both the local tool and the hosted Cloud
  Sandbox; cold-start numbers; results-out beyond bare SSH. The hosted cloud product's
  own open GitHub issue (`trycua/cua#3584`) suggests its docs may currently be ahead of
  or inconsistent with its actual backend.
- **AWS EC2 Mac / GitHub Actions macOS runners / Xcode Cloud:** these are general
  compute/CI platforms rather than agent-sandbox products, so several of the 5
  questions (secrets specifics, large-repo handling, results-out beyond
  artifacts/TestFlight) are either generic-platform behavior not worth re-deriving
  here, or genuinely undocumented by the vendor (Apple in particular publishes almost
  no Xcode Cloud infrastructure detail).
- **Fly.io:** still no first-party Fly engineering post giving reproducible *Firecracker
  memory-snapshot* restore latency numbers for Machines specifically. The Sprites gap
  noted in an earlier revision is now closed and written up above, but note what closing
  it revealed: Sprites' fast checkpoint/restore is object-store-plus-metadata, not memory
  snapshotting, so it is not evidence about Firecracker memory-snapshot performance at
  all. Separately, a secondary source reports Fly Machines at roughly 2.8 s p50 cold boot
  in 2026 versus the 300 ms claimed in the 2022 launch post; this was not traceable to a
  primary source and should be treated as UNVERIFIED rather than cited.
- **Claude Code on web:** the self-hosted-environments doc and the Docker Sandboxes
  third-party integration were referenced but not independently fetched - both are
  worth a follow-up pass, since "workspace sync" as a named Docker Sandboxes feature is
  directly on-topic for greenroom's own problem.
- **Codex cloud:** no monorepo, large-binary-asset, or git LFS guidance found anywhere
  in the docs reviewed, despite Codex cloud being widely used on large real-world
  repos.
- E2B's "sub-200ms" and "effectively eliminate cold starts" framing, and Morph's
  "<250ms" branch claim, are both vendor self-reported; neither this research pass nor
  either vendor's own docs point to an independently reproduced, third-party benchmark
  comparing them head to head under identical conditions.
