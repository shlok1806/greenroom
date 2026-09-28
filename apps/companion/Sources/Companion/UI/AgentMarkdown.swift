import AppKit
import SwiftUI

/// Agent-written text as Markdown in the redesign's type (redesign 7): the conversation,
/// Activity's notes and the verdict's words. `MarkdownText` (swift-markdown, companion ADR
/// 0009) parses; this view draws its blocks with the Figma tokens: headings at Title and Body
/// Emphasis, bold and italics in the system face, inline code and fenced code in the mono face
/// (a code block scrolls sideways and has Copy), lists, quotes with a rule, tables as a grid,
/// links in the accent that open in the browser, and a cited step as a chip that seeks the
/// player. A streaming message passes blocks cut at its last word (`StreamReveal.cut`), whose
/// arriving words `ArrivingWords` fades up, and a caret.
struct AgentMarkdown: View {
    let blocks: [MarkdownText.Block]
    /// A caret after the text while it streams.
    var caret = false
    var color: Color = Palette.text
    /// Seeks the player to a cited step; nil draws citations as plain text.
    var onStep: ((Int) -> Void)?

    init(blocks: [MarkdownText.Block], caret: Bool = false, color: Color = Palette.text, onStep: ((Int) -> Void)? = nil) {
        self.blocks = blocks
        self.caret = caret
        self.color = color
        self.onStep = onStep
    }

    init(text: String, steps: Set<Int>? = nil, color: Color = Palette.text, onStep: ((Int) -> Void)? = nil) {
        self.init(blocks: MarkdownText.blocks(text, steps: onStep == nil ? nil : steps), color: color, onStep: onStep)
    }

    static let size: CGFloat = 13
    static let codeSize: CGFloat = 12

    /// Agent text that sits in one line or a short run of words (a check's claim, what it
    /// observed, expected and saw, a task row's title, the Now line): its inline Markdown,
    /// bold, italics, code, strike-through and links, as presentation intents, so the text
    /// keeps the size and weight its place gives it. Blocks are joined with spaces; a list
    /// item keeps no marker. Never `AttributedString(markdown:)` (companion CLAUDE.md).
    static func inline(_ text: String) -> AttributedString {
        let blocks = MarkdownText.blocks(text)
        guard !blocks.isEmpty else { return AttributedString(text) }
        var out = AttributedString()
        for (index, spans) in inlineSpans(blocks).enumerated() {
            if index > 0 { out += AttributedString(" ") }
            for span in spans {
                var run = AttributedString(span.text)
                var intent: InlinePresentationIntent = []
                if span.style.contains(.strong) { intent.insert(.stronglyEmphasized) }
                if span.style.contains(.emphasis) { intent.insert(.emphasized) }
                if span.style.contains(.code) { intent.insert(.code) }
                if span.style.contains(.strikethrough) { intent.insert(.strikethrough) }
                if !intent.isEmpty { run.inlinePresentationIntent = intent }
                if let link = span.link, let url = MarkdownText.openableURL(link) {
                    run.link = url
                    run.foregroundColor = Palette.accent
                }
                out += run
            }
        }
        return out
    }

    private static func inlineSpans(_ blocks: [MarkdownText.Block]) -> [[MarkdownText.Span]] {
        blocks.flatMap { block -> [[MarkdownText.Span]] in
            switch block {
            case .heading(_, let spans), .paragraph(let spans): [spans]
            case .code(_, let text): [[MarkdownText.Span(text, .code)]]
            case .list(_, _, let items): items.flatMap { inlineSpans($0.blocks) }
            case .quote(let inner): inlineSpans(inner)
            case .table(let header, let rows): ([header] + rows).map { row in row.flatMap { $0 + [MarkdownText.Span(" ")] } }
            case .rule: []
            }
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { index, block in
                self.block(block, last: index == blocks.count - 1)
            }
            if blocks.isEmpty || !endsInParagraph, caret {
                streamingText(Text(""))
            }
        }
        .environment(\.openURL, OpenURLAction { url in
            if let step = EvidenceURL.step(url) {
                onStep?(step)
                return .handled
            }
            return MarkdownText.openableURL(url.absoluteString) == nil ? .discarded : .systemAction
        })
    }

