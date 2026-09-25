import XCTest

@testable import Companion

/// The verdict-lands moment's pure parts (ADR 0006 moment 3): the decode's frames, when a
/// verdict counts as landed, where a fail moves focus, Reduce Motion and the announcement.
final class VerdictDecodeTests: XCTestCase {
    private let maxMs = 300
    private let frameMs = 30

    private func word(_ cells: [DecodeCell]) -> String { String(cells.map(\.character)) }

    /// The last frame is the final word, every cell settled, exactly at the budget.
    func testTheDecodeSettlesToTheWordWithinItsBudget() {
        for text in ["✓ PASS", "✗ FAIL", "? INCONCLUSIVE"] {
            let frames = VerdictDecode.frames(text, maxMs: maxMs, frameMs: frameMs, seed: 29)
            XCTAssertEqual(frames.count, maxMs / frameMs + 1)
            let last = try? XCTUnwrap(frames.last)
            XCTAssertEqual(last.map(word), text)
            XCTAssertTrue(last?.allSatisfy(\.settled) ?? false, text)
            // Just short of the budget the last letter is still scrambling.
            let before = VerdictDecode.frame(text, elapsedMs: Double(maxMs) - 1, maxMs: maxMs, frameMs: frameMs, seed: 29)
            XCTAssertFalse(before.last?.settled ?? true, text)
        }
    }

    /// Letters settle left to right, never unsettle, and the first frame has none settled.
    func testLettersSettleLeftToRight() {
        let frames = VerdictDecode.frames("✗ FAIL", maxMs: maxMs, frameMs: frameMs, seed: 7)
        var settledBefore = 0
        for frame in frames {
            let letters = frame.dropFirst(2)
            let settled = letters.prefix { $0.settled }.count
            XCTAssertEqual(letters.filter(\.settled).count, settled, "settled letters are a prefix")
            XCTAssertGreaterThanOrEqual(settled, settledBefore)
            settledBefore = settled
        }
        XCTAssertEqual(frames.first?.dropFirst(2).filter(\.settled).count, 0)
        XCTAssertEqual(VerdictDecode.settlesAt(index: 0, count: 4, maxMs: 300), 75)
        XCTAssertEqual(VerdictDecode.settlesAt(index: 3, count: 4, maxMs: 300), 300)
    }

    /// The mark and the space stand from the first frame: the word is never without its mark.
    func testTheMarkNeverScrambles() {
        for frame in VerdictDecode.frames("✓ PASS", maxMs: maxMs, frameMs: frameMs, seed: 3) {
            XCTAssertEqual(frame[0], DecodeCell(character: "✓", settled: true))
            XCTAssertEqual(frame[1], DecodeCell(character: " ", settled: true))
        }
    }

    /// A scrambling cell shows a glyph from the set (one advance in the mono face), never
    /// its own final letter, which would read as settled early.
    func testAScramblingCellIsAnotherGlyph() {
        let text = "✓ PASS"
        let final = Array(text)
        for seed in UInt64(0)..<50 {
            for frame in VerdictDecode.frames(text, maxMs: maxMs, frameMs: frameMs, seed: seed) {
                for (index, cell) in frame.enumerated() where !cell.settled {
                    XCTAssertTrue(VerdictDecode.glyphs.contains(cell.character))
                    XCTAssertTrue(cell.character.isASCII)
                    XCTAssertNotEqual(cell.character, final[index])
                }
            }
        }
    }

