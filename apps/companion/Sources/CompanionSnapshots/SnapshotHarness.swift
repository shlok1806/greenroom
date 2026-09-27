import AppKit
import SwiftUI
@testable import Companion

/// Renders the real window in every key state, light and dark, at several sizes, for
/// design review (companion ADR 0001). `GREENROOM_SNAPSHOTS` names the output directory;
/// it needs a daemon at `GREENROOM_URL` serving copied runs (never a VM). It is its own
/// SwiftPM executable, so it runs in the same appearance mode as the app, which Xcode's
/// test host does not.
/// `GREENROOM_SNAPSHOTS_EMPTY_URL` points at a daemon with no runs, for the welcome state.
/// `GREENROOM_SNAPSHOTS_REFUSED_URL` points at a daemon that refuses (HTTP 403), for that state.
/// `GREENROOM_SNAPSHOTS_ONLY` limits the run to scenario names containing it.
///
/// States the copied runs do not contain (live, booting, proposed, contested) are made
/// by editing the loaded records coherently: timestamps shifted to now, the machine
/// present, the lines that end a run removed.
///
/// Nothing appears on the person's screen or Dock: the test process never becomes a
/// regular app (`.prohibited`), and each shot is a real `NSWindow` hosting `RootView`
/// with its toolbar, placed far off every display and never made key; the picture is the
/// window's own drawing (`cacheDisplay` of its frame view), so no screen-recording
/// permission is needed. Being off screen and not key, windows draw inactive: selection
/// and prominent buttons are grey.
@MainActor
final class SnapshotHarness {
    private struct Size {
        var name: String
        var width: CGFloat
        var height: CGFloat
    }

    private static let large = Size(name: "L", width: 1440, height: 900)
    private static let medium = Size(name: "M", width: 1180, height: 760)
    private static let small = Size(name: "S", width: 900, height: 600)
    /// The default window on a 1024 x 768 screen (every greenroom guest): its visible frame.
    private static let guest = Size(name: "G", width: 1024, height: 660)
    /// Just wide enough for the sidebar, the least stage and the least conversation.
    private static let tight = Size(name: "T", width: 1040, height: 660)

    private struct Scenario {
        var name: String
        var sizes: [Size]
        var runId: String?
        var pane: StagePane = .screen
        var conversation = true
        var verdictExpanded = false
        /// Seconds into every signature moment to hold it at (ADR 0006), so one renders mid-way.
        var momentFreeze: TimeInterval?
        /// The screen has the keys while driving (the house lights), as a click on it gives them.
        var drivingFocused = false
        /// Edits the store once the run view has loaded it.
        var prepare: @MainActor (RunStore) async -> Void = { _ in }
        var baseURL: URL?
        /// Every request fails as if nothing listened, without touching any port.
        var unreachable = false
        /// Shows the sidebar by hand after the window folded it, as a person would.
        var showSidebar = false
        /// The width the person dragged the sidebar to.
        var sidebarWidth = RunLayout.sidebarIdeal
        /// Opens the Greenroom section on this state (root ADR 0033). Staged by hand: the
        /// harness never runs update.sh or reads a build.
        var builds: (@MainActor (Updates) -> Void)?
        /// Opens the top bar's More menu (companion ADR 0017) as its key does, with this row
        /// selected; Builds and Updates carries whatever `moreBuilds` stages.
        var more: ActionID?
        var moreBuilds: (@MainActor (Updates) -> Void)?
    }

    private var environment: [String: String] { ProcessInfo.processInfo.environment }

