import SwiftUI
import XCTest

@testable import Companion

/// Beautiful UI's agent entrances as the Companion draws them (redesign 7): every `fade-up`
/// and the Thinking label's `fade-in` held to the frames Chromium sampled for the originals
/// (docs/22 section 6.3: opacity within 0.02, translation within 0.5 pt, onset within a frame),
/// plus the pure rules around them: which items arrived, the conversation's tool-call groups,
/// inline Markdown and the app's own dropdown keys.
@MainActor
final class AgentAnimationTests: XCTestCase {
    private struct Spec: Decodable {
        struct Run: Decodable {
            var animations: [Animation]?
            var curves: [Row]?
        }

        struct Animation: Decodable {
            struct Keyframe: Decodable {
                var offset: Double
                var easing: String
                var opacity: String?
                var transform: String?
            }

            var name: String
            var target: String
            var bornAtMs: Double
            var durationMs: Double
            var delayMs: Double?
            var keyframes: [Keyframe]
        }

        struct Row: Decodable {
            struct Sample: Decodable {
                var target: String
                var opacity: Double?
                var transform: String?
            }

            var tMs: Double
            var sample: [Sample]
        }

        var runs: [Run]
    }

    private func spec(_ name: String) throws -> (animations: [Spec.Animation], rows: [Spec.Row]) {
        let url = MotionTests.specs.appendingPathComponent(name + ".json")
        let spec = try JSONDecoder().decode(Spec.self, from: Data(contentsOf: url))
        let run = try XCTUnwrap(spec.runs.first { $0.animations != nil && $0.curves != nil })
        return (run.animations ?? [], run.curves ?? [])
    }

    private static func translateY(_ transform: String?) -> Double {
        guard let transform, transform.hasPrefix("matrix("), transform.hasSuffix(")") else { return 0 }
        let parts = transform.dropFirst(7).dropLast().split(separator: ",").compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
        return parts.count == 6 ? parts[5] : 0
    }

    /// Holds our fade-up to every sampled frame of each of the spec's fade-up entrances that
    /// last `duration` (seconds), with the stagger the component uses.
    private func assertFadeUps(_ piece: String, duration: Double, stagger: Double?, file: StaticString = #filePath, line: UInt = #line) throws {
        let (animations, rows) = try spec(piece)
        let entrances = animations.filter { $0.name == "fade-up" && $0.durationMs == duration * 1000 }
        XCTAssertFalse(entrances.isEmpty, "no fade-up of \(duration) s in \(piece)", file: file, line: line)
        var compared = 0
        for animation in entrances {
            XCTAssertEqual(Curve(css: animation.keyframes[0].easing), AgentMotion.curve, file: file, line: line)
            XCTAssertEqual(animation.keyframes.first?.opacity, "0", file: file, line: line)
            XCTAssertEqual(animation.keyframes.first?.transform, "translateY(\(Int(AgentMotion.rise))px)", file: file, line: line)
            let delay = (animation.delayMs ?? 0) / 1000
            if let stagger, delay > 0 {
                // Delays in a batch are whole steps of the stagger (a detail's lines add 120 ms).
                let steps = delay / stagger
                XCTAssertTrue(abs(steps - steps.rounded()) < 1e-6 || piece == "task-rows", "\(piece) delay \(delay)", file: file, line: line)
            }
            let born = animation.bornAtMs / 1000
            for row in rows {
                let t = row.tMs / 1000 - born
                guard t >= delay, t <= delay + duration,
                      let sample = row.sample.first(where: { $0.target == animation.target }), let opacity = sample.opacity else { continue }
                // Onset within one frame: the browser starts the clock up to a frame after birth.
                let candidates = [0.0, -1 / 60.0, 1 / 60.0].map { AgentMotion.fadeUp(at: t + $0, duration: duration, delay: delay) }
                let best = candidates.min { abs($0.opacity - opacity) < abs($1.opacity - opacity) }!
                XCTAssertEqual(best.opacity, opacity, accuracy: 0.02, "\(piece) \(animation.target) at \(row.tMs) ms", file: file, line: line)
                XCTAssertEqual(Double(best.offset), Self.translateY(sample.transform), accuracy: 0.5,
                               "\(piece) \(animation.target) at \(row.tMs) ms", file: file, line: line)
                compared += 1
            }
        }
        XCTAssertGreaterThan(compared, 10, "too few frames of \(piece) compared", file: file, line: line)
    }

