import Foundation

// PROTOTYPE: synthetic, in-memory data only. Modelled on a real run: a coding agent built
// "TipSplit", and the verifier sets Bill 120, Tip 20 %, People 3 and checks the totals.

enum Sender: String, Sendable {
    case system, coder, verifier, human

    var name: String {
        switch self {
        case .system: "system"
        case .coder: "coder"
        case .verifier: "verifier"
        case .human: "you"
        }
    }

    var voice: Voice {
        switch self {
        case .system: .chrome
        case .coder: .coder
        case .verifier: .verifier
        case .human: .human
        }
    }
}

enum Kind: String, Sendable {
    case task, progress, reply, question, answer, note, verdict, event, dispute
}

struct ProtoMessage: Identifiable, Sendable {
    var id: Int
    /// Playback time the message starts (seconds into the ~60 s script).
    var t: Double
    var from: Sender
    var kind: Kind
    var text: String
    var step: Int?
}

/// What a step does to the fake machine screen once it finishes.
struct ScreenPatch: Sendable {
    var appOpen: Bool?
    var terminal: [String]?
    var bill: String?
    var tip: Int?
    var people: Int?
    var focus: TipField??
    var pointer: CGPoint?
}

struct ProtoStep: Identifiable, Sendable {
    var id: Int
    var by: Sender
    var tool: String
    var input: String
    var durationMs: Int
    var t: Double
    var play: Double
    var error: String?
    var stdout: String?
    var exitCode: Int?
    var click: CGPoint?
    var patch: ScreenPatch?

    var failed: Bool { error != nil || (exitCode ?? 0) != 0 }

    var durationLabel: String {
        durationMs >= 1000 ? String(format: "%.1fs", Double(durationMs) / 1000) : "\(durationMs)ms"
    }
}

enum RunState: String, Sendable {
    case live, booting, needsYou, pass, fail, ended

    var word: String {
        switch self {
        case .live: "live"
        case .booting: "booting"
        case .needsYou: "needs you"
        case .pass: "pass"
        case .fail: "fail"
        case .ended: "ended"
        }
    }

    var glyph: String {
        switch self {
        case .live: "⠿"
        case .booting: "◌"
        case .needsYou: "?"
        case .pass: "✓"
        case .fail: "✗"
        case .ended: "·"
        }
    }

    var ink: Ink {
        switch self {
        case .live: .role(.live)
        case .booting: .dim
        case .needsYou: .role(.needsYou)
        case .pass: .role(.pass)
        case .fail: .role(.failure)
        case .ended: .dim
        }
    }
}

enum Day: String, Sendable { case today, yesterday }

struct ProtoRun: Identifiable, Sendable {
    var id: String
    var title: String
    var task: String
    var state: RunState
    var detail: String
    var day: Day
    var started: String
    var steps: Int
    var messages: Int
}

enum TipField: Sendable { case bill }

// MARK: - The fake machine's app

/// Positions on the 1024 x 768 guest display. Clicks in the script use these, so the
/// click marks land on the controls they hit.
enum TipSplitLayout {
    static let screen = CGSize(width: 1024, height: 768)
    static let window = CGRect(x: 318, y: 176, width: 400, height: 372)
    static let billField = CGRect(x: 438, y: 244, width: 250, height: 30)
    static let segments = CGRect(x: 438, y: 300, width: 250, height: 28)
    static let tips = [15, 18, 20, 25]
    static let stepper = CGRect(x: 628, y: 356, width: 60, height: 28)
    static let terminal = CGRect(x: 70, y: 96, width: 520, height: 300)

    static func frac(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x / screen.width, y: p.y / screen.height) }
    static var billCenter: CGPoint { frac(CGPoint(x: billField.midX, y: billField.midY)) }
    static func tipCenter(_ tip: Int) -> CGPoint {
        let i = CGFloat(tips.firstIndex(of: tip) ?? 0)
        let w = segments.width / CGFloat(tips.count)
        return frac(CGPoint(x: segments.minX + w * (i + 0.5), y: segments.midY))
    }
    static var minus: CGPoint { frac(CGPoint(x: stepper.minX + stepper.width * 0.25, y: stepper.midY)) }
    static var plus: CGPoint { frac(CGPoint(x: stepper.minX + stepper.width * 0.75, y: stepper.midY)) }
}

