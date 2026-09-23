import XCTest

@testable import Companion

/// One derived state per run (companion ADR 0002): these pin that the indicators a person
/// sees cannot disagree with each other.
final class RunFactsTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_000_000)

    private func message(_ seq: Int, _ from: MessageFrom, _ kind: MessageKind, _ text: String = "",
                         ago: TimeInterval = 100, replyTo: Int? = nil) -> Message {
        Message(seq: seq, at: now.addingTimeInterval(-ago), from: from, kind: kind, text: text.isEmpty ? "m\(seq)" : text,
                replyTo: replyTo)
    }

    private func step(_ seq: Int, ago: TimeInterval, tool: String = "machine_exec", error: String? = nil,
                      exit: Int? = nil) -> Step {
        var output: JSONValue?
        if let exit { output = .object(["exitCode": .int(exit)]) }
        return Step(seq: seq, at: now.addingTimeInterval(-ago), tool: tool, input: nil, output: output, error: error)
    }

    private func machine(_ status: MachineStatus) -> Machine {
        Machine(runId: "r", name: "m", image: "i", ip: nil, status: status, error: nil, bootSeconds: nil,
                createdAt: now.addingTimeInterval(-600), dir: "", vncUrl: nil, control: nil)
    }

    private func detail(_ machine: Machine?, destroyedAt: Date? = nil) -> RunDetail {
        RunDetail(runId: "r", createdAt: now.addingTimeInterval(-600), destroyedAt: destroyedAt, machine: machine)
    }

    private func facts(
        summary: RunSummary? = nil, detail: RunDetail?, messages: [Message]? = [], steps: [Step]? = [],
        verdict: VerdictState? = nil
    ) -> RunFacts {
        RunFacts.derive(summary: summary, detail: detail, messages: messages, steps: steps, verdict: verdict, now: now)
    }

    // MARK: - Phase

    func testAReadyMachineWithRecentActivityIsLive() {
        let f = facts(detail: detail(machine(.ready)), steps: [step(1, ago: 8)])
        XCTAssertEqual(f.phase, .live)
        XCTAssertTrue(f.machineReady)
        XCTAssertEqual(f.idle(now: now), 8)
    }

    func testNothingForFiveMinutesIsIdleEvenWithFrames() {
        let f = facts(detail: detail(machine(.ready)), messages: [message(1, .coder, .task, ago: 400)], steps: [step(1, ago: 301)])
        XCTAssertEqual(f.phase, .idle)
        XCTAssertEqual(f.rowStatus(now: now).text, "Idle 5m")
    }

    /// The bug the reviewers saw: "Live" and "Take control" on a run whose machine is gone.
    func testAReadyListRowWithNoMachineInTheDetailIsNotLive() {
        let summary = RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .ready)
        let f = facts(summary: summary, detail: detail(nil), messages: [message(1, .system, .event, "machine destroyed")])
        XCTAssertEqual(f.phase, .ended(.destroyed(byYou: false)))
        XCTAssertFalse(f.machineReady)
        XCTAssertFalse(f.isAlive)
    }

    func testTheSpecificDestroyLineSaysWhoDidIt() {
        let events = [message(1, .system, .event, "machine destroyed"), message(2, .system, .event, "human destroyed the machine")]
        XCTAssertEqual(RunFacts.ending(events), .destroyed(byYou: true))
        XCTAssertEqual(RunFacts.ending(Array(events.reversed())), .destroyed(byYou: true))
    }

    func testAMachineThatStoppedWasLost() {
        let f = facts(detail: detail(nil), messages: [message(1, .system, .event, "machine stopped: the VM exited")])
        XCTAssertEqual(f.phase, .ended(.lost("the VM exited")))
        XCTAssertEqual(f.rowStatus(now: now).text, "Machine lost")
        XCTAssertEqual(f.rowStatus(now: now).tone, .failure)
    }

    func testBootingComesFromTheMachine() {
        let f = facts(detail: detail(machine(.booting)))
        XCTAssertEqual(f.phase, .booting)
        XCTAssertFalse(f.machineReady)
    }

    // MARK: - Times

    /// Header and player measure from the same start to the same end.
    func testAFinishedRunEndsWhenItsMachineWent() {
        let end = now.addingTimeInterval(-100)
        let f = facts(detail: detail(nil, destroyedAt: end), steps: [step(1, ago: 200)])
        XCTAssertEqual(f.ended, end)
        XCTAssertEqual(f.duration(now: now), 500)
        XCTAssertEqual(f.duration(now: now.addingTimeInterval(9999)), 500)
    }

    func testALiveRunCountsToNow() {
        let f = facts(detail: detail(machine(.ready)), steps: [step(1, ago: 5)])
        XCTAssertNil(f.ended)
        XCTAssertEqual(f.duration(now: now), 600)
    }

    func testHeldRecordsWinOverTheListsLastActivity() {
        let summary = RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .ready,
                                 lastActivity: now.addingTimeInterval(-1), messages: 99)
        let f = facts(summary: summary, detail: detail(machine(.ready)), messages: [message(1, .coder, .task, ago: 400)],
                      steps: [step(1, ago: 350)])
        XCTAssertEqual(f.lastActivity, now.addingTimeInterval(-350))
        XCTAssertEqual(f.messageCount, 1)
    }

    func testWithNothingHeldTheListIsTheAnswer() {
        let summary = RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .finished, steps: 7,
                                 lastActivity: now.addingTimeInterval(-60), messages: 4)
        let f = facts(summary: summary, detail: nil, messages: nil, steps: nil)
        XCTAssertEqual(f.lastActivity, now.addingTimeInterval(-60))
        XCTAssertEqual(f.stepCount, 7)
        XCTAssertEqual(f.messageCount, 4)
    }

    // MARK: - Turn

    func testAHumanNoteIsTheVerifiersTurn() {
        let f = facts(detail: detail(machine(.ready)), messages: [message(1, .human, .note, ago: 3)])
        XCTAssertEqual(f.turn, .verifier)
    }

    func testAnUnansweredQuestionNeedsYou() {
        let messages = [message(1, .coder, .task), message(2, .verifier, .question)]
        let f = facts(detail: detail(machine(.ready)), messages: messages)
        XCTAssertTrue(f.needsYou)
        XCTAssertEqual(f.rowStatus(now: now).text, "Question for you")
        let answered = facts(detail: detail(machine(.ready)), messages: messages + [message(3, .human, .answer, replyTo: 2)])
        XCTAssertFalse(answered.needsYou)
    }

    func testAProposedVerdictNeedsReviewEvenAfterTheRunEnded() {
        let f = facts(detail: detail(nil), verdict: VerdictState(seq: 3, verdict: "pass", status: .proposed))
        XCTAssertTrue(f.needsYou)
        XCTAssertEqual(f.rowStatus(now: now).text, "Needs review")
    }

    func testAClosedVerdictReadsAsItsOutcomeAndWhoClosedIt() {
        let byAgent = facts(detail: detail(nil), verdict: VerdictState(verdict: "pass", status: .accepted, acceptedBy: .coder))
        XCTAssertEqual(byAgent.rowStatus(now: now).text, "Pass, unreviewed")
        // Unreviewed keeps the word but not the colour: it is not a pass yet.
        XCTAssertEqual(byAgent.rowStatus(now: now).tone, .unsure)
        let byYou = facts(detail: detail(nil), verdict: VerdictState(verdict: "fail", status: .accepted, acceptedBy: .human))
        XCTAssertEqual(byYou.rowStatus(now: now).text, "Fail, you accepted")
        XCTAssertEqual(byYou.rowStatus(now: now).tone, .failure)
        let unsure = facts(detail: detail(nil), verdict: VerdictState(verdict: "inconclusive", status: .accepted, acceptedBy: .human))
        XCTAssertEqual(unsure.rowStatus(now: now).tone, .unsure)
    }

    // MARK: - Steps

    func testFailuresAreToolErrorsAndNonZeroExits() {
        let f = facts(detail: detail(nil), steps: [
            step(1, ago: 9), step(2, ago: 8, error: "tart exec: deadline"), step(3, ago: 7, exit: 3), step(4, ago: 6, exit: 0),
        ])
        XCTAssertEqual(f.failures, [2, 3])
    }

    /// A tool error with an exitCode of 0 beside it is still a failure: the zero is not a result.
    func testAToolErrorWinsOverItsZeroExitCode() {
        let s = step(1, ago: 1, error: "context deadline exceeded", exit: 0)
        XCTAssertEqual(s.outcome, .error("context deadline exceeded"))
    }

    func testRiskyCommandsAreFlagged() {
        XCTAssertTrue(StepRisk.isRisky("cd ~ && rm -rf '/Users/admin/~'"))
        XCTAssertTrue(StepRisk.isRisky("sudo shutdown -h now"))
        XCTAssertFalse(StepRisk.isRisky("ls -la ~/work"))
        XCTAssertFalse(StepRisk.isRisky("rmdir build"))
    }
}