    func testTaskRowsEnterAsTheOriginalsFadeUpAtEveryFrame() throws {
        try assertFadeUps("task-rows", duration: AgentMotion.taskRow, stagger: AgentMotion.taskRowStagger)
    }

    func testToolCallsEnterAsTheOriginalsFadeUpAtEveryFrame() throws {
        try assertFadeUps("tool-chips", duration: AgentMotion.toolChip, stagger: nil)
    }

    func testThinkingsTraceEntersAsTheOriginalsFadeUpAtEveryFrame() throws {
        try assertFadeUps("thinking-state", duration: AgentMotion.traceRow, stagger: AgentMotion.traceStagger)
    }

    /// "Thought for ..." fades in over 350 ms ease-out, as the original's label does.
    func testThinkingsDoneLabelFadesInAsTheOriginal() throws {
        let (animations, _) = try spec("thinking-state")
        let fadeIn = try XCTUnwrap(animations.first { $0.name == "fade-in" })
        XCTAssertEqual(fadeIn.durationMs, AgentMotion.labelFadeIn * 1000)
        XCTAssertEqual(Curve(css: fadeIn.keyframes[0].easing), AgentMotion.labelCurve)
    }

    /// StreamText's caret is 2 pt by 1.05 em.
    func testTheStreamCaretIsTheSourcesBar() {
        XCTAssertEqual(AgentMarkdown.caret.size.width, 2)
        XCTAssertEqual(AgentMarkdown.caret.size.height, (1.05 * AgentMarkdown.size).rounded())
        XCTAssertTrue(AgentMarkdown.caret.isTemplate)
    }

    // MARK: - A message streaming in (companion ADR 0020)

    typealias Block = MarkdownText.Block
    typealias Span = MarkdownText.Span

    /// The words shown, as plain text, once `shown` words are revealed.
    private func shown(_ text: String, _ count: Int) -> String {
        MarkdownText.plain(StreamReveal.cut(MarkdownText.blocks(text), shown: count, settled: count))
    }

    func testAShortReplyStreamsAtThirtyWordsASecondFirstWordAtOnce() {
        XCTAssertEqual(StreamReveal.rate(20), 30)
        XCTAssertEqual(StreamReveal.revealed(20, elapsed: 0), 1, "the first word shows at once")
        XCTAssertEqual(StreamReveal.revealed(20, elapsed: 0.1), 4)
        XCTAssertEqual(StreamReveal.revealed(20, elapsed: 0.5), 16)
        XCTAssertEqual(StreamReveal.revealed(20, elapsed: 5), 20)
        XCTAssertEqual(StreamReveal.revealed(0, elapsed: 1), 0)
        XCTAssertEqual(StreamReveal.duration(20), 19.0 / 30 + 0.18, accuracy: 1e-9)
    }

    func testALongMessageCatchesUpAndNeverTakesMoreThanTwoSeconds() {
        for words in [60, 61, 200, 1_000, 10_000] {
            XCTAssertLessThanOrEqual(StreamReveal.duration(words), StreamReveal.longestReveal + StreamReveal.wordFade, "\(words)")
            XCTAssertEqual(StreamReveal.revealed(words, elapsed: StreamReveal.longestReveal), words, "\(words)")
        }
        XCTAssertEqual(StreamReveal.rate(600), 300)
    }

    func testRevealIsMonotonicAsTheClockRuns() {
        var last = 0
        for frame in 0...200 {
            let count = StreamReveal.revealed(90, elapsed: Double(frame) / 60)
            XCTAssertGreaterThanOrEqual(count, last)
            last = count
        }
        XCTAssertEqual(last, 90)
    }

