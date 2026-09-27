import XCTest

@testable import Companion

/// The daemon's run summary (root ADR 0036) as the app decodes and lays it out (companion ADR
/// 0019). The fixture is the daemon's own golden board, read in place so the two never drift:
/// `apps/daemon/internal/summary/testdata/board.golden.json`.
final class SummaryTests: XCTestCase {
    static var goldenURL: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent() // CompanionTests
            .deletingLastPathComponent() // Tests
            .deletingLastPathComponent() // companion
            .deletingLastPathComponent() // apps
            .appendingPathComponent("daemon/internal/summary/testdata/board.golden.json")
    }

    static func golden() throws -> SummaryBoard {
        try JSONDecoder.daemon().decode(SummaryBoard.self, from: Data(contentsOf: goldenURL))
    }

    private let tipSplit = "20260923-044138-de31017819a86d84"
    private let wordCountPass = "20260923-025814-1feb83aa6cc4b6c8"
    private let unitConvert = "20260923-051502-7ab01d2c55e0f913"
    private let todoStarting = "20260923-071502-0c1d2e3f40516273"
    private let todoNotAnswering = "20260923-070129-9f00e1a4b27c3d65"
    private let longestPaused = "20260923-060011-44c1d0e9a2b3f581"
    private let keepsText = "20260922-221040-5566778899aabbcc"

    // MARK: Decoding

    func testTheGoldenBoardDecodesWithEveryGroupAndTheMacs() throws {
        let board = try Self.golden()
        XCTAssertEqual(board.groups.map(\.id), [.needsYou, .running, .done])
        XCTAssertEqual(board.macs, SummaryMacs(free: 2, total: 3, text: "2 of 3 Macs free"))
        XCTAssertEqual(board.runs.count, 7)
    }

    func testTheFailedRunCarriesItsChecksValuesAndProof() throws {
        let s = try XCTUnwrap(try Self.golden().summary(tipSplit))
        XCTAssertEqual(s.name, "TipSplit: split the bill")
        XCTAssertEqual(s.source, "Claude Code")
        XCTAssertEqual(s.state, .failed)
        XCTAssertEqual(s.tone, .fail)
        XCTAssertEqual(s.group, .needsYou)
        XCTAssertEqual(s.primaryAction, SummaryAction(id: "accept", label: "Accept fail"))
        XCTAssertEqual(s.secondaryActions, [SummaryAction(id: "reject", label: "Reject")])
        XCTAssertEqual(s.checks.items.map(\.state), [.fail, .fail, .pass, .pass])
        let first = try XCTUnwrap(s.checks.items.first)
        XCTAssertEqual(first.expected, "$50.00")
        XCTAssertEqual(first.saw, "$10.00")
        XCTAssertEqual(first.picture?.file, "005-screenshot.png")
        XCTAssertNotNil(first.mark)
        XCTAssertEqual(s.failing?.saw, "$10.00")
    }

    func testAnUnknownStateKeepsItsWordAndBadFieldsTakeDefaults() throws {
        let json = #"{"runId":"r","name":"X","state":"thinking-hard","status":"Pondering","tone":"ultraviolet","group":"elsewhere","checks":{"items":[{"id":"a","state":"weird"}]},"primaryAction":{"id":"","label":""},"since":"not a time"}"#
        let s = try JSONDecoder.daemon().decode(Summary.self, from: Data(json.utf8))
        XCTAssertEqual(s.state, .unknown("thinking-hard"))
        XCTAssertEqual(s.status, "Pondering")
        XCTAssertEqual(s.tone, .quiet)
        XCTAssertEqual(s.group, .done)
        XCTAssertEqual(s.checks.items.first?.state, .pending)
        XCTAssertNil(s.primaryAction)
        XCTAssertEqual(s.since, .epoch)
    }

    func testTheSummaryEventParsesOffTheStream() throws {
        var parser = SSEParser()
        let payload = #"{"runId":"r1","summary":{"runId":"r1","name":"TipSplit","state":"checking","status":"Checking","tone":"live","group":"running"},"macs":{"free":1,"total":2,"text":"1 of 2 Macs free"}}"#
        XCTAssertNil(try parser.consume("event: summary"))
        XCTAssertNil(try parser.consume("data: \(payload)"))
        guard case .summary(let event)? = try parser.consume("") else { return XCTFail("no summary event") }
        XCTAssertEqual(event.summary.state, .checking)
        XCTAssertEqual(event.macs?.text, "1 of 2 Macs free")
    }

    // MARK: The board

    func testApplyingASummaryMovesTheRunToItsGroupNewestFirst() throws {
        var board = try Self.golden()
        var moved = try XCTUnwrap(board.summary(unitConvert))
        moved.group = .needsYou
        moved.state = .paused
        moved.since = Date(timeIntervalSince1970: 4_000_000_000)
        board = board.applying(moved, macs: SummaryMacs(free: 1, total: 3, text: "1 of 3 Macs free"))
        XCTAssertEqual(board.groups[0].runs.first?.runId, unitConvert)
        XCTAssertFalse(board.groups[1].runs.contains { $0.runId == unitConvert })
        XCTAssertEqual(board.groups[0].count, board.groups[0].runs.count)
        XCTAssertEqual(board.macs.free, 1)
        XCTAssertEqual(board.runs.count, 7, "a move adds no run")

        let fresh = Summary(runId: "new", name: "New run", since: Date(timeIntervalSince1970: 4_100_000_000))
        XCTAssertEqual(board.applying(fresh, macs: nil).groups[1].runs.first?.runId, "new")
        XCTAssertEqual(board.applying(fresh, macs: nil).macs.free, 1, "no macs keeps the last")
    }

    @MainActor
    func testTheStoreFoldsSummaryEventsIntoTheBoard() throws {
        let store = RunStore(client: DaemonClient(baseURL: URL(string: "http://127.0.0.1:9")!, readOnly: true))
        store.board = try Self.golden()
        var s = try XCTUnwrap(store.board?.summary(todoStarting))
        s.state = .checking
        s.status = "Checking"
        store.apply(.summary(SummaryEvent(runId: s.runId, summary: s, macs: nil)))
        XCTAssertEqual(store.board?.summary(todoStarting)?.status, "Checking")
    }

    // MARK: Rows

    private let now = DaemonDate.parse("2026-09-23T05:00:00Z")!

    func testARunRowIsAGlyphANameAndOneShortMeta() throws {
        let board = try Self.golden()
        let failed = RunRowModel(try XCTUnwrap(board.summary(tipSplit)), now: now)
        XCTAssertEqual(failed.glyph, .failed)
        XCTAssertEqual(failed.meta, "2 failed")
        XCTAssertEqual(failed.metaColor, .fail)
        XCTAssertEqual(failed.accessibilityLabel, "TipSplit: split the bill, Failed, 2 failed")

        let passed = RunRowModel(try XCTUnwrap(board.summary(wordCountPass)), now: now)
        XCTAssertEqual(passed.meta, "4 passed")
        XCTAssertEqual(passed.metaColor, .pass)

        let running = RunRowModel(try XCTUnwrap(board.summary(unitConvert)), now: now)
        XCTAssertEqual(running.glyph, .checking)
        XCTAssertTrue(running.meta.contains(":"), "an open run shows the time it has run: \(running.meta)")

        let done = RunRowModel(try XCTUnwrap(board.summary(keepsText)), now: now)
        XCTAssertFalse(done.meta.contains(":"), "a done run shows its age: \(done.meta)")

        for summary in board.runs {
            let row = RunRowModel(summary, now: now)
            let words = (row.name + " " + row.meta).split(whereSeparator: \.isWhitespace).count
            XCTAssertLessThanOrEqual(words, 7, "\(row.name): a row is six words or so, got \(words)")
        }
    }

    func testTheGlyphFollowsTheStateAndTheColourTheTone() {
        XCTAssertEqual(SummaryState.notAnswering.glyph, .warning)
        XCTAssertEqual(SummaryState.restarting.glyph, .starting)
        XCTAssertEqual(SummaryState.stopped.glyph, .stopped)
        XCTAssertEqual(SummaryTone.quiet.color, .secondary, "an unreviewed outcome keeps its shape, not its colour")
        XCTAssertEqual(SummaryTone.live.color, .accent)
    }

    func testTheSidebarShowsEveryOpenRunAndTheNewestDone() throws {
        let board = try Self.golden()
        let items = SidebarLayout.items(board, expanded: [], selected: nil)
        XCTAssertEqual(items.first, .heading(.needsYou))
        XCTAssertTrue(items.contains(.heading(.done)))
        let runs = items.filter { if case .run = $0 { true } else { false } }
        XCTAssertEqual(runs.count, 7)
        XCTAssertEqual(SidebarLayout.step(from: nil, by: 1, in: items), board.groups[0].runs.first?.runId)
        let last = try XCTUnwrap(board.runs.last?.runId)
        XCTAssertEqual(SidebarLayout.step(from: last, by: 1, in: items), last, "down at the end stays")

        let search = SidebarLayout.items(board, expanded: [], selected: nil, query: "todolist")
        XCTAssertEqual(search.filter { if case .run = $0 { true } else { false } }.count, 2)
    }

    func testALongDoneGroupFoldsBehindShowMoreButKeepsTheSelection() {
        let runs = (0..<40).map { i in
            Summary(runId: "d\(i)", name: "Run \(i)", state: .passed, status: "Passed", tone: .pass, group: .done,
                    since: Date(timeIntervalSince1970: Double(1_000_000 - i)))
        }
        let board = SummaryBoard(groups: [.init(id: .needsYou, runs: []), .init(id: .running, runs: []), .init(id: .done, runs: runs)])
        let folded = SidebarLayout.items(board, expanded: [], selected: "d30")
        XCTAssertEqual(folded.last, .more(.done, hidden: 34))
        XCTAssertTrue(folded.contains(.run("d30")))
        XCTAssertTrue(folded.contains(.empty(.needsYou)), "an empty Needs you says so")
        let open = SidebarLayout.items(board, expanded: [.done], selected: nil)
        XCTAssertEqual(open.filter { if case .run = $0 { true } else { false } }.count, 40)
    }

    // MARK: The header

    func testTheHeaderAnswersDidItPassAndWhatToDo() throws {
        let board = try Self.golden()
        let failed = HeaderModel(try XCTUnwrap(board.summary(tipSplit)), now: now)
        XCTAssertEqual(failed.status, "Failed")
        XCTAssertEqual(failed.tally, "2 of 4 checks")
        XCTAssertEqual(failed.line, "Proposed by the verifier after 3:26.")
        XCTAssertFalse(failed.isNow)
        XCTAssertEqual(failed.primary?.label, "Accept fail")

        let checking = HeaderModel(try XCTUnwrap(board.summary(unitConvert)), now: now)
        XCTAssertTrue(checking.isNow)
        XCTAssertEqual(checking.line, "clicking Convert in UnitConvert")
        XCTAssertTrue(checking.tally.hasPrefix("4 checks, "), checking.tally)
        XCTAssertEqual(checking.primary?.label, "Take control")

        let stuck = HeaderModel(try XCTUnwrap(board.summary(todoNotAnswering)), now: now)
        XCTAssertEqual(stuck.glyph, .warning)
        XCTAssertTrue(stuck.tally.hasPrefix("for "), stuck.tally)
        XCTAssertEqual(stuck.primary?.id, SummaryAction.restart)

        let paused = HeaderModel(try XCTUnwrap(board.summary(longestPaused)), now: now)
        XCTAssertEqual(paused.primary?.label, "Continue")
        XCTAssertEqual(paused.line, "The verifier ran out of time. Continue lets it go on.")
    }

    func testTheNowLineKeepsANamesCapital() {
        XCTAssertEqual(HeaderModel.lowerFirst("Clicking 25%"), "clicking 25%")
        XCTAssertEqual(HeaderModel.lowerFirst("UI read of TipSplit"), "UI read of TipSplit")
    }

    // MARK: Evidence

    func testTheCaptionPutsTheClaimNextToItsProof() {
        let failed = SummaryCheck(id: "a", text: "Each pays", state: .fail, expected: "$50.00", saw: "$10.00")
        XCTAssertEqual(EvidenceCaption.of(failed).words, "Expected $50.00, saw $10.00")
        let passed = SummaryCheck(id: "b", text: "Each pays", state: .pass, saw: "$50.00")
        XCTAssertEqual(EvidenceCaption.of(passed).words, "Saw $50.00, as expected")
        let prose = SummaryCheck(id: "c", text: "Window", state: .fail, observed: "The window has no Tip field.")
        XCTAssertEqual(EvidenceCaption.of(prose).words, "The window has no Tip field.")
        XCTAssertEqual(EvidenceCaption.of(nil), .none)
    }

    func testTheFirstFailureIsSelectedAndJKWalkTheChecks() {
        let checks = [
            SummaryCheck(id: "p", text: "p", state: .pass),
            SummaryCheck(id: "f", text: "f", state: .fail),
            SummaryCheck(id: "q", text: "q", state: .pending),
        ]
        XCTAssertEqual(CheckSelection.initial(checks), "f")
        XCTAssertEqual(CheckSelection.step(from: "f", by: 1, in: checks), "q")
        XCTAssertEqual(CheckSelection.step(from: "q", by: 1, in: checks), "p", "wraps")
        XCTAssertEqual(CheckSelection.step(from: nil, by: -1, in: checks), "q")
        XCTAssertNil(CheckSelection.initial([]))
    }

    // MARK: Times

    func testTimesAreShortAndTabular() {
        XCTAssertEqual(Clock.elapsed(258), "4:18")
        XCTAssertEqual(Clock.elapsed(3727), "1:02:07")
        XCTAssertEqual(Clock.elapsed(-3), "0:00")
        let base = Date(timeIntervalSince1970: 1_000_000)
        XCTAssertEqual(Clock.age(since: base, now: base.addingTimeInterval(30)), "now")
        XCTAssertEqual(Clock.age(since: base, now: base.addingTimeInterval(8 * 60)), "8m")
        XCTAssertEqual(Clock.age(since: base, now: base.addingTimeInterval(2 * 3600)), "2h")
        XCTAssertEqual(Clock.age(since: base, now: base.addingTimeInterval(86400)), "1d")
        XCTAssertEqual(Clock.ago(base, now: base.addingTimeInterval(12 * 60)), "12 min ago")
        XCTAssertEqual(Clock.ago(base, now: base.addingTimeInterval(20)), "just now")
    }
}
