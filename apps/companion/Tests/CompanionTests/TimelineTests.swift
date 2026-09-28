import XCTest
@testable import Companion

/// The recording's timeline, the player's state in `ShellModel`, and the visible scroller
/// (redesign 7).
@MainActor
final class TimelineTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_790_000_000)

    private func at(_ seconds: TimeInterval) -> Date { t0.addingTimeInterval(seconds) }

    private func step(_ seq: Int, _ seconds: TimeInterval, tool: String = "machine_screenshot", error: String? = nil,
                      input: JSONValue? = nil, ms: Int = 500) -> Step {
        Step(seq: seq, at: at(seconds), tool: tool, input: input, output: nil, error: error, durationMs: ms)
    }

    private func frames(every interval: TimeInterval, from: TimeInterval, to: TimeInterval, step: (TimeInterval) -> Int = { _ in 1 }) -> [Frame] {
        stride(from: from, through: to, by: interval).map { s in Frame(at: at(s), file: "\(Int(s * 1000)).jpg", step: step(s)) }
    }

    /// Four minutes of work with a click by a person, an errored step and a failed check, then
    /// an hour where nothing happens.
    private func sample(live: Bool = false, now: TimeInterval = 4000) -> RecordingTimeline {
        let steps = [
            step(1, 5),
            step(2, 30, tool: "machine_input", input: .object(["holder": .string("human"),
                                                                "actions": .array([.object(["type": .string("click"), "x": .double(0.5), "y": .double(0.5)])])])),
            step(3, 60, tool: "machine_exec", error: "exit 1"),
            step(4, 120),
            step(5, 200),
        ]
        let checks = [
            SummaryCheck(id: "a", text: "Each pays is $48.00", state: .fail, step: 4),
            SummaryCheck(id: "b", text: "Tip is $24.00", state: .pass, step: 5),
            SummaryCheck(id: "c", text: "Window shows", state: .pass, step: 5),
        ]
        return RecordingTimeline.make(frames: frames(every: 2, from: 2, to: 3800), steps: steps, checks: checks,
                                      start: t0, live: live, now: at(now))
    }

    func testMarksSayWhatEachStepIs() {
        let t = sample()
        XCTAssertEqual(t.marks.map(\.kind), [.step, .human, .failure, .failure, .keyFrame])
        XCTAssertEqual(t.marks[3].check, 1, "a failed check's proof carries its number")
        XCTAssertEqual(t.marks[4].checks, [2, 3], "a step that proves two checks carries both")
        XCTAssertEqual(t.failures.map(\.step), [3, 4])
    }

    func testTheClockIsRealTimeAndIdleStretchesAreDrawnShort() {
        let t = sample()
        XCTAssertEqual(t.duration, 3800, accuracy: 0.01, "to the last frame on a finished run")
        // The quiet minute between steps 3 and 4, the one before step 5, and the hour after it.
        let idle = t.spans.filter { $0.kind == .idle }
        XCTAssertEqual(idle.count, 3)
        XCTAssertGreaterThan(idle.map(\.length).max() ?? 0, 3000)
        // Four minutes of work take most of the bar; the hour of nothing a sliver.
        XCTAssertGreaterThan(t.fraction(of: 200), 0.85)
        XCTAssertLessThan(t.fraction(of: 200), 1)
        // The mapping runs both ways and never goes backwards.
        var last = -1.0
        for s in stride(from: 0.0, through: 3800, by: 37) {
            let f = t.fraction(of: s)
            XCTAssertGreaterThanOrEqual(f, last)
            last = f
            XCTAssertEqual(t.seconds(atFraction: f), s, accuracy: 0.5)
        }
        XCTAssertEqual(TimelineWords.clock(102, of: 258), "1:42 / 4:18")
        XCTAssertEqual(TimelineWords.clock(59.9, of: 3800), "0:59 / 1:03:20")
    }

    func testALiveRunGrowsToNow() {
        let t = sample(live: true, now: 5000)
        XCTAssertTrue(t.live)
        XCTAssertEqual(t.duration, 5000, accuracy: 0.01)
        let later = sample(live: true, now: 5060)
        XCTAssertEqual(later.duration - t.duration, 60, accuracy: 0.01)
    }

    func testFramesAndStepsAtASecond() {
        let t = sample()
        XCTAssertEqual(t.frame(at: 61)?.file, "60000.jpg")
        XCTAssertEqual(t.frame(at: 0)?.file, "2000.jpg", "before the first frame, the first")
        XCTAssertEqual(t.step(at: 65), 3)
        XCTAssertEqual(t.seconds(ofStep: 4), 120)
        XCTAssertEqual(t.stepFrame(from: 61, by: 1), 62)
        XCTAssertEqual(t.stepFrame(from: 61, by: -1), 58)
        XCTAssertEqual(t.stepFrame(from: 3800, by: 5), 3800, "clamped at the end")
    }

    func testFailuresWrapBothWays() {
        let t = sample()
        XCTAssertEqual(t.nextFailure(after: nil)?.step, 3)
        XCTAssertEqual(t.nextFailure(after: 60)?.step, 4)
        XCTAssertEqual(t.nextFailure(after: 130)?.step, 3, "past the last failure, back to the first")
        XCTAssertEqual(t.previousFailure(before: 130)?.step, 4)
        XCTAssertEqual(t.previousFailure(before: 10)?.step, 4, "before the first, round to the last")
    }

    func testPlaybackSkipsIdleAndStopsAtTheEnd() throws {
        let t = sample()
        let idle = try XCTUnwrap(t.spans.last { $0.kind == .idle })
        XCTAssertEqual(t.advance(from: 10, by: 1, speed: 4), 14)
        XCTAssertEqual(t.advance(from: idle.from - 0.1, by: 1, speed: 1), idle.to, "an idle stretch is skipped")
        XCTAssertNil(t.advance(from: 3799, by: 2, speed: 1), "past the end")
    }

    func testAGapInTheFramesIsNoPicture() {
        var f = frames(every: 2, from: 0, to: 100)
        f += frames(every: 2, from: 160, to: 200)
        let steps = stride(from: 0.0, through: 200, by: 10).enumerated().map { step($0.offset + 1, $0.element) }
        let t = RecordingTimeline.make(frames: f, steps: steps, checks: [], start: t0, live: false, now: at(300))
        XCTAssertEqual(t.spans.filter { $0.kind == .noPicture }.map(\.from), [100])
        XCTAssertEqual(TimelineWords.label(t.spans[0]), "The screen sent no picture for 1:00")
    }

    func testPlainTicksGiveWayButMarksWorthFindingStay() {
        let many = (1...400).map { step($0, Double($0) * 0.5, error: $0 == 200 ? "boom" : nil) }
        let t = RecordingTimeline.make(frames: frames(every: 1, from: 0, to: 200), steps: many, checks: [], start: t0, live: false, now: at(300))
        let shown = t.visibleMarks(width: 400)
        XCTAssertTrue(shown.contains { $0.step == 200 && $0.kind == .failure })
        XCTAssertLessThan(shown.count, 150, "400 steps on 400 pt do not become a grey band")
    }

    func testHoverWordsNameTheStepAndWhatMakesItWorthFinding() {
        let t = sample()
        let steps = [step(3, 60, tool: "machine_exec", error: "exit 1\nmore")]
        let words = TimelineWords.label(t.marks[2], steps: steps, checks: [])
        XCTAssertTrue(words.hasPrefix("Step 3 · "))
        XCTAssertTrue(words.hasSuffix("failed: exit 1"))
        let check = TimelineWords.label(t.marks[3], steps: [], checks: [SummaryCheck(id: "a", text: "Each pays is $48.00", state: .fail)])
        XCTAssertEqual(check, "Step 4 · check 1 failed: Each pays is $48.00")
    }

    func testAnEmptyRecordIsEmpty() {
        XCTAssertTrue(RecordingTimeline.make(frames: [], steps: [], checks: [], start: nil, live: false, now: t0).isEmpty)
        let starting = RecordingTimeline.make(frames: [], steps: [], checks: [], start: t0, live: true, now: at(30))
        XCTAssertEqual(starting.duration, 30, accuracy: 0.01)
        XCTAssertNil(starting.frame(at: 10))
    }

    // MARK: - The scroller

    func testTheKnobSaysWhereTheListIs() {
        let m = ScrollMetrics(offset: 0, content: 64_000, viewport: 640)
        XCTAssertTrue(m.scrollable)
        XCTAssertEqual(m.knob(track: 600).height, 24, "a long list's knob stays big enough to grab")
        XCTAssertEqual(m.knob(track: 600).y, 0)
        let end = ScrollMetrics(offset: 64_000 - 640, content: 64_000, viewport: 640)
        XCTAssertEqual(end.knob(track: 600).y, 576, accuracy: 0.01)
        XCTAssertEqual(end.offset(forKnobTop: 288, track: 600), (64_000 - 640) / 2, accuracy: 0.5)
        XCTAssertEqual(ScrollMetrics(offset: 640, content: 64_000, viewport: 640).rows(rowHeight: 32, total: 2000), "21-40 of 2,000")
        XCTAssertFalse(ScrollMetrics(offset: 0, content: 300, viewport: 640).scrollable)
    }
}