    func run() async throws {
        guard let output = environment["GREENROOM_SNAPSHOTS"], !output.isEmpty else {
            print("Set GREENROOM_SNAPSHOTS to an output directory to render screenshots.")
            return
        }
        let directory = URL(fileURLWithPath: output, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let base = URL(string: environment["GREENROOM_URL"] ?? "http://127.0.0.1:7851")!
        let only = environment["GREENROOM_SNAPSHOTS_ONLY"] ?? ""

        // `GREENROOM_SNAPSHOTS_THEMES` (comma-separated: light, dark, light-hc, dark-hc;
        // default light and dark) picks the themes each scenario renders in.
        let themes = (environment["GREENROOM_SNAPSHOTS_THEMES"] ?? "light,dark")
            .split(separator: ",").compactMap { ThemePreference(rawValue: String($0).trimmingCharacters(in: .whitespaces)) }
        for scenario in scenarios() where only.isEmpty || scenario.name.contains(only) {
            for theme in themes {
                for size in scenario.sizes {
                    let file = directory.appending(path: "\(scenario.name)-\(theme.rawValue)-\(size.name).png")
                    try await render(scenario, theme: theme, size: size, base: base, to: file)
                    print("rendered \(file.lastPathComponent)")
                }
            }
        }
    }

    // MARK: - Scenarios

    private static let passRun = "20260923-010827-9411d6139b560b5f"
    private static let twoVerdicts = "20260923-005718-21979199ff319fd2"
    private static let inputRun = "20260922-235130-bb2de75cafbb2114"
    private static let longRun = "20260919-222432-ea8dfe"
    /// A verdict citing two screenshot steps, failed steps with raw input and very long rows.
    private static let citedRun = "20260923-124520-61ea02f4836340aa"
    /// No task message, two system events.
    private static let noTaskRun = "20260923-101500-noTaskRun0000001"
    /// The signature moments' run (layer 6): TipSplit checked through the UI, with clicks.
    /// `GREENROOM_SNAPSHOTS_MOMENTS_RUN` names another.
    private var momentsRun: String {
        environment["GREENROOM_SNAPSHOTS_MOMENTS_RUN"] ?? "20260924-040039-3da76afa89136933"
    }

    /// The verdict-lands moment's fail: a TipSplit run the verifier failed, citing steps.
    /// `GREENROOM_SNAPSHOTS_FAIL_RUN` names another.
    private var failRun: String {
        environment["GREENROOM_SNAPSHOTS_FAIL_RUN"] ?? "20260923-044138-de31017819a86d84"
    }

    private func scenarios() -> [Scenario] {
        let all = [Self.large, Self.medium, Self.small]
        return [
            Scenario(name: "01-finished-agent-accepted", sizes: all, runId: Self.passRun),
            Scenario(name: "02-no-conversation", sizes: [Self.medium, Self.small], runId: Self.passRun, conversation: false),
            Scenario(name: "03-steps-failure", sizes: [Self.large, Self.medium], runId: Self.inputRun, pane: .steps) { store in
                store.requestSeek(runId: Self.inputRun, step: 16, inSteps: true)
            },
            Scenario(name: "04-long-transcript", sizes: [Self.large, Self.medium], runId: Self.longRun),
            Scenario(name: "05-verdict-proposed", sizes: [Self.large, Self.medium], runId: Self.twoVerdicts) { store in
                Self.makeProposed(store)
            },
            Scenario(name: "06-verdict-proposed-evidence", sizes: [Self.large], runId: Self.twoVerdicts, verdictExpanded: true) { store in
                Self.makeProposed(store)
            },
            Scenario(name: "07-verdict-contested", sizes: [Self.medium], runId: Self.twoVerdicts) { store in
                Self.makeContested(store)
            },
            Scenario(name: "08-live-verifier-working", sizes: [Self.large, Self.medium], runId: Self.inputRun) { store in
                Self.makeLive(store, runId: Self.inputRun, lastActivityAgo: 6)
                Self.appendHumanNote(store, runId: Self.inputRun, text: "Can you also check 15% with 4 people?", ago: 4)
            },
            Scenario(name: "09-live-idle", sizes: [Self.medium], runId: Self.inputRun) { store in
                Self.makeLive(store, runId: Self.inputRun, lastActivityAgo: 7 * 60)
            },
            Scenario(name: "10-driving", sizes: [Self.medium], runId: Self.inputRun) { store in
                // Taking control re-reads the transcript, so the edits go on after it.
                await store.pilot(for: Self.inputRun).take()
                try? await Task.sleep(for: .milliseconds(300))
                Self.makeLive(store, runId: Self.inputRun, lastActivityAgo: 5)
            },
            Scenario(name: "11-booting", sizes: [Self.medium], runId: Self.passRun) { store in
                Self.makeBooting(store, runId: Self.passRun)
            },
            Scenario(name: "12-machine-lost", sizes: [Self.medium], runId: Self.inputRun) { store in
                Self.makeLost(store, runId: Self.inputRun)
            },
            Scenario(name: "13-evidence-opened", sizes: [Self.medium], runId: Self.passRun) { store in
                store.requestSeek(runId: Self.passRun, step: 13, fromVerdict: true)
            },
            Scenario(name: "13b-evidence-step-record", sizes: [Self.medium], runId: Self.passRun, pane: .steps) { store in
                store.requestSeek(runId: Self.passRun, step: 13, fromVerdict: true, inSteps: true)
            },
            Scenario(name: "14-many-runs", sizes: [Self.large, Self.small], runId: Self.passRun) { store in
                Self.addManyRuns(store)
            },
            Scenario(name: "15-no-selection", sizes: [Self.medium]) { store in
                Self.makeProposed(store)
            },
            Scenario(name: "16-offline-with-runs", sizes: [Self.medium], runId: Self.passRun) { store in
                store.reachable = false
                store.connected = false
                store.lastError = "Could not connect to the server."
            },
            Scenario(name: "17-offline-nothing-loaded", sizes: [Self.medium], unreachable: true),
            Scenario(name: "18-welcome", sizes: [Self.medium],
                     baseURL: environment["GREENROOM_SNAPSHOTS_EMPTY_URL"].flatMap(URL.init(string:))),
            // A daemon that answers with an error, e.g. `http://localhost.:<port>`, whose
            // Host is not a loopback name.
            Scenario(name: "18b-daemon-refused", sizes: [Self.medium],
                     baseURL: environment["GREENROOM_SNAPSHOTS_REFUSED_URL"].flatMap(URL.init(string:))),
            // A daemon from before `RunSummary.task`, with a legacy run whose steps restart at 1.
            Scenario(name: "19-older-daemon-legacy-run", sizes: [Self.medium], runId: "20260919-170059-65804c",
                     baseURL: environment["GREENROOM_SNAPSHOTS_LEGACY_URL"].flatMap(URL.init(string:))),
            // A running run gets a proposed verdict while another run is open.
            Scenario(name: "20-verdict-on-a-running-run", sizes: [Self.medium], runId: Self.passRun) { store in
                if let index = store.runs.firstIndex(where: { $0.runId == Self.inputRun }) {
                    store.runs[index].status = .ready
                    store.runs[index].destroyedAt = nil
                    store.runs[index].createdAt = Date().addingTimeInterval(-90)
                    store.runs[index].lastActivity = Date().addingTimeInterval(-5)
                    store.runs[index].verdict = VerdictState(seq: 16, verdict: "fail", status: .proposed)
                }
            },
            // A 1024 x 768 screen (issues #52, #55, #64).
            Scenario(name: "21-guest-verdict", sizes: [Self.guest], runId: Self.citedRun),
            Scenario(name: "22-guest-verdict-evidence", sizes: [Self.guest], runId: Self.citedRun, verdictExpanded: true),
            Scenario(name: "23-guest-live-evidence-opened", sizes: [Self.guest, Self.tight], runId: Self.citedRun) { store in
                Self.makeLive(store, runId: Self.citedRun, lastActivityAgo: 8)
                store.requestSeek(runId: Self.citedRun, step: 26, fromVerdict: true)
            },
            Scenario(name: "24-guest-live-recent-steps", sizes: [Self.guest], runId: Self.citedRun) { store in
                Self.makeLive(store, runId: Self.citedRun, lastActivityAgo: 8)
            },
            // Following live from the start, so Recent steps holds the newest (and longest) rows.
            Scenario(name: "24b-guest-live-long-rows", sizes: [Self.guest], runId: Self.citedRun, pane: .steps) { store in
                Self.makeLive(store, runId: Self.citedRun, lastActivityAgo: 8)
                HarnessDefaults.set(StagePane.screen.rawValue, "stagePane")
            },
            Scenario(name: "25-guest-steps", sizes: [Self.guest], runId: Self.citedRun, pane: .steps),
            Scenario(name: "25b-guest-sidebar-by-hand", sizes: [Self.guest], runId: Self.citedRun, showSidebar: true),
            Scenario(name: "25c-small-sidebar-by-hand", sizes: [Self.small], runId: Self.citedRun, showSidebar: true),
            // The narrowest sidebar: every row still shows when it started beside its state (issue #99).
            Scenario(name: "25d-guest-narrowest-sidebar", sizes: [Self.guest], runId: Self.citedRun, showSidebar: true,
                     sidebarWidth: RunLayout.sidebarMinimum),
            Scenario(name: "26-guest-no-conversation", sizes: [Self.guest], runId: Self.citedRun, conversation: false),
            // A run with no task and only system events: the header and a short transcript.
            Scenario(name: "27-no-task-few-events", sizes: [Self.guest, Self.medium], runId: Self.noTaskRun),
        ] + momentScenarios() + buildsScenarios() + moreScenarios()
    }

    // MARK: - The More menu (companion ADR 0017)

    private func moreScenarios() -> [Scenario] {
        let live: @MainActor (RunStore) async -> Void = { store in
            Self.makeLive(store, runId: Self.passRun, lastActivityAgo: 8)
        }
        let old = "4c3b2a1"
        return [
            // Opened by its key on a live run: the first row selected, every section.
            Scenario(name: "55-more-live", sizes: [Self.large, Self.medium, Self.guest], runId: Self.passRun,
                     prepare: live, more: .capture),
            Scenario(name: "55b-more-updates", sizes: [Self.medium, Self.guest], runId: Self.passRun,
                     prepare: live, more: .greenroom, moreBuilds: { updates in
                         updates.stage(app: Self.stampedApp("1a2b3c4"), daemon: Self.stampedDaemon("1a2b3c4"),
                                       check: Self.checked(ahead: Self.incoming))
                     }),
            Scenario(name: "55c-more-rebuild", sizes: [Self.guest], runId: Self.passRun,
                     prepare: live, more: .exportRecording, moreBuilds: { updates in
                         updates.stage(app: Self.stampedApp(old), daemon: Self.stampedDaemon(old),
                                       check: Self.checked(ahead: []))
                     }),
            // Builds from different commits, and no check to say which is behind.
            Scenario(name: "55d-more-mismatch", sizes: [Self.guest], runId: Self.passRun,
                     prepare: live, more: .toggleConversation, moreBuilds: { updates in
                         updates.stage(app: Self.stampedApp(Self.buildMain), daemon: Self.stampedDaemon(old),
                                       check: .failed("could not reach origin"))
                     }),
            // Destroy's selection is the failure colour, never the brand.
            Scenario(name: "55e-more-destroy-selected", sizes: [Self.large, Self.guest], runId: Self.passRun,
                     prepare: live, more: .destroy),
            // A finished run: only what still applies. Narrow: the pane switch has the conversation.
            Scenario(name: "55f-more-finished", sizes: [Self.medium, Self.small], runId: Self.passRun, more: .greenroom),
        ]
    }

    // MARK: - Builds and updates (root ADR 0033)

    private static let buildMain = "9f8e7d6"
    private static let buildCheckout = "/Users/maintainer/projects/greenroom"
    private static let built = Date().addingTimeInterval(-26 * 3600)

    private static func stampedApp(_ commit: String) -> AppBuild {
        AppBuild(stamp: BuildStamp(commit: commit, builtAt: built), checkout: buildCheckout)
    }

    private static func stampedDaemon(_ commit: String, dirty: Bool = false) -> DaemonVersion {
        DaemonVersion(version: "0.0.2", commit: commit, dirty: dirty, builtAt: built.formatted(DaemonDate.plain),
                      inputHelper: 7, imageRecipe: 2, verifier: "nim", verifierModel: "nvidia/nemotron-3-super-120b-a12b",
                      visionModel: "meta/muse-glimmer-30b", checkout: buildCheckout)
    }

    private static let incoming: [UpdateCheck.Commit] = [
        .init(sha: "9f8e7d6", subject: "companion: builds and updates from the app (ADR 0033)"),
        .init(sha: "4c3b2a1", subject: "verifier: describer options per model, muse-glimmer ready (ADR 0030)"),
        .init(sha: "0d9e8f7", subject: "daemon: close the Login Items alert at image build time"),
    ]

    private static func checked(ahead: [UpdateCheck.Commit], ago: TimeInterval = 240) -> BuildsSummary.Check {
        .checked(UpdateCheck(main: buildMain, ahead: ahead.count, commits: ahead, refusal: nil), at: Date().addingTimeInterval(-ago))
    }

    private static func updateRun(_ lines: [String], status: Int32? = nil) -> UpdateRun {
        var run = UpdateRun()
        for line in lines { run.take(line) }
        if let status { run.finish(status: status) }
        return run
    }

    private static let updatingLines = [
        "step: check the checkout", "step: fetch origin", "step: fast-forward main (3 new commits)",
        "step: install the daemon", "env file: \(buildCheckout)/.env", "verifier: nim",
        "image: greenroom-lean-a (local image)", "commit: 9f8e7d6", "building /Users/maintainer/.greenroom/bin/greenroom",
    ]

    private func buildsScenarios() -> [Scenario] {
        let sizes = [Self.medium, Self.guest]
        let old = "4c3b2a1"
        return [
            Scenario(name: "47-builds-up-to-date", sizes: sizes, runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp(Self.buildMain), daemon: Self.stampedDaemon(Self.buildMain),
                              check: Self.checked(ahead: []))
            }),
            Scenario(name: "48-builds-updates-available", sizes: sizes, runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp("1a2b3c4"), daemon: Self.stampedDaemon("1a2b3c4"),
                              check: Self.checked(ahead: Self.incoming))
            }),
            // The stale daemon of 2026-09-26: the app is on main, the daemon a build behind.
            Scenario(name: "49-builds-mismatch", sizes: sizes, runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp(Self.buildMain), daemon: Self.stampedDaemon(old),
                              check: Self.checked(ahead: []))
            }),
            Scenario(name: "49b-builds-verifier-working", sizes: [Self.medium], runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp("1a2b3c4"), daemon: Self.stampedDaemon("1a2b3c4"),
                              check: Self.checked(ahead: Self.incoming), confirming: ["Check TipSplit splits the bill"])
            }),
            Scenario(name: "50-builds-updating", sizes: sizes, runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp("1a2b3c4"), daemon: Self.stampedDaemon("1a2b3c4"),
                              check: Self.checked(ahead: Self.incoming), run: Self.updateRun(Self.updatingLines))
            }),
            Scenario(name: "51-builds-failed", sizes: sizes, runId: Self.passRun, builds: { updates in
                updates.stage(app: Self.stampedApp("1a2b3c4"), daemon: Self.stampedDaemon("1a2b3c4"),
                              check: Self.checked(ahead: Self.incoming),
                              run: Self.updateRun(Self.updatingLines + [
                                  "# github.com/shlok1806/greenroom/apps/daemon/internal/verifier",
                                  "internal/verifier/describe.go:88:2: undefined: retryDescribe",
                                  "failed: install the daemon",
                              ], status: 1))
            }),
        ]
    }

    /// The signature moments (ADR 0006), each held part way through with `momentFreeze`.
    private func momentScenarios() -> [Scenario] {
        let runId = momentsRun
        return [
            // The machine booting: the loader over what the daemon has said so far.
            Scenario(name: "28-moment-boot-lines", sizes: [Self.medium], runId: runId) { store in
                Self.makeBooting(store, runId: runId)
            },
            // The machine came up while watched: its first picture resolves out of glyphs.
            Scenario(name: "29-moment-boot-reveal", sizes: [Self.medium], runId: runId, momentFreeze: 0.3) { store in
                let held = (store.messages[runId], store.steps[runId], store.frames[runId], store.details[runId])
                Self.makeBooting(store, runId: runId)
                try? await Task.sleep(for: .seconds(1))
                store.messages[runId] = held.0
                store.steps[runId] = held.1
                store.frames[runId] = held.2
                store.details[runId] = held.3
                Self.makeLive(store, runId: runId, lastActivityAgo: 4)
            },
            // Driving: everything but the screen dims, Give Back and the hint bar stay lit.
            Scenario(name: "30-moment-house-lights", sizes: [Self.medium, Self.guest], runId: runId, drivingFocused: true) { store in
                await store.pilot(for: runId).take()
                try? await Task.sleep(for: .milliseconds(300))
                Self.makeLive(store, runId: runId, lastActivityAgo: 5)
            },
            // Destroyed while watched: the picture dissolving into glyphs, then held still.
            Scenario(name: "31-moment-power-down", sizes: [Self.medium], runId: runId, momentFreeze: 0.3) { store in
                await Self.destroyWhileWatched(store, runId: runId)
            },
            Scenario(name: "32-moment-power-down-still", sizes: [Self.medium], runId: runId, momentFreeze: 1.0) { store in
                await Self.destroyWhileWatched(store, runId: runId)
            },
        ] + verdictLandsScenarios() + checklistScenarios() + limitStopScenarios() + finishScenarios()
            + twinScenarios() + selectionScenarios()
    }

    /// A run the coding agent finished with `run_finish` (root ADR 0034, companion ADR 0016):
    /// Done with its outcome in the header and the row, the summary and the ref under the
    /// status line. Each seeds the other two runs' rows too, so the list shows every word.
    private func finishScenarios() -> [Scenario] {
        let failRun = failRun
        let verified = RunFinish(
            outcome: .verified,
            summary: "Each pays now divides the total with tip by the number of people, rounded to the cent.",
            ref: RunRef(branch: "fix/each-pays-split", commit: "4f1c2a9e0b7d3c55a1e2f9087c6b4d3e2a1f0c9b",
                        pr: "https://github.com/shlok1806/greenroom/pull/171"))
        let unverified = RunFinish(
            outcome: .unverified,
            summary: "Pushed the rounding change without a passing verdict: the verifier failed Each pays at 25%.",
            ref: RunRef(branch: "fix/tip-rounding", pr: "#172"))
        let abandoned = RunFinish(
            outcome: .abandoned,
            summary: "Stopped: the bill field needs a design decision before it can take decimals.")
        let seeds = [(Self.passRun, verified), (failRun, unverified), (Self.inputRun, abandoned)]
        return [
            Scenario(name: "52-finished-verified", sizes: [Self.large, Self.guest], runId: Self.passRun) { store in
                for (runId, finish) in seeds { Self.makeFinished(store, runId: runId, finish) }
            },
            Scenario(name: "53-finished-unverified", sizes: [Self.medium, Self.guest], runId: failRun) { store in
                for (runId, finish) in seeds { Self.makeFinished(store, runId: runId, finish) }
            },
            Scenario(name: "54-finished-abandoned", sizes: [Self.medium, Self.small], runId: Self.inputRun) { store in
                for (runId, finish) in seeds { Self.makeFinished(store, runId: runId, finish) }
            },
        ]
    }

    /// A verifier turn that stopped at its time budget before a verdict (issue #127,
    /// companion ADR 0015), from a bench run exactly as recorded, whose machine is gone;
    /// then the same run made live, so the card offers Continue; after a Continue; and the
    /// same stop at the step cap. Copy `~/.greenroom/bench/runs` into the daemon's root.
    private func limitStopScenarios() -> [Scenario] {
        let runId = Self.stoppedRun
        return [
            Scenario(name: "46-real-stopped-out-of-time", sizes: [Self.large, Self.guest], runId: runId),
            Scenario(name: "46b-stopped-waiting", sizes: [Self.large, Self.medium, Self.guest], runId: runId) { store in
                Self.makeLive(store, runId: runId, lastActivityAgo: 40)
            },
            Scenario(name: "46c-stopped-continued", sizes: [Self.large], runId: runId) { store in
                Self.makeLive(store, runId: runId, lastActivityAgo: 4)
                Self.continueAfterStop(store, runId: runId)
            },
            Scenario(name: "46d-stopped-out-of-tool-calls", sizes: [Self.medium, Self.guest], runId: runId) { store in
                Self.makeLive(store, runId: runId, lastActivityAgo: 40)
                Self.restop(store, runId: runId, .steps,
                            "I used all 40 tool calls for this turn and did not finish. Send a task, or a note "
                                + "from a person, and I will continue from here. A coding agent's note does not start a turn.")
            },
        ]
    }

    /// The run as `run_finish` leaves it: the finish on its row and detail, and the system
    /// event that recorded it just before the machine's destroyed line.
    private static func makeFinished(_ store: RunStore, runId: String, _ finish: RunFinish) {
        var finish = finish
        let end = store.messages[runId]?.last { $0.text.hasPrefix("machine destroyed") }?.at
            ?? store.run(runId)?.destroyedAt ?? Date()
        finish.at = end.addingTimeInterval(-2)
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index].finish = finish }
        store.details[runId]?.finish = finish
        guard var messages = store.messages[runId] else { return }
        let insertAt = messages.lastIndex { $0.text.hasPrefix("machine destroyed") } ?? messages.count
        let seq = insertAt < messages.count ? messages[insertAt].seq : (messages.last?.seq ?? 0) + 1
        // The destroyed line (and anything after it, which nothing replies to) moves one on.
        for index in messages.indices where index >= insertAt { messages[index].seq += 1 }
        messages.insert(Message(seq: seq, at: finish.at ?? end, from: .system, kind: .event,
                                text: "run finished: \(finish.outcome.text). \(finish.summary)", finish: finish), at: insertAt)
        store.messages[runId] = messages
    }

    /// Runs that share a title (issue #157): five bench trials of one WordCount task, two
    /// started in the same minute, and three copies of one run started in the same second,
    /// two of them sharing the id's first six hex digits. Serve the bench runs and the
    /// copies (the PR says how they were made).
    private func twinScenarios() -> [Scenario] {
        let runId = "20260926-040010-37e61663c2b10bbb"
        return [
            Scenario(name: "56-twin-runs", sizes: [Self.large], runId: runId),
            // The narrowest runs column: a lone run's time gives way there, a twin's mark never.
            Scenario(name: "56b-twin-runs-narrow-column", sizes: [Self.large], runId: runId,
                     sidebarWidth: RunLayout.sidebarMinimum),
        ]
    }

    /// Issue #162: a person opens run A while it is live (pinned, selected), A's machine
    /// goes away (it moves to its day), and they open run B. Exactly one row may look
    /// selected: B's. Serve the WordCount bench trials.
    private func selectionScenarios() -> [Scenario] {
        let a = "20260926-054344-a002363c058e0127"
        let b = "20260926-040010-37e61663c2b10bbb"
        return [
            // The maintainer's case: A is open (selected) while live, its machine goes away
            // (it moves from Running to its day), then B is opened.
            Scenario(name: "57-open-another-run-after-one-ends", sizes: [Self.large], runId: a) { store in
                let accept = { Self.setVerdict(store, runId: a) { $0.status = .accepted; $0.acceptedBy = .human } }
                accept()
                Self.setLive(store, runId: a, true)
                try? await Task.sleep(for: .seconds(2))
                accept()
                Self.setLive(store, runId: a, false)
                store.selectedRunId = b
                try? await Task.sleep(for: .seconds(2))
            },
        ]
    }

    /// A run in the list (and its detail, if held) as live on a ready machine, or as
    /// ended, its machine destroyed now.
    private static func setLive(_ store: RunStore, runId: String, _ live: Bool) {
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) {
            store.runs[index].status = live ? .ready : .finished
            store.runs[index].destroyedAt = live ? nil : Date()
            store.runs[index].lastActivity = Date()
        }
        guard var detail = store.details[runId] else { return }
        detail.destroyedAt = live ? nil : Date()
        detail.machine = live
            ? Machine(runId: runId, name: detail.machineName, image: detail.image, ip: "192.168.64.12",
                      status: .ready, error: nil, bootSeconds: 11.4, createdAt: detail.createdAt, dir: "", control: nil)
            : nil
        store.details[runId] = detail
    }

    /// WordCount's "Longest word" task, whose verifier ran out of time (10 minutes).
    private static let stoppedRun = "20260926-071916-b48b96d157b71fcd"

    /// Your Continue, sent after the stop: the verifier then works again.
    private static func continueAfterStop(_ store: RunStore, runId: String) {
        guard var messages = store.messages[runId], let last = messages.last else { return }
        messages.append(Message(seq: last.seq + 1, at: last.at.addingTimeInterval(3), from: .human, kind: .note,
                                text: LimitStop.continueText))
        store.messages[runId] = messages
    }

    /// The run's stopped reply, as if it had stopped at another limit.
    private static func restop(_ store: RunStore, runId: String, _ reason: StopReason, _ text: String) {
        store.messages[runId] = store.messages[runId]?.map { message in
            guard message.stop != nil else { return message }
            var message = message
            message.stop = reason
            message.text = text
            return message
        }
    }

    /// A verdict as a checklist and the verifier's declared plan (root ADR 0024), seeded
    /// onto the TipSplit fail run: 4 checks with a fail and one not checked, 1 check, 12
    /// long ones, and the plan in the transcript while the verifier works.
    private func checklistScenarios() -> [Scenario] {
        let failRun = failRun
        let passRun = Self.passRun
        return [
            Scenario(name: "37-checks-verdict", sizes: [Self.large, Self.guest, Self.small], runId: failRun) { store in
                Self.seedChecks(store, runId: failRun, Self.fourChecks, proposed: true)
            },
            Scenario(name: "37b-checks-verdict-evidence", sizes: [Self.large], runId: failRun, verdictExpanded: true) { store in
                Self.seedChecks(store, runId: failRun, Self.fourChecks, proposed: true)
            },
            Scenario(name: "38-checks-one", sizes: [Self.medium, Self.guest], runId: passRun) { store in
                Self.seedChecks(store, runId: passRun, [
                    AcceptanceCheck(id: "each-pays", criterion: "Each pays shows the split amount", status: .pass,
                                    evidence: [13], actions: [11], observed: "Each pays read $48.00 after entering 3 people"),
                ], proposed: false)
            },
            Scenario(name: "39-checks-twelve", sizes: [Self.large, Self.guest], runId: failRun) { store in
                Self.seedChecks(store, runId: failRun, Self.twelveChecks, proposed: true)
            },
            Scenario(name: "40-checks-plan-live", sizes: [Self.medium, Self.guest], runId: failRun) { store in
                Self.seedChecks(store, runId: failRun, Self.twelveChecks, proposed: true)
                Self.cutAfterPlan(store, runId: failRun, calls: 3)
                Self.makeLive(store, runId: failRun, lastActivityAgo: 4)
            },
            Scenario(name: "40b-checks-plan-short", sizes: [Self.medium], runId: failRun) { store in
                Self.seedChecks(store, runId: failRun, Array(Self.fourChecks.prefix(3)), proposed: true)
                Self.cutAfterPlan(store, runId: failRun, calls: 2)
                Self.makeLive(store, runId: failRun, lastActivityAgo: 4)
            },
        ] + realChecklistScenarios()
    }

    /// Verdicts exactly as the verifier bench recorded them (root ADR 0024, 0025, 0027),
    /// nothing seeded: each is open for review as it was left. Copy the bench's runs into
    /// the daemon's root to render them.
    private func realChecklistScenarios() -> [Scenario] {
        let all = [Self.large, Self.medium, Self.guest]
        return [
            // 4 checks, 2 failed: TodoList's Clear done button.
            Scenario(name: "41-real-four-checks-fail", sizes: all, runId: "20260926-051843-ec112051c6d7e64d"),
            // 8 checks, all passed.
            Scenario(name: "42-real-eight-checks-pass", sizes: [Self.large, Self.guest], runId: "20260926-061121-2f86d768db46ed22"),
            // Inconclusive: 1 passed, 3 not checked.
            Scenario(name: "43-real-inconclusive", sizes: [Self.large, Self.guest], runId: "20260926-050758-921d99b9003d7b59"),
            // ADR 0027: value and visual checks, the UI read marks the answer not drawn.
            Scenario(name: "44-real-visual-not-drawn", sizes: all, runId: "20260926-034206-4cf03f48a9538400"),
            // ADR 0027: a timing check ("at once").
            Scenario(name: "45-real-timing", sizes: [Self.large, Self.guest], runId: "20260926-034346-216ce610b45da6f2"),
        ]
    }

    private static let fourChecks = [
        AcceptanceCheck(id: "tip-20", criterion: "Tip shows $24.00 for a $120 bill at 20%", status: .pass,
                        evidence: [11, 12], actions: [6, 7, 8, 9], observed: "Tip read $24.00 after setting the bill to 120 and choosing 20%."),
        AcceptanceCheck(id: "each-pays-3", criterion: "Each pays shows $48.00 with 3 people", status: .fail,
                        evidence: [11, 12], actions: [10], observed: "Each pays read $8.00, not $48.00."),
        AcceptanceCheck(id: "tip-25", criterion: "Choosing 25% changes Tip to $30.00", status: .pass,
                        evidence: [16, 17], actions: [15], observed: "Tip read $30.00 after the second click on 25%."),
        AcceptanceCheck(id: "each-pays-25", criterion: "Each pays becomes $50.00 at 25%", status: .unchecked),
    ]

    private static let twelveChecks: [AcceptanceCheck] = {
        let criteria: [(String, AcceptanceCheck.Status, [Int], String?)] = [
            ("The app window titled TipSplit is frontmost and shows the Bill, Tip and People controls", .pass, [5], "The UI read lists TipSplit frontmost with Bill, Tip and People."),
            ("Entering 120 in the Bill field replaces the previous amount instead of appending to it", .pass, [11], "Bill read 120 after Command-A and typing 120."),
            ("Tip shows $24.00 for a $120 bill at 20%", .pass, [11, 12], "Tip read $24.00."),
            ("Each pays shows $48.00 with 3 people", .fail, [11, 12], "Each pays read $8.00, not $48.00."),
            ("Choosing 25% changes Tip to $30.00", .pass, [16, 17], "Tip read $30.00 after the second click on 25%."),
            ("Each pays becomes $50.00 at 25%", .fail, [16, 17], "Each pays read $10.00, not $50.00."),
            ("The 25% segment shows as selected after one click, without needing a second click to take effect", .fail, [14], "After the first click the 20% segment still read selected."),
            ("The People stepper cannot go below 1 when its minus arrow is clicked repeatedly from 1", .unchecked, [], nil),
            ("The Bill field accepts decimal amounts such as 84.50 and keeps two decimal places in every total", .unchecked, [], nil),
            ("Clearing the Bill field shows $0.00 in Tip and Each pays rather than an error or a blank", .unchecked, [], nil),
            ("Totals update without pressing Return", .pass, [11], "Tip and Each pays changed as soon as 120 was typed."),
            ("Each pays rounds half a cent up", .unchecked, [], nil),
        ]
        return criteria.enumerated().map { index, item in
            AcceptanceCheck(id: "check-\(index + 1)", criterion: item.0, status: item.1, evidence: item.2,
                            actions: [], observed: item.3)
        }
    }()

    /// The verdict answers `checks`, and the verifier declared them right after the task:
    /// the plan goes in after the first task, every later message moves up one seq, and
    /// the verdict follows its message. `proposed` reopens the verdict for review.
    private static func seedChecks(_ store: RunStore, runId: String, _ checks: [AcceptanceCheck], proposed: Bool) {
        guard var messages = store.messages[runId],
              let task = messages.first(where: { $0.kind == .task }),
              let oldSeq = store.verdict(runId)?.seq else { return }
        let shift = { (seq: Int) in seq > task.seq ? seq + 1 : seq }
        messages = messages.map { message in
            var message = message
            message.seq = shift(message.seq)
            message.replyTo = message.replyTo.map(shift)
            if message.kind == .verdict, message.seq == shift(oldSeq) { message.checks = checks }
            return message
        }
        let declared = checks.map { AcceptanceCheck(id: $0.id, criterion: $0.criterion) }
        let plan = Message(seq: task.seq + 1, at: task.at.addingTimeInterval(4), from: .verifier, kind: .progress,
                           text: "declare_checks {}\n\(checks.count) checks declared", checks: declared)
        messages.insert(plan, at: (messages.firstIndex { $0.seq > task.seq + 1 }) ?? messages.endIndex)
        if proposed { messages.removeAll { $0.kind == .accept && $0.replyTo == shift(oldSeq) } }
        store.messages[runId] = messages
        setVerdict(store, runId: runId) {
            $0.seq = shift(oldSeq)
            if proposed {
                $0.status = .proposed
                $0.acceptedBy = nil
            }
        }
    }

    /// The run while the verifier works: the transcript up to its plan and `calls` tool
    /// calls after it, no verdict yet.
    private static func cutAfterPlan(_ store: RunStore, runId: String, calls: Int) {
        guard let messages = store.messages[runId],
              let plan = messages.first(where: { CheckPlan.isPlan($0) }) else { return }
        store.messages[runId] = messages.filter { $0.seq <= plan.seq + calls }
        store.details[runId]?.verdict = VerdictState()
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index].verdict = nil }
    }

    /// A verdict arriving while its run is open (ADR 0006, Verdict lands): mid-decode, with
    /// the border part drawn, and after it, for a pass and a fail. A fail moves the run to
    /// the step it cites first (the fail run has no step that errored).
    private func verdictLandsScenarios() -> [Scenario] {
        let passRun = momentsRun
        let failRun = failRun
        return [
            Scenario(name: "33-moment-verdict-lands-pass", sizes: [Self.large, Self.medium], runId: passRun, momentFreeze: 0.14) { store in
                await Self.landVerdict(store, runId: passRun)
            },
            Scenario(name: "34-moment-verdict-landed-pass", sizes: [Self.large, Self.medium], runId: passRun, momentFreeze: 1.0) { store in
                await Self.landVerdict(store, runId: passRun)
            },
            Scenario(name: "35-moment-verdict-lands-fail", sizes: [Self.large, Self.medium], runId: failRun, momentFreeze: 0.14) { store in
                await Self.landVerdict(store, runId: failRun)
            },
            Scenario(name: "36-moment-verdict-landed-fail", sizes: [Self.large, Self.medium], runId: failRun, momentFreeze: 1.0) { store in
                await Self.landVerdict(store, runId: failRun)
            },
        ]
    }

    // MARK: - Coherent edits

    /// The pass verdict before anyone accepted it: the accept is removed, so the
    /// transcript and the state agree.
    private static func makeProposed(_ store: RunStore) {
        let runId = twoVerdicts
        guard var messages = store.messages[runId],
              let verdict = messages.last(where: { $0.kind == .verdict }) else { return }
        messages.removeAll { $0.kind == .accept && $0.replyTo == verdict.seq }
        store.messages[runId] = messages
        setVerdict(store, runId: runId) {
            $0.status = .proposed
            $0.acceptedBy = nil
        }
    }

    /// The first (fail) verdict, disputed twice by the coding agent, so only a person
    /// can close it. Everything after the second dispute is dropped.
    private static func makeContested(_ store: RunStore) {
        let runId = twoVerdicts
        guard let held = store.messages[runId],
              let fail = held.first(where: { $0.kind == .verdict && $0.verdict == "fail" }) else { return }
        var messages = held.filter { $0.seq <= fail.seq }
        let reasons = [
            "The screenshot was taken before the rebuild finished; Each pays should read $48.00 now.",
            "I rebuilt again and relaunched. Please re-check: Each pays is $48.00.",
        ]
        for (offset, reason) in reasons.enumerated() {
            let seq = (messages.last?.seq ?? 0) + 1
            messages.append(Message(seq: seq, at: fail.at.addingTimeInterval(Double(40 + offset * 60)), from: .coder,
                                    kind: .dispute, text: reason, replyTo: fail.seq))
        }
        store.messages[runId] = messages
        let state = VerdictState(seq: fail.seq, verdict: "fail", summary: fail.text, evidence: fail.evidence,
                                 status: .contested, disputes: 2)
        store.details[runId]?.verdict = state
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index].verdict = state }
    }

    /// The run live and waiting on the verifier, then its verdict coming in through the
    /// event stream, proposed: the path a real verdict takes (`RunStore.apply`, then the
    /// run re-read), so the run view sees it land.
    private static func landVerdict(_ store: RunStore, runId: String) async {
        guard let verdict = store.verdict(runId), let seq = verdict.seq,
              let message = store.messages[runId]?.first(where: { $0.seq == seq }) else { return }
        store.messages[runId] = store.messages[runId]?.filter { $0.seq < seq }
        store.details[runId]?.verdict = VerdictState()
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index].verdict = nil }
        makeLive(store, runId: runId, lastActivityAgo: 3)
        try? await Task.sleep(for: .milliseconds(600))
        guard let live = store.messages[runId] else { return }
        var arrived = message
        arrived.at = (live.last?.at ?? Date()).addingTimeInterval(2)
        store.apply(.message(runId: runId, message: arrived))
        var proposed = verdict
        proposed.status = .proposed
        proposed.acceptedBy = nil
        store.details[runId]?.verdict = proposed
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index].verdict = proposed }
    }

    private static func setVerdict(_ store: RunStore, runId: String, _ change: (inout VerdictState) -> Void) {
        if var state = store.details[runId]?.verdict {
            change(&state)
            store.details[runId]?.verdict = state
        }
        if let index = store.runs.firstIndex(where: { $0.runId == runId }), var state = store.runs[index].verdict {
            change(&state)
            store.runs[index].verdict = state
        }
    }

    /// A finished run as it was while alive: every time shifted so the last activity is
    /// `lastActivityAgo` seconds before now, the destroy step and lines removed, the
    /// machine ready. The live stream stays connecting (`SilentScreen`).
    private static func makeLive(_ store: RunStore, runId: String, lastActivityAgo: TimeInterval) {
        guard var messages = store.messages[runId], var steps = store.steps[runId] else { return }
        messages.removeAll { $0.from == .system && ($0.text.contains("destroyed") || $0.text.contains("stopped")) }
        steps.removeAll { $0.tool == "machine_destroy" }
        let last = max(messages.last?.at ?? .distantPast, steps.last?.at ?? .distantPast)
        let shift = Date().addingTimeInterval(-lastActivityAgo).timeIntervalSince(last)
        store.messages[runId] = messages.map { var m = $0; m.at += shift; return m }
        store.steps[runId] = steps.map { var s = $0; s.at += shift; return s }
        // The recorder keeps capturing an idle machine, so frames run up to now.
        let now = Date()
        store.frames[runId] = (store.frames[runId] ?? []).map { var f = $0; f.at += shift; return f }.filter { $0.at <= now }
        guard var detail = store.details[runId] else { return }
        detail.createdAt += shift
        detail.destroyedAt = nil
        detail.machine = Machine(
            runId: runId, name: detail.machineName, image: detail.image, ip: detail.ip ?? "192.168.64.12",
            status: .ready, error: nil, bootSeconds: 11.4, createdAt: detail.createdAt, dir: "", control: nil
        )
        store.details[runId] = detail
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) {
            store.runs[index].status = .ready
            store.runs[index].destroyedAt = nil
            store.runs[index].createdAt = detail.createdAt
            store.runs[index].lastActivity = Date().addingTimeInterval(-lastActivityAgo)
        }
    }

    /// A live run whose machine goes away while its screen shows: the run as it was
    /// while alive, then as the daemon has it, finished.
    private static func destroyWhileWatched(_ store: RunStore, runId: String) async {
        let held = (store.messages[runId], store.steps[runId], store.frames[runId], store.details[runId],
                    store.runs.first { $0.runId == runId })
        makeLive(store, runId: runId, lastActivityAgo: 5)
        try? await Task.sleep(for: .seconds(1.5))
        store.messages[runId] = held.0
        store.steps[runId] = held.1
        store.frames[runId] = held.2
        store.details[runId] = held.3
        if let run = held.4, let index = store.runs.firstIndex(where: { $0.runId == runId }) { store.runs[index] = run }
    }

    /// A run 20 seconds old: one create step, no messages, no frames, no verdict.
    private static func makeBooting(_ store: RunStore, runId: String) {
        let created = Date().addingTimeInterval(-20)
        store.messages[runId] = []
        store.frames[runId] = []
        store.steps[runId] = (store.steps[runId] ?? []).prefix(1).map { var s = $0; s.at = created; return s }
        guard var detail = store.details[runId] else { return }
        detail.createdAt = created
        detail.destroyedAt = nil
        detail.verdict = VerdictState()
        detail.machine = Machine(
            runId: runId, name: detail.machineName, image: detail.image, ip: nil,
            status: .booting, error: nil, bootSeconds: nil, createdAt: created, dir: "", control: nil,
            boot: [
                BootPhase(phase: .clone, at: created, seconds: 0.1, detail: detail.image),
                BootPhase(phase: .start, at: created.addingTimeInterval(0.1), seconds: 0, detail: detail.machineName),
                BootPhase(phase: .agent, at: created.addingTimeInterval(0.2), seconds: 14.6),
                BootPhase(phase: .ip, at: created.addingTimeInterval(14.8), seconds: 0.2, detail: "192.168.64.12"),
                BootPhase(phase: .key, at: created.addingTimeInterval(15), seconds: 0.4),
                BootPhase(phase: .settings, at: created.addingTimeInterval(15.4)),
            ]
        )
        store.details[runId] = detail
        if let index = store.runs.firstIndex(where: { $0.runId == runId }) {
            store.runs[index].status = .booting
            store.runs[index].createdAt = created
            store.runs[index].lastActivity = created
            store.runs[index].destroyedAt = nil
            store.runs[index].verdict = nil
            store.runs[index].steps = 1
            store.runs[index].messages = 0
        }
    }

    /// The run's VM went away under it: the daemon's "machine stopped" line replaces the
    /// destroy lines.
    private static func makeLost(_ store: RunStore, runId: String) {
        guard var messages = store.messages[runId] else { return }
        let end = messages.last { $0.text.contains("destroyed") }?.at ?? Date()
        messages.removeAll { $0.from == .system && $0.text.contains("destroyed") }
        let seq = (messages.last?.seq ?? 0) + 1
        messages.append(Message(seq: seq, at: end, from: .system, kind: .event,
                                text: "machine stopped: its VM exited"))
        store.messages[runId] = messages
    }

    private static func appendHumanNote(_ store: RunStore, runId: String, text: String, ago: TimeInterval) {
        var held = store.messages[runId] ?? []
        let seq = (held.last?.seq ?? 0) + 1
        held.append(Message(seq: seq, at: Date().addingTimeInterval(-ago), from: .human, kind: .note, text: text))
        store.messages[runId] = held
    }

    /// Forty rows across a week with every lifecycle and verdict state, built from the
    /// real rows. Live rows have recent activity; finished rows have none after their end.
    private static func addManyRuns(_ store: RunStore) {
        let templates = store.runs
        guard !templates.isEmpty else { return }
        let verdicts: [VerdictState?] = [
            VerdictState(verdict: "pass", status: .accepted, acceptedBy: .human),
            VerdictState(verdict: "fail", status: .accepted, acceptedBy: .coder),
            nil,
            VerdictState(verdict: "pass", status: .accepted, acceptedBy: .coder),
            VerdictState(verdict: "fail", status: .rejected),
            VerdictState(verdict: "inconclusive", status: .accepted, acceptedBy: .human),
        ]
        var extra: [RunSummary] = []
        for index in 0..<40 {
            var run = templates[index % templates.count]
            run.runId = String(format: "20260922-%06d-%06x", 100_000 + index, 0xA1B2C3 + index * 977)
            run.createdAt = Date().addingTimeInterval(-Double(index) * 4 * 3600 - 900)
            run.steps = 5 + (index * 37) % 400
            run.messages = 3 + (index * 13) % 120
            switch index {
            case 0:
                run.status = .ready
                run.lastActivity = Date().addingTimeInterval(-12)
                run.verdict = nil
            case 1:
                run.status = .booting
                run.createdAt = Date().addingTimeInterval(-40)
                run.lastActivity = run.createdAt
                run.verdict = nil
            case 2:
                run.status = .ready
                run.lastActivity = Date().addingTimeInterval(-11 * 60)
                run.verdict = nil
            case 3:
                run.status = .finished
                run.lastActivity = run.createdAt.addingTimeInterval(900)
                run.verdict = VerdictState(verdict: "pass", status: .proposed)
            case 4:
                run.status = .finished
                run.lastActivity = run.createdAt.addingTimeInterval(900)
                run.verdict = VerdictState(verdict: "fail", status: .contested, disputes: 2)
            default:
                run.status = .finished
                run.destroyedAt = run.createdAt.addingTimeInterval(1200)
                run.lastActivity = run.createdAt.addingTimeInterval(1200)
                run.verdict = verdicts[index % verdicts.count]
            }
            // Distinct briefs, as a week of real work would have.
            let subjects = ["Settings sheet saves the theme", "Onboarding skips on second launch", "Export as PDF keeps margins",
                            "Search finds accented names", "Dark mode icons in the toolbar", "Undo after paste in the editor",
                            "Crash on empty project list", "Login keeps the session after relaunch"]
            let areas = ["", " in the menu bar app", " on a fresh account", " after an update", " with VoiceOver on"]
            run.task = "\(subjects[index % subjects.count])\(areas[(index / subjects.count) % areas.count]): build the branch, run it and check it as a user would."
            extra.append(run)
        }
        store.runs = (store.runs + extra).sorted { $0.createdAt > $1.createdAt }
    }

    // MARK: - Rendering

    private func render(_ scenario: Scenario, theme: ThemePreference, size: Size, base: URL, to file: URL) async throws {
        HarnessDefaults.set(theme.rawValue, ThemePreference.key)
        let appearance: NSAppearance.Name = theme.colorScheme == .dark ? .darkAqua : .aqua
        HarnessDefaults.set(scenario.pane.rawValue, "stagePane")
        HarnessDefaults.set(scenario.conversation, "showsConversation")
        HarnessDefaults.set(false, "stepsErrorsOnly")
        HarnessDefaults.set(scenario.sidebarWidth, "sidebarWidth")
        HarnessDefaults.set(scenario.runId ?? "none", "selectedRunId")

        let client: DaemonClient
        if scenario.unreachable {
            let config = URLSessionConfiguration.ephemeral
            config.protocolClasses = [RefusingURLProtocol.self]
            client = DaemonClient(baseURL: DaemonClient.defaultBaseURL, session: URLSession(configuration: config))
        } else {
            client = DaemonClient(baseURL: scenario.baseURL ?? base)
        }
        let store = RunStore(client: client, controlClient: GrantingControlClient(), screenSource: SilentScreen())
        await store.resync()
        store.connected = store.reachable == true
        // Chosen before the window, so the list does not first pick the newest run.
        store.selectedRunId = scenario.runId

        let window = OffscreenWindow(
            contentRect: NSRect(x: OffscreenWindow.origin.x, y: OffscreenWindow.origin.y, width: size.width, height: size.height),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        // The app's window has no title bar (`.hiddenTitleBar`); a harness window is made by
        // hand, and the first one of a run drew its title before the chrome hid it.
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.appearance = NSAppearance(named: appearance)
        window.title = "Greenroom Companion"
        let keyboard = KeyboardModel(store: store)
        let host = NSHostingController(rootView: RootView(store: store, keyboard: keyboard)
            .environment(\.momentFreeze, scenario.momentFreeze))
        host.sceneBridgingOptions = [.toolbars, .title]
        window.contentViewController = host
        window.setContentSize(NSSize(width: size.width, height: size.height))
        window.setFrameOrigin(OffscreenWindow.origin)
        // Ordered in so SwiftUI lays out and draws, but on no display and never key.
        window.orderFrontRegardless()

        // The run view reads the run when it appears; edits go on top, once.
        try await Task.sleep(for: .seconds(1.5))
        await scenario.prepare(store)
        if scenario.drivingFocused { keyboard.responder = .guest }
        if let builds = scenario.builds {
            builds(store.updates)
            // Opened by hand: `perform(.greenroom)` would read the build and check again.
            keyboard.greenroomOpen = true
        }
        if let row = scenario.more {
            scenario.moreBuilds?(store.updates)
            keyboard.perform(.more)
            keyboard.moreSelection = row
        }
        if scenario.verdictExpanded, let runId = scenario.runId {
            store.updateVerdictDraft(runId) { $0.expanded = true }
        }
        // A narrow window folds its sidebar on first layout; ask for the size again.
        window.setContentSize(NSSize(width: size.width, height: size.height))
        if scenario.showSidebar {
            try await Task.sleep(for: .milliseconds(500))
            NotificationCenter.default.post(name: RootView.showSidebarNotification, object: nil)
            try await Task.sleep(for: .milliseconds(500))
        }
        // Frames, artifacts and the conversation load over HTTP after the first layout.
        try await Task.sleep(for: .seconds(scenario.runId == Self.longRun ? 2.5 : 1.8))

        // A window that grew past the size asked for is a layout bug (issue #64).
        if window.frame.width > size.width + 1 || window.frame.height > size.height + 1 {
            print("window grew: asked \(Int(size.width)) x \(Int(size.height)), got \(Int(window.frame.width)) x \(Int(window.frame.height))")
        }
        try snapshot(window: window, to: file)
        await store.releaseAllControl()
        window.orderOut(nil)
        window.close()
        try await Task.sleep(for: .milliseconds(200))
    }

    private func snapshot(window: NSWindow, to file: URL) throws {
        guard let frameView = window.contentView?.superview else { return }
        let bounds = frameView.bounds
        guard let rep = frameView.bitmapImageRepForCachingDisplay(in: bounds) else { return }
        // Drawn in the window's appearance, which `cacheDisplay` does not pick up by itself.
        (window.appearance ?? NSAppearance.currentDrawing()).performAsCurrentDrawingAppearance {
            frameView.cacheDisplay(in: bounds, to: rep)
        }
        guard let png = rep.representation(using: .png, properties: [:]) else { return }
        try png.write(to: file)
    }
}