struct ScreenState: Equatable, Sendable {
    var appOpen = false
    var terminal: [String] = ["admin@tipsplit-vm ~ % "]
    var bill = ""
    var tip = 15
    var people = 1
    var focus: TipField?
    var pointer = CGPoint(x: 0.62, y: 0.82)

    var billValue: Double { Double(bill) ?? 0 }
    var tipAmount: Double { billValue * Double(tip) / 100 }
    /// The bug under test: TipSplit divides only the tip.
    var eachPays: Double { tipAmount / Double(max(people, 1)) }

    mutating func apply(_ p: ScreenPatch) {
        if let v = p.appOpen { appOpen = v }
        if let v = p.terminal { terminal = v }
        if let v = p.bill { bill = v }
        if let v = p.tip { tip = v }
        if let v = p.people { people = v }
        if let v = p.focus { focus = v }
        if let v = p.pointer { pointer = v }
    }
}

// MARK: - The script

enum Synthetic {
    static let runID = "tipsplit"
    static let length: Double = 62
    /// Wall clock of playback time 0, and how many real seconds one playback second stands for.
    static let clockStart = 14 * 3600 + 20 * 60 + 4
    static let clockScale = 6.0

    static func clock(_ t: Double) -> String {
        let s = clockStart + Int(t * clockScale)
        return String(format: "%02d:%02d", s / 3600, (s / 60) % 60)
    }

    static let buildOut = """
    Building for debugging...
    [1/6] Compiling TipSplit TipModel.swift
    [2/6] Compiling TipSplit ContentView.swift
    [3/6] Compiling TipSplit TipSplitApp.swift
    [4/6] Emitting module TipSplit
    [5/6] Linking TipSplit
    Build complete! (38.19s)
    """

    static let testOut = """
    Test Suite 'TipModelTests' started
    Test Case 'testTipAmount' passed (0.002 seconds)
    Test Case 'testPerPersonSplitsTotal' failed
      XCTAssertEqual failed: ("8.0") is not equal to ("48.0")
    Executed 2 tests, with 1 failure in 0.004 seconds
    """