final class VerdictReviewTests: XCTestCase {
    private let at = Date(timeIntervalSince1970: 72_720)
    private let clock: (Date) -> String = { _ in "20:12" }

    private func accept(by from: MessageFrom) -> Message {
        Message(seq: 5, at: at, from: from, kind: .accept, text: "", replyTo: 4)
    }

    func testAnAgentAcceptingIsNotAHumanReview() {
        let review = VerdictReview.of(VerdictState(seq: 4, verdict: "pass", status: .accepted, acceptedBy: .coder),
                                      messages: [accept(by: .coder)], timeOfDay: clock)
        XCTAssertEqual(review.state, "Unreviewed")
        XCTAssertEqual(review.decision, "accepted by the coding agent at 20:12")
        XCTAssertFalse(review.humanReviewed)
        XCTAssertEqual(review.note, "No person has reviewed this verdict.")
    }

    func testYourAcceptIsTheReview() {
        let review = VerdictReview.of(VerdictState(seq: 4, verdict: "pass", status: .accepted, acceptedBy: .human),
                                      messages: [accept(by: .human)], timeOfDay: clock)
        XCTAssertEqual(review.state, "You accepted")
        XCTAssertEqual(review.decision, "at 20:12")
        XCTAssertTrue(review.humanReviewed)
        XCTAssertNil(review.note)
    }