    private var endsInParagraph: Bool {
        if case .paragraph? = blocks.last { return true }
        return false
    }

    @ViewBuilder
    private func block(_ block: MarkdownText.Block, last: Bool) -> some View {
        switch block {
        case .heading(let level, let spans):
            text(spans, size: level <= 1 ? 15 : Self.size, bold: true)
                .foregroundStyle(color)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, Gap.x4)
                .accessibilityAddTraits(.isHeader)
        case .paragraph(let spans):
            let text = text(spans, size: Self.size, bold: false)
            (last ? streamingText(text) : AnyView(styledParagraph(text)))
        case .code(let language, let text):
            AgentCodeBlock(text: text, language: language)
        case .list(let ordered, let start, let items):
            VStack(alignment: .leading, spacing: Gap.x4) {
                ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(marker(ordered: ordered, number: start + index, checked: item.checked))
                            .font(.system(size: Self.size))
                            .monospacedDigit()
                            .foregroundStyle(Palette.textSecondary)
                            .frame(minWidth: ordered ? 18 : 10, alignment: .trailing)
                        AgentMarkdown(blocks: item.blocks, color: color, onStep: onStep)
                    }
                }
            }
        case .quote(let inner):
            HStack(alignment: .top, spacing: Gap.x8) {
                RoundedRectangle(cornerRadius: 1).fill(Palette.border).frame(width: 3)
                AgentMarkdown(blocks: inner, color: Palette.textSecondary, onStep: onStep)
            }
            .fixedSize(horizontal: false, vertical: true)
        case .table(let header, let rows):
            AgentTable(header: header, rows: rows)
        case .rule:
            Rectangle().fill(Palette.border).frame(height: 1).padding(.vertical, Gap.x4)
        }
    }

    private func styledParagraph(_ text: Text) -> some View {
        text
            .foregroundStyle(color)
            .lineSpacing(4)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// The last paragraph with the caret after it while the message streams.
    private func streamingText(_ text: Text) -> AnyView {
        var out = text
        if caret {
            // StreamText's caret: a 2 pt bar 1.05 em tall, radius 1, 1.5 after the text,
            // sitting on the descender as the source's inline-block does.
            out = out + Text("\u{2009}").font(.system(size: Self.size))
                + Text(Image(nsImage: Self.caret)).foregroundColor(Palette.text).baselineOffset(-3)
        }
        return AnyView(styledParagraph(out))
    }

    /// The caret as a template image, so the text's colour fills it.
    static let caret: NSImage = {
        let bar = NSSize(width: AgentMotion.caretWidth, height: (AgentMotion.caretEm * AgentMarkdown.size).rounded())
        let image = NSImage(size: bar, flipped: false) { rect in
            NSColor.black.setFill()
            NSBezierPath(roundedRect: rect, xRadius: 1, yRadius: 1).fill()
            return true
        }
        image.isTemplate = true
        return image
    }()

    private func marker(ordered: Bool, number: Int, checked: Bool?) -> String {
        switch checked {
        case true?: "✓"
        case false?: "○"
        case nil: ordered ? "\(number)." : "•"
        }
    }

    /// Spans as text: runs of settled words as one styled string, each word still arriving
    /// marked (`ArrivingWord`) so a streaming reveal can fade it.
    private func text(_ spans: [MarkdownText.Span], size: CGFloat, bold: Bool) -> Text {
        guard spans.contains(where: { $0.arriving != nil }) else { return Text(attributed(spans, size: size, bold: bold)) }
        var out = Text("")
        var start = 0
        while start < spans.count {
            let arriving = spans[start].arriving
            var end = start + 1
            while end < spans.count, spans[end].arriving == arriving { end += 1 }
            let run = Text(attributed(Array(spans[start..<end]), size: size, bold: bold))
            out = out + (arriving.map { run.customAttribute(ArrivingWord(index: $0)) } ?? run)
            start = end
        }
        return out
    }

    /// Spans as one styled string in the system face; code in the mono face on a quiet fill,
    /// links in the accent, a cited step as a chip.
    private func attributed(_ spans: [MarkdownText.Span], size: CGFloat, bold: Bool) -> AttributedString {
        var out = AttributedString()
        for span in spans {
            var run = AttributedString(span.text)
            let strong = bold || span.style.contains(.strong)
            var font = Font.system(size: size, weight: strong ? .semibold : .regular)
            if span.style.contains(.emphasis) { font = font.italic() }
            run.font = font
            if span.style.contains(.code) {
                run.font = .system(size: Self.codeSize, design: .monospaced)
                run.backgroundColor = Palette.bgSelected
            }
            if span.style.contains(.strikethrough) { run.strikethroughStyle = .single }
            if let step = span.step, onStep != nil, let url = EvidenceURL.url(step: step) {
                run = AttributedString(span.text.replacingOccurrences(of: " ", with: "\u{00A0}"))
                run.link = url
                run.font = .system(size: Self.codeSize, weight: .medium, design: .monospaced)
                run.backgroundColor = Palette.bgSelected
                run.foregroundColor = Palette.text
            } else if let link = span.link, let url = MarkdownText.openableURL(link) {
                run.link = url
                run.foregroundColor = Palette.accent
                run.underlineStyle = .single
            }
            out += run
        }
        return out
    }
}