    static let steps: [ProtoStep] = {
        let L = TipSplitLayout.self
        var s: [ProtoStep] = []
        func add(_ by: Sender, _ tool: String, _ input: String, _ ms: Int, _ t: Double, _ play: Double = 0.5,
                 error: String? = nil, stdout: String? = nil, exit: Int? = nil, click: CGPoint? = nil, patch: ScreenPatch? = nil) {
            s.append(ProtoStep(id: s.count + 1, by: by, tool: tool, input: input, durationMs: ms, t: t, play: play,
                               error: error, stdout: stdout, exitCode: exit, click: click, patch: patch))
        }
        func xy(_ p: CGPoint) -> String { String(format: "{\"x\":%.2f,\"y\":%.2f}", p.x, p.y) }
        let prompt = "admin@tipsplit-vm TipSplit % "
        add(.coder, "machine_exec", "{\"cmd\":\"sw_vers\"}", 310, 1.0, 0.6,
            stdout: "ProductName:    macOS\nProductVersion: 15.6\nBuildVersion:   24G84", exit: 0,
            patch: ScreenPatch(terminal: ["admin@tipsplit-vm ~ % sw_vers", "ProductName:    macOS", "ProductVersion: 15.6", "BuildVersion:   24G84", "admin@tipsplit-vm ~ % cd ~/work/TipSplit"]))
        add(.coder, "machine_exec", "{\"cmd\":\"swift build\"}", 38_190, 2.0, 3.0, stdout: buildOut, exit: 0,
            patch: ScreenPatch(terminal: [prompt + "swift build"] + buildOut.components(separatedBy: "\n") + [prompt]))
        add(.coder, "machine_exec", "{\"cmd\":\"scripts/bundle.sh && open build/TipSplit.app\"}", 2_140, 5.3, 0.8,
            stdout: "bundled build/TipSplit.app (ad-hoc signed)", exit: 0,
            patch: ScreenPatch(appOpen: true, terminal: [prompt + "swift build"] + buildOut.components(separatedBy: "\n") + [prompt + "scripts/bundle.sh && open build/TipSplit.app", "bundled build/TipSplit.app (ad-hoc signed)", prompt]))
        add(.verifier, "machine_screenshot", "{}", 420, 11.0)
        add(.verifier, "machine_click", xy(L.billCenter), 90, 12.0, click: L.billCenter, patch: ScreenPatch(focus: .some(.bill), pointer: L.billCenter))
        add(.verifier, "machine_type", "{\"text\":\"120\"}", 240, 12.8, 0.6, patch: ScreenPatch(bill: "120"))
        add(.verifier, "machine_key", "{\"key\":\"tab\"}", 60, 13.8, 0.3, patch: ScreenPatch(focus: .some(nil)))
        add(.verifier, "machine_screenshot", "{}", 410, 14.4)
        add(.verifier, "machine_click", xy(L.tipCenter(20)), 80, 15.3, click: L.tipCenter(20), patch: ScreenPatch(tip: 20, pointer: L.tipCenter(20)))
        add(.verifier, "machine_screenshot", "{}", 400, 16.1)
        add(.verifier, "machine_click", xy(L.plus), 70, 16.9, click: L.plus, patch: ScreenPatch(people: 2, pointer: L.plus))
        add(.verifier, "machine_click", xy(L.plus), 70, 17.6, click: L.plus, patch: ScreenPatch(people: 3, pointer: L.plus))
        add(.verifier, "machine_screenshot", "{}", 430, 18.4)
        add(.verifier, "machine_exec",
            "{\"cmd\":\"osascript -e 'tell app \\\"System Events\\\" to get value of static text 4 of window 1 of process \\\"TipSplit\\\"'\"}",
            1_310, 24.5, 0.9,
            error: "System Events got an error: osascript is not allowed assistive access. (-1719)")
        add(.verifier, "machine_screenshot", "{\"region\":[0.30,0.52,0.42,0.20]}", 380, 31.0)
        add(.verifier, "machine_exec", "{\"cmd\":\"grep -n -A2 perPerson Sources/TipSplit/TipModel.swift\"}", 120, 32.0, 0.6,
            stdout: "14:    var perPerson: Double {\n15:        tipAmount / Double(people)\n16:    }", exit: 0)
        add(.verifier, "machine_exec", "{\"cmd\":\"swift test --filter TipModelTests\"}", 9_140, 34.2, 2.2, stdout: testOut, exit: 1)
        add(.verifier, "machine_click", xy(L.tipCenter(25)), 80, 42.5, click: L.tipCenter(25), patch: ScreenPatch(tip: 25, pointer: L.tipCenter(25)))
        add(.verifier, "machine_screenshot", "{}", 400, 43.2)
        add(.verifier, "machine_click", xy(L.tipCenter(20)), 80, 44.0, click: L.tipCenter(20), patch: ScreenPatch(tip: 20, pointer: L.tipCenter(20)))
        add(.verifier, "machine_click", xy(L.minus), 70, 44.7, click: L.minus, patch: ScreenPatch(people: 2, pointer: L.minus))
        add(.verifier, "machine_screenshot", "{}", 410, 45.3)
        add(.verifier, "machine_click", xy(L.plus), 70, 46.0, click: L.plus, patch: ScreenPatch(people: 3, pointer: L.plus))
        add(.verifier, "machine_screenshot", "{}", 420, 46.7)
        add(.verifier, "machine_click", xy(L.billCenter), 90, 47.5, click: L.billCenter, patch: ScreenPatch(focus: .some(.bill), pointer: L.billCenter))
        add(.verifier, "machine_key", "{\"key\":\"a\",\"mods\":[\"cmd\"]}", 60, 48.1, 0.3)
        add(.verifier, "machine_type", "{\"text\":\"60\"}", 180, 48.7, 0.5, patch: ScreenPatch(bill: "60"))
        add(.verifier, "machine_screenshot", "{}", 400, 49.4)
        add(.verifier, "machine_key", "{\"key\":\"a\",\"mods\":[\"cmd\"]}", 60, 54.2, 0.3)
        add(.verifier, "machine_type", "{\"text\":\"120\"}", 240, 54.8, 0.6, patch: ScreenPatch(bill: "120", focus: .some(nil)))
        add(.verifier, "machine_screenshot", "{}", 440, 55.8)
        return s
    }()

