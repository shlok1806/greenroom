import XCTest

@testable import Companion

/// What the old window knew about a step that the redesign keeps (redesign 7's parity): a
/// command that exited non-zero failed, as a tool error does, everywhere a failure shows.
final class StepParityTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 3_000_000)

    private func exec(_ seq: Int, exit code: Int, command: String = "swift build") -> Step {
        var step = Step(seq: seq, at: t0.addingTimeInterval(Double(seq)), tool: "machine_exec",
                        input: .object(["command": .string(command)]))
        step.output = .object(["exitCode": .int(code)])
        step.durationMs = 1200
        return step
    }

    func testANonZeroExitIsAFailureWithItsCode() {
        XCTAssertFalse(exec(1, exit: 0).failed)
        XCTAssertNil(exec(1, exit: 0).failure)
        XCTAssertTrue(exec(2, exit: 1).failed)
        XCTAssertEqual(exec(2, exit: 1).failure, "exited with code 1")
        var errored = exec(3, exit: 0)
        errored.error = "timed out after 600 s"
        XCTAssertEqual(errored.failure, "timed out after 600 s")
    }

    func testTheChipSaysTheExitCodeAndFlagsARiskyCommand() {
        let steps = [exec(1, exit: 2, command: "rm -rf build")]
        let chip = ActivityLayout.chip(steps[0], in: steps)
        XCTAssertEqual(chip.state, .error("exit 2"))
        XCTAssertTrue(chip.risky)
        XCTAssertFalse(ActivityLayout.chip(exec(2, exit: 0), in: steps).risky)
    }

    func testActivityAndTheConversationCountAnExitAsFailed() {
        let steps = [exec(1, exit: 0), exec(2, exit: 1)]
        let sections = ActivityLayout.sections(messages: [], steps: steps, checks: [], working: false)
        XCTAssertEqual(sections.flatMap(\.rows).map(\.glyph), [.passed, .failed])
        XCTAssertEqual(ConversationLayout.doneLabel(steps), "2 tool calls, 1 failed, 0:02")
    }
}
