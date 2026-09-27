# 17. Scene setup: getting the app under test into a known state

Date: 2026-09-27. Status: plan, not a decision. Feeds a grilling session and later ADRs.

A **scene** is the state a verification starts from. It covers the app under test, built
from the change, running with its launch configuration, its helper services up and
answering, and its data seeded, in a machine whose toolchains and permissions it needs.
Today the coding agent builds each scene by hand with `machine_sync` and `machine_exec`,
and describes it to the verifier in prose. This document measures what that costs in
today's runs, reviews how other tools declare this state, and proposes a checked-in
project file with named scenes that the daemon builds, checks and can reset. The
Greenroom-on-Greenroom scene is the first worked example.

Vocabulary: this document avoids "phase" (in the Companion glossary it is a run's
booting, live, idle, ended or failed) and "stage" (the Companion's main column). A
scene's parts are called **sections**, and "step" keeps its meaning of one recorded tool
call.

## 0. The short version

1. **Setup, not verification, was the costly part of today's dogfood runs.**
   - Hand-built scenes took 2 to 11 minutes from "machine ready" to "task posted",
     with 5 to 10 coder tool calls each.
   - In one run, wrong scene data cost a whole verification round. A truncated copy of a
     recording made the verifier fail four checks on the app, and two hand resets
     followed.
   - Across 305 bench trials, the bench's scripted setup took a median of 34 to 38 s
     and never failed.
2. **Every comparable tool splits setup into ordered, named parts.** Slow and secret-free
   work that can be cached comes first. Per-run data and secrets come next, then the
   per-launch arguments. Readiness is an explicit probe, never "the process started". A
   failed setup is reported apart from a failed test (section 2).
3. **Proposal: `greenroom.json`, checked in at the project root.** It declares the
   project's toolchains, build, seed data, guest services, app launch, readiness probes
   and data checks, and names **scenes** that a task can reference.
   - A new tool, `machine_scene`, has the **daemon** build a scene in the guest. Each
     section is recorded as its own steps. The tool runs the readiness probes and data
     checks and returns a scene report.
   - `machine_scene_reset` puts the scene back to its starting state.
   - The verifier gets the report, not the coder's prose, and may reset the scene
     itself.
4. **Smallest valuable first step: no daemon code.** Check in the Companion's scene as
   one guest script plus trimmed fixture runs, including a data check that would have
   caught today's truncated recording. Then measure the next five dogfood runs. The
   schema and the tool come after the script has shown which sections are really
   needed (section 4).

## 1. The measured problem

### 1.1 Data and method

Sources, all read-only:

- The three dogfood runs from 2026-09-26/27: `conversation.jsonl`, `steps.jsonl` and
  `manifest.json` in `~/.greenroom/runs/<id>/`.
- The earlier demo runs from 2026-09-25 (`20260925-*`).
- The coordinator's timeline for issue #153:
  `~/.claude/jobs/1b9b4166/tmp/dogfood/timeline.txt`.
- The bench results in `~/.greenroom/bench/results/*.jsonl` (`setupSeconds`, `seconds`,
  `ending`).
- `~/.greenroom/dogfood/` was empty when this was written. The dogfood agents had not
  written their logs yet.

The definitions:

- **Setup wall:** the time from the "machine is ready" event to the coder's first `task`
  message.
- **Setup calls:** the coder's tool calls in that window that build the scene. Calls that
  test the change itself, such as `go test` of the branch, are left out.
- **Setup-caused waste:** verifier rounds or coder messages whose cause, as the coder
  wrote it, was the scene and not the app.

### 1.2 Today's dogfood runs

| Run | Change verified | Setup wall | Setup calls (tool time) | Verification wall | Rounds | Setup-caused waste |
| --- | --- | --- | --- | --- | --- | --- |
| `20260926-231011-600cf88cfbdbcba8` | Companion stop-limit card (`rsi/stop-limit-card`) | 2 min 3 s | 5 (48.7 s) plus 7 more in two resets (20.1 s) | 41.5 min (23:12:48 to 23:54:20) | 4 tasks or disputes, 1 correction | Round 1 (10.7 min): 4 of Run B's checks failed on a truncated copy of its recording. One correction message came from ambiguous fixture data. Two hand resets. |
| `20260926-234503-3057f9fab7ae9133` | #153 verdict card | 11 min 14 s | 10 (about 70 s), plus a 6 min gap with no machine calls | 13.1 min (23:56:52 to 00:09:57) | 3 | No data error. One Go install call failed (exit 127, PATH). |
| `20260927-000233-acbc2b008dfc6a8f` | #146 blank transcript | 3 min 46 s | 8 (153.5 s) | Still running at 00:13 (43 messages) | 1 so far | None so far. It needed a Python venv and a `sed` edit to a synced script (toolchain gap). |

What the setup calls were:

- **600cf88.**
  - It synced the Companion source and a directory the coder prepared on the host. That
    directory held a cross-built `greenroom` binary and two hand-edited copies of a
    recorded run.
  - It ran `swift build` (37.3 s).
  - It started the daemon with `nohup ./greenroom serve -root ... -tart /usr/bin/false
    -verifier manual -addr 127.0.0.1:7777 -frame-interval 0` and then `sleep 2`.
  - It launched the Companion with `launchctl asuser ... GREENROOM_URL=...` and then
    `sleep 5`.
  - Each of the two resets killed both processes, removed the daemon root, synced it
    again, restarted the daemon and relaunched the app (steps 47 to 50 and 66 to 68).
- **3057f9.**
  - It installed Go by hand: download and untar, a PATH edit in `.zprofile`, and a
    first call that failed with exit 127.
  - Then came six minutes with no machine calls. During them the daemon binary was
    cross-built **on the host** (its mtime is 23:55:09Z), even though Go was now in the
    guest.
  - It made three syncs: source, binary and a scratch daemon root.
  - It ran `swift build` (53.5 s), started the daemon as a `machine_session_start`
    session on port 7851, checked it with `curl`, and launched the Companion with
    `sleep 4`.
- **acbc2b.**
  - It made four syncs: two Companion trees, the design notes, and a guest directory
    holding a binary, two runs and a load script.
  - It ran two `swift build`s in one call (94.3 s, exit 1 from a `grep -c` in the same
    line).
  - It made a Python venv with Pillow and `sed`-patched the synced `loop.sh` to use it.
  - It started the daemon with `nohup` and `sleep 2`, and started two background render
    loops with `sleep 45`.

### 1.3 Earlier runs and the bench

- **Demo apps (2026-09-25).**
  - Groceries went from ready to task in 28 s, with one `swiftc` and a launch in a single
    exec (10.0 s).
  - HelloGreenroom took about 3 min, with a 49.6 s build, a re-sync and a rebuild
    (`20260925-182812-20cf20b99f0a188c`).
- **Vorssaint (`20260925-184835-8ba3655cc574f7b4`, `20260925-184836-21e3c89e770a69ae`).**
  - Both machines ran for 45 min and never got a task.
  - Their setup polled `build.sh` with sleep loops: 5 exec calls just to wait.
  - They set OS state with `defaults write com.apple.symbolichotkeys`, then clicked
    through System Settings.
  - Scenes can need OS preferences, not only app data.
- **Bench (ADR 0025).**
  - The runner's scripted setup is boot, sync, `build.sh`, and `open` plus a `pgrep`
    wait.
  - `SetupSeconds` median: 34.1 s (`simple-dev-main-r2`, 90 trials), 36.7 s
    (`simple-dev-0027-r1`, 90), 37.1 s (`simple-dev-main-r3`, 84) and 38.4 s
    (`baseline-pre0024-dev`, 41). The maximum was 85.6 s.
  - There was no `setup_error` ending in 305 trials.
  - The verification that followed took a median of 75 to 126 s.
  - A declared scene costs about a third of a trial. A hand-built one costs several times
    the verification itself.

### 1.4 Failure modes

| # | Failure mode | Seen in | What would have caught or prevented it |
| --- | --- | --- | --- |
| F1 | **Wrong data, blamed on the app.** A copy of Run B's recording was truncated. The Companion showed "No conversation yet", and the verifier failed 4 checks on the app. | 600cf88, seq 33 to 34 | A data check before the task: "the daemon serves 2 runs, and Run B has at least N messages" |
| F2 | **Ambiguous data.** Both fixture runs came from one recording and showed the same title and short id (b48b96). The coder's task described them wrongly and needed a correction. | 600cf88, seq 31 | Fixtures with distinct ids, and a check that short ids are distinct |
| F3 | **Checks consume state.** Clicking Continue posts a message to Run B, so each re-check needed a hand reset: kill both processes, delete the root, sync again, restart both. | 600cf88, steps 47 to 50 and 66 to 68 | A reset to the scene's start, callable by the coder and the verifier |
| F4 | **Toolchains paid per run.** Go was downloaded each run (#164), and the PATH edit failed once. The image's Python lacks Pillow. | 3057f9, acbc2b | Toolchains declared and checked against the image manifest (ADR 0019); layers or volumes (#164) |
| F5 | **Split host and guest builds.** The daemon was cross-built on the host while the Companion was built in the guest. A remote agent (ADR 0021) would need its own Go and the same cross-compile flags. | 600cf88, 3057f9 | Build in the guest from the synced tree, with a cache (#129) |
| F6 | **No two runs set up the same way.** There were three launch methods (`nohup` plus `launchctl asuser`, a pty session, and `open` in the bench), two ports (7777 and 7851) and three data layouts. | All three | One declared launch and one runner |
| F7 | **Readiness by sleeping.** Commands used `sleep 2`, `sleep 4`, `sleep 5`, `sleep 45`, and polling loops on `build.sh`. | All, and Vorssaint | Readiness probes with timeouts |
| F8 | **Unrelated files in scene data.** The scratch daemon root synced in 3057f9 held `daemon.lock` and the SSH key pair (`id_ed25519`, `.pub`) that the daemon creates in any root. No harm this time, but this is how a secret gets into a scene. | 3057f9 (`scratch153`) | Scene data lists what it copies, and the loader refuses key and env file patterns |
| F9 | **The verifier sees the scene only through prose.** When the prose and the scene disagree (F1, F2), the verifier's fail lands on the app, and the coder has to dispute. | 600cf88 | A daemon-written scene report in the verifier's context, and a separate "scene not ready" ending |

Buyers will meet the same thing at a larger scale. Section 6.3 of
`docs/15-market-and-pricing.md` lists "Setup friction is low enough for PLG" as a
riskiest assumption: "Xcode projects need signing, secrets, simulators, sometimes backend
fixtures and test accounts, and a way to tell the verifier what 'works' means." Real
apps will add backends, seeded databases, logins, feature flags, mock APIs, sample files,
TCC permissions (#131), toolchains (#164) and build caches (#129). Every one of these is
today another prose paragraph and another few `machine_exec` calls.

## 2. What other tools do

All facts come from the tools' own docs, fetched on 2026-09-26. cirrus-ci.org did not
resolve, so the Cirrus facts come from its docs source on GitHub.

| Tool | File | What it gives | Lesson for Greenroom |
| --- | --- | --- | --- |
| Dev Containers | `.devcontainer/devcontainer.json` | Ordered hooks: `initializeCommand` on the host, then `onCreateCommand`, `updateContentCommand` and `postCreateCommand` in the container, then `postStartCommand` on every start and `postAttachCommand` on every attach. A failed hook skips the later ones. `waitFor` names the hook that must finish before a tool connects (default `updateContentCommand`). `features` add toolchains. `containerEnv` is fixed; `remoteEnv` can change without a rebuild. The open spec has no secrets. | Name the boundary between cacheable setup and per-run setup, and say which part must finish before anyone looks. [containers.dev/implementors/json_reference](https://containers.dev/implementors/json_reference/), [spec](https://containers.dev/implementors/spec/) |
| Codespaces prebuilds | same file | A prebuild runs up to `updateContentCommand` and never `postCreateCommand`. Prebuilds see only repository or organization secrets, never user secrets. `secrets` in devcontainer.json lists the required names with a description and a doc URL, and the user is prompted for the values. | Snapshot or cache only the secret-free part. The file names secrets and never holds them. [about prebuilds](https://docs.github.com/en/codespaces/prebuilding-your-codespaces/about-github-codespaces-prebuilds), [recommended secrets](https://docs.github.com/en/codespaces/setting-up-your-project-for-codespaces/configuring-dev-containers/specifying-recommended-secrets-for-a-repository) |
| Docker Compose | `compose.yaml` | `healthcheck` takes `test`, `interval`, `timeout`, `retries`, `start_period` and `start_interval`. `depends_on` takes a `condition`: `service_started`, `service_healthy` or `service_completed_successfully`. `profiles` gives named variants. `env_file`, with `environment` overriding it. | Readiness is its own probe. A seed or migration is a one-shot job whose exit code gates what follows. [services reference](https://docs.docker.com/reference/compose-file/services/) |
| GitHub Actions `services:` | workflow YAML | Service containers with `--health-cmd` options, so the job waits "until postgres has started". They need a Linux runner. | Container services are not available on macOS runners. Backing services must run natively in the guest or in a sidecar, behind the same kind of health gate. [service containers](https://docs.github.com/en/actions/tutorials/use-containerized-services/use-docker-service-containers) |
| Vercel and Netlify previews | project settings, `netlify.toml` | Env values scoped by environment (Production, Preview, Development) and by branch, with branch values overriding. Changes apply only to new deployments. Netlify has `[context.deploy-preview.environment]`, wildcard branch contexts, and a read-only `CONTEXT` variable. | Env is layered (base, then scene, then run) and resolved once per run. Record the resolved names, not the secret values, so a run can be reproduced. [Vercel env](https://vercel.com/docs/environment-variables), [Netlify env](https://docs.netlify.com/build/environment-variables/overview/) |
| Playwright | `playwright.config.ts` | `webServer`: `command`, `url` (ready on 2xx, 3xx and some 4xx), `timeout`, `env`, `reuseExistingServer`, and `wait` (a stdout regex). Setup projects are preferred to `globalSetup` because they show up in traces and reports. `storageState` saves login state, and the docs say to gitignore it because it holds live cookies. | Make setup a first-class, traced step with its own line in the report. Saved login state is a secret. [webServer](https://playwright.dev/docs/test-webserver), [global setup](https://playwright.dev/docs/test-global-setup-teardown), [auth](https://playwright.dev/docs/auth) |
| Cypress | `cypress.config.js` | Seed through `cy.task` or the backend's API. Reset state in `before` or `beforeEach`, never in `after`. `cy.session` caches login and re-runs setup when `validate` fails. | Reset at the start, not the end, and seed through the app's own API or CLI. Trust a cached state only after validating it. [best practices](https://docs.cypress.io/app/core-concepts/best-practices), [cy.session](https://docs.cypress.io/api/commands/session) |
| Xcode | `.xctestplan`, schemes | A test plan has named configurations. Each sets launch arguments, environment variables, language, region and simulated location. `xcodebuild -testPlan P -only-test-configuration C` runs one. `XCUIApplication.launchArguments` and `launchEnvironment` apply at the next launch. | Named configurations are the unit for variants. The app reads a launch flag and gets itself into a known state, instead of an agent clicking it there. [test plans](https://developer.apple.com/documentation/xcode/organizing-tests-to-improve-feedback), [launchArguments](https://developer.apple.com/documentation/xcuiautomation/xcuiapplication/launcharguments) |
| Maestro | flow YAML | `launchApp` takes `clearState`, `clearKeychain`, `permissions` and `arguments` in one step. If `onFlowStart` fails, the flow is marked failed and its body is skipped. | "Clear, grant, launch with arguments" is one atomic step. Setup failure is recorded apart from the test. [launchApp](https://docs.maestro.dev/reference/commands-available/launchapp.md), [hooks](https://docs.maestro.dev/maestro-flows/flow-control-and-logic/hooks.md) |
| Cirrus CI and Tart | `.cirrus.yml` | `macos_instance` runs on Tart. `<name>_cache` takes `folder`, `fingerprint_script` and `populate_script`: the script's output, hashed, is the cache key. `background_script` starts services without waiting for them. `tart run --dir=name:path:ro` mounts a host folder read-only. Packer builds golden images. | The closest analog to Greenroom. Bake slow setup into the image, key dependency caches on a fingerprint, and give the verifier exactly the tree it was handed. [writing tasks](https://github.com/cirruslabs/cirrus-ci-docs/blob/master/docs/guide/writing-tasks.md), [Tart quick start](https://tart.run/quick-start/) |
| Nix and devenv | `flake.nix`, `devenv.nix` | `processes.<name>.exec` with readiness probes (exec, `http.get`, or a notify `READY=1`). `after = ["devenv:processes:db@ready"]` orders processes. `devenv test` builds the env, starts processes, runs `enterTest` and stops them. There are profiles, and it runs natively on macOS without Docker. | This is the right shape for an in-VM app stack: processes, readiness, ordering and a test entry point in one file. [processes](https://devenv.sh/processes/), [tests](https://devenv.sh/tests/), [profiles](https://devenv.sh/profiles/) |
| Replit | `.replit`, `replit.nix` | `run`, `entrypoint`, `onBoot`, `[nix] packages`, `[[ports]]` and `[run.env]`. | A useful manifest can be very small. [configuration](https://docs.replit.com/replit-app/configuration) |

Lessons that carry over:

1. **Ordered sections, with the cacheable and secret-free work first.** This is
   Devcontainer's order, the Codespaces prebuild boundary and Packer with Tart. A
   Greenroom machine is disposable, so "start" means a fresh clone, and a cache may hold
   only secret-free outputs.
2. **Readiness is a probe with a timeout** (Compose `service_healthy`, Playwright `url`,
   devenv `@ready`), never a `sleep`.
3. **Seeds are one-shot jobs with an exit code**, run through the app's own API or CLI,
   never through UI clicks.
4. **Reset at the start** (Cypress, Maestro `clearState`). Leave the state as it is after
   a failure, so it can be inspected.
5. **Setup failure is its own outcome** (Maestro, Playwright setup projects). For
   Greenroom this means the verifier never verdicts an app whose scene did not come up.
6. **Named variants** (Compose profiles, Xcode configurations, devenv profiles, Netlify
   contexts). A scene name plus a commit should determine the starting state.
7. **Known state enters through launch arguments and environment** where the app
   supports it (XCUIApplication, Maestro).
8. **Secrets are declared by name and injected late** (Codespaces, Cirrus `ENCRYPTED[]`,
   Vercel and Netlify scopes). They are never cached, and saved login state counts as a
   secret.
9. **Caches are keyed on fingerprints** (Cirrus). This is already the shape of #129.
10. **Independence.** The readiness and data checks the verifier relies on should be run
    and recorded by the platform, not asserted by the agent whose work is being checked.

## 3. Proposed design

### 3.1 The pieces

- **`greenroom.json`**, checked in at the project root. It holds the project's shared
  sections and its named scenes. Scripts and fixtures live anywhere in the repository
  and are referenced by relative path, as bench cases reference patches.
- **`internal/scene`**, a daemon package that parses and validates the file and builds a
  scene on a machine through `machine.Manager`. It sits beside `internal/bench`: it
  drives `machine`, and `mcpserver` and `bench` use it.
- **`machine_scene {runId, project, scene}`**, a new MCP tool.
  - `project` is the synced project's directory in the guest, for example
    `~/work/greenroom`.
  - The daemon reads `greenroom.json` from the **guest copy**. This works the same for a
    local agent and a remote agent (ADR 0021), and the daemon never reads the agent's
    disk.
  - It returns a scene report.
- **`machine_scene_reset {runId}`**: back to the scene's start, callable by the coder and
  the verifier.
- **A scene report** that the daemon writes into the run and puts in the verifier's
  context. There is also a new ending for a scene that did not come up.

### 3.2 The file

The format is JSON, decoded strictly like `bench/cases/*.json` (`strictDecode`), so no
new dependency is needed and an unknown key is an error that names the file. See the
open questions for YAML or TOML.

```json
{
  "version": 1,
  "toolchains": { "go": "1.27", "xcode": "27" },
  "build": [
    { "name": "daemon", "cwd": "apps/daemon", "run": "go build -o ~/.scene/bin/greenroom .",
      "cache": { "paths": ["~/Library/Caches/go-build", "~/go/pkg/mod"], "key": ["apps/daemon/go.sum"] } },
    { "name": "companion", "cwd": "apps/companion", "run": "swift build",
      "cache": { "paths": [".build"], "key": ["apps/companion/Package.resolved"] } }
  ],
  "services": {
    "daemon": {
      "run": "~/.scene/bin/greenroom serve -root $SCENE_DATA/root -tart /usr/bin/false -verifier manual -addr 127.0.0.1:7777 -frame-interval 0",
      "ready": [{ "http": "http://127.0.0.1:7777/api/runs", "timeout": 20 }]
    }
  },
  "app": {
    "run": "apps/companion/.build/out/Products/Debug/Companion",
    "env": { "GREENROOM_URL": "http://127.0.0.1:7777" },
    "ready": [{ "window": { "app": "Companion" }, "timeout": 30 }]
  },
  "scenes": {
    "companion-stopped-runs": {
      "describe": "The Companion shows two recorded runs of 'I added a \"Longest word\" line to WordCount'. Run A (short id b48b96) stopped at its time limit and its machine was destroyed. Run B (short id c0471f) stopped at its time limit with no machine behind it. Nothing is live.",
      "data": [{ "from": "apps/companion/Fixtures/runs/stopped-a", "to": "root/runs/20260926-071916-b48b96d157b71fcd" },
               { "from": "apps/companion/Fixtures/runs/stopped-b", "to": "root/runs/20260926-071917-c0471f00d157b7aa" }],
      "expect": [{ "name": "two runs, distinct short ids, both with messages",
                   "run": "apps/companion/Fixtures/check.sh 127.0.0.1:7777 2" }]
    }
  }
}
```

The sections, in the order the daemon builds them:

| Section | Runs | Cached? | Secrets? | Recorded as |
| --- | --- | --- | --- | --- |
| `toolchains` | Checked against the machine's `toolchain` manifest (ADR 0019). A missing tool fails here by name. Until #164 exists, an optional `install` command is allowed, recorded as "installed at run time". | Yes (image or #164 volume) | No | One step: what is present and what was installed |
| `build` | Named commands in the synced tree. `cache.paths` are restored before and saved after, keyed on the image, the toolchain and a hash of the `cache.key` files (#129). | Yes | No | One step per build, with cache hit or miss |
| `permissions` | TCC grants for the app under test (#131: `accessibility`, `screenCapture`, `appleEvents:<bundleId>`, ...) and `defaults` domains to write (OS prefs such as Vorssaint's symbolic hotkeys). | No | No | One step naming each grant, which the verifier sees |
| `data` and `seed` | `data` copies fixture paths from the guest copy of the project into `$SCENE_DATA` (a fresh directory each time). `seed` runs one-shot commands with an exit code, such as a migration, `defaults import`, or a POST to the backend's seed API. | No | Only by name | One step per copy or command, with a content digest of the copied data |
| `services` | Long-lived guest processes started as pty sessions (ADR 0017), so their output can be read with `machine_session_read` and they end with the machine. `after` orders them. | No | Injected | One step per service, and its readiness result |
| `app` | The app under test, with `env`, `args` and `ready` probes. A bundle is launched with `open`; a bare executable runs in a session. | No | Injected | One step with the launch line and readiness |
| `expect` | Data checks run once, after everything is ready. Each is a command that must exit 0. | No | No | One step per check, with its output |

- **Readiness probes** are `process` (a name), `http` (a URL answering 2xx), `port`,
  `window` (an on-screen window of an app, read with the input helper's
  `--desktop`/CGWindowList path from ADR 0018), `ax` (an accessibility element with a
  given label, read the way `machine_ui` reads) and `command` (exit 0). Each is polled
  until it holds or its timeout passes. A timeout fails the scene and names the probe and
  its last output.
- **A scene** is a name plus overrides: its own `data`, `seed`, `env`, `args` and
  `expect`, and a `describe` text. The shared sections apply to every scene. A task names
  a scene instead of describing the data.

What the file must never hold:

- secret values;
- absolute host paths, because the file is read in the guest and may come from another
  computer (ADR 0021);
- anything the verifier must not see. The verifier sees the file's resolved report.

### 3.3 How the coding agent uses it

Before:

```
machine_create, machine_wait
machine_sync source, machine_sync data, machine_sync binary
machine_exec swift build
machine_exec nohup daemon ...; sleep 2; curl
machine_exec launch app; sleep 5; pgrep
(prose task describing what data is on screen)
```

After:

```
machine_create, machine_wait
machine_sync ~/work/greenroom
machine_scene {project: "~/work/greenroom", scene: "companion-stopped-runs"}
task: "Scene companion-stopped-runs. What changed: ... Checks: ..."
```

`machine_scene` blocks at most 50 s like `machine_exec` (ADR 0015). A longer build returns
`running: true` and a handle, and `machine_scene_wait` collects the result. When a probe
or check fails, the report names the section, the command and its output tail, and the
coder fixes the scene before posting a task. This turns F1 and F2 into a failed
`machine_scene`, which costs seconds, instead of a failed verification round, which cost
10.7 minutes.

The coder can still use `machine_exec` for anything the file does not cover. The scene
is the default path, not a cage.

### 3.4 How the verifier uses it

- **The scene report goes in the verifier's context** with the task. It gives the scene
  name, its `describe` text, the resolved launch line and environment names (values of
  secrets withheld), the TCC grants, the data digest, and each probe's and check's
  result, with their steps.
- **The verifier can cite scene steps as setup facts, not as evidence.** The ADR 0024
  rule that evidence steps must be the verifier's own stays. A check about what the
  screen shows still needs the verifier's own observation.
- **A new tool, `reset_scene`.** It rebuilds the scene's data, services and app from the
  start: kill the app, stop the services, copy fresh data, run the seeds, start the
  services, relaunch the app, re-run the probes and checks. This replaces the "do not
  relaunch" dilemma in the verifier's prompt for the cases F3 describes:
  - a check that consumes state (Continue);
  - a persistence check (the bench's `not_persisted` family);
  - a re-check after a dispute.

  A task can forbid it, as it can forbid a relaunch today.
- **A new ending, `scene_not_ready`.** It applies if a reset fails, or if a probe the
  verifier re-runs (a `probe_scene` read) no longer holds. The verifier then asks or
  reports inconclusive, and never verdicts the app. This extends ADR 0028's "a crash is
  evidence" line to its setup-side twin.
- **State probes.** A scene may declare read-only `probes`: commands that read state the
  screen does not show, such as `sqlite3 app.db 'select count(*) from items'`, `defaults
  read <domain>` or `curl .../api/runs/<id>`. This gives the verifier a declared, safe
  route for the "property checks through state, not pixels" recommendation in
  `docs/13-verifier-quality.md`, instead of free-form `machine_exec`.

**Independence.** The coder writes the scene, and the coder is the party whose claim is
being checked. A scene could hide a bug, for example by seeding only the data that
works. The mitigations:

- The file is checked in and reviewed with the code.
- The daemon, not the coder, runs the probes and checks and records them.
- The report says when `greenroom.json` or any file it references differs from the
  default branch. This needs the base branch's digest; see the open questions.
- The verifier is told to treat a scene changed in the same change as part of the claim.

### 3.5 Images, caches, sync and bench

- **Images.**
  - `toolchains` is matched against the toolchain manifest the image already reports
    (ADR 0019, 0026).
  - The Go and Node layers or volumes of #164 are keyed by the versions the file names.
  - The dialog gate (ADR 0018) is unchanged.
  - A scene never changes the image. It runs in a clone.
- **Build cache (#129).**
  - The file supplies what #129's design needs: the paths (`cache.paths`) and the key
    inputs (`cache.key`, hashed together with the image name and the toolchain
    versions).
  - The cache holds only `build` outputs, which run before any secret is injected.
    This is the Codespaces prebuild boundary.
- **Sync.**
  - The coder's `machine_sync` stays the only way source gets in, local or remote, so
    `machine_scene` needs no new transport.
  - Fixture data comes from the synced tree. This removes the extra syncs of
    hand-prepared host directories that F1 and F8 came from.
  - After copying data, the runner records a content digest (file count, bytes and
    sha256), so a truncated copy shows as a different digest from the fixture's.
- **Warm starts.** A ready scene could later be suspended and resumed. Decision 6 of
  `docs/09-image-strategy.md` ("no warm start until measured") still holds. Measure
  cold scene time first.
- **Bench (ADR 0025).**
  - `bench.Runner.setUp` is already a scene runner for one fixed shape: sync, patch,
    `build.sh`, `open` with a `pgrep` wait, and a disturbance.
  - Each `bench/apps/<app>/app.json` becomes a `greenroom.json` with a `build` of
    `./build.sh`, an `app` of the bundle, and `process` and `window` probes.
  - The runner then calls `internal/scene`. Every bench run exercises the scene runner,
    and `SetupSeconds` and `setup_error` become its regression numbers.
  - Patches stay a bench concept, applied before sync as today. The `infra`
    disturbances stay in the bench.
  - Bench cases gain scenes with seeded state (UserDefaults) for the `not_persisted`
    family.

### 3.6 Security

- **Secrets are never in the file.**
  - The file lists names: `"secrets": [{"name": "STRIPE_TEST_KEY", "for": ["services.backend"], "description": "..."}]`.
  - For a local daemon, values come from a daemon-side store: a per-project env file
    under the daemon's root, mode 600, or the Keychain later.
  - For a remote agent (ADR 0021), `greenroom connect` reads them from its own
    environment and sends them in the request body over the tunnel. The daemon keeps
    them in memory only.
  - A scene whose secret is missing fails in the `secrets` check by name, before anything
    starts.
- **Values never reach a recorded command line.** Steps record their command text in
  `steps.jsonl`. The runner writes a mode 600 env file in the guest, with the values on
  stdin (ADR 0023) and a recorded step that names the variables only. Services and the
  app read that file.
- **Scrubbing.** Every recorded output (steps, session reads and the conversation) has
  secret values replaced by `[secret NAME]`, matched exactly. The screen is not
  scrubbed. A scene that shows a secret on screen leaks it into frames, and the docs
  must say so.
- **The verifier never receives values.** It sees names and "present".
- **Data hygiene (F8).** The `data` loader refuses private key and env file patterns
  (`id_*`, `*.pem`, `*.p12`, `.env*`) unless the scene lists them in `allowSecretsFiles`,
  and records what it copied.
- **No host services in v1.** A guest that can reach a service on the host through the
  NAT gateway can reach whatever else listens there. Hosted and remote daemons (ADR 0021)
  have no agent host to reach anyway. Backends run in the guest, or are external
  services reached by URL with credentials from secrets.
- **Commands in the file run as the guest user**, like `machine_exec`. The file adds no
  privilege the coder did not already have.

### 3.7 Things macOS makes harder

- **No container services in the guest.**
  - Actions service containers need Linux.
  - Docker in a macOS guest would need nested virtualization, which this plan has not
    verified on these hosts. **UNVERIFIED; assume no.**
  - Postgres or Redis run natively in the guest (a toolchain volume or Homebrew
    bottles), or in a Linux sidecar VM in a later phase.
  - Apple's two-VM limit applies to macOS guests. Whether a Linux sidecar counts
    against the host's slots is an open question.
- **Launching GUI apps with env.**
  - Today's runs used three methods.
  - The runner will use `open` for bundles and a pty session for bare executables, which
    is what worked in 3057f9.
  - Whether `open --env` passes environment variables to the app on macOS 27 is
    **UNVERIFIED**. Test it in phase 0.
- **iOS later.** The `simctl` subcommands in `docs/12-ios-expansion.md` section 3.2 map
  onto scene sections:
  - `privacy grant` to `permissions`;
  - `SIMCTL_CHILD_*` and `launch` arguments to `app.env` and `app.args`;
  - `openurl`, `push` and `location` to `seed`;
  - `status_bar override` to `seed`;
  - `erase` to reset.

## 4. Phased plan

### Phase 0: the Companion scene as a checked-in script (no daemon code)

This is the smallest step that removes today's failures, and it commits to no API. It
also teaches which sections a real scene needs before a schema freezes them.

1. **`apps/companion/Fixtures/runs/`: two to four trimmed recorded runs.**
   - They cover the stop-limit pair, a verdict card run and a long transcript.
   - Each has distinct ids and titles, `conversation.jsonl`, `steps.jsonl`,
     `manifest.json` and only the screenshots and frames the Companion needs.
   - They are made once by a script (`Fixtures/make.sh <run id> ...`) that copies and
     trims a recording and never copies a daemon root's key files.
   - The snapshot harness moves to these fixtures, so its default run ids stop depending
     on one host's `~/.greenroom/runs`.
2. **`apps/companion/Fixtures/check.sh <addr> <count>`**, the data check. It asserts the
   daemon serves `count` runs, that their short ids are distinct, and that every run has
   at least one message and a readable conversation. It would have caught F1 and F2.
3. **`apps/companion/scripts/scene.sh up|reset|down <scene>`, run in the guest.**
   - It builds the daemon from the synced tree, installing Go 1.27 to `~/goroot` if it
     is missing (5.9 s measured in 3057f9) until #164 lands.
   - It builds the Companion.
   - It copies the scene's fixtures into a fresh root, starts `greenroom serve`, and
     waits for `/api/runs` with a timeout instead of a sleep.
   - It runs `check.sh`, launches the Companion, and waits for its window.
   - It prints one line per section with timings.
4. **The dogfood coordinator's notes** tell coders to use it: `machine_sync` the repo,
   then `machine_exec` `scene.sh up <scene>`.

What to measure over the next five Companion dogfood runs:

- setup wall;
- setup calls;
- setup-caused rounds;
- the time `reset` takes.

**Pass signal:** setup wall under 3 minutes cold with at most 3 setup calls, and no
round lost to data.

### Phase 1: `greenroom.json` and `machine_scene`

- Write `internal/scene` covering what phase 0's script needed: `toolchains` (check
  only), `build`, `data`, `services`, `app`, `ready` and `expect`.
- Build each section through `Manager.Exec` and sessions, recorded with a
  `by: "scene"` marker.
- Add the scene report in the run directory and in the verifier's context, plus the
  `machine_scene` and `machine_scene_wait` tools.
- Port the Companion scene to the file. Port one bench app and run the bench through
  `internal/scene`. `SetupSeconds` should stay within 10% of today's 34 to 38 s median,
  with no `setup_error`.
- Record the decision in an ADR.

### Phase 2: reset, secrets, permissions

- Add `machine_scene_reset` and the verifier's `reset_scene`, and teach the prompt when
  to use it. Measure how often re-checks after a dispute still need the coder.
- Add the `scene_not_ready` ending, counted in the bench as its own ending, as
  `setup_error` is.
- Add secrets: names in the file, the daemon-side store, the connect path and scrubbing,
  with tests that no recorded file contains a secret value.
- Add `permissions` through `machine_grant` (#131), recorded and shown to the verifier.

### Phase 3: caches and toolchains keyed by the file

- Add #129's cache, keyed by `cache.key` together with the image and toolchains. Measure
  cold and warm scene time on the Companion and on Vorssaint.
- Add #164's toolchain layers or volumes for the versions the file names.
- Flag a scene changed against the default branch in the report.

### Phase 4: heavier backends and iOS

- Native guest services for Postgres and Redis. Then a Linux sidecar VM if a real
  customer backend needs containers.
- The iOS scene sections of 3.7, with the iOS work.

### What to measure throughout

| Metric | Today | Phase 0 target | Phase 3 target |
| --- | --- | --- | --- |
| Setup wall, machine ready to task | 2 min 3 s to 11 min 14 s (hand-built); bench 34 to 38 s (scripted) | under 3 min cold | under 60 s warm |
| Coder setup calls | 5 to 10 | 2 or 3 | 2 (sync, scene) |
| Rounds lost to setup | 1 of 3 dogfood runs | 0 in 5 runs | 0 |
| Hand resets per run | up to 2 | 0 (script `reset`) | 0 (tool) |
| Scene failures by section | not recorded | printed by script | in the report, counted by the bench |

## 5. Worked example: Greenroom on Greenroom

**Goal:** verify a Companion change against recorded runs served by a daemon inside the
guest, with nothing live.

**Today (600cf88).** The coder:

1. cross-built `greenroom` on the host;
2. copied a recorded run twice and edited the copies by hand so both stopped at the time
   limit;
3. synced both into the guest and started the daemon with `nohup` and `sleep 2`;
4. built and launched the Companion with `launchctl asuser` and `sleep 5`;
5. wrote the data's description into the task by hand.

The copies came out truncated and indistinguishable. That cost one verification round,
one correction and two hand resets.

**With the scene (phase 1 form):**

```
machine_create {image: "greenroom-base-v7-r2"}
machine_wait
machine_sync {source: "/.../greenroom", exclude: [".git", "node_modules", ".build", ".serena"]}
machine_scene {project: "~/work/greenroom", scene: "companion-stopped-runs"}
```

The daemon then works through the sections:

1. **`toolchains`:** it reads `toolchain` and finds Xcode 27. Go 1.27 is missing, so it
   installs it (recorded) until #164.
2. **`build`:**
   - `daemon`: `go build` in the guest, with the Go build cache restored when #129 lands.
   - `companion`: `swift build`, with `.build` restored.
3. **`data`:**
   - The two fixture runs from `apps/companion/Fixtures/runs/` are copied into a fresh
     `$SCENE_DATA/root/runs/`.
   - The digest is recorded: file count, bytes and sha256 per run.
4. **`services.daemon`:**
   - `greenroom serve -root $SCENE_DATA/root -tart /usr/bin/false -verifier manual
     -addr 127.0.0.1:7777 -frame-interval 0` runs as a pty session.
   - The probe waits for `GET /api/runs` to answer 200, with a 20 s timeout.
5. **`expect`:** `Fixtures/check.sh 127.0.0.1:7777 2` checks for 2 runs, distinct short
   ids (b48b96 and c0471f), and more than 0 messages each. A truncated copy fails here,
   before any task.
6. **`app`:**
   - The Companion is launched with `GREENROOM_URL=http://127.0.0.1:7777`.
   - The `window` probe waits for a Companion window. An `ax` probe waits for a row
     titled `I added a "Longest word" line to WordCount`.
7. **The report**, to the coder and into the verifier's context, carries:
   - the `describe` text;
   - the grants (none);
   - the data digest;
   - the steps of each probe and check.

Then the task, shorter because the scene carries the facts:

> Scene companion-stopped-runs (see the scene report). What changed: a reply that stopped
> at its time limit shows as a card with a Continue button while the verifier waits.
> Checks: 1. Run B's card is headed "Stopped, out of time" ... You may reset the scene.

After the verifier clicks Continue on Run B, it calls `reset_scene` before re-checking,
and does not ask the coder. The same file serves the other dogfood scenes with different
fixtures and checks:

- `companion-verdict-card` for #153: one run with a refused-then-posted verdict;
- `companion-long-transcript` for #146: the 280-message run, plus a `seed` that starts the
  render load loop, and `toolchains` asking for a Python with Pillow.

It also serves the snapshot harness, which needs exactly this daemon with copied runs on
the host (`apps/companion/CLAUDE.md`).

## 6. Open questions for the maintainer

1. **Format and place.**
   - The options are `greenroom.json` at the root with strict JSON (no new dependency,
     consistent with `app.json` and the bench cases), YAML (comments, but a new
     dependency and YAML's pitfalls) or TOML.
   - The root or a `.greenroom/` folder? The daemon's own root is also `~/.greenroom`, so
     the folder name may confuse.
2. **Should `machine_scene` also sync?** One call is simpler for agents. Two keep the
   transport in one place (connect already handles `machine_sync`).
3. **Verifier reset by default?** `reset_scene` changes the machine. Allow it unless the
   task forbids it, or only when the task allows it?
4. **Scene changes in the change under test.** Flagging them needs the base branch's
   digest. Should the daemon compute it (it needs `.git` in the guest, which today's
   syncs exclude), or should the coder pass the base digest, or a GitHub check later?
5. **Fixture size in git.** One recorded run with frames is about 5 MB. Trim frames to a
   few, use Git LFS, or generate synthetic runs with a `greenroom fixture` command?
6. **Host services at all?** This plan says no for v1. Is there a local-only case worth
   an explicit opt-in, such as a backend already running on the developer's Mac?
7. **Where local secrets live:** an env file under the daemon root, or the Keychain from
   the start?
8. **Linux sidecar VMs for container backends:** do they count against the host's VM
   slots, and is Tart's Linux support worth the second VM kind before a customer asks for
   it?
9. **Bench migration timing.** Should the bench move onto `internal/scene` in phase 1, as
   the scene runner's test, or after phase 2, so bench results before and after stay
   comparable?
10. **Name.** "Scene" is new vocabulary. It needs a glossary entry (`/domain-modeling`)
    once it settles, and a check that it does not collide with Companion terms.
