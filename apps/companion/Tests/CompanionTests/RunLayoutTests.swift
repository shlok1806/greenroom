import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The window's width and height rules on small screens (issues #52, #55, #64). A
/// 1024 x 768 screen (every greenroom guest) has a 1024 x 678 visible frame; its default
/// window is 1024 x 660, whose content under the toolbar is about 608 tall.
@MainActor
final class RunLayoutTests: XCTestCase {
    // MARK: - #64: the first window fits the screen

    func testDefaultWindowFitsASmallScreen() {
        XCTAssertEqual(RunLayout.defaultWindowSize(visible: CGSize(width: 1024, height: 678)),
                       CGSize(width: 1024, height: 678))
    }

    func testDefaultWindowIsTheSpecsOnALargeScreen() {
        XCTAssertEqual(RunLayout.defaultWindowSize(visible: CGSize(width: 2560, height: 1415)),
                       CGSize(width: 1320, height: 840))
    }

    func testDefaultWindowIsNeverUnderTheMinimum() {
        XCTAssertEqual(RunLayout.defaultWindowSize(visible: CGSize(width: 800, height: 500)),
                       RunLayout.windowMinimum)
    }

    // MARK: - #55: the stage keeps the spec's minimum

    func testStageMinimumIsTheSpecs() {
        XCTAssertGreaterThanOrEqual(RunLayout.stageMinimum, 440)
    }

    /// Whatever the detail's width, a conversation beside the stage leaves the stage its minimum.
    func testConversationNeverTakesTheStagesMinimum() {
        for chosen in [RunLayout.conversationMinimum, RunLayout.conversationIdeal, RunLayout.conversationMaximum] {
            for detail in stride(from: 500.0, through: 1600, by: 1) {
                guard let conversation = RunLayout.conversation(chosen, in: detail) else { continue }
                XCTAssertGreaterThanOrEqual(detail - RunLayout.divider - conversation, RunLayout.stageMinimum,
                                            "detail \(detail), chosen \(chosen)")
                XCTAssertGreaterThanOrEqual(conversation, RunLayout.conversationMinimum)
            }
        }
    }

    /// A 1024 pt window folds its sidebar first, so both the stage and the conversation fit.
    func testA1024WindowFoldsTheSidebarAndKeepsTheConversation() {
        XCTAssertGreaterThan(RunLayout.sidebarFoldWidth(showsConversation: true), 1024)
        XCTAssertEqual(RunLayout.conversation(RunLayout.conversationIdeal, in: 1024), RunLayout.conversationIdeal)
        // Without the conversation the stage fits beside the sidebar.
        XCTAssertLessThanOrEqual(RunLayout.sidebarFoldWidth(showsConversation: false), 1024)
    }

    /// The sidebar shown by hand in a 1024 window: the conversation gives way, not the stage.
    func testTheConversationGivesWayToASidebarShownByHand() {
        let detail = 1024 - RunLayout.sidebarIdeal - RunLayout.divider
        XCTAssertNil(RunLayout.conversation(RunLayout.conversationIdeal, in: detail))
    }

    func testAnUnmeasuredDetailKeepsTheChosenWidth() {
        XCTAssertEqual(RunLayout.conversation(420, in: 0), 420)
        XCTAssertEqual(RunLayout.conversation(2000, in: 0), RunLayout.conversationMaximum)
    }

    // MARK: - #52: the verdict card is capped in its column

    func testTheVerdictCardTakesAtMostItsShareOfTheColumn() {
        XCTAssertNil(RunLayout.verdictCardMaximum(column: 0))
        let cap = try! XCTUnwrap(RunLayout.verdictCardMaximum(column: 608))
        XCTAssertLessThanOrEqual(cap, 608 * RunLayout.verdictCardShare)
    }

