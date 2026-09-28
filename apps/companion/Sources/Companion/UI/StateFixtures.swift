#if DEBUG
import Foundation

/// The run window's states as fixtures, for the snapshot harness and the tests (companion ADR
/// 0019): the daemon's golden board (root ADR 0036), with the TipSplit fail run's summary
/// edited into each state the way the daemon words it, as the Figma screens do. Debug only.
enum StateFixtures {
    static let tipSplit = "20260923-044138-de31017819a86d84"
    static let wordCount = "20260923-025814-1feb83aa6cc4b6c8"
    static let start = DaemonDate.parse("2026-09-23T04:41:38Z")!

    enum State: String, CaseIterable, Sendable {
        case failed, live, paused, starting, notAnswering, restarting, warning, done

        /// Seconds after the run's start the state is looked at (the Figma screens' clocks).
        var at: TimeInterval {
            switch self {
            case .failed: 12 * 60
            case .live, .warning: 258
            case .paused: 600
            case .starting: 24
            case .notAnswering: 262
            case .restarting: 212
            case .done: 3600
            }
        }

        /// A restart is held to the budget of the screen that asked for it (not answering): the
        /// Figma frame 07b says what the Mac is doing and which check waits, about 64 words.
        var budget: WordBudget.Screen {
            switch self {
            case .failed, .paused, .notAnswering, .restarting, .warning: .runVerdictWaiting
            case .live: .runLive
            case .starting: .booting
            case .done: .runFinished
            }
        }
    }

    /// The four TipSplit checks as a plan: declared, none answered yet.
    static func planned() -> [SummaryCheck] {
        ["Window shows Bill, Tip and People", "Tip is $24.00 for $120 at 20%", "Each pays is $48.00 for 3 people", "Each pays becomes $50.00 at 25%"]
            .enumerated().map { SummaryCheck(id: "p\($0.offset)", text: $0.element, state: .pending) }
    }

    /// The golden board with the TipSplit run in `state`; the fail's pictures point at the
    /// copied run's own screenshots, with the mark where "Each pays: $10.00" sits.
    static func board(_ golden: SummaryBoard, state: State = .failed) -> SummaryBoard {
        var board = golden
        edit(&board, tipSplit) { s in
            s.checks.items = s.checks.items.map { item in
                var item = item
                if item.saw == "$10.00" {
                    item.picture = SummaryPicture(kind: "screenshot", file: "017-screenshot.png", step: 17)
                    item.mark = SummaryBox(x: 0.314, y: 0.638, w: 0.232, h: 0.05)
                    item.observed = "Bill 120, 3 people, then 25%."
                } else {
                    item.picture = SummaryPicture(kind: "screenshot", file: "012-screenshot.png", step: 12)
                    item.mark = nil
                }
                return item
            }
            apply(state, to: &s)
        }
        return board
    }

    static func apply(_ state: State, to s: inout Summary) {
        let plan = SummaryChecks(total: 4, pending: 4, text: "4 checks planned", items: planned())
        switch state {
        case .failed:
            break
        case .live, .warning:
            (s.state, s.status, s.tone, s.group) = (.checking, "Checking", .live, state == .warning ? .needsYou : .running)
            s.now = "Clicking 25% in TipSplit"; s.detail = nil; s.elapsedSeconds = 258
            s.primaryAction = SummaryAction(id: SummaryAction.takeControl, label: "Take control"); s.secondaryActions = []
            s.checks = plan
            s.machine = SummaryMachine(status: "on", warning: state == .warning ? "The Mac is running low on resources; save what you need." : nil)
        case .paused:
            (s.state, s.status, s.tone, s.group) = (.paused, "Paused", .wait, .needsYou)
            s.detail = "The verifier ran out of time. Continue lets it go on."; s.now = nil; s.elapsedSeconds = 600
            s.primaryAction = SummaryAction(id: SummaryAction.continue, label: "Continue"); s.secondaryActions = []
            s.checks = plan; s.machine = SummaryMachine(status: "on")
        case .starting:
            (s.state, s.status, s.tone, s.group) = (.starting, "Starting", .live, .running)
            s.detail = nil; s.now = "Waiting for the Mac"; s.elapsedSeconds = 24
            s.primaryAction = nil; s.secondaryActions = []
            s.checks = SummaryChecks(); s.machine = SummaryMachine(status: "starting")
        case .notAnswering:
            (s.state, s.status, s.tone, s.group) = (.notAnswering, "Not answering", .wait, .needsYou)
            s.detail = "The Mac's screen stopped answering. Restart it? Your files are kept."; s.now = nil
            s.since = start.addingTimeInterval(200); s.elapsedSeconds = 262
            s.primaryAction = SummaryAction(id: SummaryAction.restart, label: "Restart the Mac")
            s.secondaryActions = [SummaryAction(id: SummaryAction.keepWaiting, label: "Keep waiting")]
            s.checks = plan; s.machine = SummaryMachine(status: "on")
            s.lastFrame = SummaryPicture(kind: "screenshot", file: "017-screenshot.png")
        case .restarting:
            (s.state, s.status, s.tone, s.group) = (.restarting, "Restarting", .live, .running)
            s.detail = "Your files are kept. Checks pick up where they stopped."; s.now = nil
            s.since = start.addingTimeInterval(200); s.elapsedSeconds = 212
            s.primaryAction = nil; s.secondaryActions = []
            s.checks = plan; s.machine = SummaryMachine(status: "restarting")
            s.lastFrame = SummaryPicture(kind: "screenshot", file: "017-screenshot.png")
        case .done:
            s.group = .done; s.detail = "You accepted it."
            s.primaryAction = nil; s.secondaryActions = []; s.machine = SummaryMachine(status: "off")
            s.endedAt = start.addingTimeInterval(986)
        }
    }