    func testEachWordFadesUpOverItsOwnFewHundredthsOfASecond() {
        let landing = StreamReveal.landing(5, of: 20)
        XCTAssertEqual(StreamReveal.motion(5, of: 20, elapsed: landing).opacity, 0)
        XCTAssertEqual(StreamReveal.motion(5, of: 20, elapsed: landing).offset, StreamReveal.wordRise)
        let middle = StreamReveal.motion(5, of: 20, elapsed: landing + StreamReveal.wordFade / 2)
        XCTAssertGreaterThan(middle.opacity, 0.5, "the design's out curve is past half by halfway")
        XCTAssertLessThan(middle.opacity, 1)
        XCTAssertEqual(StreamReveal.motion(5, of: 20, elapsed: landing + StreamReveal.wordFade).opacity, 1)
        XCTAssertEqual(StreamReveal.motion(5, of: 20, elapsed: landing + StreamReveal.wordFade).offset, 0)
        XCTAssertTrue((0.15...0.2).contains(StreamReveal.wordFade))
    }

    func testTheCutFallsBetweenWordsNeverInsideOneOrAMarkdownToken() {
        let text = "The **tip reads** `$24.00`, not _twenty_ dollars."
        XCTAssertEqual(StreamReveal.words(MarkdownText.blocks(text)), 7)
        XCTAssertEqual(shown(text, 1), "The ")
        XCTAssertEqual(shown(text, 2), "The tip ")
        XCTAssertEqual(shown(text, 3), "The tip reads ")
        XCTAssertEqual(shown(text, 4), "The tip reads $24.00, ", "a word runs across styles to the next space")
        XCTAssertEqual(shown(text, 7), "The tip reads $24.00, not twenty dollars.")
        guard case .paragraph(let spans)? = StreamReveal.cut(MarkdownText.blocks(text), shown: 2, settled: 2).first else {
            return XCTFail("a paragraph")
        }
        XCTAssertEqual(spans.map(\.text), ["The ", "tip "])
        XCTAssertEqual(spans[1].style, .strong, "bold shows bold at once, never as stars")
    }

    func testBlocksRevealInOrderListsByItemCodeByLine() {
        let text = "Found:\n\n- one two\n- three\n\n```\nline a\nline b\n```\n\nDone."
        let blocks = MarkdownText.blocks(text)
        XCTAssertEqual(StreamReveal.words(blocks), 1 + 3 + 2 + 1)
        XCTAssertEqual(StreamReveal.cut(blocks, shown: 2, settled: 2).count, 2)
        guard case .list(_, _, let items)? = StreamReveal.cut(blocks, shown: 2, settled: 2).last else { return XCTFail("a list") }
        XCTAssertEqual(items.count, 1)
        guard case .code(_, let code)? = StreamReveal.cut(blocks, shown: 5, settled: 5).last else { return XCTFail("code") }
        XCTAssertEqual(code, "line a")
        XCTAssertEqual(StreamReveal.cut(blocks, shown: 7, settled: 7), blocks)
    }

    func testOnlyWordsStillFadingAreMarkedArriving() {
        let blocks = MarkdownText.blocks("one two three four")
        guard case .paragraph(let spans)? = StreamReveal.cut(blocks, shown: 4, settled: 2).first else { return XCTFail("a paragraph") }
        XCTAssertEqual(spans.map(\.text), ["one two ", "three ", "four"])
        XCTAssertEqual(spans.map(\.arriving), [nil, 2, 3])
    }

