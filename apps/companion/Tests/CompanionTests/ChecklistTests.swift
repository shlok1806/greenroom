import XCTest

@testable import Companion

/// A verdict as a checklist and the verifier's declared plan (root ADR 0024;
/// `Model/Checklist.swift`): decoding, the scope line, the marks, and the plan's fold.
final class ChecklistTests: XCTestCase {
    private func decode(_ json: String) throws -> Message {
        try JSONDecoder.daemon().decode(Message.self, from: Data(json.utf8))
    }

    private func check(_ id: String, _ status: AcceptanceCheck.Status, evidence: [Int] = [],
                       criterion: String? = nil) -> AcceptanceCheck {
        AcceptanceCheck(id: id, criterion: criterion ?? "Criterion \(id)", status: status, evidence: evidence)
    }

    // MARK: - Decoding

    func testAVerdictDecodesItsChecks() throws {
        let message = try decode("""
        {"seq": 16, "at": "2026-09-25T10:00:00Z", "from": "verifier", "kind": "verdict", "text": "Each pays is wrong.",
         "verdict": "fail", "evidence": ["artifacts/017-screenshot.png"],
         "checks": [
           {"id": "each-pays", "criterion": "Each pays shows the split amount", "status": "fail",
            "evidence": [12, 14], "actions": [11], "observed": "Each pays read $8.00 after entering 3 people"},
           {"id": "tip", "criterion": "Tip shows $24.00", "status": "pass", "evidence": [12], "actions": []}
         ]}
        """)
        XCTAssertEqual(message.checks.count, 2)
        XCTAssertEqual(message.checks[0], AcceptanceCheck(
            id: "each-pays", criterion: "Each pays shows the split amount", status: .fail,
            evidence: [12, 14], actions: [11], observed: "Each pays read $8.00 after entering 3 people"))
        XCTAssertNil(message.checks[1].observed)
        XCTAssertEqual(message.evidence, ["artifacts/017-screenshot.png"])
    }

    /// The declaring progress message sets only the id and the criterion.
    func testADeclaredCheckHasOnlyItsCriterion() throws {
        let message = try decode("""
        {"seq": 3, "at": "2026-09-25T10:00:00Z", "from": "verifier", "kind": "progress", "text": "declare_checks {}",
         "checks": [{"id": "each-pays", "criterion": "Each pays shows the split amount"}]}
        """)
        XCTAssertEqual(message.checks, [AcceptanceCheck(id: "each-pays", criterion: "Each pays shows the split amount")])
        XCTAssertEqual(message.checks[0].status, .unchecked)
        XCTAssertEqual(message.checks[0].evidence, [])
    }

    func testAnUnknownStatusReadsAsNotChecked() throws {
        let message = try decode("""
        {"seq": 1, "kind": "verdict", "checks": [{"id": "a", "criterion": "A", "status": "skipped"},
                                                 {"id": "b", "criterion": "B", "status": null}]}
        """)
        XCTAssertEqual(message.checks.map(\.status), [.unchecked, .unchecked])
    }

    func testStepNumbersDecodeLeniently() throws {
        let message = try decode("""
        {"seq": 1, "kind": "verdict", "checks": [{"id": "a", "criterion": "A", "status": "pass",
          "evidence": [12, "step 14", "15", 16.0, {"x": 1}, null], "actions": "none", "observed": "  "}]}
        """)
        XCTAssertEqual(message.checks[0].evidence, [12, 14, 15, 16])
        XCTAssertEqual(message.checks[0].actions, [])
        XCTAssertNil(message.checks[0].observed, "a blank sentence is no sentence")
    }

    /// Old messages have no checks and read exactly as before; a malformed list is dropped,
    /// never the message.
    func testAMessageWithoutChecksStillDecodes() throws {
        let old = try decode("""
        {"seq": 16, "at": "2026-09-23T06:00:00Z", "from": "verifier", "kind": "verdict", "text": "ok",
         "verdict": "pass", "evidence": ["step 11"]}
        """)
        XCTAssertEqual(old.checks, [])
        XCTAssertEqual(old.citedSteps, [11])
        let malformed = try decode("""
        {"seq": 17, "kind": "verdict", "text": "ok", "verdict": "pass", "checks": {"id": "a"}}
        """)
        XCTAssertEqual(malformed.checks, [])
        XCTAssertEqual(malformed.text, "ok")
    }

    // MARK: - Marks

