import XCTest

@testable import Companion

/// How each transcript card reads: tool calls, folding, questions, the verdict card's
/// frame and the verdict's line in the history (`Model/TranscriptCards.swift`).
final class TranscriptCardsTests: XCTestCase {
    private func message(_ seq: Int, _ from: MessageFrom, _ kind: MessageKind, _ text: String = "",
                         replyTo: Int? = nil, step: Int? = nil, verdict: String? = nil) -> Message {
        Message(seq: seq, at: Date(timeIntervalSince1970: Double(seq) * 60), from: from, kind: kind, text: text,
                replyTo: replyTo, step: step, verdict: verdict)
    }

    private func step(_ seq: Int, _ tool: String, input: JSONValue? = nil, output: JSONValue? = nil,
                      error: String? = nil, ms: Int = 0) -> Step {
        Step(seq: seq, at: Date(timeIntervalSince1970: 0), tool: tool, input: input, output: output, error: error, durationMs: ms)
    }

    private let time: (Date) -> String = { _ in "20:12" }

    // MARK: - Tool calls

    func testAToolCallReadsAsASentenceWithTwoFacts() {
        let call = message(3, .verifier, .progress, "machine_screenshot {}\nstep 9\nThe screen shows TipSplit", step: 9)
        let row = ToolCallRow.of(call, steps: [step(9, "machine_screenshot", ms: 1240)])
        XCTAssertEqual(row.phrase, "Took a screenshot")
        XCTAssertEqual(row.state, .done)
        XCTAssertEqual(row.facts, ["step 9", "1.2 s"])
        XCTAssertEqual(row.step, 9)
    }

    func testAToolCallWithNoRecordReadsItsProgress() {
        let call = message(3, .verifier, .progress, "machine_exec {\"command\":\"swift test\"}\nstep 4\nok", step: 4)
        let row = ToolCallRow.of(call, steps: [])
        XCTAssertEqual(row.phrase, "Ran swift test")
        XCTAssertEqual(row.facts, ["step 4"])
        XCTAssertEqual(row.state, .done)
    }

    func testAFailedStepIsFailed() {
        let call = message(3, .verifier, .progress, "machine_exec {\"command\":\"make\"}\nstep 5\n", step: 5)
        let exited = step(5, "machine_exec", input: .object(["command": .string("make")]), output: .object(["exitCode": .int(2)]))
        XCTAssertEqual(ToolCallRow.of(call, steps: [exited]).state, .failed)
        let errored = step(5, "machine_exec", error: "machine is gone")
        let row = ToolCallRow.of(call, steps: [errored])
        XCTAssertEqual(row.state, .failed)
        XCTAssertEqual(row.raw.first { $0.label == "Error" }?.text, "machine is gone")
    }

    /// Refused before it ran, so no step: its "error:" result is the failure.
    func testACallTheDaemonRefusedIsFailed() {
        let call = message(5, .verifier, .progress, "machine_click {\"element\":0}\nerror: machine_click needs an element id")
        let row = ToolCallRow.of(call, steps: [])
        XCTAssertEqual(row.state, .failed)
        XCTAssertNil(row.step)
        XCTAssertEqual(row.raw.last, ToolCallRow.Raw(label: "Error", text: "machine_click needs an element id"))
        // Words that merely mention an error are not a refusal.
        let fine = message(6, .verifier, .progress, "machine_exec {\"command\":\"ls\"}\nstep 6\nerror: none found", step: 6)
        XCTAssertEqual(ToolCallRow.of(fine, steps: []).state, .done)
    }

    func testNoMoreThanTwoFacts() {
        let call = message(3, .verifier, .progress, "machine_screenshot {}", step: 9)
        XCTAssertLessThanOrEqual(ToolCallRow.of(call, steps: [step(9, "machine_screenshot", ms: 5)]).facts.count, ToolCallRow.maxFacts)
    }