    /// The arriving words really are drawn faded inside the paragraph's one `Text`.
    func testArrivingWordsDrawFadedThenWhole() async throws {
        let blocks = MarkdownText.blocks("Settled words then **ARRIVING** words")
        let cut = StreamReveal.cut(blocks, shown: 5, settled: 3)
        func ink(_ elapsed: Double) async throws -> Int {
            let host = ParkedHost(AgentMarkdown(blocks: cut).textRenderer(ArrivingWords(words: 5, elapsed: elapsed))
                .frame(width: 400, height: 60, alignment: .topLeading).background(Color.white), size: CGSize(width: 400, height: 60))
            defer { host.close() }
            await host.settle(0.2)
            let image = try XCTUnwrap(host.image())
            let data = try XCTUnwrap(image.dataProvider?.data as Data?)
            var dark = 0
            for index in stride(from: 0, to: data.count - 3, by: 4) where data[index] < 128 { dark += 1 }
            return dark
        }
        let early = try await ink(StreamReveal.landing(3, of: 5))
        let late = try await ink(10)
        XCTAssertGreaterThan(late, early + 50, "the arriving words drew at full ink only once their fade ended")
    }

    // MARK: - Arrivals

    func testOnlyWhatArrivesWhileTheViewIsOpenFadesUpInOrder() {
        var arrivals = Arrivals()
        arrivals.note(["a"])
        XCTAssertFalse(arrivals.isNew("a"), "nothing is new before the view opened")
        arrivals.reset(["a", "b"])
        XCTAssertFalse(arrivals.isNew("a"))
        arrivals.note(["a", "b", "c", "d"])
        XCTAssertTrue(arrivals.isNew("c"))
        XCTAssertTrue(arrivals.isNew("d"))
        XCTAssertEqual(arrivals.delay("c", stagger: 0.08), 0)
        XCTAssertEqual(arrivals.delay("d", stagger: 0.08), 0.08, accuracy: 1e-9)
        arrivals.note(["a", "b", "c", "d"])
        XCTAssertTrue(arrivals.isNew("d"), "the batch stays until something else arrives")
        arrivals.note(["a", "b", "c", "d", "e"])
        XCTAssertFalse(arrivals.isNew("d"))
        XCTAssertEqual(arrivals.delay("e", stagger: 0.08), 0)
    }

    // MARK: - The conversation's tool calls

    private let t0 = Date(timeIntervalSince1970: 2_000_000)

    private func message(_ seq: Int, _ at: Double, _ from: MessageFrom, _ kind: MessageKind = .reply) -> Message {
        Message(seq: seq, at: t0.addingTimeInterval(at), from: from, kind: kind, text: "m\(seq)")
    }

    private func step(_ seq: Int, _ at: Double, error: String? = nil) -> Step {
        var step = Step(seq: seq, at: t0.addingTimeInterval(at), tool: "machine_screenshot", input: .object([:]))
        step.durationMs = 500
        step.error = error
        return step
    }

    func testTheConversationPutsEachCallAfterTheMessageBeforeIt() {
        let messages = [message(1, 0, .coder, .task), message(5, 30, .verifier)]
        let steps = [step(1, 5), step(2, 10), step(3, 40, error: "timed out")]
        let items = ConversationLayout.items(messages: messages, steps: steps, working: false)
        XCTAssertEqual(items.map(\.id), ["m1", "t1", "m5", "t5"])
        guard case .tools(_, let first, let live) = items[1] else { return XCTFail("calls after the task") }
        XCTAssertEqual(first.map(\.seq), [1, 2])
        XCTAssertFalse(live)
        XCTAssertEqual(ConversationLayout.doneLabel(first), "2 tool calls, 0:06")
        guard case .tools(_, let last, _) = items[3] else { return XCTFail("calls after the reply") }
        XCTAssertEqual(ConversationLayout.doneLabel(last), "1 tool call, 1 failed, 0:01")
    }

    func testWhileTheVerifierWorksItsCallsAreLiveEvenBeforeTheFirst() {
        let messages = [message(1, 0, .coder, .task)]
        let items = ConversationLayout.items(messages: messages, steps: [], working: true)
        XCTAssertEqual(items.map(\.id), ["m1", "t1"])
        guard case .tools(_, let steps, let live) = items[1] else { return XCTFail("a live group") }
        XCTAssertTrue(steps.isEmpty)
        XCTAssertTrue(live)
        XCTAssertEqual(ConversationLayout.items(messages: messages, steps: [], working: false).map(\.id), ["m1"])
    }