/// Settings for this process only. The harness shares `UserDefaults.standard` with every
/// other harness process (one executable, one domain): two runs at once read each other's
/// theme and pane, and drew light scenarios in dark and panes the scenario never opened.
/// The argument domain outranks the saved one, lives only in this process and is never
/// written to disk.
enum HarnessDefaults {
    static func set(_ value: Any, _ key: String) {
        let defaults = UserDefaults.standard
        var domain = defaults.volatileDomain(forName: UserDefaults.argumentDomain)
        domain[key] = value
        defaults.setVolatileDomain(domain, forName: UserDefaults.argumentDomain)
    }
}

/// A window that stays where it is put: AppKit would otherwise pull a titled window back
/// onto a display when it is ordered in.
private final class OffscreenWindow: NSWindow {
    static let origin = NSPoint(x: -30_000, y: -30_000)

    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

/// Lends the screen without a daemon, so the driving state can be rendered.
private struct GrantingControlClient: ControlClient {
    func takeControl(runId: String) async throws -> ControlResponse {
        ControlResponse(
            control: ControlLease(holder: "human", since: Date(), expires: Date().addingTimeInterval(60), actions: 0),
            screen: GuestScreen(width: 1024, height: 768)
        )
    }

    func renewControl(runId: String) async throws -> ControlResponse { try await takeControl(runId: runId) }

    func releaseControl(runId: String) async throws {}

    func input(runId: String, actions: [InputAction]) async throws -> InputResult {
        InputResult(actions: actions.count, screen: GuestScreen(width: 1024, height: 768))
    }
}

/// A live screen that never sends a picture, so a dressed-up live run shows its recording
/// with "Connecting" rather than the daemon refusing a machine that does not exist.
private struct SilentScreen: ScreenSource {
    func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> {
        AsyncThrowingStream { _ in }
    }
}

/// Fails every request the way a closed port does.
final class RefusingURLProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
    }
    override func stopLoading() {}
}