/// A fenced block: the language and Copy on top, the code in the mono face, scrolling
/// sideways rather than wrapping (code keeps its lines).
struct AgentCodeBlock: View {
    let text: String
    var language: String?
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Gap.x8) {
                Text(language.flatMap { $0.isEmpty ? nil : $0 } ?? "code").textStyle(.caption).foregroundStyle(Palette.textSecondary)
                Spacer()
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(text, forType: .string)
                    copied = true
                } label: {
                    HStack(spacing: 4) {
                        IconView(icon: copied ? .check : .copy, size: 12)
                        Text(copied ? "Copied" : "Copy").textStyle(.caption)
                    }
                    .foregroundStyle(Palette.textSecondary)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("Copy the code")
                .accessibilityIdentifier("code.copy")
            }
            .padding(.horizontal, Gap.x8)
            .frame(height: 26)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
            ScrollView(.horizontal) {
                Text(text)
                    .font(.system(size: AgentMarkdown.codeSize, design: .monospaced))
                    .foregroundStyle(Palette.text)
                    .lineSpacing(3)
                    .textSelection(.enabled)
                    .fixedSize()
                    .padding(Gap.x8)
            }
            .scrollIndicators(.visible)
        }
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(Palette.bgSelected.opacity(0.6)))
        .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1))
        .clipShape(RoundedRectangle(cornerRadius: Corner.control))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("\(language ?? "") code")
    }
}

/// A Markdown table: the header heavier, a rule under it, cells in the body size; wide tables
/// scroll sideways.
struct AgentTable: View {
    let header: [[MarkdownText.Span]]
    let rows: [[[MarkdownText.Span]]]

    var body: some View {
        ScrollView(.horizontal) {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Gap.x16, verticalSpacing: 6) {
                GridRow {
                    ForEach(Array(header.enumerated()), id: \.offset) { _, cell in
                        Text(MarkdownText.plain(cell)).font(.system(size: 12, weight: .semibold)).foregroundStyle(Palette.text)
                    }
                }
                Rectangle().fill(Palette.border).frame(height: 1).gridCellUnsizedAxes(.horizontal)
                ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                    GridRow {
                        ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                            Text(MarkdownText.plain(cell)).font(.system(size: 12)).foregroundStyle(Palette.text)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            .textSelection(.enabled)
            .padding(Gap.x8)
        }
        .scrollIndicators(.visible)
        .background(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(Palette.border, lineWidth: 1))
    }
}

/// A message that arrived while the conversation showed, streamed in once the way ChatGPT and
/// Claude stream (companion ADR 0020): whole words, each fading up as it lands, at a reading
/// pace that catches up on a long message. The daemon sends each message whole, so the reveal
/// knows every word from the start and nothing here fakes tokens. Pure, so tests hold it.
enum StreamReveal {
    /// The steady pace, in words a second.
    static let wordsPerSecond = 30.0
    /// The longest a reveal runs (before its last word's fade): a long message speeds up.
    static let longestReveal = 2.0
    /// Each word's fade up: opacity 0 to 1 and a small rise, on the design's curve.
    static let wordFade = 0.18
    static let wordRise: CGFloat = 3
    static let curve = AgentMotion.curve

