import AppKit
import SwiftUI

/// Agent-written text as Markdown in the redesign's type (redesign 7): the conversation,
/// Activity's notes and the verdict's words. `MarkdownText` (swift-markdown, companion ADR
/// 0009) parses; this view draws its blocks with the Figma tokens: headings at Title and Body
/// Emphasis, bold and italics in the system face, inline code and fenced code in the mono face
/// (a code block scrolls sideways and has Copy), lists, quotes with a rule, tables as a grid,
/// links in the accent that open in the browser, and a cited step as a chip that seeks the
/// player. A streaming message passes its not-yet-settled `tail`, drawn fading as Beautiful
/// UI's StreamText draws it, and a caret.
struct AgentMarkdown: View {
    let blocks: [MarkdownText.Block]
    /// The characters still arriving, drawn after the last paragraph, fading.
    var tail = ""
    /// A caret after the text while it streams.
    var caret = false
    var color: Color = Palette.text
    /// Seeks the player to a cited step; nil draws citations as plain text.
    var onStep: ((Int) -> Void)?

    init(blocks: [MarkdownText.Block], tail: String = "", caret: Bool = false, color: Color = Palette.text, onStep: ((Int) -> Void)? = nil) {
        self.blocks = blocks
        self.tail = tail
        self.caret = caret
        self.color = color
        self.onStep = onStep
    }

    init(text: String, steps: Set<Int>? = nil, color: Color = Palette.text, onStep: ((Int) -> Void)? = nil) {
        self.init(blocks: MarkdownText.blocks(text, steps: onStep == nil ? nil : steps), color: color, onStep: onStep)
    }

    static let size: CGFloat = 13
    static let codeSize: CGFloat = 12

    var body: some View {
        VStack(alignment: .leading, spacing: Gap.x8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { index, block in
                self.block(block, last: index == blocks.count - 1)
            }
            if blocks.isEmpty || !endsInParagraph, !tail.isEmpty || caret {
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
            Text(attributed(spans, size: level <= 1 ? 15 : Self.size, bold: true))
                .foregroundStyle(color)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, Gap.x4)
                .accessibilityAddTraits(.isHeader)
        case .paragraph(let spans):
            let text = Text(attributed(spans, size: Self.size, bold: false))
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

    /// The last paragraph with the arriving tail and the caret after it.
    private func streamingText(_ text: Text) -> AnyView {
        var out = text
        let characters = Array(tail)
        for (index, character) in characters.enumerated() {
            let opacity = StreamReveal.tailOpacity(index, of: characters.count)
            out = out + Text(String(character)).font(.system(size: Self.size)).foregroundColor(color.opacity(opacity))
        }
        if caret {
            out = out + Text("\u{2009}▍").font(.system(size: Self.size)).foregroundColor(Palette.text)
        }
        return AnyView(styledParagraph(out))
    }

    private func marker(ordered: Bool, number: Int, checked: Bool?) -> String {
        switch checked {
        case true?: "✓"
        case false?: "○"
        case nil: ordered ? "\(number)." : "•"
        }
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

/// Beautiful UI's StreamText (`components/atoms/StreamText.tsx`, MIT, spec `stream-text.json`,
/// docs/22 C12) as a reveal of text that has arrived whole: two characters every 9 ms, the
/// last six drawn fading under the source's mask (black at 20%, 20% black at the end), a
/// caret while it runs. The daemon sends each message whole, so nothing here fakes tokens.
enum StreamReveal {
    static let charactersPerTick = 2
    static let tickMs = 9.0
    static let tailLength = 6
    /// The source blurs the tail 1.6 px; SwiftUI `Text` runs cannot blur one by one, so the
    /// fade carries the blur's weight (documented gap, docs/22 C12).
    static let tailBlur = 1.6

    /// Characters shown `elapsed` seconds after the message arrived.
    static func revealed(_ total: Int, elapsed: TimeInterval) -> Int {
        guard elapsed > 0 else { return 0 }
        return min(total, Int((elapsed * 1000 / tickMs).rounded(.down)) * charactersPerTick)
    }

    /// How long a message of `total` characters takes.
    static func duration(_ total: Int) -> TimeInterval {
        Double((total + charactersPerTick - 1) / charactersPerTick) * tickMs / 1000
    }

    /// The opacity of the `index`th of the tail's `count` characters: the mask
    /// `linear-gradient(to right, black 20%, rgb(0 0 0 / 20%))` at the character's centre.
    static func tailOpacity(_ index: Int, of count: Int) -> Double {
        guard count > 0 else { return 1 }
        let x = (Double(index) + 0.5) / Double(count)
        return x <= 0.2 ? 1 : 1 - 0.8 * (x - 0.2) / 0.8
    }

    /// The settled text and the tail after `revealed` characters.
    static func split(_ text: String, revealed: Int) -> (settled: String, tail: String) {
        let shown = String(text.prefix(revealed))
        guard revealed < text.count else { return (shown, "") }
        let cut = max(0, shown.count - tailLength)
        return (String(shown.prefix(cut)), String(shown.dropFirst(cut)))
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
            TimelineView(.animation) { context in
                let count = StreamReveal.revealed(text.count, elapsed: context.date.timeIntervalSince(start))
                let (settled, tail) = StreamReveal.split(text, revealed: count)
                AgentMarkdown(blocks: MarkdownText.blocks(settled, steps: onStep == nil ? nil : steps), tail: tail, caret: count < text.count,
                              onStep: onStep)
                    .onChange(of: count >= text.count) { _, done in if done { finished() } }
            }
            .contentShape(Rectangle())
            .onTapGesture { finished() }
            .accessibilityLabel(text)
        } else {
            AgentMarkdown(text: text, steps: steps, onStep: onStep)
        }
    }
}
