import AppKit
import Observation
import SwiftUI

// PROTOTYPE: one observable model holds every toggle, scene and the playback clock.

enum Pane: String, CaseIterable, Sendable {
    case runs, screen, steps, transcript

    var title: String {
        switch self {
        case .runs: "runs"
        case .screen: "screen"
        case .steps: "steps"
        case .transcript: "transcript"
        }
    }
}

enum LayoutMode: String, Sendable {
    case wide, medium, narrow
}

enum VerdictKind: Sendable { case pass, fail }

enum Review: Sendable, Equatable { case none, accepted, disputed }

struct Ripple: Sendable {
    var point: CGPoint
    var start: Double
    var driving: Bool
}

/// What the agent is doing at a playback instant.
enum Activity: Equatable, Sendable {
    case idle
    case typing(message: Int, chars: Int)
    case running(step: Int)
    case thinking(since: Double, who: Sender)
}

@Observable @MainActor
final class PrototypeModel {
    // MARK: toggles
    var theme: ThemeID = .dark
    var palette: ThemePalette { .named(theme) }
    var followAgent = true
    var simulateReduceMotion = false
    var systemReduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
    var reduceMotion: Bool { simulateReduceMotion || systemReduceMotion }
    var showStrip = true
    var clickMarks = true
    var forcedLayout: LayoutMode?

    // MARK: clock
    /// Seconds since launch; ticks at 30 Hz.
    var now: Double = 0
    private let launch = Date()
    private var tickTask: Task<Void, Never>?

    // MARK: playback
    var playT: Double = 0
    var playing = false

    // MARK: focus and selection
    var focus: Pane = .transcript
    var narrowPane: Pane = .transcript
    var composerActive = false
    var composer = ""
    var paletteOpen = false
    var paletteQuery = ""
    var paletteIndex = 0
    var helpOpen = false
    var zoomed: Pane?
    var pendingG = false
    var selectedRun = Synthetic.runID
    var runCursor = Synthetic.runID
    var selectedStep: Int?
    var selectedMessage: Int?
    var expandedSteps: Set<Int> = []
    var expandedMessages: Set<Int> = []
    var transcriptBack = 0
    var stepsBack = 0
    var runsScroll = 0
    var questionOption = 0
    var layoutMode: LayoutMode = .wide

    // MARK: scenes
    var driving = false
    var drivingSince: Double = 0
    var drivenKeys = ""
    var bootStart: Double?
    var powerDownStart: Double?
    var welcomeStart: Double?
    var verdictForced: VerdictKind?
    var verdictLandedAt: Double?
    var review: Review = .none
    var undoUntil: Double?
    var confirmDestroy = false
    var ripples: [Ripple] = []
    var flashAt: Double?
    var toast: (text: String, until: Double)?
    var appearAt: Double = 0
    var runStateChangedAt: [String: Double] = [:]

    // MARK: machine screen
    var screenImage: CGImage?
    var screenImageVersion = 0
    private var renderedScreen: ScreenState?
    var pointer = CGPoint(x: 0.62, y: 0.82)
    @ObservationIgnored var lumaKey = ""
    @ObservationIgnored var lumaValue: LumaGrid?
    var playAfterBoot = false

    init() {
        applyLaunchScene()
    }