    /// The board Figma frame 03 draws ("Run, failed, evidence"): TipSplit failed and open,
    /// the sidebar's runs as the mockup names them, the checks in the mockup's order. For the
    /// pixel comparison against the frame (docs/22 section 7), where content that differs from
    /// the mockup's would count against the clone.
    /// `compact`: the board frame 03's compact variant draws (one run running, three done
    /// shown of 43).
    static func mockup(_ golden: SummaryBoard, compact: Bool = false, at: State = .failed) -> SummaryBoard {
        let now = start.addingTimeInterval(at.at)
        guard var tip = board(golden).summary(tipSplit) else { return golden }
        let order = ["Each pays becomes", "Each pays is", "Tip is", "Window shows"]
        tip.checks.items.sort { a, b in
            (order.firstIndex { a.text.hasPrefix($0) } ?? order.count) < (order.firstIndex { b.text.hasPrefix($0) } ?? order.count)
        }
        func run(_ id: String, _ name: String, _ state: SummaryState, _ tone: SummaryTone, _ group: SummaryGroup,
                 age: TimeInterval, elapsed: Int = 0) -> Summary {
            var s = Summary(runId: id, name: name, state: state, status: "", tone: tone, group: group,
                            since: now.addingTimeInterval(-age))
            s.elapsedSeconds = elapsed
            if group == .done { s.endedAt = now.addingTimeInterval(-age) }
            return s
        }
        var word = run(wordCount, "WordCount: case buttons", .passed, .pass, .needsYou, age: 8 * 60)
        word.checks = SummaryChecks(total: 4, passed: 4)
        let hour: TimeInterval = 3600, day: TimeInterval = 86400
        var done = [
            run("m-done-0", "WordCount: longest word", .passed, .pass, .done, age: 2 * hour),
            run("m-done-1", "TodoList: Clear done", .failed, .fail, .done, age: 5 * hour),
            run("m-done-2", "UnitConvert: result size", .stopped, .quiet, .done, age: 1 * day + 60),
            run("m-done-3", "TodoList: add item", .passed, .pass, .done, age: 1 * day + 120),
            run("m-done-4", "WordCount: keeps text", .passed, .pass, .done, age: 2 * day),
        ]
        done += (0..<38).map { run("m-old-\($0)", "Older run \($0)", .passed, .pass, .done, age: 3 * day + TimeInterval($0) * hour) }
        var running = [
            run("m-run-0", "UnitConvert: Temperature", .checking, .live, .running, age: 72, elapsed: 72),
            run("m-run-1", "TodoList: summary line", .starting, .live, .running, age: 24, elapsed: 24),
        ]
        if compact {
            running.removeLast()
            done.swapAt(2, 3)
        }
        return SummaryBoard(groups: [
            .init(id: .needsYou, runs: [tip, word]),
            .init(id: .running, runs: running),
            .init(id: .done, runs: done),
        ], macs: golden.macs)
    }