    func testEachStatusHasAGlyphAColourAndAWord() {
        XCTAssertEqual(CheckMark.of(.pass), CheckMark(glyph: "✓", role: .pass, word: "Passed"))
        XCTAssertEqual(CheckMark.of(.fail), CheckMark(glyph: "✗", role: .failure, word: "Failed"))
        XCTAssertEqual(CheckMark.of(.unchecked), CheckMark(glyph: "○", role: .dim, word: "Not checked"))
        XCTAssertEqual(Set(AcceptanceCheck.Status.allCases.map { CheckMark.of($0).glyph }).count, 3)
    }

    // MARK: - Tally (companion ADR 0011)

    func testAVerdictWithNoChecksHasNoTally() {
        XCTAssertEqual(Checklist(checks: []).tally, [])
        XCTAssertNil(Checklist(checks: []).summary)
    }

    func testTheTallyCountsFailedThenNotCheckedThenPassedLeavingOutZero() {
        let list = Checklist(checks: [check("a", .pass), check("b", .fail), check("c", .unchecked),
                                      check("d", .unchecked), check("e", .pass)])
        XCTAssertEqual(list.tally.map(\.text), ["1 failed", "2 not checked", "2 passed"])
        XCTAssertEqual(list.summary, "1 failed, 2 not checked, 2 passed of 5 checks")
        XCTAssertEqual(Checklist(checks: [check("a", .pass)]).tally.map(\.text), ["1 passed"])
        XCTAssertEqual(Checklist(checks: [check("a", .pass)]).summary, "1 passed of 1 check")
        XCTAssertEqual(list.passed, 2)
        XCTAssertEqual(list.failed, 1)
        XCTAssertEqual(list.unchecked.map(\.id), ["c", "d"])
    }

    /// A run's row says the verdict's scope in a few characters (companion ADR 0012).
    func testTheRowTallySaysWhatFailedElseWhatWasNotCheckedElseAllPassed() {
        XCTAssertNil(Checklist(checks: []).rowTally)
        XCTAssertEqual(Checklist(checks: [check("a", .fail), check("b", .unchecked), check("c", .pass)]).rowTally, "1/3 failed")
        XCTAssertEqual(Checklist(checks: [check("a", .unchecked), check("b", .pass)]).rowTally, "1/2 unchecked")
        XCTAssertEqual(Checklist(checks: [check("a", .pass), check("b", .pass)]).rowTally, "2/2 passed")
    }

    func testTheRunListsVerdictCarriesItsChecks() throws {
        let state = try JSONDecoder.daemon().decode(VerdictState.self, from: Data("""
        {"seq": 7, "verdict": "fail", "status": "proposed",
         "checks": [{"id": "a", "criterion": "A", "kinds": ["visual"], "status": "fail", "evidence": [6, 7]}]}
        """.utf8))
        XCTAssertEqual(state.checks.map(\.id), ["a"])
        XCTAssertEqual(state.checks.first?.kinds, [.visual])
        let broken = try JSONDecoder.daemon().decode(VerdictState.self, from: Data("""
        {"seq": 7, "verdict": "fail", "status": "proposed", "checks": "nope"}
        """.utf8))
        XCTAssertEqual(broken.checks, [])
        XCTAssertEqual(broken.status, .proposed)
    }

    // MARK: - Evidence marks (companion ADR 0014)

    func testAnUnseenTextKnowsWhereItSits() {
        let read = uiRead(6, [["value": "Each pays: $49.56", "rendered": "blank"]])
        var step = read
        step.output = .object(["elements": .array([.object([
            "value": .string("Each pays: $49.56"), "rendered": .string("blank"),
            "x": .double(0.434), "y": .double(0.636), "w": .double(0.234), "h": .int(0),
        ]), .object([
            "value": .string("Tip: $15.12"), "rendered": .string("covered"),
            "x": .double(0.5), "y": .double(0.5), "w": .double(0.2), "h": .double(0.1),
        ])])])
        let unseen = UnseenText.all(in: step)
        XCTAssertNil(unseen[0].frame, "a frame with no height is no frame")
        let frame = try! XCTUnwrap(unseen[1].frame)
        XCTAssertEqual(frame.minX, 0.4, accuracy: 1e-9)
        XCTAssertEqual(frame.minY, 0.45, accuracy: 1e-9)
        XCTAssertEqual(frame.width, 0.2, accuracy: 1e-9)
        XCTAssertNil(UnseenText.all(in: read).first?.frame)
    }

