import XCTest

@testable import Companion

/// The timeline is arithmetic over a recording that can be two thousand frames
/// long and a track that is a few hundred points wide. Every one of these is a
/// rounding decision a screenshot cannot show, which is why they are pinned
/// here rather than tried in the window.
final class FrameTimelineTests: XCTestCase {
    private let epoch = Date(timeIntervalSince1970: 1_700_000_000)

    /// `count` frames, one every `every` seconds, with a new step every
    /// `stepEvery` frames.
    private func frames(_ count: Int, every: TimeInterval = 2, stepEvery: Int = 10) -> [Frame] {
        (0..<count).map { index in
            Frame(
                at: epoch.addingTimeInterval(Double(index) * every),
                file: "\(index).jpg",
                step: index / stepEvery
            )
        }
    }

    // MARK: - Positions

    func testTheFirstFrameIsTheLeftEdgeAndTheLastIsTheRight() {
        let timeline = FrameTimeline(count: 100, width: 400)
        XCTAssertEqual(timeline.x(of: 0), 0)
        XCTAssertEqual(timeline.x(of: 99), 400)
    }

    func testTheMiddleFrameIsTheMiddleOfTheTrack() {
        let timeline = FrameTimeline(count: 101, width: 400)
        XCTAssertEqual(timeline.x(of: 50), 200, accuracy: 0.001)
    }

    /// A recording of one frame has one position, not a division by zero.
    func testASingleFrameSitsAtTheLeftEdge() {
        let timeline = FrameTimeline(count: 1, width: 400)
        XCTAssertEqual(timeline.x(of: 0), 0)
        XCTAssertEqual(timeline.index(atX: 399), 0)
    }

    func testAnEmptyRecordingAnswersZero() {
        let timeline = FrameTimeline(count: 0, width: 400)
        XCTAssertTrue(timeline.isEmpty)
        XCTAssertEqual(timeline.x(of: 3), 0)
        XCTAssertEqual(timeline.index(atX: 200), 0)
    }

    func testAPointOnTheTrackPicksTheNearestFrame() {
        let timeline = FrameTimeline(count: 101, width: 400)
        XCTAssertEqual(timeline.index(atX: 0), 0)
        XCTAssertEqual(timeline.index(atX: 200), 50)
        XCTAssertEqual(timeline.index(atX: 400), 100)
    }

    /// A drag that leaves the track still has to land somewhere: the end it
    /// left by.
    func testAPointOffTheTrackClampsToTheEnds() {
        let timeline = FrameTimeline(count: 101, width: 400)
        XCTAssertEqual(timeline.index(atX: -80), 0)
        XCTAssertEqual(timeline.index(atX: 900), 100)
    }

    func testSeekingAndReadingBackAgreeOnEveryFrameOfALongRecording() {
        let timeline = FrameTimeline(count: 2385, width: 900)
        for index in stride(from: 0, to: 2385, by: 97) {
            XCTAssertEqual(timeline.index(atX: timeline.x(of: index)), index, "frame \(index)")
        }
    }

    // MARK: - Time

    func testOffsetIsMeasuredFromTheFirstFrameNotFromTheRun() {
        let list = frames(10)
        XCTAssertEqual(FrameTimeline.offset(list, at: 0), 0)
        XCTAssertEqual(FrameTimeline.offset(list, at: 5), 10)
        XCTAssertEqual(FrameTimeline.duration(list), 18)
    }

    func testAnEmptyRecordingHasNoDuration() {
        XCTAssertEqual(FrameTimeline.duration([]), 0)
        XCTAssertEqual(FrameTimeline.offset([], at: 4), 0)
    }

    func testAnIndexOffTheEndIsNotAnOffset() {
        XCTAssertEqual(FrameTimeline.offset(frames(3), at: 99), 0)
    }

    // MARK: - Ticks

    func testATickMarksEveryStepBoundary() {
        let list = frames(50, stepEvery: 10)
        let ticks = FrameTimeline(count: list.count, width: 1000).ticks(for: list)
        XCTAssertEqual(ticks.map(\.step), [1, 2, 3, 4])
        XCTAssertEqual(ticks.map(\.index), [10, 20, 30, 40])
    }

    /// Drawn naively, a few hundred steps across a few hundred points are a
    /// grey smear. Ticks closer than the gap are dropped so what is left is
    /// still a map.
    func testTicksTooCloseTogetherAreDropped() {
        let list = frames(400, stepEvery: 1)
        let timeline = FrameTimeline(count: list.count, width: 200)
        let ticks = timeline.ticks(for: list, minGap: 5)
        XCTAssertLessThanOrEqual(ticks.count, 41)
        for (earlier, later) in zip(ticks, ticks.dropFirst()) {
            XCTAssertGreaterThanOrEqual(later.x - earlier.x, 5)
        }
    }

    func testARecordingWithOneStepHasNoTicks() {
        let list = frames(40, stepEvery: 1000)
        XCTAssertTrue(FrameTimeline(count: list.count, width: 500).ticks(for: list).isEmpty)
    }

    func testAnEmptyRecordingHasNoTicks() {
        XCTAssertTrue(FrameTimeline(count: 0, width: 500).ticks(for: []).isEmpty)
    }
}