    /// The raw call is the tool, its input as laid-out JSON, and what came back without
    /// the step line the row already shows.
    func testTheRawCallIsToolInputAndResult() {
        let call = message(3, .verifier, .progress, "machine_input {\"actions\":[{\"type\":\"click\",\"x\":0.5}]}\nstep 7\n{\"ok\":true}", step: 7)
        let raw = ToolCallRow.of(call, steps: []).raw
        XCTAssertEqual(raw.map(\.label), ["Tool", "Input", "Result"])
        XCTAssertEqual(raw[0].text, "machine_input")
        XCTAssertEqual(raw[1].text, "{\n  \"actions\": [\n    {\n      \"type\": \"click\",\n      \"x\": 0.5\n    }\n  ]\n}")
        XCTAssertEqual(raw[2].text, "{\n  \"ok\": true\n}")
    }

    func testAnEmptyInputIsLeftOut() {
        let call = message(3, .verifier, .progress, "machine_screenshot {}\nstep 9\nThe screen shows it", step: 9)
        XCTAssertEqual(ToolCallRow.of(call, steps: []).raw.map(\.label), ["Tool", "Result"])
    }

    func testProgressThatIsNotAToolCallIsItsWords() {
        let call = message(3, .verifier, .progress, "Looking at the screen now")
        let row = ToolCallRow.of(call, steps: [])
        XCTAssertEqual(row.raw, [ToolCallRow.Raw(label: "Message", text: "Looking at the screen now")])
        XCTAssertNil(row.step)
        XCTAssertTrue(row.facts.isEmpty)
    }

    // MARK: - Folding

    func testAShortRunOfCallsShowsWhole() {
        XCTAssertEqual(ToolCallFold.visible([1, 2, 3, 4], open: false).calls, [1, 2, 3, 4])
        XCTAssertEqual(ToolCallFold.visible([1, 2, 3, 4], open: false).hidden, 0)
    }

    func testALongRunFoldsToItsLastFew() {
        let fold = ToolCallFold.visible(Array(1...9), open: false)
        XCTAssertEqual(fold.calls, [7, 8, 9])
        XCTAssertEqual(fold.hidden, 6)
        XCTAssertEqual(ToolCallFold.summary(hidden: 6, open: false), "6 earlier tool calls")
        XCTAssertEqual(ToolCallFold.visible(Array(1...9), open: true).calls.count, 9)
    }

    // MARK: - Questions

    func testAnUnansweredQuestionIsOpen() {
        let question = message(8, .verifier, .question, "Can you confirm?")
        XCTAssertEqual(QuestionCardState.of(question, in: [question]), QuestionCardState(open: true, result: nil))
    }

    func testAnAnsweredQuestionSaysWhoAnswered() {
        let question = message(8, .verifier, .question, "Can you confirm?")
        let byCoder = [question, message(9, .coder, .answer, "Take another screenshot", replyTo: 8)]
        XCTAssertEqual(QuestionCardState.of(question, in: byCoder, timeOfDay: time),
                       QuestionCardState(open: false, result: "Answered by the coding agent at 20:12, below"))
        let byYou = [question, message(9, .human, .answer, "Yes", replyTo: 8)]
        XCTAssertEqual(QuestionCardState.of(question, in: byYou, timeOfDay: time).result, "Answered by you at 20:12, below")
    }

    /// An answer to another question does not close this one.
    func testOnlyItsOwnAnswerClosesAQuestion() {
        let question = message(8, .verifier, .question, "Which one?")
        let other = message(9, .human, .answer, "That one", replyTo: 3)
        XCTAssertTrue(QuestionCardState.of(question, in: [question, other]).open)
    }

    // MARK: - The verdict card

    private func review(_ verdict: VerdictState, _ messages: [Message] = []) -> VerdictReview {
        VerdictReview.of(verdict, messages: messages, timeOfDay: time)
    }

    func testAProposedVerdictIsOutlinedInDimWithItsOutcomeInTheForeground() {
        let verdict = VerdictState(seq: 4, verdict: "pass", status: .proposed)
        let look = VerdictAppearance.of(verdict, review: review(verdict))
        XCTAssertEqual(look.edge, .dim)
        XCTAssertFalse(look.outcomeInColour)
        XCTAssertEqual(look.outcome, "✓ PASS")
        XCTAssertNil(look.result)
    }

    func testAContestedVerdictIsStillOpen() {
        let verdict = VerdictState(seq: 4, verdict: "fail", status: .contested, disputes: 2)
        let look = VerdictAppearance.of(verdict, review: review(verdict))
        XCTAssertEqual(look.edge, .dim)
        XCTAssertFalse(look.outcomeInColour)
        XCTAssertEqual(look.outcome, "✗ FAIL")
    }

