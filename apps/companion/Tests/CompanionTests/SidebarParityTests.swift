import AppKit
import XCTest

@testable import Companion

/// What the native window kept from the old one's sidebar and menus (companion ADR 0019's
/// parity pass): twin marks, the row's tooltip, the search over id, start and task, hiding the
/// runs, `/`, the offline line and Details' fallbacks.
@MainActor
final class SidebarParityTests: XCTestCase {
    private let start = Calendar.current.date(from: DateComponents(year: 2026, month: 9, day: 28, hour: 16, minute: 4))!

    private func run(_ id: String, _ name: String, started: Date, group: SummaryGroup = .done) -> Summary {
        Summary(runId: id, name: name, state: .passed, status: "Passed", tone: .pass, group: group, since: started, startedAt: started)
    }

    private func board(_ runs: [Summary]) -> SummaryBoard {
        SummaryBoard(groups: SummaryGroup.allCases.map { g in .init(id: g, runs: runs.filter { $0.group == g }) },
                     macs: SummaryMacs(free: 1, total: 1, text: "1 of 1 Mac free"))
    }

    // MARK: - Twin marks

    func testRunsWithTheSameNameAreToldApartByWhenTheyStarted() {
        let a = run("20260928-160400-aaaaaa11", "TipSplit: split the bill", started: start)
        let b = run("20260928-161500-bbbbbb22", "TipSplit: split the bill", started: start.addingTimeInterval(11 * 60))
        let alone = run("20260928-170000-cccccc33", "WordCount: longest word", started: start.addingTimeInterval(3600))
        let marks = RunRowModel.twinMarks([a, b, alone])
        XCTAssertEqual(marks[a.runId], .time(Chrome.shortTime(a.startedAt)))
        XCTAssertEqual(marks[b.runId], .time(Chrome.shortTime(b.startedAt)))
        XCTAssertNil(marks[alone.runId], "a name of its own needs no mark")

        let now = start.addingTimeInterval(2 * 3600)
        let row = RunRowModel(a, now: now, twin: marks[a.runId])
        XCTAssertEqual(row.name, "TipSplit: split the bill", "the name stays whole")
        XCTAssertEqual(row.meta, "\(Chrome.shortTime(a.startedAt)) · \(RunRowModel.meta(a, now: now).0)", "the mark leads the meta")
        XCTAssertTrue(row.accessibilityLabel.contains(Chrome.shortTime(a.startedAt)))
        XCTAssertEqual(RunRowModel(alone, now: now, twin: nil).meta, RunRowModel.meta(alone, now: now).0)

        XCTAssertEqual(RunRowModel.distinctName(a, twin: marks[a.runId]), "TipSplit: split the bill, \(Chrome.shortTime(a.startedAt))")
        XCTAssertEqual(RunRowModel.distinctName(alone, twin: nil), "WordCount: longest word")
    }

    func testTwinsStartedInTheSameMinuteGoToTheSecondThenTheId() {
        let a = run("20260928-160400-aaaaaa11", "Same", started: start)
        let b = run("20260928-160400-bbbbbb22", "Same", started: start.addingTimeInterval(17))
        let c = run("20260928-160400-cccccc33", "Same", started: start.addingTimeInterval(17))
        let marks = RunRowModel.twinMarks([a, b, c])
        XCTAssertEqual(marks[a.runId], .time(Chrome.timeOfDay(a.startedAt)))
        XCTAssertEqual(marks[b.runId], .tag("#bbbbbb"))
        XCTAssertEqual(marks[c.runId], .tag("#cccccc"))
    }