    // MARK: - Inline Markdown

    func testInlineMarkdownKeepsItsPlacesTypeAndStylesTheRuns() {
        let text = AgentMarkdown.inline("Tip reads **$24.00**, not `$20`")
        XCTAssertEqual(String(text.characters), "Tip reads $24.00, not $20")
        let bold = text.runs.first { String(text[$0.range].characters) == "$24.00" }
        XCTAssertEqual(bold?.inlinePresentationIntent, .stronglyEmphasized)
        let code = text.runs.first { String(text[$0.range].characters) == "$20" }
        XCTAssertEqual(code?.inlinePresentationIntent, .code)
        XCTAssertNil(text.runs.first?.font, "the place's type stays")
        XCTAssertEqual(String(AgentMarkdown.inline("- one\n- two").characters), "one two")
        XCTAssertEqual(String(AgentMarkdown.inline("").characters), "")
    }

    // MARK: - The app's own dropdown

    /// A menu from a trigger at the window's foot (the speed button) opens above it, whole.
    func testTheDropdownOpensAboveATriggerWithNoRoomBelow() {
        let items = ["1×", "2×", "4×"].map { DropdownItem(id: $0, title: $0) {} }
        let window = CGSize(width: 1024, height: 660)
        let foot = DropdownCenter.Open(id: "speed", anchor: CGRect(x: 540, y: 620, width: 30, height: 22), items: items,
                                       width: 150, alignTrailing: true)
        let place = DropdownLayer.placement(foot, in: window)
        XCTAssertTrue(place.above)
        XCTAssertEqual(place.y + DropdownLayer.height(items), 616)
        let top = DropdownCenter.Open(id: "more", anchor: CGRect(x: 980, y: 40, width: 28, height: 28), items: items,
                                      width: 260, alignTrailing: true)
        XCTAssertEqual(DropdownLayer.placement(top, in: window).y, 72)
        XCTAssertFalse(DropdownLayer.placement(top, in: window).above)
    }

    func testTheDropdownMovesWithArrowsHomeAndEndAndSkipsDisabledRows() {
        let center = DropdownCenter()
        var ran: [String] = []
        let items = ["Alpha", "Beta", "Gamma", "Delta"].enumerated().map { index, title in
            DropdownItem(id: title, title: title, disabled: index == 2) { ran.append(title) }
        }
        center.toggle("menu", anchor: .zero, items: items)
        XCTAssertTrue(center.isOpen)
        XCTAssertTrue(center.handle(keyCode: 125, characters: "", flags: []))
        XCTAssertEqual(center.highlighted, 0)
        _ = center.handle(keyCode: 125, characters: "", flags: [])
        _ = center.handle(keyCode: 125, characters: "", flags: [])
        XCTAssertEqual(center.highlighted, 3, "the disabled row is skipped")
        _ = center.handle(keyCode: 115, characters: "", flags: [])
        XCTAssertEqual(center.highlighted, 0, "Home")
        _ = center.handle(keyCode: 119, characters: "", flags: [])
        XCTAssertEqual(center.highlighted, 3, "End")
        _ = center.handle(keyCode: 0, characters: "b", flags: [])
        XCTAssertEqual(center.highlighted, 1, "type-ahead")
        _ = center.handle(keyCode: 36, characters: "\r", flags: [])
        XCTAssertEqual(ran, ["Beta"])
        XCTAssertFalse(center.isOpen)
        center.toggle("menu", anchor: .zero, items: items)
        _ = center.handle(keyCode: 48, characters: "\t", flags: [])
        XCTAssertFalse(center.isOpen, "Tab closes")
        center.toggle("menu", anchor: .zero, items: items)
        XCTAssertFalse(center.handle(keyCode: 40, characters: "k", flags: .command), "a Command key closes it and goes on")
        XCTAssertFalse(center.isOpen)
    }
}