    /// The same seed gives the same frames; another seed another scramble; frames change.
    func testTheDecodeIsDeterministicGivenASeed() {
        let one = VerdictDecode.frames("✗ FAIL", maxMs: maxMs, frameMs: frameMs, seed: 16)
        XCTAssertEqual(one, VerdictDecode.frames("✗ FAIL", maxMs: maxMs, frameMs: frameMs, seed: 16))
        XCTAssertNotEqual(one, VerdictDecode.frames("✗ FAIL", maxMs: maxMs, frameMs: frameMs, seed: 17))
        XCTAssertNotEqual(one[0].map(\.character), one[1].map(\.character), "the scramble moves each frame")
        // Within one frame the time does not matter, only which frame it is.
        XCTAssertEqual(VerdictDecode.frame("✗ FAIL", elapsedMs: 31, maxMs: maxMs, frameMs: frameMs, seed: 16),
                       VerdictDecode.frame("✗ FAIL", elapsedMs: 59, maxMs: maxMs, frameMs: frameMs, seed: 16))
    }

    /// The timings are the design data's, inside ADR 0006's budgets.
    func testTheTimingsAreTheTokensWithinBudget() {
        let motion = DesignData.shared.tokens.motion
        XCTAssertLessThanOrEqual(motion.decode.maxMs, 300)
        XCTAssertLessThanOrEqual(motion.draw.maxMs, 250)
        XCTAssertGreaterThan(motion.decode.frameMs, 0)
        XCTAssertLessThan(motion.decode.frameMs, motion.decode.maxMs)
        XCTAssertEqual(VerdictLanding.duration(motion), Double(max(motion.decode.maxMs, motion.draw.maxMs)) / 1000)
    }
}

final class VerdictLandingTests: XCTestCase {
    private func onScreen(_ seq: Int?, run: String = "run-1") -> VerdictLanding.OnScreen {
        VerdictLanding.OnScreen(runId: run, seq: seq)
    }

    /// A verdict whose message came in live, past the one on screen, lands.
    func testANewVerdictWhileTheRunIsOpenLands() {
        XCTAssertTrue(VerdictLanding.lands(onScreen: onScreen(nil), runId: "run-1", current: 16, arrivedLive: 16))
        XCTAssertTrue(VerdictLanding.lands(onScreen: onScreen(9), runId: "run-1", current: 16, arrivedLive: 16))
    }

    /// Opening a run that already has a verdict: nothing was on screen for it yet, or the
    /// view still names the run it showed before.
    func testOpeningARunWithAVerdictDoesNotLand() {
        XCTAssertFalse(VerdictLanding.lands(onScreen: nil, runId: "run-1", current: 16, arrivedLive: nil))
        XCTAssertFalse(VerdictLanding.lands(onScreen: onScreen(3, run: "other"), runId: "run-1", current: 16, arrivedLive: 16))
        // The list catching up after the run opened: the verdict was never a live message.
        XCTAssertFalse(VerdictLanding.lands(onScreen: onScreen(nil), runId: "run-1", current: 16, arrivedLive: nil))
    }

    /// A resync reads the transcript whole: a verdict it finds did not arrive live, and one
    /// that did is not past what is on screen any more.
    func testAResyncReplayDoesNotLand() {
        XCTAssertFalse(VerdictLanding.lands(onScreen: onScreen(9), runId: "run-1", current: 16, arrivedLive: 9))
        XCTAssertFalse(VerdictLanding.lands(onScreen: onScreen(16), runId: "run-1", current: 16, arrivedLive: 16))
        XCTAssertFalse(VerdictLanding.lands(onScreen: onScreen(16), runId: "run-1", current: nil, arrivedLive: 16))
    }

    /// A fail moves focus to the first failing step, else to the step it cites first.
    func testAFailMovesFocusToTheFirstFailingStep() {
        XCTAssertEqual(VerdictLanding.focus(outcome: "fail", failures: [4, 9], cited: 12, typing: false, driving: false), .firstFailure)
        XCTAssertEqual(VerdictLanding.focus(outcome: "fail", failures: [], cited: 12, typing: false, driving: false), .cited(12))
        XCTAssertNil(VerdictLanding.focus(outcome: "fail", failures: [], cited: nil, typing: false, driving: false))
        XCTAssertNil(VerdictLanding.focus(outcome: "pass", failures: [4], cited: 12, typing: false, driving: false))
        XCTAssertNil(VerdictLanding.focus(outcome: "inconclusive", failures: [4], cited: 12, typing: false, driving: false))
    }