    /// A read's outline carries onto later evidence only while nothing was typed or clicked.
    func testMarksCarryFromAReadToLaterEvidenceUntilAnInput() {
        func element(_ text: String) -> JSONValue {
            .object(["value": .string(text), "rendered": .string("blank"),
                     "x": .double(0.5), "y": .double(0.5), "w": .double(0.2), "h": .double(0.1)])
        }
        let steps = [
            Step(seq: 6, at: .epoch, tool: "machine_ui", output: .object(["elements": .array([element("Each pays: $49.56")])])),
            Step(seq: 7, at: .epoch, tool: "machine_screenshot"),
            Step(seq: 8, at: .epoch, tool: "machine_click"),
            Step(seq: 9, at: .epoch, tool: "machine_screenshot"),
        ]
        let check = AcceptanceCheck(id: "a", criterion: "Each pays is shown in bold", status: .fail, evidence: [6, 7, 9])
        XCTAssertEqual(check.unseenMarks(atStep: 6, in: steps).map(\.text), ["Each pays: $49.56"])
        XCTAssertEqual(check.unseenMarks(atStep: 7, in: steps).map(\.text), ["Each pays: $49.56"])
        XCTAssertEqual(check.unseenMarks(atStep: 9, in: steps), [], "a click came between")
        XCTAssertEqual(check.unseenMarks(atStep: 8, in: steps), [], "not this check's evidence")
        let other = AcceptanceCheck(id: "b", criterion: "Tip shows $15.12", status: .pass, evidence: [6, 7])
        XCTAssertEqual(other.unseenMarks(atStep: 7, in: steps), [])
    }

    // MARK: - Kinds (root ADR 0027)

    func testKindsDecodeFromTheListOrTheEarlySingleKind() throws {
        let message = try decode("""
        {"seq": 7, "kind": "verdict", "checks": [
          {"id": "a", "criterion": "A", "status": "fail", "kinds": ["visual"]},
          {"id": "b", "criterion": "B", "status": "fail", "kind": "timing", "within": 3},
          {"id": "c", "criterion": "C", "status": "pass", "kinds": ["value"]},
          {"id": "d", "criterion": "D", "status": "pass", "kinds": ["visual", "timing"], "within": 2.5},
          {"id": "e", "criterion": "E", "status": "pass", "kinds": 12, "within": "soon"},
          {"id": "f", "criterion": "F", "status": "pass", "kinds": ["colour"]}
        ]}
        """)
        XCTAssertEqual(message.checks.map(\.kinds), [[.visual], [.timing], [.value], [.visual, .timing], [], [.other("colour")]])
        XCTAssertEqual(message.checks.map(\.kindTag), ["visual", "timing, within 3 s", nil,
                                                       "visual · timing, within 2.5 s", nil, "colour"])
        XCTAssertNil(message.checks[4].within)
    }

    func testKindsSurviveAnEncodeAndDecode() throws {
        let check = AcceptanceCheck(id: "a", criterion: "A", status: .fail, evidence: [3], kinds: [.timing], within: 2)
        let data = try JSONEncoder().encode(check)
        XCTAssertEqual(try JSONDecoder.daemon().decode(AcceptanceCheck.self, from: data), check)
    }

    // MARK: - Selection

    func testAStepShowsTheSelectedCheckWhenItCitesItElseTheFirstInCardOrder() {
        let list = Checklist(checks: [check("p", .pass, evidence: [6, 7]), check("f", .fail, evidence: [7])])
        XCTAssertEqual(list.check(citing: 7)?.id, "f")
        XCTAssertEqual(list.check(citing: 7, preferring: "p")?.id, "p")
        XCTAssertEqual(list.check(citing: 6, preferring: "f")?.id, "p")
        XCTAssertNil(list.check(citing: 9))
    }

    func testNextAndPreviousCheckWalkTheCardOrderAndWrap() {
        let list = Checklist(checks: [check("p", .pass), check("f", .fail), check("u", .unchecked)])
        XCTAssertEqual(list.check(after: nil, by: 1)?.id, "f")
        XCTAssertEqual(list.check(after: nil, by: -1)?.id, "p")
        XCTAssertEqual(list.check(after: "f", by: 1)?.id, "u")
        XCTAssertEqual(list.check(after: "p", by: 1)?.id, "f")
        XCTAssertEqual(list.check(after: "f", by: -1)?.id, "p")
        XCTAssertEqual(list.check(after: "gone", by: 1)?.id, "f")
        XCTAssertNil(Checklist(checks: []).check(after: nil, by: 1))
    }

    // MARK: - Evidence named by what it is, and what is not drawn (root ADR 0027)