    /// The board Figma frame 04 draws ("Run, passed"): TipSplit's fix passed and open, its four
    /// checks proven on three pictures, the sidebar as the mockup names it.
    static func mockupPassed(_ golden: SummaryBoard) -> SummaryBoard {
        let now = start.addingTimeInterval(State.failed.at)
        guard var tip = board(golden).summary(tipSplit) else { return golden }
        (tip.name, tip.state, tip.status, tip.tone, tip.group) = ("TipSplit: fix each pays", .passed, "Passed", .pass, .needsYou)
        tip.startedAt = now.addingTimeInterval(-360)
        tip.since = now.addingTimeInterval(-20)
        tip.elapsedSeconds = 372
        tip.detail = "Proposed by the verifier after 6:12."
        tip.failing = nil
        tip.primaryAction = SummaryAction(id: SummaryAction.accept, label: "Accept pass")
        tip.secondaryActions = [SummaryAction(id: SummaryAction.reject, label: "Reject")]
        func shot(_ step: Int) -> SummaryPicture {
            SummaryPicture(kind: "screenshot", file: String(format: "%03d-screenshot.png", step), step: step)
        }
        tip.checks = SummaryChecks(total: 4, passed: 4, text: "4 of 4 checks", items: [
            SummaryCheck(id: "each-25", text: "Each pays becomes $50.00 at 25%", state: .pass, expected: "$50.00", saw: "$50.00",
                         observed: "Bill 120, 3 people, then 25%.", step: 17, picture: shot(17),
                         mark: SummaryBox(x: 0.314, y: 0.638, w: 0.232, h: 0.05)),
            SummaryCheck(id: "each", text: "Each pays is $48.00 for 3 people", state: .pass, expected: "$48.00", saw: "$48.00",
                         observed: "Bill 120, 3 people.", step: 12, picture: shot(12)),
            SummaryCheck(id: "tip", text: "Tip is $24.00 for $120 at 20%", state: .pass, expected: "$24.00", saw: "$24.00",
                         observed: "Tip reads $24.00.", step: 12, picture: shot(12)),
            SummaryCheck(id: "window", text: "Window shows Bill, Tip and People", state: .pass,
                         observed: "The window shows Bill, Tip and People.", step: 3, picture: shot(3)),
        ])
        func run(_ id: String, _ name: String, _ state: SummaryState, _ tone: SummaryTone, _ group: SummaryGroup,
                 age: TimeInterval, elapsed: Int = 0) -> Summary {
            var s = Summary(runId: id, name: name, state: state, status: "", tone: tone, group: group,
                            since: now.addingTimeInterval(-age))
            s.elapsedSeconds = elapsed
            if group == .done { s.endedAt = now.addingTimeInterval(-age) }
            return s
        }
        var clear = run("m-need-1", "TodoList: Clear done", .failed, .fail, .needsYou, age: 3 * 60)
        clear.checks = SummaryChecks(total: 3, passed: 2, failed: 1)
        let hour: TimeInterval = 3600, day: TimeInterval = 86400
        var done = [
            run("m-done-0", "TipSplit: split the bill", .failed, .fail, .done, age: 1 * hour),
            run("m-done-1", "WordCount: longest word", .passed, .pass, .done, age: 2 * hour),
            run("m-done-2", "UnitConvert: result size", .stopped, .quiet, .done, age: 1 * day + 60),
            run("m-done-3", "TodoList: add item", .passed, .pass, .done, age: 1 * day + 120),
            run("m-done-4", "WordCount: keeps text", .passed, .pass, .done, age: 2 * day),
        ]
        done += (0..<38).map { run("m-old-\($0)", "Older run \($0)", .passed, .pass, .done, age: 3 * day + TimeInterval($0) * hour) }
        return SummaryBoard(groups: [
            .init(id: .needsYou, runs: [tip, clear]),
            .init(id: .running, runs: [run("m-run-0", "UnitConvert: Temperature", .checking, .live, .running, age: 72, elapsed: 72)]),
            .init(id: .done, runs: done),
        ], macs: golden.macs)
    }

    /// The mockup's board with TipSplit in `state` as Figma 02 (live) and 07a/07b (not
    /// answering, restarting) draw it: two checks passed, two to go.
    static func mockup(_ golden: SummaryBoard, state: State) -> SummaryBoard {
        var board = mockup(golden, at: state)
        if state == .notAnswering || state == .restarting {
            // Figma 07 draws one other run running.
            board.groups = board.groups.map { group in
                var group = group
                group.runs.removeAll { $0.runId == "m-run-1" }
                group.count = group.runs.count
                return group
            }
        }
        edit(&board, tipSplit) { s in
            apply(state, to: &s)
            s.detail = state == .live ? nil : s.detail
            let items = [
                SummaryCheck(id: "window", text: "Window shows Bill, Tip and People", state: .pass),
                SummaryCheck(id: "tip", text: "Tip is $24.00 for $120 at 20%", state: .pass, saw: "$24.00"),
                SummaryCheck(id: "each", text: "Each pays is $48.00 for 3 people", state: .pending),
                SummaryCheck(id: "each-25", text: "Each pays becomes $50.00 at 25%", state: .pending),
            ]
            s.checks = SummaryChecks(total: 4, passed: 2, pending: 2, text: "2 of 4 checks", items: items)
        }
        // A run still running leads its group, as the frames draw it.
        board.groups = board.groups.map { group in
            var group = group
            if let i = group.runs.firstIndex(where: { $0.runId == tipSplit }), group.id == .running {
                group.runs.insert(group.runs.remove(at: i), at: 0)
            }
            return group
        }
        return board
    }

    static func edit(_ board: inout SummaryBoard, _ runId: String, _ change: (inout Summary) -> Void) {
        guard var s = board.summary(runId) else { return }
        change(&s)
        board = board.applying(s, macs: nil)
    }
}
#endif