    func testContestedSaysOnlyYouCanCloseIt() {
        let review = VerdictReview.of(VerdictState(seq: 4, verdict: "fail", status: .contested, disputes: 2), messages: [])
        XCTAssertEqual(review.state, "Contested")
        XCTAssertEqual(review.decision, "only a person can close it")
        XCTAssertEqual(review.note, "The coding agent disputed it 2 times.")
        XCTAssertFalse(review.closed)
    }

    func testChecksComeFromTheRecord() {
        let steps = [Step(seq: 9, at: at, tool: "machine_screenshot"), Step(seq: 16, at: at, tool: "machine_exec", error: "boom")]
        let verdict = VerdictState(seq: 4, verdict: "pass", evidence: ["step 9", "step 16", "step 40"], status: .proposed)
        let message = Message(seq: 4, at: at, from: .verifier, kind: .verdict, text: "", verdict: "fail")
        XCTAssertEqual(VerdictCheck.checks(verdict, messages: [message], steps: steps),
                       [.failedStep(16), .missingStep(40), .outcomeMismatch(state: "pass", message: "fail")])
        XCTAssertEqual(VerdictCheck.checks(VerdictState(seq: 1, verdict: "pass", status: .proposed), messages: [], steps: nil),
                       [.noEvidence])
    }

    func testAStepAndItsScreenshotAreOnePieceOfEvidence() {
        let items = ["step 13", "/runs/x/013-screenshot.png", "the tip reads $24"].map(Evidence.parse)
        XCTAssertEqual(VerdictCard.byStep(items), [.step(13), .text("the tip reads $24")])
    }
}

final class TitleAndWordsTests: XCTestCase {
    func testAShortTitleIsTheFirstClauseWithoutAsides() {
        XCTAssertEqual(
            RunTitle.short(task: "TipSplit (a tip calculator I built) is on screen with Bill 120. Do not click.", runId: "x"),
            "TipSplit with Bill 120")
        XCTAssertEqual(
            RunTitle.short(task: "Look around the synced project in ~/work: list it, read its README", runId: "x"),
            "Look around the synced project in ~/work")
        XCTAssertEqual(RunTitle.short(task: "Check $24.00 is shown", runId: "x"), "Check $24.00 is shown")
    }

