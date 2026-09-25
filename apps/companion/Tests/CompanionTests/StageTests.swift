import XCTest

@testable import Companion

/// The stage's pure rules (layer 4): the loader's motion and timer, the steps as a thinking
/// trace, what the screen's well says with no picture, and the scrubber's step ticks.
final class LoaderMotionTests: XCTestCase {
    private let tokens = DesignData.shared.tokens.motion.loader

    func testTheTokensAreTheDriveLoader() {
        XCTAssertEqual(tokens.grid, 3)
        XCTAssertEqual(tokens.cycleMs, 650)
        XCTAssertEqual(tokens.stepMs, 90)
    }

    /// A chevron pointing right: the middle row leads, the outer rows trail it by a step,
    /// and each column is a step later than the one before.
    func testTheWavefrontIsAChevronDrivingRight() {
        let delays = (0..<3).map { row in (0..<3).map { LoaderMotion.delayMs(row: row, column: $0, grid: 3, stepMs: 90) } }
        XCTAssertEqual(delays, [[90, 180, 270], [0, 90, 180], [90, 180, 270]])
    }

    func testACellRestsUntilItsFirstPulse() {
        XCTAssertNil(LoaderMotion.phase(elapsedMs: 50, delayMs: 90, cycleMs: 650))
        XCTAssertEqual(LoaderMotion.level(phase: nil), LoaderMotion.rest)
        XCTAssertEqual(LoaderMotion.level(row: 0, column: 2, elapsedMs: 100, tokens: tokens, frozen: false), LoaderMotion.rest)
    }

    func testThePulseFollowsPixelOnsKeyframes() {
        XCTAssertEqual(LoaderMotion.level(phase: 0), LoaderMotion.rest, accuracy: 1e-9)
        XCTAssertEqual(LoaderMotion.level(phase: 0.18), 1, accuracy: 1e-9)
        XCTAssertEqual(LoaderMotion.level(phase: 0.3), 1, accuracy: 1e-9)
        XCTAssertEqual(LoaderMotion.level(phase: 0.62), LoaderMotion.rest, accuracy: 1e-9)
        XCTAssertEqual(LoaderMotion.level(phase: 0.9), LoaderMotion.rest, accuracy: 1e-9)
        // Rising, then falling, never past the ends.
        XCTAssertGreaterThan(LoaderMotion.level(phase: 0.1), LoaderMotion.rest)
        XCTAssertLessThan(LoaderMotion.level(phase: 0.1), 1)
        XCTAssertLessThan(LoaderMotion.level(phase: 0.55), LoaderMotion.level(phase: 0.45))
    }

    func testThePulseRepeatsEveryCycle() {
        let first = LoaderMotion.phase(elapsedMs: 100, delayMs: 0, cycleMs: 650)
        let later = LoaderMotion.phase(elapsedMs: 100 + 650 * 3, delayMs: 0, cycleMs: 650)
        XCTAssertEqual(first!, later!, accuracy: 1e-9)
    }

    /// The cycle is shorter than the sweep, so two fronts are in flight at once.
    func testTwoFrontsAreInFlight() {
        let sweep = LoaderMotion.delayMs(row: 0, column: 2, grid: 3, stepMs: tokens.stepMs)
        XCTAssertLessThan(tokens.cycleMs, sweep + tokens.cycleMs)
        let lit = (0..<3).flatMap { row in (0..<3).map { column in
            LoaderMotion.level(row: row, column: column, elapsedMs: 5000 + 30, tokens: tokens, frozen: false)
        } }.filter { $0 > 0.9 }
        XCTAssertFalse(lit.isEmpty)
    }

    /// Reduce Motion: the grid freezes at rest.
    func testFrozenEveryCellRests() {
        for row in 0..<3 {
            for column in 0..<3 {
                XCTAssertEqual(LoaderMotion.level(row: row, column: column, elapsedMs: 1234, tokens: tokens, frozen: true),
                               LoaderMotion.rest)
            }
        }
    }

    func testTheShimmerBandEntersAndLeavesTheLabel() {
        XCTAssertLessThan(LoaderMotion.shimmerCenter(elapsed: 0), 0)
        XCTAssertGreaterThan(LoaderMotion.shimmerCenter(elapsed: LoaderMotion.shimmerPeriod * 0.999), 1)
        XCTAssertEqual(LoaderMotion.shimmerCenter(elapsed: 0.3), LoaderMotion.shimmerCenter(elapsed: 0.3 + LoaderMotion.shimmerPeriod),
                       accuracy: 1e-9)
    }

    func testElapsedIsZeroPaddedMinutesAndSeconds() {
        XCTAssertEqual(LoaderMotion.elapsed(0), "00:00")
        XCTAssertEqual(LoaderMotion.elapsed(7.9), "00:07")
        XCTAssertEqual(LoaderMotion.elapsed(63), "01:03")
        XCTAssertEqual(LoaderMotion.elapsed(754), "12:34")
        XCTAssertEqual(LoaderMotion.elapsed(3723), "1:02:03")
        XCTAssertEqual(LoaderMotion.elapsed(-5), "00:00")
    }