    func start() {
        guard tickTask == nil else { return }
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.systemReduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
            }
        }
        refreshScreen()
        tickTask = Task { @MainActor [weak self] in
            var last = Date()
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(33))
                guard let self else { return }
                let d = Date()
                self.tick(dt: d.timeIntervalSince(last))
                last = d
            }
        }
    }

    // MARK: tick

    private func tick(dt: Double) {
        now = Date().timeIntervalSince(launch)
        if playing {
            let before = playT
            playT = min(Synthetic.length, playT + dt)
            crossed(from: before, to: playT)
            if playT >= Synthetic.length { playing = false }
        }
        ripples.removeAll { now - $0.start > 0.9 }
        if let u = undoUntil, now > u { undoUntil = nil }
        if let t = toast, now > t.until { toast = nil }
        if let b = bootStart, now - b > Boot.total + 0.2 {
            bootStart = nil
            if playAfterBoot { playAfterBoot = false; playRun(fromStart: true) }
        }
        refreshScreen()
    }

    /// Events that fire when playback passes them.
    private func crossed(from a: Double, to b: Double) {
        for step in Synthetic.steps {
            let done = step.t + step.play
            if let click = step.click, done > a, done <= b, clickMarks, !reduceMotion {
                ripples.append(Ripple(point: click, start: now, driving: false))
            }
        }
        if verdictForced == nil, Synthetic.verdictAt > a, Synthetic.verdictAt <= b {
            landVerdict()
        }
        if Synthetic.questionAt > a, Synthetic.questionAt <= b {
            runStateChangedAt[Synthetic.runID] = now
        }
        if Synthetic.answerAt > a, Synthetic.answerAt <= b {
            runStateChangedAt[Synthetic.runID] = now
        }
    }

    // MARK: derived playback state

    var isScripted: Bool { selectedRun == Synthetic.runID }

    var visibleMessages: [ProtoMessage] {
        guard isScripted else {
            return Synthetic.runs.first { $0.id == selectedRun }.map(Synthetic.stubMessages) ?? []
        }
        return Synthetic.messages.filter { $0.t <= playT && $0.kind != .verdict || $0.kind == .verdict && verdict != nil }
    }

    var visibleSteps: [ProtoStep] {
        guard isScripted else { return [] }
        return Synthetic.steps.filter { $0.t <= playT }
    }

    func stepRunning(_ s: ProtoStep) -> Bool { isScripted && playT < s.t + s.play && playT >= s.t }

    /// Streaming rate for a message, in characters per playback second.
    static func rate(_ m: ProtoMessage) -> Double {
        switch m.from {
        case .verifier: 150
        case .coder: 120
        default: 400
        }
    }

    /// How many characters of a message have arrived (nil = all of it).
    func typed(_ m: ProtoMessage) -> Int? {
        guard isScripted, m.kind != .progress, m.from != .human, m.from != .system else { return nil }
        let n = Int((playT - m.t) * Self.rate(m))
        return n >= m.text.count ? nil : max(0, n)
    }

    var activity: Activity {
        guard isScripted, playT > 0.01, !machineDestroyed else { return .idle }
        if let m = visibleMessages.last(where: { typed($0) != nil }), let n = typed(m) {
            return .typing(message: m.id, chars: n)
        }
        if let s = visibleSteps.last(where: stepRunning) { return .running(step: s.id) }
        if playT >= Synthetic.length - 0.05 || verdict != nil { return .idle }
        if questionOpen { return .idle }
        let lastEvent = max(
            visibleMessages.last.map { $0.t + Double($0.text.count) / Self.rate($0) } ?? 0,
            visibleSteps.last.map { $0.t + $0.play } ?? 0
        )
        let who: Sender = playT < 6.6 ? .coder : .verifier
        return .thinking(since: lastEvent, who: who)
    }

    var questionOpen: Bool {
        isScripted && playT >= Synthetic.questionAt && playT < Synthetic.answerAt
    }

    /// The human's words being typed into the composer by the script.
    var scriptedComposer: String? {
        guard isScripted, !composerActive else { return nil }
        for m in Synthetic.messages where m.from == .human {
            let lead = min(2.2, Double(m.text.count) / 30)
            if playT >= m.t - lead, playT < m.t {
                let n = Int(Double(m.text.count) * (playT - (m.t - lead)) / lead)
                return String(m.text.prefix(n))
            }
        }
        return nil
    }

    var verdict: VerdictKind? {
        if let f = verdictForced { return f }
        return isScripted && playT >= Synthetic.verdictAt ? .fail : nil
    }

    var machineDestroyed: Bool { powerDownStart != nil }

    func runState(_ run: ProtoRun) -> RunState {
        guard run.id == Synthetic.runID else { return run.state }
        if machineDestroyed && verdict == nil { return .ended }
        if let v = verdict { return v == .pass ? .pass : .fail }
        if questionOpen { return .needsYou }
        if bootStart != nil { return .booting }
        return .live
    }

    // MARK: screen state

    var screenState: ScreenState {
        var s = ScreenState()
        guard isScripted else { return s }
        let t: Double
        if !playing, focus == .steps, let sel = selectedStep, let step = Synthetic.steps.first(where: { $0.id == sel }) {
            t = step.t + step.play
        } else {
            t = playT
        }
        for step in Synthetic.steps where step.t + step.play <= t + 0.0001 {
            if let p = step.patch { s.apply(p) }
        }
        if let running = Synthetic.steps.first(where: { t >= $0.t && t < $0.t + $0.play }), running.tool == "machine_type",
           let p = running.patch, let full = p.bill {
            let n = Int(Double(full.count) * (t - running.t) / running.play)
            s.bill = String(full.prefix(n))
        }
        return s
    }

    func refreshScreen() {
        var s = screenState
        if !driving { pointer = s.pointer }
        s.pointer = .zero
        guard s != renderedScreen else { return }
        renderedScreen = s
        screenImage = FakeDesktopRenderer.render(s, clock: Synthetic.clock(playT))
        screenImageVersion += 1
    }

    // MARK: layout

    func resolveLayout(cols: Int) -> LayoutMode {
        if let f = forcedLayout { return f }
        if cols >= 180 { return .wide }
        if cols >= 118 { return .medium }
        return .narrow
    }

    var visiblePanes: [Pane] {
        switch layoutMode {
        case .wide: Pane.allCases
        case .medium: [.runs, .screen, .steps, .transcript]
        case .narrow: [.runs, .screen, .steps, .transcript]
        }
    }

    // MARK: scenes

    func landVerdict(_ kind: VerdictKind? = nil) {
        if let kind { verdictForced = kind }
        verdictLandedAt = now
        review = .none
        runStateChangedAt[Synthetic.runID] = now
        if verdict == .fail {
            focus = .steps
            // the cursor glides to the first failing evidence step
            let target = Synthetic.evidence.first { id in Synthetic.steps.first { $0.id == id }?.failed == true } ?? Synthetic.evidence[0]
            selectedStep = target
            stepsBack = 0
        }
    }

    func startBoot() {
        selectedRun = Synthetic.runID
        powerDownStart = nil
        bootStart = reduceMotion ? nil : now
        playAfterBoot = true
        playT = 0
        playing = false
        verdictForced = nil
        verdictLandedAt = nil
        review = .none
    }

    func playRun(fromStart: Bool) {
        selectedRun = Synthetic.runID
        if fromStart || playT >= Synthetic.length {
            playT = 0
            verdictForced = nil
            verdictLandedAt = nil
            review = .none
            powerDownStart = nil
            expandedSteps = []
            selectedStep = nil
            transcriptBack = 0
            stepsBack = 0
        }
        playing = true
    }

    func togglePlay() {
        if playing { playing = false } else { playRun(fromStart: false) }
    }

    func takeControl() {
        guard !machineDestroyed, isScripted else { return }
        driving = true
        drivingSince = now
        drivenKeys = ""
        composerActive = false
        paletteOpen = false
        helpOpen = false
    }

    func giveBack() {
        driving = false
    }

    func powerDown() {
        driving = false
        confirmDestroy = false
        powerDownStart = reduceMotion ? now - 10 : now
        playing = false
        runStateChangedAt[Synthetic.runID] = now
    }

    func restoreMachine() {
        powerDownStart = nil
    }

    func showToast(_ text: String, seconds: Double = 2.4) {
        toast = (text, now + seconds)
    }

    func accept() {
        guard verdict != nil, review == .none else { return }
        review = .accepted
        undoUntil = now + 5
    }

    func dispute() {
        guard verdict != nil, review == .none else { return }
        review = .disputed
        undoUntil = now + 5
        composerActive = true
        composer = "I dispute this: "
    }

    func undo() {
        guard undoUntil != nil else { return }
        review = .none
        undoUntil = nil
        if composer == "I dispute this: " { composer = ""; composerActive = false }
    }

    func capture() {
        guard isScripted, !machineDestroyed else { return }
        flashAt = now
        showToast("screenshot captured · step \(visibleSteps.count + 1)")
    }

    func sendComposer() {
        let text = composer.trimmingCharacters(in: .whitespacesAndNewlines)
        composer = ""
        composerActive = false
        guard !text.isEmpty else { return }
        showToast("sent to the verifier · \"\(text.prefix(40))\"")
    }

    // MARK: launch scene (for screenshots)

    private func applyLaunchScene() {
        let env = ProcessInfo.processInfo.environment
        if let t = env["PROTO_THEME"], let id = ThemeID(rawValue: t) { theme = id }
        if env["PROTO_NOSTRIP"] != nil { showStrip = false }
        if let l = env["PROTO_LAYOUT"], let mode = LayoutMode(rawValue: l) { forcedLayout = mode }
        if let t = env["PROTO_T"], let v = Double(t) {
            playT = v
            if v >= Synthetic.verdictAt { verdictLandedAt = -10 }
        }
        switch env["PROTO_SCENE"] ?? "" {
        case "play": playing = true
        case "palette": paletteOpen = true
        case "help": helpOpen = true
        case "drive": driving = true
        case "welcome": welcomeStart = 0
        case "boot": bootStart = 0.3
        case "powerdown": powerDownStart = 0.2
        case "pass": verdictForced = .pass; verdictLandedAt = 0.2
        case "fail": playT = max(playT, Synthetic.verdictAt); landVerdict(); verdictLandedAt = 0.2
        case "composer": composerActive = true; composer = "Can you also try a bill of 0?"
        case "", "boot+play":
            if env["PROTO_T"] == nil { bootStart = 0.4; playAfterBoot = true }
        default: break
        }
    }
}

/// Boot reveal timeline (seconds from the start of the scene).
enum Boot {
    static let connecting = 1.3
    static let terminalEnd = 4.1
    static let glyphsEnd = 4.5
    static let total = 5.1

    struct Line { var at: Double; var label: String; var detail: String; var result: String; var resultAt: Double }

    static let lines: [Line] = [
        Line(at: 1.30, label: "clone", detail: "macos-15-xcode → tipsplit-vm", result: "0.07s", resultAt: 1.45),
        Line(at: 1.55, label: "boot", detail: "tart run tipsplit-vm --no-graphics", result: "28.9s", resultAt: 3.05),
        Line(at: 3.15, label: "ssh", detail: "admin@192.168.64.7", result: "1.6s", resultAt: 3.55),
        Line(at: 3.65, label: "ready", detail: "screen 1024×768 · control free", result: "31.4s", resultAt: 3.75),
    ]
}
