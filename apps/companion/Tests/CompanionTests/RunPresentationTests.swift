import XCTest

@testable import Companion

final class RunPresentationTests: XCTestCase {
    private func message(
        _ seq: Int,
        _ from: MessageFrom,
        _ kind: MessageKind,
        at seconds: TimeInterval? = nil
    ) -> Message {
        Message(seq: seq, at: Date(timeIntervalSince1970: seconds ?? Double(seq)), from: from, kind: kind, text: "m\(seq)")
    }

    // MARK: - Title

    func testARunIsNamedByItsTaskOnOneLine() {
        XCTAssertEqual(RunTitle.text(task: "Check\n  the tip\tscreen", runId: "20260923-010827-9411d6"), "Check the tip screen")
    }

    func testAHeadingMarkDoesNotLeadTheName() {
        XCTAssertEqual(RunTitle.text(task: "## Verify the build", runId: "x"), "Verify the build")
    }

    func testARunWithNoTaskFallsBackToItsHash() {
        XCTAssertEqual(RunTitle.text(task: nil, runId: "20260923-010827-9411d6139b"), "Run 9411d6")
        XCTAssertEqual(RunTitle.text(task: "   ", runId: "plain"), "Untitled run")
    }

    func testTheTaskIsTheFirstTaskMessage() {
        let messages = [message(1, .system, .event), message(2, .coder, .task), message(3, .human, .task)]
        XCTAssertEqual(RunTitle.task(in: messages), "m2")
        XCTAssertNil(RunTitle.task(in: [message(1, .coder, .note)]))
    }

    // MARK: - Evidence

    func testAStepReferenceIsAStep() {
        XCTAssertEqual(Evidence.parse("step 16"), .step(16))
        XCTAssertEqual(Evidence.parse(" Step 3 "), .step(3))
        XCTAssertEqual(Evidence.parse("step16"), .step(16))
    }

    func testAPathIsAnArtifactNamedByItsFile() {
        let item = Evidence.parse("/Users/me/.greenroom/runs/abc/016-screenshot.png")
        XCTAssertEqual(item, .artifact("016-screenshot.png"))
        XCTAssertEqual(item.step, 16)
        XCTAssertEqual(item.label, "016-screenshot.png")
    }

    func testAnythingElseIsWordsWithNoStep() {
        let item = Evidence.parse("the tip reads $24.00")
        XCTAssertEqual(item, .text("the tip reads $24.00"))
        XCTAssertNil(item.step)
        XCTAssertEqual(Evidence.parse("steps were fine"), .text("steps were fine"))
    }

    func testOnlyANumberedArtifactHasAStep() {
        XCTAssertEqual(Evidence.step(ofArtifact: "007-screenshot.png"), 7)
        XCTAssertNil(Evidence.step(ofArtifact: "screenshot.png"))
        XCTAssertNil(Evidence.step(ofArtifact: "2026.png"))
    }

    // MARK: - Tools

    func testKnownToolsReadAsWords() {
        XCTAssertEqual(ToolCatalog.entry(for: "machine_exec").title, "Command")
        XCTAssertEqual(ToolCatalog.entry(for: "machine_screenshot").symbol, "camera")
        XCTAssertEqual(ToolCatalog.entry(for: "machine_session_start").title, "Session")
    }

    func testAnUnknownToolKeepsItsName() {
        XCTAssertEqual(ToolCatalog.entry(for: "report_verdict").title, "report_verdict")
        XCTAssertEqual(ToolCatalog.entry(for: "").title, "Tool")
    }

    func testAProgressMessageNamesItsTool() {
        XCTAssertEqual(ToolCatalog.tool(ofProgress: "machine_screenshot {}\nstep 9\nThe image"), "machine_screenshot")
        XCTAssertNil(ToolCatalog.tool(ofProgress: "The machine is ready."))
        XCTAssertNil(ToolCatalog.tool(ofProgress: ""))
    }

    // MARK: - Transcript layout

    func testToolCallsInARowFoldIntoOneGroup() {
        let items = TranscriptLayout.items([
            message(1, .coder, .task),
            message(2, .verifier, .progress),
            message(3, .verifier, .progress),
            message(4, .verifier, .reply),
        ])
        XCTAssertEqual(items.count, 3)
        guard case .toolCalls(let calls) = items[1] else { return XCTFail("expected a group, got \(items[1])") }
        XCTAssertEqual(calls.map(\.seq), [2, 3])
        XCTAssertEqual(items[1].id, 2)
        XCTAssertEqual(items[1].lastSeq, 3)
    }