    private func uiRead(_ seq: Int, _ elements: [[String: String]]) -> Step {
        Step(seq: seq, at: .epoch, tool: "machine_ui",
             output: .object(["elements": .array(elements.map { .object($0.mapValues(JSONValue.string)) })]))
    }

    func testEvidenceIsNamedByItsTool() {
        let steps = [Step(seq: 7, at: .epoch, tool: "machine_screenshot"), uiRead(6, []),
                     Step(seq: 4, at: .epoch, tool: "machine_exec"), Step(seq: 5, at: .epoch, tool: "machine_click")]
        XCTAssertEqual([7, 6, 4, 5, 9].map { EvidenceStep.of($0, in: steps).label },
                       ["Screenshot 7", "UI read 6", "Command 4", "Step 5", "Step 9"])
        XCTAssertFalse(EvidenceStep.of(9, in: steps).held)
        XCTAssertTrue(EvidenceStep.of(7, in: steps).held)
    }

    func testAUIReadReportsTheTextAPersonCannotSee() {
        let read = uiRead(6, [
            ["role": "StaticText", "value": "Tip: $15.12"],
            ["role": "StaticText", "value": "Each pays: $49.56", "rendered": "blank"],
            ["role": "Button", "label": "Reset", "rendered": "covered"],
            ["role": "StaticText", "title": "Menu", "rendered": "offscreen"],
            ["role": "Group", "rendered": "blank"],
            ["role": "StaticText", "value": "Odd", "rendered": "shimmering"],
        ])
        XCTAssertEqual(UnseenText.all(in: read), [
            UnseenText(text: "Each pays: $49.56", why: .blank),
            UnseenText(text: "Reset", why: .covered),
            UnseenText(text: "Menu", why: .offscreen),
        ])
    }

    /// A read marks every unseen text on screen; a check hears only of what it is about.
    func testACheckIsWarnedOnlyOfUnseenTextItIsAbout() {
        let steps = [uiRead(6, [["value": "Each pays: $49.56", "rendered": "blank"],
                                ["value": "Reset", "rendered": "covered"]]),
                     Step(seq: 7, at: .epoch, tool: "machine_screenshot")]
        let byLabel = AcceptanceCheck(id: "a", criterion: "Each pays is shown in large bold text", status: .fail, evidence: [6, 7])
        XCTAssertEqual(byLabel.unseenWarnings(in: steps), ["UI read 6: \"Each pays: $49.56\" is not drawn"])
        let byValue = AcceptanceCheck(id: "b", criterion: "The line reads \"Each pays: $49.56\"", status: .pass, evidence: [6, 6])
        XCTAssertEqual(byValue.unseenWarnings(in: steps), ["UI read 6: \"Each pays: $49.56\" is not drawn"])
        let other = AcceptanceCheck(id: "c", criterion: "Tip shows $15.12", status: .pass, evidence: [6])
        XCTAssertEqual(other.unseenWarnings(in: steps), [])
    }

    // MARK: - Order and evidence

    func testTheCardListsFailuresThenNotCheckedThenPasses() {
        let list = Checklist(checks: [check("p1", .pass), check("u1", .unchecked), check("f1", .fail),
                                      check("p2", .pass), check("f2", .fail)])
        XCTAssertEqual(list.ordered.map(\.id), ["f1", "f2", "u1", "p1", "p2"])
    }

    func testCitedStepsAreTheChecksEvidenceFailuresFirstThenTheFreeListEachOnce() {
        let message = Message(seq: 9, at: .epoch, from: .verifier, kind: .verdict, text: "", verdict: "fail",
                              evidence: ["step 3", "step 12", "artifacts/017-screenshot.png"],
                              checks: [check("a", .pass, evidence: [12, 14]), check("b", .fail, evidence: [14, 16])])
        // A screenshot artifact is its step's evidence too (`Evidence.step`).
        // The failed check's first, then the passed one's, then the free list.
        XCTAssertEqual(message.citedSteps, [14, 16, 12, 3, 17])
        XCTAssertEqual(Checklist(checks: message.checks).firstFailedStep, 14)
        XCTAssertNil(Checklist(checks: [check("a", .fail)]).firstFailedStep, "a fail with no evidence cites nothing")
    }