    /// Words a second for a message of `words` words.
    static func rate(_ words: Int) -> Double { max(wordsPerSecond, Double(words) / longestReveal) }

    /// Words shown `elapsed` seconds after the reveal began. The first shows at once.
    static func revealed(_ words: Int, elapsed: TimeInterval) -> Int {
        guard words > 0, elapsed >= 0 else { return 0 }
        return min(words, Int((elapsed * rate(words)).rounded(.down)) + 1)
    }

    /// When the `word`th word (from 0) lands.
    static func landing(_ word: Int, of words: Int) -> TimeInterval { Double(word) / rate(words) }

    /// How long the whole reveal takes, the last word's fade included.
    static func duration(_ words: Int) -> TimeInterval { words == 0 ? 0 : landing(words - 1, of: words) + wordFade }

    /// The `word`th word's opacity and how far below its place it draws, `elapsed` seconds in.
    static func motion(_ word: Int, of words: Int, elapsed: TimeInterval) -> (opacity: Double, offset: CGFloat) {
        let progress = curve.value(at: (elapsed - landing(word, of: words)) / wordFade)
        return (progress, wordRise * CGFloat(1 - progress))
    }

    /// The words in `blocks`, as the reveal counts them.
    static func words(_ blocks: [MarkdownText.Block]) -> Int {
        var cutter = Cutter(shown: .max, settled: .max)
        _ = cutter.blocks(blocks)
        return cutter.next
    }

    /// `blocks` as they show `elapsed` seconds in: cut after the last word shown, never inside
    /// one, with each word still fading carrying its index (`Span.arriving`).
    static func cut(_ blocks: [MarkdownText.Block], words: Int, elapsed: TimeInterval) -> [MarkdownText.Block] {
        cut(blocks, shown: revealed(words, elapsed: elapsed), settled: revealed(words, elapsed: elapsed - wordFade))
    }

    /// `blocks` with their first `shown` words; words from `settled` on are marked arriving.
    static func cut(_ blocks: [MarkdownText.Block], shown: Int, settled: Int) -> [MarkdownText.Block] {
        var cutter = Cutter(shown: shown, settled: settled)
        return cutter.blocks(blocks)
    }

    /// Walks the blocks in reading order, numbering words. A word is a run of non-space
    /// characters and the spaces after it, across styles; a cited step's chip is one word, a
    /// code block's line, a table's header or row and a rule each count as one.
    private struct Cutter {
        let shown: Int
        let settled: Int
        /// The index the next word will take; the count once the walk ends.
        var next = 0

        var full: Bool { next >= shown }

        mutating func blocks(_ blocks: [MarkdownText.Block]) -> [MarkdownText.Block] {
            var out: [MarkdownText.Block] = []
            for block in blocks {
                if full { break }
                if let cut = self.block(block) { out.append(cut) }
            }
            return out
        }

        private mutating func block(_ block: MarkdownText.Block) -> MarkdownText.Block? {
            switch block {
            case .heading(let level, let spans):
                return self.spans(spans).map { .heading(level: level, $0) }
            case .paragraph(let spans):
                return self.spans(spans).map { .paragraph($0) }
            case .code(let language, let text):
                let lines = text.split(separator: "\n", omittingEmptySubsequences: false)
                let take = min(lines.count, shown - next)
                next += take
                return take > 0 ? .code(language: language, text: lines.prefix(take).joined(separator: "\n")) : nil
            case .list(let ordered, let start, let items):
                var kept: [MarkdownText.Item] = []
                for item in items {
                    if full { break }
                    let inner = blocks(item.blocks)
                    if inner.isEmpty { break }
                    kept.append(MarkdownText.Item(checked: item.checked, blocks: inner))
                }
                return kept.isEmpty ? nil : .list(ordered: ordered, start: start, items: kept)
            case .quote(let inner):
                let cut = blocks(inner)
                return cut.isEmpty ? nil : .quote(cut)
            case .table(let header, let rows):
                next += 1
                var kept: [[[MarkdownText.Span]]] = []
                for row in rows {
                    if full { break }
                    next += 1
                    kept.append(row)
                }
                return .table(header: header, rows: kept)
            case .rule:
                next += 1
                return .rule
            }
        }