    func testALongClauseIsCutAtAWord() {
        let title = RunTitle.short(task: String(repeating: "word ", count: 40), runId: "x", limit: 20)
        XCTAssertEqual(title, "word word word word…")
    }

    /// Same titles say when they started, which does not change as more runs arrive.
    func testSameTitlesSayWhenTheyStarted() {
        let old = RunSummary(runId: "a", createdAt: Date(timeIntervalSince1970: 60), task: "Check the tip.")
        let new = RunSummary(runId: "b", createdAt: Date(timeIntervalSince1970: 3660), task: "Check the tip. Again.")
        let other = RunSummary(runId: "c", createdAt: Date(timeIntervalSince1970: 7200), task: "Something else")
        let titles = RunTitle.distinct([new, other, old])
        XCTAssertEqual(titles["a"], "Check the tip, \(Chrome.shortTime(old.createdAt))")
        XCTAssertEqual(titles["b"], "Check the tip, \(Chrome.shortTime(new.createdAt))")
        XCTAssertEqual(titles["c"], "Something else")
        // Two in one minute fall back to seconds.
        let twin = RunSummary(runId: "d", createdAt: Date(timeIntervalSince1970: 61), task: "Check the tip")
        XCTAssertNotEqual(RunTitle.distinct([old, twin])["a"], RunTitle.distinct([old, twin])["d"])
    }

    func testAStateDescribingBriefBecomesNeutral() {
        XCTAssertEqual(RunTitle.neutral("TipSplit is on screen with Bill 120"), "TipSplit with Bill 120")
        XCTAssertEqual(RunTitle.neutral("TipSplit is running on screen. Verify it works"), "TipSplit: verify it works")
        XCTAssertEqual(RunTitle.neutral("Build the app"), "Build the app")
        // Long subjects are a sentence, not a name: left alone.
        XCTAssertEqual(RunTitle.neutral("The thing I asked about yesterday is running"), "The thing I asked about yesterday is running")
    }

    func testBookkeepingPrefixesAreStripped() {
        XCTAssertEqual(TranscriptText.clean("[I reported verdict pass] The screenshot shows"), "The screenshot shows")
        XCTAssertEqual(TranscriptText.clean("A [bracket] inside"), "A [bracket] inside")
        XCTAssertEqual(TranscriptText.clean("[only brackets]"), "[only brackets]")
    }

    func testAFailThatReadsLikeAPassIsFlagged() {
        let text = "The tip is correctly calculated as $24.00 and Each pays correctly shows $48.00, which matches the expected calculation."
        let verdict = VerdictState(seq: 1, verdict: "fail", summary: text, evidence: ["step 9"], status: .proposed)
        XCTAssertEqual(VerdictCheck.checks(verdict, messages: [], steps: nil), [.readsLike("pass")])
        // A mixed account ("correct ... however ... instead of") is not flagged either way.
        XCTAssertNil(VerdictCheck.readsLike("The tip is correct. However Each pays shows $8.00 instead of $48.00."))
    }

    /// The daemon stops a run's verifier only when its machine is destroyed.
    func testTheVerifierListensUntilTheMachineIsDestroyed() {
        let created = Date(timeIntervalSince1970: 0)
        func facts(_ summary: RunSummary) -> RunFacts {
            RunFacts.derive(summary: summary, detail: nil, messages: nil, steps: nil, verdict: nil, now: created)
        }
        XCTAssertTrue(facts(RunSummary(runId: "r", createdAt: created, status: .ready)).verifierListens)
        XCTAssertTrue(facts(RunSummary(runId: "r", createdAt: created, status: .failed)).verifierListens)
        var destroyed = RunSummary(runId: "r", createdAt: created, status: .finished)
        destroyed.destroyedAt = created
        XCTAssertFalse(facts(destroyed).verifierListens)
    }

    func testReadsLikeCountsWholeWordsOnce() {
        XCTAssertNil(VerdictCheck.readsLike("The app launched correctly."), "one word counted twice")
        XCTAssertEqual(VerdictCheck.readsLike("The total is incorrectly shown, the tip is wrong."), "fail")
        XCTAssertNil(VerdictCheck.readsLike("The networks and frameworks load; it bypasses the cache."))
        XCTAssertEqual(VerdictCheck.readsLike("It doesn\u{2019}t update and the label is missing."), "fail")
    }

