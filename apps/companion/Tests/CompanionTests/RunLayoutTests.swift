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

    /// #64 after a relaunch: the restored frame, 1320 wide at x = -148, is put back on a
    /// 1024 x 678 screen; a frame that fits is left alone.
    func testARestoredFrameWiderThanTheScreenIsFittedToIt() {
        let visible = CGRect(x: 0, y: 25, width: 1024, height: 678)
        let fitted = RunLayout.fitted(frame: CGRect(x: -148, y: 30, width: 1320, height: 678), visible: visible)
        XCTAssertEqual(fitted, CGRect(x: 0, y: 25, width: 1024, height: 678))
        let fine = CGRect(x: 100, y: 60, width: 900, height: 600)
        XCTAssertEqual(RunLayout.fitted(frame: fine, visible: visible), fine)
    }

    /// Only the run window is fitted; a smaller panel or dialog would be grown to its minimum.
    func testOnlyTheRunWindowIsKeptOnScreen() {
        func window(_ id: String?, _ style: NSWindow.StyleMask = [.titled, .resizable], panel: Bool = false) -> NSWindow {
            let rect = CGRect(x: 0, y: 0, width: 260, height: 150)
            let window = panel
                ? NSPanel(contentRect: rect, styleMask: style, backing: .buffered, defer: true)
                : NSWindow(contentRect: rect, styleMask: style, backing: .buffered, defer: true)
            window.isReleasedWhenClosed = false
            window.identifier = id.map { NSUserInterfaceItemIdentifier($0) }
            return window
        }
        XCTAssertTrue(AppDelegate.isRunWindow(window("main")))
        XCTAssertTrue(AppDelegate.isRunWindow(window("main-AppWindow-1")))
        XCTAssertFalse(AppDelegate.isRunWindow(window(nil)))
        XCTAssertFalse(AppDelegate.isRunWindow(window("mainly")))
        XCTAssertFalse(AppDelegate.isRunWindow(window("main", panel: true)))
        XCTAssertFalse(AppDelegate.isRunWindow(window("main", [.titled])))
        XCTAssertFalse(AppDelegate.isRunWindow(window("main", [.borderless])))
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
                guard let conversation = PaneLayout.conversationWidth(chosen, in: detail) else { continue }
                XCTAssertGreaterThanOrEqual(detail - RunLayout.divider - conversation, RunLayout.stageMinimum,
                                            "detail \(detail), chosen \(chosen)")
                XCTAssertGreaterThanOrEqual(conversation, RunLayout.conversationMinimum)
            }
        }
    }

    /// At the narrowest medium window the runs fold to their strip, and the stage and the
    /// conversation both keep their room.
    func testTheNarrowestMediumWindowFitsTheStageAndTheConversation() {
        let tokens = DesignData.shared.tokens.layout
        let detail = tokens.mediumMinWidth - tokens.runsStripWidth - 1
        XCTAssertNotNil(PaneLayout.conversationWidth(RunLayout.conversationIdeal, in: detail))
    }

    /// At the narrowest wide window, the runs column at its widest still leaves the
    /// conversation room beside the stage.
    func testTheNarrowestWideWindowFitsTheWidestRunsColumn() {
        let tokens = DesignData.shared.tokens.layout
        let detail = tokens.wideMinWidth - tokens.runsMaxWidth - 1
        XCTAssertNotNil(PaneLayout.conversationWidth(RunLayout.conversationIdeal, in: detail))
    }

    func testAnUnmeasuredDetailKeepsTheChosenWidth() {
        XCTAssertEqual(PaneLayout.conversationWidth(420, in: 0), 420)
        XCTAssertEqual(PaneLayout.conversationWidth(2000, in: 0), RunLayout.conversationMaximum)
    }

    // MARK: - #52: the verdict card is capped in its column

    func testTheVerdictCardTakesAtMostItsShareOfTheColumn() {
        XCTAssertNil(RunLayout.verdictCardMaximum(column: 0))
        let cap = try! XCTUnwrap(RunLayout.verdictCardMaximum(column: 608))
        XCTAssertLessThanOrEqual(cap, 608 * RunLayout.verdictCardShare)
    }

    /// A verdict with checks waiting for review takes more of its column (companion ADR 0011).
    func testAVerdictUnderReviewTakesMoreOfTheColumn() {
        let column = 800.0
        let usual = try! XCTUnwrap(RunLayout.verdictCardMaximum(column: column))
        let review = try! XCTUnwrap(RunLayout.verdictCardMaximum(column: column, reviewing: true))
        XCTAssertGreaterThan(review, usual)
        XCTAssertLessThanOrEqual(review, column * RunLayout.verdictReviewShare)
        XCTAssertLessThanOrEqual(review, column - RunLayout.verdictReviewLeaves)
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

    /// #52 at every size, not only 1024 x 768: a verdict citing 14 steps, its evidence open,
    /// still fits a 1440 x 900 window's conversation column and a full-screen 2640 x 1680 one.
    func testOpenEvidenceWithManyCitesFitsLargeWindowsToo() {
        for column in [CGSize(width: 400, height: 780), CGSize(width: 720, height: 1560)] {
            let store = Self.storeWithCitedVerdict(cites: 14)
            store.updateVerdictDraft("run-1") { $0.expanded = true }
            let host = NSHostingController(rootView: ConversationView(store: store, runId: "run-1"))
            host.view.frame = CGRect(origin: .zero, size: column)
            host.view.layoutSubtreeIfNeeded()
            let size = host.sizeThatFits(in: column)
            XCTAssertLessThanOrEqual(size.height, column.height + 1, "a \(column) column needs \(size.height) pt")
        }
    }

    /// The key an older build saved the expansion under is removed at launch, so nothing
    /// that ever reads it again can bring the stuck state back.
    func testLaunchForgetsTheOldSavedExpansion() throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "RunLayoutTests-\(UUID())"))
        defaults.set(true, forKey: "verdictCardExpanded")
        LegacyDefaults.forget(in: defaults)
        XCTAssertNil(defaults.object(forKey: "verdictCardExpanded"))
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
    private static func storeWithCitedVerdict(cites: Int = 2) -> RunStore {
        let store = RunStore()
        let start = Date(timeIntervalSince1970: 1_000_000)
        let claims = "TipSplit builds and launches, but the split is wrong. At step 2 the bill is $180.00 with an 18% tip "
            + "and 4 people, and the screen reads Each pays: $45.00 where it should read $53.10. At step 3, after changing "
            + "the tip to 20%, it still reads $45.00, so the tip is never added."
        let cited = (2...(cites + 1)).map { "step \($0)" }
        let verdict = VerdictState(seq: 2, verdict: "fail", summary: claims, evidence: cited, status: .proposed)
        store.runs = [RunSummary(runId: "run-1", createdAt: start, destroyedAt: start.addingTimeInterval(60),
                                 status: .finished, verdict: verdict)]
        store.messages["run-1"] = [
            Message(seq: 1, at: start, from: .coder, kind: .task, text: "Check TipSplit's split."),
            Message(seq: 2, at: start.addingTimeInterval(40), from: .verifier, kind: .verdict, text: claims,
                    verdict: "fail", evidence: cited),
        ]
        store.steps["run-1"] = (1...(cites + 1)).map { seq in
            Step(seq: seq, at: start.addingTimeInterval(Double(seq * 10)), tool: "machine_screenshot",
                 durationMs: 800)
        }
        return store
    }
}
