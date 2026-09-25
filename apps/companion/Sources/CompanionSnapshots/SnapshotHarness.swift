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
        /// Edits the store once the run view has loaded it.
        var prepare: @MainActor (RunStore) async -> Void = { _ in }
        var baseURL: URL?
        /// Every request fails as if nothing listened, without touching any port.
        var unreachable = false
        /// Shows the sidebar by hand after the window folded it, as a person would.
        var showSidebar = false
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

        for scenario in scenarios() where only.isEmpty || scenario.name.contains(only) {
            for appearance in [NSAppearance.Name.aqua, .darkAqua] {
                for size in scenario.sizes {
                    let mode = appearance == .aqua ? "light" : "dark"
                    let file = directory.appending(path: "\(scenario.name)-\(mode)-\(size.name).png")
                    try await render(scenario, appearance: appearance, size: size, base: base, to: file)
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
                UserDefaults.standard.set(StagePane.screen.rawValue, forKey: "stagePane")
            },
            Scenario(name: "25-guest-steps", sizes: [Self.guest], runId: Self.citedRun, pane: .steps),
            Scenario(name: "25b-guest-sidebar-by-hand", sizes: [Self.guest], runId: Self.citedRun, showSidebar: true),
            Scenario(name: "25c-small-sidebar-by-hand", sizes: [Self.small], runId: Self.citedRun, showSidebar: true),
            Scenario(name: "26-guest-no-conversation", sizes: [Self.guest], runId: Self.citedRun, conversation: false),
            // A run with no task and only system events: the header and a short transcript.
            Scenario(name: "27-no-task-few-events", sizes: [Self.guest, Self.medium], runId: Self.noTaskRun),
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
            status: .booting, error: nil, bootSeconds: nil, createdAt: created, dir: "", control: nil
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

    private func render(_ scenario: Scenario, appearance: NSAppearance.Name, size: Size, base: URL, to file: URL) async throws {
        let defaults = UserDefaults.standard
        defaults.set(scenario.pane.rawValue, forKey: "stagePane")
        defaults.set(scenario.conversation, forKey: "showsConversation")
        defaults.set(false, forKey: "stepsErrorsOnly")
        defaults.set(scenario.runId ?? "none", forKey: "selectedRunId")

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
        window.appearance = NSAppearance(named: appearance)
        window.title = "Greenroom Companion"
        let host = NSHostingController(rootView: RootView(store: store))
        host.sceneBridgingOptions = [.toolbars, .title]
        window.contentViewController = host
        window.setContentSize(NSSize(width: size.width, height: size.height))
        window.setFrameOrigin(OffscreenWindow.origin)
        // Ordered in so SwiftUI lays out and draws, but on no display and never key.
        window.orderFrontRegardless()

        // The run view reads the run when it appears; edits go on top, once.
        try await Task.sleep(for: .seconds(1.5))
        await scenario.prepare(store)
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