    /// Never while the person types (the composer keeps the keys) or drives the screen.
    func testFocusStaysPutWhileTypingOrDriving() {
        XCTAssertNil(VerdictLanding.focus(outcome: "fail", failures: [4], cited: 12, typing: true, driving: false))
        XCTAssertNil(VerdictLanding.focus(outcome: "fail", failures: [4], cited: 12, typing: false, driving: true))
        let plan = VerdictLanding.plan(outcome: "fail", failures: [4], cited: nil, reduceMotion: false, typing: true, driving: false)
        XCTAssertNil(plan.focus)
        XCTAssertTrue(plan.plays, "the moment still plays; only focus stays")
    }

    /// Reduce Motion: no scramble and no draw, the card shows at once; focus still moves
    /// and VoiceOver still hears the outcome.
    func testReduceMotionShowsTheCardAtOnce() {
        let plan = VerdictLanding.plan(outcome: "fail", failures: [4, 9], cited: nil, reduceMotion: true, typing: false, driving: false)
        XCTAssertFalse(plan.plays)
        XCTAssertEqual(plan.focus, .firstFailure)
        XCTAssertEqual(plan.announcement, "Verdict: Fail, 2 steps failed")
        XCTAssertTrue(VerdictLanding.plan(outcome: "pass", failures: [], cited: nil, reduceMotion: false,
                                          typing: false, driving: false).plays)
    }

    /// The final outcome in words, once: never the scramble.
    func testTheAnnouncementIsTheFinalOutcome() {
        XCTAssertEqual(VerdictLanding.announcement(outcome: "fail", failedSteps: 2), "Verdict: Fail, 2 steps failed")
        XCTAssertEqual(VerdictLanding.announcement(outcome: "fail", failedSteps: 1), "Verdict: Fail, 1 step failed")
        XCTAssertEqual(VerdictLanding.announcement(outcome: "fail", failedSteps: 0), "Verdict: Fail")
        XCTAssertEqual(VerdictLanding.announcement(outcome: "pass", failedSteps: 3), "Verdict: Pass")
    }
}

/// The store marks a verdict as live only when its message comes through the stream.
@MainActor
final class LiveVerdictTests: XCTestCase {
    private func store() -> RunStore {
        let store = RunStore()
        store.runs = [RunSummary(runId: "run-1", createdAt: Date(timeIntervalSince1970: 0), status: .ready)]
        store.messages["run-1"] = [Message(seq: 1, at: Date(timeIntervalSince1970: 1), from: .human, kind: .task, text: "check it")]
        return store
    }

    private func verdict(_ seq: Int) -> Message {
        Message(seq: seq, at: Date(timeIntervalSince1970: Double(seq)), from: .verifier, kind: .verdict, text: "broken",
                verdict: "fail")
    }

    func testAVerdictMessageFromTheStreamIsLive() {
        let store = store()
        XCTAssertNil(store.liveVerdicts["run-1"])
        store.apply(.message(runId: "run-1", message: verdict(2)))
        XCTAssertEqual(store.liveVerdicts["run-1"], 2)
        // Another kind of message leaves it.
        store.apply(.message(runId: "run-1", message: Message(seq: 3, at: Date(timeIntervalSince1970: 3), from: .verifier,
                                                              kind: .note, text: "more")))
        XCTAssertEqual(store.liveVerdicts["run-1"], 2)
    }

    /// A repeat out of a reconnect, a transcript read whole (open, resync) and a run that
    /// is not open never mark one.
    func testReplaysAndReadsAreNotLive() {
        let store = store()
        store.messages["run-1"]?.append(verdict(2))
        store.apply(.message(runId: "run-1", message: verdict(2)))
        XCTAssertNil(store.liveVerdicts["run-1"])
        store.apply(.message(runId: "other", message: verdict(5)))
        XCTAssertNil(store.liveVerdicts["other"])
    }
}