    func testElapsedInWords() {
        XCTAssertEqual(LoaderMotion.spokenElapsed(0), "0 seconds")
        XCTAssertEqual(LoaderMotion.spokenElapsed(1), "1 second")
        XCTAssertEqual(LoaderMotion.spokenElapsed(63), "1 minute 3 seconds")
        XCTAssertEqual(LoaderMotion.spokenElapsed(7200), "2 hours")
    }
}

final class StepTraceTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_000_000)

    private func step(_ seq: Int, error: String? = nil, exit: Int? = nil, command: String = "swift test") -> Step {
        var output: JSONValue?
        if let exit { output = .object(["exitCode": .int(exit)]) }
        return Step(seq: seq, at: now, tool: "machine_exec", input: .object(["command": .string(command)]),
                    output: output, error: error, durationMs: 640)
    }

    static func facts(_ phase: RunFacts.Phase, turn: RunFacts.Turn = .nobody, ready: Bool = true,
                      last: Date = Date(timeIntervalSince1970: 999_000)) -> RunFacts {
        RunFacts(phase: phase, turn: turn, started: Date(timeIntervalSince1970: 990_000), ended: nil, lastActivity: last,
                 lastStep: nil, stepCount: 0, failures: [], messageCount: 0, verdict: nil, machineReady: ready,
                 verifierListens: true)
    }

    func testAStepThatEndedWellIsDone() {
        XCTAssertEqual(StepTrace.state(of: step(1)), .done)
        XCTAssertEqual(StepTrace.state(of: step(1, exit: 0)), .done)
        XCTAssertEqual(StepTrace.glyph(.done), "✓")
    }

    func testAFailureCarriesItsWord() {
        XCTAssertEqual(StepTrace.state(of: step(1, exit: 2)), .failed(word: "exit 2"))
        XCTAssertEqual(StepTrace.state(of: step(1, error: "timed out")), .failed(word: "error"))
        XCTAssertEqual(StepTrace.glyph(.failed(word: "exit 2")), "✗")
        XCTAssertEqual(StepTrace.spoken(.failed(word: "exit 2")), "errored, exit 2")
    }

    func testTheRunningStateTicksInsteadOfAGlyph() {
        XCTAssertNil(StepTrace.glyph(.running))
        XCTAssertEqual(StepTrace.spoken(.running), "working")
    }

    func testARowReadsInPlainWordsWithAtMostTwoFacts() {
        let steps = [step(1), step(2, exit: 1, command: "rm -rf build")]
        let row = StepTrace.row(for: steps[1], in: steps, playhead: 2)
        XCTAssertEqual(row.seq, 2)
        XCTAssertEqual(row.words, "Ran rm -rf build")
        XCTAssertEqual(row.state, .failed(word: "exit 1"))
        XCTAssertTrue(row.failed)
        XCTAssertEqual(row.duration, "640 ms")
        XCTAssertTrue(row.atPlayhead)
        XCTAssertTrue(row.risky)
        let other = StepTrace.row(for: steps[0], in: steps, playhead: 2)
        XCTAssertFalse(other.atPlayhead)
        XCTAssertFalse(other.failed)
    }

    /// Only the verifier's turn on a live machine runs; nothing else ticks.
    func testOnlyTheVerifiersTurnOnALiveMachineIsWorking() {
        let working = StepTrace.working(Self.facts(.live, turn: .verifier))
        XCTAssertEqual(working?.words, "The verifier is working")
        XCTAssertEqual(working?.since, Date(timeIntervalSince1970: 999_000))
        XCTAssertNil(StepTrace.working(Self.facts(.live, turn: .coder)))
        XCTAssertNil(StepTrace.working(Self.facts(.live, turn: .you("question"))))
        XCTAssertNil(StepTrace.working(Self.facts(.idle, turn: .verifier)))
        XCTAssertNil(StepTrace.working(Self.facts(.booting, turn: .verifier)))
        XCTAssertNil(StepTrace.working(Self.facts(.ended(.finished), turn: .verifier)))
    }

    func testTheTrackHasTheSameStatesPerCell() {
        let steps = [step(1), step(2, exit: 1), step(3)]
        let cells = StepTrace.cells(steps, current: 3, working: true)
        XCTAssertEqual(cells.map(\.state), [.done, .failed(word: "exit 1"), .done, .running])
        XCTAssertEqual(cells.map(\.current), [false, false, true, false])
        XCTAssertEqual(cells.last?.seq, nil)
        XCTAssertEqual(StepTrace.cells(steps, current: nil, working: false).count, 3)
    }

    func testTheSummaryCountsErrors() {
        XCTAssertEqual(StepTrace.summary(count: 26, failures: 0), "26 steps")
        XCTAssertEqual(StepTrace.summary(count: 1, failures: 1), "1 step, 1 errored")
    }
}

