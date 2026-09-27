#if DEBUG
import Foundation

/// A board of many runs for the harness and the performance tests (companion ADR 0019 point
/// 8): a few runs that need you, a few running, the rest done, with names, checks and times
/// shaped like real ones. Debug builds only.
enum SyntheticBoard {
    private static let apps = ["TipSplit", "WordCount", "UnitConvert", "TodoList", "HelloGreenroom", "Notes", "Timer", "Weather"]
    private static let subjects = ["split the bill", "case buttons", "Temperature", "summary line", "longest word", "keeps text",
                                   "add item", "Clear done", "result size", "round cents", "your name", "dark mode"]

    static func board(runs count: Int, now: Date = Date(), needsYou: Int = 3, running: Int = 4) -> SummaryBoard {
        var all: [Summary] = []
        for i in 0..<count {
            let group: SummaryGroup = i < needsYou ? .needsYou : (i < needsYou + running ? .running : .done)
            let started = now.addingTimeInterval(-Double(i) * 1_800 - 300)
            let name = "\(apps[i % apps.count]): \(subjects[(i / apps.count) % subjects.count])"
            let failed = i % 5 == 1
            var s = Summary(runId: String(format: "20260923-%06d-%016x", i, i * 7919), name: name)
            s.group = group
            s.startedAt = started
            s.since = started.addingTimeInterval(200)
            switch group {
            case .needsYou:
                (s.state, s.status, s.tone) = failed ? (.failed, "Failed", .fail) : (.passed, "Passed", .pass)
                s.checks = SummaryChecks(total: 4, passed: failed ? 2 : 4, failed: failed ? 2 : 0, text: failed ? "2 of 4 checks failed" : "4 of 4 checks passed")
            case .running:
                (s.state, s.status, s.tone) = i % 2 == 0 ? (.checking, "Checking", .live) : (.starting, "Starting", .live)
                s.elapsedSeconds = 60 + i * 13
                s.machine = SummaryMachine(status: "on")
            case .done:
                let outcome: (SummaryState, String, SummaryTone) = failed ? (.failed, "Failed", .fail) : (i % 7 == 3 ? (.stopped, "Stopped", .quiet) : (.passed, "Passed", .pass))
                (s.state, s.status, s.tone) = outcome
                s.endedAt = started.addingTimeInterval(400)
                s.elapsedSeconds = 400
                s.machine = SummaryMachine(status: "off")
            }
            all.append(s)
        }
        return SummaryBoard(groups: SummaryGroup.allCases.map { g in .init(id: g, runs: all.filter { $0.group == g }) },
                            macs: SummaryMacs(free: 2, total: 3, text: "2 of 3 Macs free"))
    }
}
#endif