    static let p1 = """
    **Readback after entering the values.** The window shows Bill `120.00`, Tip `20%` and People `3`. The totals read:

    - Tip: **$24.00**, which matches 120 × 0.20
    - Each pays: **$8.00**, which does not. (120 + 24) ÷ 3 is $48.00

    $8.00 is exactly the tip divided by three, so the split looks like it ignores the bill. I want to see it in the model before I call it.
    """

    static let p2 = """
    ## What the code does

    `perPerson` divides only the tip:

    ```swift
    var perPerson: Double {
        tipAmount / Double(people)
    }
    ```

    It should divide `bill + tipAmount`. The coder's own test agrees with me: `testPerPersonSplitsTotal` fails with `8.0` against `48.0`. I'll try a second bill so this can't be a rounding quirk.
    """

    static let p3 = """
    With Bill **60** the window says Each pays $4.00: again the tip over three (60 × 0.20 ÷ 3), where $24.00 is right. Tip rounds to cents on both bills ($24.00, $12.00), so your note checks out. Two bills, the same error, and a failing unit test: this is a logic bug, not a display glitch. Putting Bill back to 120 for the evidence frame.
    """

    static let verdictReason = "Each pays shows $8.00; expected $48.00 (bill + tip split 3 ways)"
    static let passReason = "Tip shows $24.00 and Each pays shows $48.00, as expected"
    static let evidence = [13, 17, 31]

    static let messages: [ProtoMessage] = {
        var m: [ProtoMessage] = []
        func add(_ t: Double, _ from: Sender, _ kind: Kind, _ text: String, step: Int? = nil) {
            m.append(ProtoMessage(id: m.count + 1, t: t, from: from, kind: kind, text: text, step: step))
        }
        add(0.0, .system, .event, "machine ready · macos-15-xcode · booted in 31.4s")
        add(0.4, .coder, .note, "Syncing TipSplit to the machine and building it before I hand over.")
        for step in steps where step.by == .coder { add(step.t, .coder, .progress, "", step: step.id) }
        add(6.6, .coder, .task, "Built **TipSplit**, a SwiftUI tip calculator, in `~/work/TipSplit`. Please verify: set Bill to 120, Tip to 20% and People to 3, then check that Tip shows $24.00 and Each pays shows $48.00.")
        add(9.0, .verifier, .reply, "On it. I'll launch it and drive the window the way a person would, then read both totals off the screen.")
        for step in steps where step.by == .verifier && step.id <= 13 { add(step.t, .verifier, .progress, "", step: step.id) }
        add(19.4, .verifier, .reply, p1)
        add(24.5, .verifier, .progress, "", step: 14)
        add(25.6, .verifier, .question, "I can't read the values through Accessibility: osascript isn't allowed assistive access on this image. Should I grant it in System Settings, or is reading the screen enough?")
        add(29.0, .human, .answer, "Reading the screen is fine. Please don't touch privacy settings on the image.")
        add(30.2, .verifier, .reply, "Understood: screen only, no settings changes.")
        add(31.0, .verifier, .progress, "", step: 15)
        add(32.0, .verifier, .progress, "", step: 16)
        add(33.4, .human, .note, "Also check the Tip row rounds to cents.")
        add(34.2, .verifier, .progress, "", step: 17)
        add(36.8, .verifier, .reply, p2)
        for step in steps where (18...28).contains(step.id) { add(step.t, .verifier, .progress, "", step: step.id) }
        add(50.2, .verifier, .reply, p3)
        for step in steps where step.id >= 29 { add(step.t, .verifier, .progress, "", step: step.id) }
        add(57.4, .verifier, .verdict, verdictReason)
        add(59.6, .coder, .reply, "Seen. `perPerson` should split `bill + tipAmount`; fixing it now.")
        return m.sorted { $0.t < $1.t }.enumerated().map { i, msg in
            var msg = msg
            msg.id = i + 1
            return msg
        }
    }()

