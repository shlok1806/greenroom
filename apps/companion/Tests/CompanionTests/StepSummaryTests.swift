import XCTest

@testable import Companion

/// A hundred and thirty rows reading `machine_input` are a log. These are the
/// rules that turn them back into a timeline.
final class StepSummaryTests: XCTestCase {
    private func step(_ tool: String, input: JSONValue?, output: JSONValue? = nil) -> Step {
        Step(seq: 1, at: Date(timeIntervalSince1970: 0), tool: tool, input: input, output: output)
    }

    func testACommandIsItsOwnSummary() {
        let line = StepSummary.line(for: step("machine_exec", input: .object(["command": .string("go build ./...")])))
        XCTAssertEqual(line, "go build ./...")
    }

    /// A command that wraps in a shell must not make the row two lines high.
    func testACommandIsFlattenedOntoOneLine() {
        let line = StepSummary.line(for: step("machine_exec", input: .object(["command": .string("cat <<EOF\nhello\nEOF")])))
        XCTAssertEqual(line, "cat <<EOF hello EOF")
    }

    /// The app speaks fractions (ADR 0009), so the evidence reads in
    /// percentages rather than in pixels it never knew.
    func testAClickReadsAsAPlaceOnTheScreen() {
        let actions = JSONValue.array([
            .object(["type": .string("click"), "x": .double(0.35), "y": .double(0.4)])
        ])
        XCTAssertEqual(StepSummary.line(for: step("machine_input", input: .object(["actions": actions]))), "click 35%, 40%")
    }

    func testTypingShowsWhatWasTyped() {
        let actions = JSONValue.array([
            .object(["type": .string("type"), "text": .string("hi shlok")])
        ])
        XCTAssertEqual(StepSummary.line(for: step("machine_input", input: .object(["actions": actions]))), "type \"hi shlok\"")
    }

    func testLongTypingIsClipped() {
        let typed = String(repeating: "a", count: 80)
        let actions = JSONValue.array([
            .object(["type": .string("type"), "text": .string(typed)])
        ])
        let line = StepSummary.line(for: step("machine_input", input: .object(["actions": actions])))
        XCTAssertTrue(line.hasSuffix("…\""), line)
        XCTAssertLessThanOrEqual(line.count, 40)
    }

    func testAShortcutKeepsItsModifiers() {
        let actions = JSONValue.array([
            .object(["type": .string("key"), "key": .string("a"), "mods": .array([.string("cmd")])])
        ])
        XCTAssertEqual(StepSummary.line(for: step("machine_input", input: .object(["actions": actions]))), "key cmd-a")
    }

    /// A batch is one step (ADR 0009). The first action is spelled out and the
    /// rest are counted, because that is the grain a reviewer reads.
    func testABatchSpellsOutTheFirstActionAndCountsTheRest() {
        let actions = JSONValue.array([
            .object(["type": .string("down"), "x": .double(0.1), "y": .double(0.2)]),
            .object(["type": .string("move"), "x": .double(0.5), "y": .double(0.5)]),
            .object(["type": .string("up"), "x": .double(0.5), "y": .double(0.5)]),
        ])
        XCTAssertEqual(StepSummary.line(for: step("machine_input", input: .object(["actions": actions]))), "down 10%, 20% +2 more")
    }

    func testAnEmptyBatchSaysSo() {
        XCTAssertEqual(StepSummary.line(for: step("machine_input", input: .object(["actions": .array([])]))), "no actions")
    }

    func testAScreenshotIsNamedByItsArtifact() {
        let step = step(
            "machine_screenshot",
            input: .object([:]),
            output: .object(["path": .string("/runs/abc/016-screenshot.png")])
        )
        XCTAssertEqual(StepSummary.line(for: step), "016-screenshot.png")
    }

    func testACreateNamesItsImage() {
        XCTAssertEqual(StepSummary.line(for: step("machine_create", input: .object(["image": .string("greenroom-base")]))), "greenroom-base")
    }

    /// A tool the app has never met still has to say something.
    func testAnUnknownToolFallsBackToCompactJson() {
        let line = StepSummary.line(for: step("machine_future", input: .object(["b": .int(2), "a": .int(1)])))
        XCTAssertEqual(line, "{\"a\":1,\"b\":2}")
    }

    func testAStepWithNoInputSaysNothing() {
        XCTAssertEqual(StepSummary.line(for: step("machine_boot", input: nil)), "")
    }
}