    /// A verdict with checks cites its steps per check, so it is not "No evidence cited";
    /// a check citing a step the record lacks is caught like any cited step.
    func testTheRecordChecksCoverTheChecksEvidence() {
        let message = Message(seq: 9, at: .epoch, from: .verifier, kind: .verdict, text: "Tip is right.", verdict: "pass",
                              checks: [check("a", .pass, evidence: [2, 40])])
        let verdict = VerdictState(seq: 9, verdict: "pass", status: .proposed)
        let steps = [Step(seq: 2, at: .epoch, tool: "machine_ui")]
        let found = VerdictCheck.checks(verdict, messages: [message], steps: steps)
        XCTAssertFalse(found.contains(.noEvidence))
        XCTAssertTrue(found.contains(.missingStep(40)))
        let bare = VerdictCheck.checks(verdict, messages: [Message(seq: 9, at: .epoch, from: .verifier, kind: .verdict,
                                                                   text: "", verdict: "pass")], steps: steps)
        XCTAssertTrue(bare.contains(.noEvidence))
    }

    // MARK: - The plan

    private func plan(_ count: Int, seq: Int = 3) -> Message {
        Message(seq: seq, at: .epoch, from: .verifier, kind: .progress, text: "declare_checks {}",
                checks: (1...count).map { AcceptanceCheck(id: "c\($0)", criterion: "Criterion \($0)") })
    }

    func testAShortPlanShowsWhole() {
        let shown = CheckPlan.of(plan(1), open: false)
        XCTAssertEqual(shown, CheckPlan(title: "Plan: 1 check", criteria: ["Criterion 1"], hidden: 0))
        XCTAssertEqual(CheckPlan.of(plan(5), open: false)?.hidden, 0, "folding one away saves no room")
    }

    func testALongPlanFoldsToItsFirstFew() {
        let folded = CheckPlan.of(plan(12), open: false)
        XCTAssertEqual(folded?.title, "Plan: 12 checks")
        XCTAssertEqual(folded?.criteria.count, CheckPlan.shown)
        XCTAssertEqual(folded?.hidden, 8)
        XCTAssertEqual(CheckPlan.fold(hidden: 8, open: false), "8 more")
        let open = CheckPlan.of(plan(12), open: true)
        XCTAssertEqual(open?.criteria.count, 12)
        XCTAssertEqual(open?.hidden, 0)
    }

    func testOnlyAVerifierProgressMessageWithChecksIsAPlan() {
        XCTAssertTrue(CheckPlan.isPlan(plan(2)))
        var tool = plan(2)
        tool.checks = []
        XCTAssertFalse(CheckPlan.isPlan(tool))
        var verdict = plan(2)
        verdict.kind = .verdict
        XCTAssertFalse(CheckPlan.isPlan(verdict))
        XCTAssertNil(CheckPlan.of(verdict, open: false))
    }

    /// The plan is its own item, shown even while tool calls are hidden, and it splits the
    /// tool calls around it.
    func testThePlanIsItsOwnTranscriptItem() {
        let at = Date(timeIntervalSince1970: 1000)
        func call(_ seq: Int) -> Message {
            Message(seq: seq, at: at, from: .verifier, kind: .progress, text: "machine_ui {}\nstep \(seq)", step: seq)
        }
        var declared = plan(3, seq: 3)
        declared.at = at
        let messages = [call(2), declared, call(4), call(5)]
        let items = TranscriptLayout.items(messages, calendar: Calendar(identifier: .gregorian))
        XCTAssertEqual(items, [.toolCalls([call(2)]), .plan(declared), .toolCalls([call(4), call(5)])])
        XCTAssertEqual(items[1].id, 3)
        let hidden = TranscriptLayout.items(messages, toolCalls: false, calendar: Calendar(identifier: .gregorian))
        XCTAssertEqual(hidden, [.plan(declared)])
    }

    /// The daemon may answer a check by id alone; its words come from the plan declared
    /// before the verdict, the latest one when it was declared again.
    func testAVerdictAnsweringByIdTakesTheCriterionFromThePlan() {
        var first = plan(2, seq: 3)
        first.checks[0].criterion = "Old words"
        let second = plan(2, seq: 5)
        let verdict = Message(seq: 9, at: .epoch, from: .verifier, kind: .verdict, text: "", verdict: "pass",
                              checks: [AcceptanceCheck(id: "c1", criterion: "", status: .pass),
                                       AcceptanceCheck(id: "c2", criterion: "Its own words", status: .pass),
                                       AcceptanceCheck(id: "zz", criterion: "", status: .unchecked)])
        let list = Checklist.of(verdict, in: [first, second, verdict])
        XCTAssertEqual(list.checks.map(\.criterion), ["Criterion 1", "Its own words", ""])
        XCTAssertEqual(Checklist.of(nil, in: []).checks, [])
    }
}