    func testTwinMarksOverTwoThousandRunsStayCheap() {
        let many = SyntheticBoard.board(runs: 2000).runs
        _ = RunRowModel.twinMarks(Array(many.prefix(20))) // the time zone and formatters, once
        let t = Date()
        let marks = RunRowModel.twinMarks(many)
        let took = Date().timeIntervalSince(t)
        print(String(format: "twin marks over 2000 runs: %.1f ms", took * 1000))
        XCTAssertEqual(marks.count, 2000, "the synthetic board repeats its 96 names")
        // The synthetic runs start 30 minutes apart, so each name's twins share a time of day
        // two days apart and all fall back to id tags: the costly case. About 25 ms in a debug
        // build (every pair compared took 60); the sidebar also only works them out again when
        // a run's id, name or start changes (`TwinCache`). Held loosely: a loaded machine
        // doubles it.
        XCTAssertLessThan(took, 0.5)

        // Id tags cost a sort, not every pair: 20,000 copies of one run compared pairwise took
        // tens of seconds.
        let ids = (0..<20_000).map { String(format: "20260923-160400-%016x", $0 * 7919) }
        let tagStart = Date()
        let tags = RunTitle.idTags(ids)
        XCTAssertEqual(Set(tags.values).count, ids.count, "every tag tells its run apart")
        XCTAssertLessThan(Date().timeIntervalSince(tagStart), 3)
    }

    // MARK: - The row's tooltip

    func testTheRowsTooltipHasTheWholeNameTheStatusAndTheStart() {
        let a = run("r1", "A very long name that the row clips at its edge", started: start)
        let tip = RunRowModel.tooltip(a)
        XCTAssertTrue(tip.hasPrefix("A very long name that the row clips at its edge\nPassed, started "))
        XCTAssertTrue(tip.hasSuffix(start.formatted(date: .abbreviated, time: .shortened)))
        XCTAssertEqual(RunRowModel.tooltip(run("r2", "", started: .epoch)), "Run r2\nPassed")
    }

    // MARK: - The search

    func testTheSearchMatchesIdStartAndTask() {
        let a = run("20260928-160400-abcdef12", "TipSplit: split the bill", started: start)
        XCTAssertTrue(SidebarSearch.matches(a, needle: "tipsplit"))
        XCTAssertTrue(SidebarSearch.matches(a, needle: "passed"), "the status")
        XCTAssertTrue(SidebarSearch.matches(a, needle: "abcdef"), "the run id")
        XCTAssertTrue(SidebarSearch.matches(a, needle: Chrome.shortTime(start).lowercased()), "the start, as 16:04")
        XCTAssertTrue(SidebarSearch.matches(a, needle: start.formatted(date: .abbreviated, time: .omitted).lowercased()), "the start's day")
        XCTAssertFalse(SidebarSearch.matches(a, needle: "rounding"))
        XCTAssertTrue(SidebarSearch.matches(a, needle: "rounding", task: "Check the tip ROUNDING on 120"), "the task")
    }

    func testTheSidebarReadsTasksOnlyWhileSearching() {
        let a = run("20260928-160400-aaaaaa11", "TipSplit: split the bill", started: start)
        let b = run("20260928-170000-bbbbbb22", "WordCount: longest word", started: start.addingTimeInterval(3600))
        var reads = 0
        let tasks: () -> [String: String] = {
            reads += 1
            return [b.runId: "Type a paragraph and read the longest word"]
        }
        _ = SidebarLayout.items(board([a, b]), expanded: [], selected: nil, tasks: tasks)
        XCTAssertEqual(reads, 0, "no query, no task read")
        let found = SidebarLayout.items(board([a, b]), expanded: [], selected: nil, query: " Paragraph ", tasks: tasks)
        XCTAssertEqual(found, [.heading(.done), .run(b.runId)])
        XCTAssertEqual(reads, 1, "one read per search, not per run")
        XCTAssertEqual(SidebarLayout.items(board([a, b]), expanded: [], selected: nil, query: "nothing like it", tasks: tasks), [])
        XCTAssertEqual(SidebarSearch.empty(" nothing like it "), "No run matches \u{201C}nothing like it\u{201D}")
    }

    // MARK: - Hiding the runs and `/`