    func testAVerdictYouAcceptedTakesItsColourAndShowsTheResult() {
        let verdict = VerdictState(seq: 4, verdict: "fail", status: .accepted, acceptedBy: .human)
        let messages = [message(4, .verifier, .verdict, verdict: "fail"), message(5, .human, .accept, replyTo: 4)]
        let look = VerdictAppearance.of(verdict, review: review(verdict, messages))
        XCTAssertEqual(look.edge, .outcome)
        XCTAssertTrue(look.outcomeInColour)
        XCTAssertEqual(look.result, "✓ You accepted this fail verdict at 20:12. It is closed.")
    }

    /// ADR 0003: the coding agent agreeing with its own verifier keeps the word, not the colour.
    func testAnAgentAcceptedVerdictKeepsItsWordNotItsColour() {
        let verdict = VerdictState(seq: 4, verdict: "pass", status: .accepted, acceptedBy: .coder)
        let look = VerdictAppearance.of(verdict, review: review(verdict))
        XCTAssertEqual(look.edge, .hairline)
        XCTAssertFalse(look.outcomeInColour)
        XCTAssertEqual(look.outcome, "✓ PASS")
        // It still has an action (a re-check), so no result takes the actions' place.
        XCTAssertNil(look.result)
    }

    /// A rejected verdict's outcome is one nobody stands behind: its word, no colour.
    func testARejectedVerdictKeepsItsWordAndSaysSo() {
        let verdict = VerdictState(seq: 4, verdict: "pass", status: .rejected)
        let messages = [message(4, .verifier, .verdict, verdict: "pass"), message(5, .human, .dispute, "no", replyTo: 4)]
        let look = VerdictAppearance.of(verdict, review: review(verdict, messages))
        XCTAssertEqual(look.edge, .hairline)
        XCTAssertFalse(look.outcomeInColour)
        XCTAssertEqual(look.result, "✗ You rejected this pass verdict at 20:12.")
    }

    /// Inconclusive has no colour to take, so its edge stays quiet even once accepted.
    func testAnAcceptedInconclusiveVerdictHasNoColouredEdge() {
        let verdict = VerdictState(seq: 4, verdict: "inconclusive", status: .accepted, acceptedBy: .human)
        let look = VerdictAppearance.of(verdict, review: review(verdict))
        XCTAssertEqual(look.edge, .hairline)
        XCTAssertEqual(look.outcome, "? INCONCLUSIVE")
    }

    // MARK: - The verdict in the history

    func testTheLatestVerdictPointsAtTheCardInItsWords() {
        let said = message(4, .verifier, .verdict, "It works", verdict: "pass")
        let verdict = VerdictState(seq: 4, verdict: "pass", status: .proposed)
        let line = VerdictLine.of(said, current: verdict, review: review(verdict))
        XCTAssertEqual(line.title, "✓ Verdict: Pass")
        XCTAssertEqual(line.note, "needs review, in full above")
        XCTAssertTrue(line.latest)
        XCTAssertFalse(line.outcomeInColour)
    }

    func testTheLatestVerdictTakesColourOnlyOnceYouAcceptedIt() {
        let said = message(4, .verifier, .verdict, "It works", verdict: "pass")
        let verdict = VerdictState(seq: 4, verdict: "pass", status: .accepted, acceptedBy: .human)
        let line = VerdictLine.of(said, current: verdict, review: review(verdict))
        XCTAssertTrue(line.outcomeInColour)
        XCTAssertEqual(line.note, "you accepted, in full above")
    }

    func testAnEarlierVerdictIsSuperseded() {
        let said = message(4, .verifier, .verdict, "Broken", verdict: "fail")
        let current = VerdictState(seq: 15, verdict: "pass", status: .accepted, acceptedBy: .coder)
        let line = VerdictLine.of(said, current: current, review: review(current))
        XCTAssertEqual(line.title, "✗ Earlier verdict: Fail")
        XCTAssertEqual(line.note, "superseded")
        XCTAssertFalse(line.latest)
        XCTAssertFalse(line.outcomeInColour)
    }
}
