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

    // MARK: - Scope

    func testAVerdictWithNoChecksHasNoScope() {
        XCTAssertNil(Checklist(checks: []).scope)
    }

    func testEveryCheckPassed() {
        XCTAssertEqual(Checklist(checks: [check("a", .pass), check("b", .pass)]).scope, "Verified: 2 of 2 checks")
        XCTAssertEqual(Checklist(checks: [check("a", .pass)]).scope, "Verified: 1 of 1 check")
    }

    func testTheScopeNamesWhatWasNotChecked() {
        let list = Checklist(checks: [
            check("a", .pass), check("b", .pass), check("c", .pass),
            check("d", .unchecked, criterion: "Each pays becomes $50.00 at 25%"),
        ])
        XCTAssertEqual(list.scope, "Verified: 3 of 4 checks; not checked: Each pays becomes $50.00 at 25%")
    }

    func testFailuresComeBeforeWhatWasNotChecked() {
        let list = Checklist(checks: [
            check("a", .pass), check("b", .fail),
            check("c", .unchecked, criterion: "C"), check("d", .unchecked, criterion: "D"),
        ])
        XCTAssertEqual(list.scope, "Verified: 1 of 4 checks; failed: 1; not checked: C; D")
        XCTAssertEqual(list.passed, 1)
        XCTAssertEqual(list.failed, 1)
        XCTAssertEqual(list.unchecked.map(\.id), ["c", "d"])
    }

    /// Past two, the rows name them; the scope counts them.
    func testManyUncheckedAreCounted() {
        let list = Checklist(checks: [check("a", .pass)] + (1...3).map { check("u\($0)", .unchecked) })
        XCTAssertEqual(list.scope, "Verified: 1 of 4 checks; not checked: 3")
    }

    func testALongCriterionIsClippedAtAWordInTheScope() {
        let long = "The Bill field accepts decimal amounts such as 84.50 and keeps two decimal places in every total"
        let clipped = Checklist.clip(long)
        XCTAssertEqual(clipped, "The Bill field accepts decimal amounts such as 84.50 and...")
        XCTAssertLessThanOrEqual(clipped.count, 63)
        XCTAssertEqual(Checklist.clip("Short one"), "Short one")
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