    private func key(_ characters: String, code: UInt16, flags: NSEvent.ModifierFlags = []) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil,
                         characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
    }

    func testControlCommandSHidesTheRunsAndSlashSearchesThem() {
        let saved = AppDefaults.shared.object(forKey: ShellModel.sidebarHiddenKey)
        defer { AppDefaults.shared.set(saved, forKey: ShellModel.sidebarHiddenKey) }
        let shell = ShellModel(store: RunStore())
        shell.sidebarHidden = false
        let keys = Keys(shell: shell)

        XCTAssertTrue(keys.handle(key("s", code: 1, flags: [.control, .command]), responder: nil))
        XCTAssertTrue(shell.sidebarHidden)
        XCTAssertTrue(AppDefaults.shared.bool(forKey: ShellModel.sidebarHiddenKey), "remembered")
        XCTAssertTrue(ShellModel(store: RunStore()).sidebarHidden, "a new window opens as it was left")

        let before = shell.searchRequest
        XCTAssertFalse(keys.handle(key("/", code: 44), responder: NSTextView()), "a field someone types in keeps /")
        XCTAssertEqual(shell.searchRequest, before)
        XCTAssertTrue(keys.handle(key("/", code: 44), responder: nil))
        XCTAssertEqual(shell.searchRequest, before + 1)
        XCTAssertFalse(shell.sidebarHidden, "searching shows the runs")
    }

    func testThePaletteHidesTheRunsAndNamesTwinsApart() {
        let saved = AppDefaults.shared.object(forKey: ShellModel.sidebarHiddenKey)
        defer { AppDefaults.shared.set(saved, forKey: ShellModel.sidebarHiddenKey) }
        let store = RunStore()
        let a = run("20260928-160400-aaaaaa11", "Same name", started: start)
        let b = run("20260928-161500-bbbbbb22", "Same name", started: start.addingTimeInterval(11 * 60))
        store.board = board([a, b])
        let shell = ShellModel(store: store)
        shell.sidebarHidden = false
        let options = PaletteOptions.of(shell)
        let titles = options.filter { $0.section == "Go to run" }.map(\.title)
        XCTAssertEqual(Set(titles), ["Same name, \(Chrome.shortTime(a.startedAt))", "Same name, \(Chrome.shortTime(b.startedAt))"])
        let sidebar = try? XCTUnwrap(options.first { $0.id == "sidebar" })
        XCTAssertEqual(sidebar?.title, "Hide the runs")
        XCTAssertEqual(sidebar?.keys, "⌃⌘S")
        sidebar?.action()
        XCTAssertTrue(shell.sidebarHidden)
        XCTAssertEqual(PaletteOptions.of(shell).first { $0.id == "sidebar" }?.title, "Show the runs")
    }

    // MARK: - Offline with runs held

    func testTheOfflineLineSaysWhatShowsAndWhy() {
        XCTAssertEqual(StaleDataBanner.text(words: nil), "Greenroom is not answering. Showing what was last loaded.")
        XCTAssertEqual(StaleDataBanner.text(words: "greenroom answered 500: boom"), "greenroom answered 500: boom. Showing what was last loaded.")
        XCTAssertEqual(StaleDataBanner.text(words: "Refused."), "Refused. Showing what was last loaded.")
    }

    // MARK: - Details

    func testDetailsFallBackToTheRunList() {
        let listed = RunSummary(runId: "r1", createdAt: start, image: "greenroom-base-15", messages: 7, frames: 42,
                                models: VerifierModels(brain: "nim", model: "claude-opus"))
        let none = RunDetailFacts(detail: nil, listed: listed, messages: nil, frames: nil)
        XCTAssertEqual(none.image, "greenroom-base-15")
        XCTAssertEqual(none.models?.verifier, "claude-opus")
        XCTAssertEqual(none.messages, 7)
        XCTAssertEqual(none.frames, 42)
        XCTAssertNil(none.boot)

        var detail = RunDetail(runId: "r1", createdAt: start)
        detail.image = "greenroom-dev"
        detail.machine = Machine(runId: "r1", name: "m", image: "greenroom-dev", ip: nil, status: .ready, error: nil, bootSeconds: 12.44,
                                 createdAt: start, dir: "", control: nil)
        let held = RunDetailFacts(detail: detail, listed: listed, messages: [], frames: nil)
        XCTAssertEqual(held.image, "greenroom-dev", "the run's own detail first")
        XCTAssertEqual(held.boot, "12.4 s")
        XCTAssertEqual(held.messages, 0, "the held conversation counts, even when empty")
        XCTAssertEqual(held.models?.verifier, "claude-opus", "a detail without models falls back to the list's")
    }
}