    func testASpeakerIsNamedOncePerTurn() {
        let items = TranscriptLayout.items([
            message(1, .coder, .task),
            message(2, .coder, .note),
            message(3, .verifier, .reply),
            message(4, .verifier, .progress),
            message(5, .verifier, .reply),
        ])
        let shown = items.compactMap { item -> Bool? in
            if case .message(_, let showsSender) = item { return showsSender }
            return nil
        }
        // After tool calls the verifier is named again.
        XCTAssertEqual(shown, [true, false, true, true])
    }

    func testEventsAndAcceptsAreLinesNotSpeech() {
        let items = TranscriptLayout.items([
            message(1, .system, .event),
            message(2, .coder, .accept),
            message(3, .system, .note),
        ])
        for item in items {
            guard case .event = item else { return XCTFail("\(item) should be an event line") }
        }
    }

    func testHidingToolCallsJoinsTheTurnsAroundThem() {
        let items = TranscriptLayout.items([
            message(1, .verifier, .reply),
            message(2, .verifier, .progress),
            message(3, .verifier, .reply),
        ], toolCalls: false)
        XCTAssertEqual(items.count, 2)
        guard case .message(_, let showsSender) = items[1] else { return XCTFail("expected a message") }
        XCTAssertFalse(showsSender)
    }

    func testANewDayIsMarkedWhereItStarts() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let nearMidnight: TimeInterval = 86_400 - 60
        let items = TranscriptLayout.items([
            message(1, .coder, .task, at: nearMidnight),
            message(2, .verifier, .reply, at: nearMidnight + 30),
            message(3, .verifier, .reply, at: nearMidnight + 120),
        ], calendar: calendar)
        XCTAssertEqual(items.count, 4)
        guard case .day(let day, let seq) = items[2] else { return XCTFail("expected a day marker, got \(items[2])") }
        XCTAssertEqual(day, Date(timeIntervalSince1970: 86_400))
        XCTAssertEqual(seq, 3)
        XCTAssertNotEqual(items[2].id, items[3].id)
        // The verifier is named again on the new day.
        guard case .message(_, let showsSender) = items[3] else { return XCTFail("expected a message") }
        XCTAssertTrue(showsSender)
    }

    // MARK: - Connection

    func testConnectionFollowsTheLastRead() {
        XCTAssertEqual(ConnectionState.derive(reachable: nil, hasData: false), .connecting)
        XCTAssertEqual(ConnectionState.derive(reachable: true, hasData: false), .online)
        XCTAssertEqual(ConnectionState.derive(reachable: false, hasData: false), .offline(hasData: false))
        XCTAssertEqual(ConnectionState.derive(reachable: false, hasData: true), .offline(hasData: true))
    }

    // MARK: - Layout

    func testTheConversationKeepsItsWidthUntilTheStageNeedsIt() {
        XCTAssertEqual(RunLayout.conversation(420, in: 1200), 420)
        // 700 across leaves the stage its 380 and the line 1.
        XCTAssertEqual(RunLayout.conversation(420, in: 700), 319)
        XCTAssertEqual(RunLayout.conversation(420, in: 500), RunLayout.conversationMinimum)
        XCTAssertEqual(RunLayout.conversation(9000, in: 3000), RunLayout.conversationMaximum)
        // Before the first layout there is no width yet; the choice stands.
        XCTAssertEqual(RunLayout.conversation(420, in: 0), 420)
    }

    // MARK: - Words

    func testAgreementReadsAsWhatToDo() {
        XCTAssertEqual(Chrome.agreementTitle(VerdictState(status: .proposed)), "Awaiting review")
        XCTAssertEqual(Chrome.agreementTitle(VerdictState(status: .accepted, acceptedBy: .human)), "Accepted by you")
        XCTAssertEqual(Chrome.agreementTitle(VerdictState(status: .contested)), "Contested, needs you")
        XCTAssertEqual(Chrome.outcomeTitle("inconclusive"), "Inconclusive")
        XCTAssertEqual(Chrome.outcomeTitle(nil), "Verdict")
    }

    func testAgesAreShortEnoughForARow() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-3), now: now), "now")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-45), now: now), "45s")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-32 * 60), now: now), "32m")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-5 * 3600), now: now), "5h")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-3 * 86_400), now: now), "3d")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(-15 * 86_400), now: now), "2w")
        XCTAssertEqual(Chrome.age(now.addingTimeInterval(60), now: now), "now")
    }
}