    func testClaimedValuesAreTheNumbersInTheWords() {
        XCTAssertEqual(VerdictCheck.claimedValues("Tip 20% of 120 = $24.00; Each pays $48.00, not $8.00. Ratio 1.5, $24.00 again"),
                       ["20%", "$24.00", "$48.00", "$8.00", "1.5"])
    }

    func testEachDisputeCarriesTheVerifiersAnswer() {
        let at = Date()
        let messages = [
            Message(seq: 1, at: at, from: .verifier, kind: .verdict, text: "fail", verdict: "fail"),
            Message(seq: 2, at: at, from: .coder, kind: .dispute, text: "rebuilt", replyTo: 1),
            Message(seq: 3, at: at, from: .verifier, kind: .progress, text: "machine_screenshot {}"),
            Message(seq: 4, at: at, from: .verifier, kind: .verdict, text: "still fail", verdict: "fail"),
            Message(seq: 5, at: at, from: .coder, kind: .dispute, text: "again", replyTo: 4),
        ]
        let history = DisputeRecord.history(for: VerdictState(seq: 4, verdict: "fail", status: .contested, disputes: 2), in: messages)
        XCTAssertEqual(history.map(\.dispute.seq), [2, 5])
        XCTAssertEqual(history.map { $0.answer?.seq }, [4, nil])
    }

    func testMarksCloseTogetherMergeWithEvidenceFirst() {
        let marks = [
            FrameTimeline.Mark(x: 10, step: 1, kind: .failure),
            FrameTimeline.Mark(x: 14, step: 2, kind: .evidence(verdict: "pass")),
            FrameTimeline.Mark(x: 40, step: 3, kind: .failure),
        ]
        let groups = FrameTimeline.cluster(marks, minGap: 8)
        XCTAssertEqual(groups.count, 2)
        XCTAssertEqual(groups[0].first.step, 2)
        XCTAssertEqual(groups[0].marks.count, 2)
    }

    func testEventsNameThePersonAsYou() {
        XCTAssertEqual(Chrome.eventText("human took control of the screen"), "You took control of the screen")
        XCTAssertEqual(Chrome.eventText("machine is ready"), "Machine is ready")
    }

    func testDurationsReadInMinutesPastAMinute() {
        XCTAssertEqual(Chrome.duration(640), "640 ms")
        XCTAssertEqual(Chrome.duration(42_900), "42.9 s")
        XCTAssertEqual(Chrome.duration(300_021), "5m 0s")
        XCTAssertEqual(Chrome.duration(3_720_000), "1h 2m")
    }

    func testSpansAreShort() {
        XCTAssertEqual(Chrome.span(8), "8s")
        XCTAssertEqual(Chrome.span(301), "5m")
        XCTAssertEqual(Chrome.span(7_500), "2h 5m")
        XCTAssertEqual(Chrome.span(7_200), "2h")
    }

    func testOneDestroyIsOneLine() {
        let at = Date()
        let items = TranscriptLayout.items([
            Message(seq: 1, at: at, from: .system, kind: .event, text: "machine destroyed"),
            Message(seq: 2, at: at, from: .system, kind: .event, text: "human destroyed the machine"),
        ])
        XCTAssertEqual(items.count, 1)
        guard case .event(let kept) = items[0] else { return XCTFail("expected an event") }
        XCTAssertEqual(kept.text, "human destroyed the machine")
    }

    /// A legacy log restarted its numbering on reattach: "179, 180, 1".
    func testStepsThatNumberBackwardsAreRenumberedInOrder() {
        let at = Date()
        let steps = [179, 180, 1].map { Step(seq: $0, at: at, tool: "machine_exec") }
        XCTAssertEqual(StepLog.normalized(steps).map(\.seq), [179, 180, 181])
        let fine = [1, 2, 5].map { Step(seq: $0, at: at, tool: "machine_exec") }
        XCTAssertEqual(StepLog.normalized(fine).map(\.seq), [1, 2, 5])
    }
}
