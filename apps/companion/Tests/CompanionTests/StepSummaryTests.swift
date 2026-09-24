import XCTest

@testable import Companion

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

    // MARK: - #56: UI reads and capture approvals

    /// The verifier's most frequent step: the app and how much of it was read, never
    /// the daemon's internal `reader`.
    func testAUIReadNamesTheAppAndItsElements() {
        let elements = JSONValue.array((1...20).map { .object(["id": .int($0), "role": .string("Button")]) })
        let read = step("machine_ui", input: .object(["limit": .int(200), "reader": .string("verifier")]),
                        output: .object(["app": .string("TipSplit"), "elements": elements]))
        XCTAssertEqual(StepSummary.line(for: read), "TipSplit, 20 elements")
        XCTAssertEqual(ToolCatalog.entry(for: "machine_ui").title, "Read UI")
    }

    func testAUIReadWithoutAnAnswerSaysWhichAppItAskedFor() {
        let named = step("machine_ui", input: .object(["limit": .int(200), "reader": .string("coder"), "app": .string("TextEdit")]))
        XCTAssertEqual(StepSummary.line(for: named), "TextEdit")
        let frontmost = step("machine_ui", input: .object(["limit": .int(200), "reader": .string("verifier")]))
        XCTAssertEqual(StepSummary.line(for: frontmost), "frontmost app")
    }

    func testACaptureApprovalNamesTheApp() {
        let approve = step("machine_approve_capture", input: .object(["app": .string("/System/Applications/TextEdit.app")]))
        XCTAssertEqual(StepSummary.line(for: approve), "TextEdit")
        XCTAssertEqual(ToolCatalog.entry(for: "machine_approve_capture").title, "Approve capture")
    }

    /// The single-action tools record the action's fields without a type; seen in the app
    /// as a Click row reading {"x":0.5,"y":0.41}.
    func testASingleActionToolReadsAsItsAction() {
        let click = step("machine_click", input: .object(["x": .double(0.5), "y": .double(0.41)]))
        XCTAssertEqual(StepSummary.line(for: click), "click 50%, 41%")
        let byElement = step("machine_click", input: .object(["element": .int(3)]))
        XCTAssertEqual(StepSummary.line(for: byElement), "click element 3")
        // The daemon reads element 0 as no element and clicks at x and y.
        let atPoint = step("machine_click", input: .object(["element": .int(0), "x": .double(0.5), "y": .double(0.41)]))
        XCTAssertEqual(StepSummary.line(for: atPoint), "click 50%, 41%")
        let key = step("machine_key", input: .object(["key": .string("a"), "mods": .array([.string("cmd")])]))
        XCTAssertEqual(StepSummary.line(for: key), "key cmd-a")
    }

    /// Seen in the app under a cited machine_ui step: {"limit":200,"reader":"verifier"}.
    func testAnEvidenceCaptionForAUIReadIsWords() {
        let read = step("machine_ui", input: .object(["limit": .int(200), "reader": .string("verifier")]),
                        output: .object(["app": .string("TipSplit"), "elements": .array([.object([:]), .object([:])])]))
        XCTAssertEqual(StepExcerpt.text(read), "TipSplit, 2 elements")
        let bare = step("machine_ui", input: .object(["limit": .int(200), "reader": .string("verifier")]))
        XCTAssertEqual(StepExcerpt.text(bare), "frontmost app")
    }

    /// Keys, scrolls and waits read as words when they record under their own name.
    func testEveryMachineToolHasATitle() {
        for tool in ["machine_key", "machine_scroll", "machine_wait", "machine_list", "machine_ui", "machine_approve_capture"] {
            XCTAssertFalse(ToolCatalog.entry(for: tool).title.contains("_"), tool)
        }
    }

    /// The fallback shows what was asked, not the daemon's bookkeeping.
    func testTheFallbackNeverShowsTheReader() {
        let line = StepSummary.line(for: step("machine_future", input: .object(["reader": .string("verifier"), "limit": .int(5)])))
        XCTAssertEqual(line, "{\"limit\":5}")
    }

    /// A tool-call row whose step is not held yet reads its progress message the same way.
    func testAProgressMessageIsSummarisedLikeItsStep() {
        XCTAssertEqual(StepSummary.line(ofProgress: "machine_ui {\"limit\":200}\n{\"app\":\"TipSplit\",\"elements\":[{\"id\":1},{\"id\":2}]}"),
                       "TipSplit, 2 elements")
        XCTAssertEqual(StepSummary.line(ofProgress: "machine_approve_capture {\"app\":\"/Applications/Tip.app\"}\nok"), "Tip")
        XCTAssertEqual(StepSummary.line(ofProgress: "machine_exec {\"command\":\"ls -la\"}\nexit 0"), "ls -la")
        XCTAssertEqual(StepSummary.line(ofProgress: "machine_screenshot {}\nstep 9"), "")
    }
}