final class WellStateTests: XCTestCase {
    private let frame = Frame(at: Date(timeIntervalSince1970: 0), file: "0.jpg", step: 0)

    private func facts(_ phase: RunFacts.Phase, ready: Bool = false) -> RunFacts {
        StepTraceTests.facts(phase, ready: ready)
    }

    func testABootingMachineShowsTheLoader() {
        XCTAssertEqual(WellState.of(facts: facts(.booting), frames: nil, connecting: false), .booting)
    }

    func testFramesMeanAPictureIsOnItsWay() {
        XCTAssertEqual(WellState.of(facts: facts(.live, ready: true), frames: [frame], connecting: true), .loadingFrame)
    }

    func testConnectingWithNoRecordingShowsTheLoader() {
        XCTAssertEqual(WellState.of(facts: facts(.live, ready: true), frames: [], connecting: true), .connecting)
    }

    func testTheStageTicksBeforeTheFramesAreRead() {
        XCTAssertEqual(WellState.of(facts: facts(.ended(.finished)), frames: nil, connecting: false), .reading)
    }

    func testAMachineThatFailedSaysSo() {
        XCTAssertEqual(WellState.of(facts: facts(.failed("no image")), frames: [], connecting: false), .failedToStart("no image"))
    }

    func testAReadyMachineWaitsForItsFirstFrame() {
        XCTAssertEqual(WellState.of(facts: facts(.live, ready: true), frames: [], connecting: false), .waitingForFirstFrame)
    }

    func testAnEndedRunWithNoFramesHasNoRecording() {
        XCTAssertEqual(WellState.of(facts: facts(.ended(.finished)), frames: [], connecting: false), .noFrames)
    }
}

final class StepTickTests: XCTestCase {
    private let epoch = Date(timeIntervalSince1970: 1_700_000_000)

    private func frames(_ steps: [Int]) -> [Frame] {
        steps.enumerated().map { Frame(at: epoch.addingTimeInterval(Double($0.offset) * 2), file: "\($0.offset).jpg", step: $0.element) }
    }

    func testEveryStepBoundaryHasAPlainTick() {
        let list = frames([0, 0, 1, 1, 2, 2])
        let ticks = FrameTimeline(count: list.count, width: 1000).stepTicks(for: list, failed: [])
        XCTAssertEqual(ticks.map(\.step), [1, 2])
        XCTAssertEqual(ticks.map(\.failed), [false, false])
    }

    func testAFailedStepIsTicked() {
        let list = frames([0, 0, 1, 1, 2, 2])
        let ticks = FrameTimeline(count: list.count, width: 1000).stepTicks(for: list, failed: [2])
        XCTAssertEqual(ticks.map(\.failed), [false, true])
    }

    /// Steps 2 and 3 ran between two captures: the frames jump from 1 to 4, yet the
    /// failure at 3 still gets its tick, where "show step 3" would land.
    func testAFailureWithNoFrameOfItsOwnStillGetsATick() {
        let list = frames([0, 1, 1, 4, 4])
        let timeline = FrameTimeline(count: list.count, width: 1000)
        let ticks = timeline.stepTicks(for: list, failed: [3])
        let failed = ticks.filter(\.failed)
        XCTAssertEqual(failed.map(\.step), [3])
        XCTAssertEqual(failed.first?.x, timeline.x(of: 3))
        // The plain tick at step 4's boundary sat on the same spot: the failure replaces it.
        XCTAssertFalse(ticks.contains { $0.step == 4 })
    }

    /// Crowded, plain ticks give way; failures never do.
    func testAPlainTickNeverHidesAFailure() {
        let list = frames(Array(0..<400))
        let timeline = FrameTimeline(count: list.count, width: 200)
        let failed: Set<Int> = [101, 250, 399]
        let ticks = timeline.stepTicks(for: list, failed: failed, minGap: 5)
        XCTAssertEqual(Set(ticks.filter(\.failed).map(\.step)), failed)
        for (earlier, later) in zip(ticks, ticks.dropFirst()) {
            XCTAssertGreaterThanOrEqual(later.x - earlier.x, 5 - 1e-9)
        }
    }

    func testFailuresCrowdedTogetherMergeIntoTheFirst() {
        let list = frames(Array(0..<400))
        let ticks = FrameTimeline(count: list.count, width: 200).stepTicks(for: list, failed: [100, 101, 102], minGap: 5)
        XCTAssertEqual(ticks.filter(\.failed).map(\.step), [100])
    }

    func testNoTrackNoTicks() {
        XCTAssertTrue(FrameTimeline(count: 0, width: 200).stepTicks(for: [], failed: [1]).isEmpty)
        let one = frames([3])
        XCTAssertTrue(FrameTimeline(count: 1, width: 200).stepTicks(for: one, failed: [3]).isEmpty)
    }
}
