import XCTest

@testable import Companion

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

    // MARK: - Steps and marks

    func testAStepLandsOnItsFirstFrameOrTheLast() {
        let list = frames(50, stepEvery: 10)
        XCTAssertEqual(FrameTimeline.index(ofStep: 2, in: list), 20)
        XCTAssertEqual(FrameTimeline.index(ofStep: 0, in: list), 0)
        XCTAssertEqual(FrameTimeline.index(ofStep: 99, in: list), 49)
        XCTAssertNil(FrameTimeline.index(ofStep: 1, in: []))
    }

    func testEvidenceAndFailuresAreMarkedOncePerStep() {
        let list = frames(50, stepEvery: 10)
        let timeline = FrameTimeline(count: list.count, width: 490)
        let marks = timeline.marks(for: list, failed: [1, 3], evidence: [3, 4], verdict: "pass")
        XCTAssertEqual(marks.map(\.step), [1, 3, 4])
        XCTAssertEqual(marks.map(\.kind), [.failure, .evidence(verdict: "pass"), .evidence(verdict: "pass")])
        XCTAssertEqual(marks[0].x, timeline.x(of: 10))
    }

    func testAOneFrameRecordingHasNoMarks() {
        let list = frames(1)
        XCTAssertTrue(FrameTimeline(count: 1, width: 300).marks(for: list, failed: [0], evidence: [], verdict: nil).isEmpty)
    }

    // MARK: - Squeezed idle time

    /// Ten active frames then ninety idle ones: the active stretch gets most of the track.
    func testIdleStretchesAreSqueezed() {
        var list = frames(100, stepEvery: 1)
        for index in 10..<100 { list[index].step = list[9].step }
        let timeline = FrameTimeline(frames: list, width: 1000)
        XCTAssertGreaterThan(timeline.x(of: 9), 300)
        XCTAssertEqual(timeline.x(of: 99), 1000, accuracy: 0.001)
    }

    func testASqueezedTrackStillMapsBothWays() {
        var list = frames(60, stepEvery: 1)
        for index in 20..<60 { list[index].step = list[19].step }
        let timeline = FrameTimeline(frames: list, width: 600)
        var previous = -1.0
        for index in list.indices {
            let x = timeline.x(of: index)
            XCTAssertGreaterThan(x, previous)
            previous = x
            XCTAssertEqual(timeline.index(atX: x), index)
        }
    }
}