        /// A paragraph's runs up to the last word shown, split where a word's arriving mark
        /// changes. Nil when no word of it shows.
        private mutating func spans(_ spans: [MarkdownText.Span]) -> [MarkdownText.Span]? {
            var out: [MarkdownText.Span] = []
            // The word the characters belong to; nil before the paragraph's first.
            var word: Int?
            var inWord = false
            // Spaces before the paragraph's first word, which go with it.
            var pending = ""
            walk: for span in spans {
                if span.step != nil {
                    if !inWord {
                        guard next < shown else { break walk }
                        word = next
                        next += 1
                        inWord = true
                    }
                    append(pending + span.text, from: span, word: word ?? 0, to: &out)
                    pending = ""
                    continue
                }
                var buffer = ""
                for character in span.text {
                    if character.isWhitespace {
                        inWord = false
                        if word == nil { pending.append(character) } else { buffer.append(character) }
                        continue
                    }
                    if !inWord {
                        if let word, !buffer.isEmpty { append(buffer, from: span, word: word, to: &out) }
                        buffer = pending
                        pending = ""
                        guard next < shown else { break walk }
                        word = next
                        next += 1
                        inWord = true
                    }
                    buffer.append(character)
                }
                if let word, !buffer.isEmpty { append(buffer, from: span, word: word, to: &out) }
            }
            return out.isEmpty ? nil : out
        }

        /// Adds `text` in `span`'s style as part of `word`, joining the run before it when
        /// nothing tells them apart.
        private func append(_ text: String, from span: MarkdownText.Span, word: Int, to out: inout [MarkdownText.Span]) {
            var run = span
            run.text = text
            run.arriving = word >= settled ? word : nil
            if span.step == nil, var last = out.last, last.step == nil, last.style == run.style, last.link == run.link,
               last.arriving == run.arriving {
                last.text += text
                out[out.count - 1] = last
            } else {
                out.append(run)
            }
        }
    }
}

/// Marks a run of a streaming message's words that is still fading in.
struct ArrivingWord: TextAttribute {
    var index: Int
}

/// Draws each arriving word from its own clock (`StreamReveal.motion`), so words fade up
/// inside one wrapped `Text` without laying the line out again.
struct ArrivingWords: TextRenderer {
    var words: Int
    var elapsed: TimeInterval

    func draw(layout: Text.Layout, in context: inout GraphicsContext) {
        for line in layout {
            for run in line {
                guard let word = run[ArrivingWord.self] else {
                    context.draw(run)
                    continue
                }
                let motion = StreamReveal.motion(word.index, of: words, elapsed: elapsed)
                var faded = context
                faded.opacity = motion.opacity
                faded.translateBy(x: 0, y: motion.offset)
                faded.draw(run)
            }
        }
    }
}

/// A message's words, streamed in once when they arrived while the run was open, instant
/// otherwise (and under Reduce Motion). A click finishes the reveal.
struct StreamingMarkdown: View {
    let text: String
    /// When the reveal began; nil shows the whole text at once.
    var start: Date?
    var steps: Set<Int>?
    var onStep: ((Int) -> Void)?
    var finished: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        if let start, !reduceMotion {
            // Parsed once per message, not per frame; each frame only cuts it.
            let blocks = MarkdownText.blocks(text, steps: onStep == nil ? nil : steps)
            let words = StreamReveal.words(blocks)
            let duration = StreamReveal.duration(words)
            TimelineView(.animation) { context in
                let elapsed = context.date.timeIntervalSince(start)
                let done = elapsed >= duration
                AgentMarkdown(blocks: StreamReveal.cut(blocks, words: words, elapsed: elapsed), caret: !done, onStep: onStep)
                    .textRenderer(ArrivingWords(words: words, elapsed: elapsed))
                    // Checked on the first frame too: a reveal already over hands back at once.
                    .onChange(of: done, initial: true) { _, done in if done { finished() } }
            }
            .contentShape(Rectangle())
            .onTapGesture { finished() }
            .accessibilityLabel(text)
        } else {
            AgentMarkdown(text: text, steps: steps, onStep: onStep)
        }
    }
}
