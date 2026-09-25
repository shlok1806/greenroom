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

/// Plain words first (ADR 0008): a step reads as a sentence a person would say.
final class StepPhraseTests: XCTestCase {
    private func step(_ seq: Int, _ tool: String, input: JSONValue?, output: JSONValue? = nil) -> Step {
        Step(seq: seq, at: Date(timeIntervalSince1970: 0), tool: tool, input: input, output: output)
    }

    private func actions(_ list: [JSONValue]) -> JSONValue { .object(["actions": .array(list), "holder": .string("verifier")]) }

    private func element(_ id: Int, _ role: String, x: Double, y: Double, w: Double, h: Double, depth: Int = 1,
                         extra: [String: JSONValue] = [:]) -> JSONValue {
        var fields: [String: JSONValue] = ["id": .int(id), "role": .string(role), "x": .double(x), "y": .double(y),
                                           "w": .double(w), "h": .double(h), "depth": .int(depth)]
        fields.merge(extra) { $1 }
        return .object(fields)
    }

    /// The TipSplit window as the daemon's `machine_ui` read it (seen in a real run).
    private var tipSplitRead: Step {
        step(5, "machine_ui", input: .object(["limit": .int(200), "reader": .string("verifier")]), output: .object([
            "app": .string("TipSplit"),
            "elements": .array([
                element(1, "Window", x: 0.5, y: 0.469, w: 0.43, h: 0.486, depth: 0, extra: ["title": .string("TipSplit")]),
                element(3, "StaticText", x: 0.325, y: 0.401, w: 0.018, h: 0.021, extra: ["value": .string("Bill")]),
                element(4, "TextField", x: 0.461, y: 0.402, w: 0.137, h: 0.031, extra: ["identifier": .string("bill"), "value": .string("84.00")]),
                element(6, "RadioGroup", x: 0.523, y: 0.456, w: 0.191, h: 0.031),
                element(9, "RadioButton", x: 0.548, y: 0.456, w: 0.047, h: 0.031, depth: 2, extra: ["label": .string("20%")]),
                element(11, "StaticText", x: 0.4, y: 0.512, w: 0.056, h: 0.021, extra: ["value": .string("People: 2")]),
                element(12, "Incrementor", x: 0.446, y: 0.512, w: 0.02, h: 0.034),
                element(13, "Button", x: 0.446, y: 0.504, w: 0.02, h: 0.017, depth: 2, extra: ["subrole": .string("IncrementArrow")]),
            ]),
        ]))
    }

    private func click(_ seq: Int, x: Double, y: Double) -> Step {
        step(seq, "machine_input", input: actions([.object(["type": .string("click"), "x": .double(x), "y": .double(y)])]))
    }

    func testAClickIsNamedByTheControlUnderIt() {
        let read = tipSplitRead
        XCTAssertEqual(StepSummary.phrase(for: click(6, x: 0.461, y: 0.402), in: [read]), "Clicked Bill field")
        XCTAssertEqual(StepSummary.phrase(for: click(7, x: 0.548, y: 0.456), in: [read]), "Clicked 20%")
        XCTAssertEqual(StepSummary.phrase(for: click(8, x: 0.446, y: 0.504), in: [read]), "Clicked People +")
    }

    /// Only a read from before the click names it; with none, the place in words.
    func testAClickWithNoEarlierReadSaysWhere() {
        XCTAssertEqual(StepSummary.phrase(for: click(4, x: 0.461, y: 0.402), in: [tipSplitRead]), "Clicked at 46%, 40%")
        XCTAssertEqual(StepSummary.phrase(for: click(6, x: 0.461, y: 0.402)), "Clicked at 46%, 40%")
    }

    func testTypingKeysAndCommandsReadAsSentences() {
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_input", input: actions([.object(["type": .string("type"), "text": .string("120")])]))),
                       "Typed 120")
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_input", input: actions([.object(["type": .string("key"), "key": .string("a"), "mods": .array([.string("cmd")])])]))),
                       "Pressed ⌘A")
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_input", input: actions([.object(["type": .string("key"), "key": .string("tab")])]))),
                       "Pressed Tab")
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_exec", input: .object(["command": .string("swift test")]))), "Ran swift test")
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_screenshot", input: nil)), "Took a screenshot")
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_key", input: .object(["key": .string("z"), "mods": .array([.string("shift"), .string("cmd")])]))),
                       "Pressed ⇧⌘Z")
    }

    func testMachineLifecycleStepsReadAsSentences() {
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_create", input: .object(["image": .string("greenroom-base")]))),
                       "Created a machine from greenroom-base")
        XCTAssertEqual(StepSummary.phrase(for: step(2, "machine_boot", input: nil)), "Booted the machine")
        XCTAssertEqual(StepSummary.phrase(for: step(3, "machine_sync", input: .object(["dest": .string("TipSplit")]))), "Copied TipSplit to the machine")
        XCTAssertEqual(StepSummary.phrase(for: tipSplitRead), "Read TipSplit")
        XCTAssertEqual(StepSummary.phrase(for: step(9, "machine_destroy", input: nil)), "Destroyed the machine")
    }

    func testABatchSaysItsFirstActionAndCountsTheRest() {
        let batch = step(1, "machine_input", input: actions([
            .object(["type": .string("click"), "x": .double(0.1), "y": .double(0.2)]),
            .object(["type": .string("type"), "text": .string("hi")]),
            .object(["type": .string("key"), "key": .string("return")]),
        ]))
        XCTAssertEqual(StepSummary.phrase(for: batch), "Clicked at 10%, 20%, then 2 more")
    }

    func testLongTypingIsClipped() {
        let typed = String(repeating: "a", count: 80)
        let phrase = StepSummary.phrase(for: step(1, "machine_input", input: actions([.object(["type": .string("type"), "text": .string(typed)])])))
        XCTAssertTrue(phrase.hasSuffix("…"), phrase)
        XCTAssertLessThanOrEqual(phrase.count, 40)
    }

    /// A tool the app has never met still says something, with its raw name.
    func testAnUnknownToolSaysItsNameAndWhatItWasGiven() {
        XCTAssertEqual(StepSummary.phrase(for: step(1, "machine_future", input: .object(["a": .int(1)]))), "Used machine_future: {\"a\":1}")
    }

    func testAProgressMessageReadsLikeItsStep() {
        XCTAssertEqual(StepSummary.phrase(ofProgress: "machine_exec {\"command\":\"swift test\"}\nexit 0"), "Ran swift test")
        XCTAssertEqual(StepSummary.phrase(ofProgress: "machine_screenshot {}\nstep 9"), "Took a screenshot")
        XCTAssertEqual(StepSummary.phrase(ofProgress: "machine_ui {\"limit\":200}\n{\"app\":\"TipSplit\",\"elements\":[]}"), "Read TipSplit")
        XCTAssertEqual(StepSummary.phrase(ofProgress: "Looking at the totals now"), "Looking at the totals now")
    }
}