    static let verdictAt = 57.4
    static let questionAt = 25.6
    static let answerAt = 29.0

    static let runs: [ProtoRun] = [
        ProtoRun(id: runID, title: "TipSplit tip calculator", task: "Verify TipSplit totals", state: .live, detail: "31 steps · 44 msgs", day: .today, started: "14:20", steps: 31, messages: 44),
        ProtoRun(id: "checkout", title: "Checkout form validation", task: "Check the postcode field rejects letters", state: .needsYou, detail: "question", day: .today, started: "14:02", steps: 12, messages: 9),
        ProtoRun(id: "onboarding", title: "Onboarding carousel swipe", task: "Swipe through the four onboarding cards", state: .booting, detail: "clone 0.07s", day: .today, started: "14:31", steps: 0, messages: 1),
        ProtoRun(id: "darkmode", title: "Settings dark mode toggle", task: "Toggle dark mode and check every pane repaints", state: .live, detail: "8 steps", day: .today, started: "14:25", steps: 8, messages: 6),
        ProtoRun(id: "mdexport", title: "Markdown export to PDF", task: "Export a note with a table to PDF", state: .pass, detail: "19 steps · 2m 40s", day: .today, started: "12:48", steps: 19, messages: 14),
        ProtoRun(id: "ratelimit", title: "Login rate limiter", task: "Five bad passwords lock the form for 30s", state: .fail, detail: "24 steps · 4m 02s", day: .today, started: "11:15", steps: 24, messages: 17),
        ProtoRun(id: "photos", title: "Photos grid scroll perf", task: "Scroll 2,000 thumbnails without dropped frames", state: .pass, detail: "11 steps · 1m 55s", day: .today, started: "10:02", steps: 11, messages: 8),
        ProtoRun(id: "invoice", title: "Invoice PDF totals", task: "Invoice totals match the line items", state: .fail, detail: "27 steps · 6m 10s", day: .yesterday, started: "17:40", steps: 27, messages: 21),
        ProtoRun(id: "search", title: "Search debounce", task: "Search waits 300 ms after the last key", state: .pass, detail: "15 steps · 2m 12s", day: .yesterday, started: "16:05", steps: 15, messages: 10),
        ProtoRun(id: "menubar", title: "Menu bar extra quits", task: "Quit from the menu bar extra", state: .ended, detail: "destroyed by you", day: .yesterday, started: "15:12", steps: 6, messages: 5),
    ]

    /// A short transcript for the runs the prototype does not script in full.
    static func stubMessages(_ run: ProtoRun) -> [ProtoMessage] {
        var m: [ProtoMessage] = [
            ProtoMessage(id: 1, t: 0, from: .system, kind: .event, text: run.state == .booting ? "machine booting · macos-15-xcode" : "machine ready · macos-15-xcode"),
            ProtoMessage(id: 2, t: 0, from: .coder, kind: .task, text: run.task + "."),
        ]
        switch run.state {
        case .needsYou:
            m.append(ProtoMessage(id: 3, t: 0, from: .verifier, kind: .question, text: "The postcode field accepts `SW1A 1AA` and `90210`. Should letters be allowed for UK postcodes, or is this US-only?"))
        case .pass:
            m.append(ProtoMessage(id: 3, t: 0, from: .verifier, kind: .verdict, text: "Everything in the task checks out on screen."))
        case .fail:
            m.append(ProtoMessage(id: 3, t: 0, from: .verifier, kind: .verdict, text: "The behaviour in the task does not happen; see the evidence steps."))
        case .ended:
            m.append(ProtoMessage(id: 3, t: 0, from: .system, kind: .event, text: "machine destroyed by you"))
        case .live:
            m.append(ProtoMessage(id: 3, t: 0, from: .verifier, kind: .reply, text: "Working through it now."))
        case .booting:
            break
        }
        return m
    }
}
