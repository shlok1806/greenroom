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
                createdAt: now.addingTimeInterval(-600), dir: "", control: nil)
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
        XCTAssertEqual(f.rowStatus(now: now).text, "Pass, needs review")
        // The outcome the verifier proposes leads (companion ADR 0012).
        let fail = facts(detail: detail(nil), verdict: VerdictState(seq: 3, verdict: "fail", status: .proposed))
        XCTAssertEqual(fail.rowStatus(now: now).text, "Fail, needs review")
        let contested = facts(detail: detail(nil), verdict: VerdictState(seq: 3, verdict: "fail", status: .contested, disputes: 2))
        XCTAssertEqual(contested.rowStatus(now: now).text, "Fail, contested")
        XCTAssertEqual(contested.rowStatus(now: now).tone, .attention)
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
    // MARK: - A turn stopped at a limit (issue #127, companion ADR 0015)

    private func stopped(_ seq: Int, _ stop: StopReason, ago: TimeInterval = 30) -> Message {
        var reply = message(seq, .verifier, .reply, "I used all 40 tool calls for this turn", ago: ago)
        reply.stop = stop
        return reply
    }

    /// Nothing happens until someone sends a message, so a live run waits on you.
    func testALiveRunStoppedAtALimitNeedsYou() {
        let messages = [message(1, .coder, .task, ago: 700), stopped(2, .steps)]
        let f = facts(detail: detail(machine(.ready)), messages: messages)
        XCTAssertEqual(f.stoppedAt, .steps)
        XCTAssertTrue(f.needsYou)
        XCTAssertEqual(f.rowStatus(now: now).text, "Stopped, out of tool calls")
        XCTAssertEqual(f.rowStatus(now: now).tone, .attention)
    }

    func testContinuingEndsTheWait() {
        let messages = [message(1, .coder, .task, ago: 700), stopped(2, .time, ago: 60), message(3, .human, .note, "Continue.", ago: 5)]
        let f = facts(detail: detail(machine(.ready)), messages: messages)
        XCTAssertNil(f.stoppedAt)
        XCTAssertEqual(f.turn, .verifier)
    }

    /// Once the machine is gone nothing will continue it; the row still says why there
    /// is no verdict.
    func testAnEndedRunStoppedAtALimitSaysSoWithoutNeedingYou() {
        let messages = [message(1, .coder, .task, ago: 700), stopped(2, .time), message(3, .system, .event, "machine destroyed", ago: 20)]
        let f = facts(detail: detail(nil, destroyedAt: now.addingTimeInterval(-20)), messages: messages)
        XCTAssertFalse(f.needsYou)
        XCTAssertFalse(f.verifierListens)
        XCTAssertEqual(f.rowStatus(now: now).text, "No verdict, out of time")
        XCTAssertEqual(f.rowStatus(now: now).tone, .quiet)
    }

    /// A question or a verdict waiting on you still leads the row.
    func testAQuestionOutranksAStop() {
        let messages = [message(1, .coder, .task, ago: 700), message(2, .verifier, .question, "Which?", ago: 60)]
        let f = facts(detail: detail(machine(.ready)), messages: messages)
        XCTAssertEqual(f.rowStatus(now: now).text, "Question for you")
        XCTAssertNil(f.stoppedAt)
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
        XCTAssertEqual(review.note, "No person reviewed it.")
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
        XCTAssertEqual(review.decision, "only you can close it")
        XCTAssertEqual(review.note, "The coding agent disputed it 2 times.")
        XCTAssertFalse(review.closed)
    }

    /// #58: the verifier stops with a destroyed machine, so Reject cannot promise a
    /// second look there.
    func testOnADestroyedRunRejectPromisesNoSecondLook() {
        let proposed = VerdictState(seq: 4, verdict: "fail", status: .proposed)
        let listening = VerdictReview.explanation(proposed, unreviewed: false, verifierListens: true, alive: true)
        XCTAssertTrue(listening.contains("looks again"), listening)

        let stopped = VerdictReview.explanation(proposed, unreviewed: false, verifierListens: false, alive: false)
        XCTAssertFalse(stopped.contains("looks again"), stopped)
        XCTAssertTrue(stopped.contains("nothing will look again"), stopped)

        let contested = VerdictState(seq: 4, verdict: "fail", status: .contested)
        let contestedStopped = VerdictReview.explanation(contested, unreviewed: false, verifierListens: false, alive: false)
        XCTAssertFalse(contestedStopped.contains("looks again"), contestedStopped)
        XCTAssertTrue(contestedStopped.contains("nothing will look again"), contestedStopped)
    }

    func testARejectionOnADestroyedRunSaysNobodyWasAskedToLookAgain() {
        let rejected = VerdictState(seq: 4, verdict: "pass", status: .rejected)
        let dispute = Message(seq: 5, at: at, from: .human, kind: .dispute, text: "wrong", replyTo: 4)
        XCTAssertEqual(VerdictReview.of(rejected, messages: [dispute], verifierListens: true, timeOfDay: clock).note,
                       "You asked the verifier to look again.")
        XCTAssertEqual(VerdictReview.of(rejected, messages: [dispute], verifierListens: false, timeOfDay: clock).note,
                       "The verifier stopped with the machine. Nothing will look again.")
        // Rejected while it still listened, and it answered: that look happened.
        let answer = Message(seq: 6, at: at, from: .verifier, kind: .reply, text: "Looked again.")
        XCTAssertEqual(VerdictReview.of(rejected, messages: [dispute, answer], verifierListens: false, timeOfDay: clock).note,
                       "You asked the verifier to look again.")
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

    @MainActor
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

    /// Seen in the app: "UI only (no build): set Bill" became "UI only : set Bill".
    func testAnAsideBeforePunctuationLeavesNoSpaceBeforeIt() {
        XCTAssertEqual(RunTitle.short(task: "UI only (no build): set Bill to 50", runId: "x"), "UI only: set Bill to 50")
        XCTAssertEqual(RunTitle.short(task: "Open it (TextEdit); type hi", runId: "x"), "Open it; type hi")
    }

    /// Only a dropped aside takes the space before punctuation with it.
    func testPunctuationAfterASpaceWithoutAnAsideIsKept() {
        XCTAssertEqual(RunTitle.short(task: "Add .env to .gitignore", runId: "x"), "Add .env to .gitignore")
        XCTAssertEqual(RunTitle.short(task: "Run ./build.sh", runId: "x"), "Run ./build.sh")    }

    /// Seen in the app: a run with no task read "Run 3f2a" as its title and again under it.
    func testARunWithoutATaskSaysSoUnderItsTitle() {
        XCTAssertEqual(RunTitle.subtitle(task: nil, alive: true), "No task yet")
        XCTAssertEqual(RunTitle.subtitle(task: "  ", alive: false), "No task was sent to this run")
        XCTAssertEqual(RunTitle.subtitle(task: "Check the tip", alive: true), "Check the tip")
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

    // MARK: - Twins (issue #157, companion ADR 0018)

    /// Five trials of one task, two in one minute, and copies of one run in one second:
    /// every twin gets a mark no other twin has; a run with its own title gets none.
    func testEveryTwinGetsAMarkNoOtherTwinHas() {
        let at = { (seconds: TimeInterval) in Date(timeIntervalSince1970: 1_000_000 + seconds) }
        let task = "WordCount has UPPERCASE and lowercase buttons"
        let trials = [
            RunSummary(runId: "20260926-035636-1262b62890da6847", createdAt: at(0), task: task),
            RunSummary(runId: "20260926-040010-37e61663c2b10bbb", createdAt: at(214), task: task),
            RunSummary(runId: "20260926-040017-c8bd11ccf06c1e2a", createdAt: at(221), task: task),
        ]
        let copies = [
            RunSummary(runId: "20260926-071916-b48b96d157b71fcd", createdAt: at(9000), task: "Longest word"),
            RunSummary(runId: "20260926-071916-b48b96d157b71fce", createdAt: at(9000), task: "Longest word"),
            RunSummary(runId: "20260926-071917-c0471f00d157b7aa", createdAt: at(9000), task: "Longest word"),
        ]
        let alone = RunSummary(runId: "20260926-080000-aaaaaa0000000000", createdAt: at(12000), task: "Something else")
        let marks = RunTitle.twinMarks(trials + copies + [alone])

        XCTAssertNil(marks[alone.runId])
        XCTAssertEqual(marks[trials[0].runId], .time(Chrome.shortTime(trials[0].createdAt)))
        // Same minute: to the second.
        XCTAssertEqual(marks[trials[1].runId], .time(Chrome.timeOfDay(trials[1].createdAt)))
        XCTAssertEqual(marks[trials[2].runId], .time(Chrome.timeOfDay(trials[2].createdAt)))
        // Same second: the id's digits, as many as tell them apart.
        XCTAssertEqual(marks[copies[0].runId], .tag("#b48b96d157b71fcd"))
        XCTAssertEqual(marks[copies[1].runId], .tag("#b48b96d157b71fce"))
        XCTAssertEqual(marks[copies[2].runId], .tag("#c0471f"))
        let all = (trials + copies).compactMap { marks[$0.runId]?.text }
        XCTAssertEqual(Set(all).count, all.count, "two twins share a mark: \(all)")
    }

    func testAnIdTagIsTheRunHashUnlessItIsShared() {
        let tags = RunTitle.idTags(["20260927-000233-acbc2b008dfc6a8f", "20260927-003333-7b483488f7e62556"])
        XCTAssertEqual(tags["20260927-000233-acbc2b008dfc6a8f"], "#acbc2b")
        XCTAssertEqual(tags["20260927-000233-acbc2b008dfc6a8f"], "#" + Chrome.runHash("20260927-000233-acbc2b008dfc6a8f"))
        XCTAssertEqual(RunTitle.idTags(["a-b-abcdef12", "a-b-abcdef13"])["a-b-abcdef12"], "#abcdef12")
    }

    /// The strip's tooltips say the same mark as the row.
    func testDistinctTitlesCarryTheTwinMark() {
        let one = RunSummary(runId: "x-y-b48b96d157b71fcd", createdAt: Date(timeIntervalSince1970: 60), task: "Check it")
        let two = RunSummary(runId: "x-y-b48b96d157b71fce", createdAt: Date(timeIntervalSince1970: 60), task: "Check it")
        let titles = RunTitle.distinct([one, two])
        XCTAssertEqual(titles[one.runId], "Check it, #b48b96d157b71fcd")
        XCTAssertEqual(titles[two.runId], "Check it, #b48b96d157b71fce")
    }

    /// A twin's mark leads its row's second line and never gives way; a lone run's time
    /// gives way first, after its tally (companion ADR 0012).
    func testATwinsMarkNeverGivesWayInItsRow() {
        XCTAssertEqual(RowMeta.lines(time: "23:00", twin: nil, running: nil, tally: "1/2 failed", steps: nil),
                       ["1/2 failed · 23:00", "1/2 failed", ""])
        XCTAssertEqual(RowMeta.lines(time: "23:00", twin: .time("23:00:10"), running: nil, tally: "1/2 failed", steps: nil),
                       ["23:00:10 · 1/2 failed", "23:00:10"])
        XCTAssertEqual(RowMeta.lines(time: "02:19", twin: .tag("#c0471f"), running: nil, tally: nil, steps: "39 steps"),
                       ["#c0471f · 02:19 · 39 steps", "#c0471f · 02:19", "#c0471f"])
        XCTAssertEqual(RowMeta.lines(time: "02:19", twin: nil, running: "running 4m", tally: nil, steps: "3 steps"),
                       ["02:19 · running 4m · 3 steps", "02:19 · running 4m", "02:19", ""])
    }

    func testAStateDescribingBriefBecomesNeutral() {
        XCTAssertEqual(RunTitle.neutral("TipSplit is on screen with Bill 120"), "TipSplit with Bill 120")
        XCTAssertEqual(RunTitle.neutral("TipSplit is running on screen. Verify it works"), "TipSplit: verify it works")
        XCTAssertEqual(RunTitle.neutral("Build the app"), "Build the app")
        // #96: an acronym or a name keeps its capitals ("uI only", "aPI docs" before).
        XCTAssertEqual(RunTitle.neutral("TipSplit is running. UI only: set Bill 84.00"), "TipSplit: UI only: set Bill 84.00")
        XCTAssertEqual(RunTitle.neutral("Safari is open. API docs page"), "Safari: API docs page")
        XCTAssertEqual(RunTitle.neutral("Safari is open. TipSplit renders"), "Safari: TipSplit renders")
        XCTAssertEqual(RunTitle.neutral("Safari is open. I think it hangs"), "Safari: I think it hangs")
        XCTAssertEqual(RunTitle.neutral("TipSplit is running. A tip of 18% shows"), "TipSplit: a tip of 18% shows")
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

    /// Seen in the app: the same "It claims 20% $24.00 ..." row under every cited step. A
    /// step shows only the values of the sentences that name it; a verdict that names no
    /// step has its values once, for the whole verdict.
    func testEachCitedStepShowsOnlyTheValuesClaimedAtIt() {
        let text = "At step 26 the bill is $180.00 with an 18% tip and the screen reads $45.00. "
            + "At step 27, after changing the tip to 20%, it still reads $45.00. Steps 30 and 31 show $0.00."
        XCTAssertEqual(VerdictCheck.claimedValues(text, atStep: 26), ["$180.00", "18%", "$45.00"])
        XCTAssertEqual(VerdictCheck.claimedValues(text, atStep: 27), ["20%", "$45.00"])
        XCTAssertEqual(VerdictCheck.claimedValues(text, atStep: 31), ["$0.00"])
        XCTAssertEqual(VerdictCheck.claimedValues(text, atStep: 9), [])
        let serial = "Steps 4, 5, and 6 show $3.00."
        XCTAssertEqual(VerdictCheck.claimedValues(serial, atStep: 6), ["$3.00"])
        XCTAssertEqual(VerdictCheck.claimedValues(serial, atStep: 4), ["$3.00"])
        let decimal = "At step 12 and 13.50 later the total is wrong."
        XCTAssertEqual(VerdictCheck.claimedValues(decimal, atStep: 12), ["13.50"])
        XCTAssertEqual(VerdictCheck.claimedValues(decimal, atStep: 13), [])
        XCTAssertTrue(VerdictCheck.namesSteps(text))
        XCTAssertFalse(VerdictCheck.namesSteps("Each pays $48.00 at 20%."))
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