    func testTheVerdictBodyScrollsInWhatTheCapLeaves() {
        // Short: all of it.
        XCTAssertEqual(RunLayout.verdictBody(natural: 120, card: 360, chrome: 150), 120)
        // Two cited pictures: what is left after the headline and actions.
        XCTAssertEqual(RunLayout.verdictBody(natural: 800, card: 360, chrome: 150), 210)
        // A very short column still leaves the reasons a strip to scroll.
        XCTAssertEqual(RunLayout.verdictBody(natural: 800, card: 100, chrome: 150), RunLayout.verdictBodyMinimum)
        // No cap: natural height.
        XCTAssertEqual(RunLayout.verdictBody(natural: 800, card: nil, chrome: 150), 800)
    }

    /// The conversation column with the evidence of a verdict citing two screenshots
    /// open must fit a 1024 x 660 window's column, not push the window's content away.
    func testConversationWithOpenEvidenceFitsA660Window() {
        let store = Self.storeWithCitedVerdict()
        store.updateVerdictDraft("run-1") { $0.expanded = true }
        let host = NSHostingController(rootView: ConversationView(store: store, runId: "run-1"))
        let column = CGSize(width: 380, height: 608)
        host.view.frame = CGRect(origin: .zero, size: column)
        host.view.layoutSubtreeIfNeeded()
        let size = host.sizeThatFits(in: column)
        XCTAssertLessThanOrEqual(size.height, column.height + 1, "the column needs \(size.height) pt")
    }

    // MARK: - #52: opening the evidence is per verdict, never saved

    func testEvidenceStartsFoldedWhateverAnOlderBuildSaved() {
        UserDefaults.standard.set(true, forKey: "verdictCardExpanded")
        defer { UserDefaults.standard.removeObject(forKey: "verdictCardExpanded") }
        let store = Self.storeWithCitedVerdict()
        XCTAssertFalse(store.verdictDraft("run-1").expanded)
    }

    func testOpeningOneVerdictsEvidenceOpensNoOther() {
        let store = Self.storeWithCitedVerdict()
        store.runs.append(RunSummary(runId: "run-2", createdAt: Date(timeIntervalSince1970: 0), status: .finished,
                                     verdict: VerdictState(seq: 3, verdict: "pass", evidence: ["step 1"], status: .proposed)))
        store.updateVerdictDraft("run-1") { $0.expanded = true }
        XCTAssertTrue(store.verdictDraft("run-1").expanded)
        XCTAssertFalse(store.verdictDraft("run-2").expanded)

        // A new verdict on the same run starts folded.
        store.runs[0].verdict = VerdictState(seq: 9, verdict: "pass", evidence: ["step 2"], status: .proposed)
        XCTAssertFalse(store.verdictDraft("run-1").expanded)

        // A relaunch is a new store: nothing is open.
        XCTAssertFalse(Self.storeWithCitedVerdict().verdictDraft("run-1").expanded)
    }

    // MARK: - Fixture

    /// A finished run whose proposed fail verdict cites two screenshot steps.
    private static func storeWithCitedVerdict() -> RunStore {
        let store = RunStore()
        let start = Date(timeIntervalSince1970: 1_000_000)
        let claims = "TipSplit builds and launches, but the split is wrong. At step 2 the bill is $180.00 with an 18% tip "
            + "and 4 people, and the screen reads Each pays: $45.00 where it should read $53.10. At step 3, after changing "
            + "the tip to 20%, it still reads $45.00, so the tip is never added."
        let verdict = VerdictState(seq: 2, verdict: "fail", summary: claims, evidence: ["step 2", "step 3"], status: .proposed)
        store.runs = [RunSummary(runId: "run-1", createdAt: start, destroyedAt: start.addingTimeInterval(60),
                                 status: .finished, verdict: verdict)]
        store.messages["run-1"] = [
            Message(seq: 1, at: start, from: .coder, kind: .task, text: "Check TipSplit's split."),
            Message(seq: 2, at: start.addingTimeInterval(40), from: .verifier, kind: .verdict, text: claims,
                    verdict: "fail", evidence: ["step 2", "step 3"]),
        ]
        store.steps["run-1"] = (1...3).map { seq in
            Step(seq: seq, at: start.addingTimeInterval(Double(seq * 10)), tool: "machine_screenshot",
                 durationMs: 800)
        }
        return store
    }
}
