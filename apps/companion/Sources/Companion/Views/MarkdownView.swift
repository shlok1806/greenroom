import SwiftUI

/// A message's Markdown drawn Glamour style in the app's two faces (spec, Type): prose in
/// the reading face, headings in its wider cut at the same size, emphasis in its italics,
/// inline code and code blocks in the mono face, lists with glyph bullets, quotes with a
/// `│` rule, tables as a mono grid. A cited step is an evidence chip that seeks it.
/// Parsing is `MarkdownText`; this view only draws its blocks.
struct MarkdownView: View {
    let blocks: [MarkdownText.Block]
    var size: CGFloat = TypeScale.reading
    /// Seeks the screen and the steps to a cited step. Nil draws citations as plain text.
    var onStep: ((Int) -> Void)?

    @Environment(\.theme) private var theme
    @Environment(\.ground) private var ground

    init(blocks: [MarkdownText.Block], size: CGFloat = TypeScale.reading, onStep: ((Int) -> Void)? = nil) {
        self.blocks = blocks
        self.size = size
        self.onStep = onStep
    }

    /// `steps`: the numbers in the run's record, so only real steps become chips.
    init(text: String, steps: Set<Int>? = nil, size: CGFloat = TypeScale.reading, onStep: ((Int) -> Void)? = nil) {
        self.init(blocks: MarkdownText.blocks(text, steps: onStep == nil ? nil : steps), size: size, onStep: onStep)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            // By position: a message can say the same thing twice.
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                self.block(block)
            }
        }
        .environment(\.openURL, OpenURLAction { url in
            guard let step = EvidenceURL.step(url) else { return .systemAction }
            onStep?(step)
            return .handled
        })
    }

    @ViewBuilder
    private func block(_ block: MarkdownText.Block) -> some View {
        switch block {
        case .heading(let level, let spans):
            // Never bigger than the reading text, only heavier and wider (ADR 0008).
            Text(attributed(spans, heading: true))
                .headingStyle(size: level <= 2 ? size : min(size, TypeScale.readingSmall))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, Space.xs)
                .accessibilityAddTraits(.isHeader)
        case .paragraph(let spans):
            prose(spans)
        case .code(let language, let text):
            CodeBlock(text: text, language: language)
        case .list(let ordered, let start, let items):
            list(ordered: ordered, start: start, items: items)
        case .quote(let inner):
            HStack(alignment: .top, spacing: Space.s) {
                // The `│` rule, drawn as a line so it runs the quote's whole height.
                Rectangle()
                    .fill(theme.dim(on: ground))
                    .frame(width: Space.hairline)
                    .accessibilityHidden(true)
                MarkdownView(blocks: inner, size: size, onStep: onStep)
                    .foregroundStyle(.secondary)
            }
            .fixedSize(horizontal: false, vertical: true)
        case .table(let header, let rows):
            TableBlock(header: header, rows: rows)
        case .rule:
            Hairline()
                .padding(.vertical, Space.xs)
        }
    }

    private func prose(_ spans: [MarkdownText.Span]) -> some View {
        Text(attributed(spans, heading: false))
            .readingStyle(size: size)
            .tint(theme.foreground)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
    }

    private func list(ordered: Bool, start: Int, items: [MarkdownText.Item]) -> some View {
        // Wide enough for the longest number, so every item's text starts on one line.
        let widest = ordered ? "\(start + max(items.count - 1, 0))." : "•"
        return VStack(alignment: .leading, spacing: Space.xs) {
            ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    ZStack(alignment: .trailing) {
                        Text(widest).hidden()
                        Text(marker(ordered: ordered, number: start + index, checked: item.checked))
                            .foregroundStyle(.secondary)
                    }
                    .font(Typeface.monoRegular.font(size: TypeScale.monoSmall))
                    .monospacedDigit()
                    .accessibilityHidden(!ordered && item.checked == nil)
                    MarkdownView(blocks: item.blocks, size: size, onStep: onStep)
                }
            }
        }
    }

    private func marker(ordered: Bool, number: Int, checked: Bool?) -> String {
        switch checked {
        case true?: "✓"
        case false?: "○"
        case nil: ordered ? "\(number)." : "•"
        }
    }

    /// Spans as one styled string. Emphasis takes the reading face's italics; inline code
    /// the mono face on the highlight, a little smaller so its x-height matches; a cited
    /// step the mono face as a chip that links to the step.
    private func attributed(_ spans: [MarkdownText.Span], heading: Bool) -> AttributedString {
        var out = AttributedString()
        for span in spans {
            var run = AttributedString(span.text)
            let face: Typeface
            switch (span.style.contains(.strong) || heading, span.style.contains(.emphasis)) {
            case (true, true): face = .readingSemiBoldItalic
            case (true, false): face = heading ? .headingSemiBold : .readingSemiBold
            case (false, true): face = .readingItalic
            case (false, false): face = .readingRegular
            }
            if span.style.contains(.code) {
                run.font = Typeface.monoRegular.font(size: size - 1.5)
                run.backgroundColor = theme.highlight
            } else if !heading {
                run.font = face.font(size: size)
            }
            if span.style.contains(.strikethrough) { run.strikethroughStyle = .single }
            if let step = span.step, onStep != nil, let url = EvidenceURL.url(step: step) {
                // One chip never breaks across lines: "step" and "16" stay together.
                run = AttributedString(span.text.replacingOccurrences(of: " ", with: "\u{00A0}"))
                run.link = url
                run.font = Typeface.monoMedium.font(size: size - 1.5)
                run.backgroundColor = theme.highlight
                run.foregroundColor = theme.foreground
            } else if let link = span.link, let url = URL(string: link) {
                run.link = url
                run.underlineStyle = .single
            }
            out += run
        }
        return out
    }
}

/// A cited step as a link inside prose. The scheme never leaves the app: `MarkdownView`
/// handles it with its own `openURL`, and nothing here reaches the daemon.
enum EvidenceURL {
    static let scheme = "greenroom-step"

    static func url(step: Int) -> URL? {
        URL(string: "\(scheme):\(step)")
    }

    static func step(_ url: URL) -> Int? {
        guard url.scheme == scheme else { return nil }
        return Int(url.absoluteString.dropFirst(scheme.count + 1))
    }
}

/// Code or output in the mono face on a quiet panel, wrapped rather than scrolled (a
/// column this narrow would show a scroller per block), with its language named when the
/// message gave one.
struct CodeBlock: View {
    let text: String
    var language: String?

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            if let language, !language.isEmpty {
                Text(language.uppercased())
                    .font(Typeface.monoMedium.font(size: TypeScale.label))
                    .tracking(0.8)
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("\(language) code")
            }
            Text(text)
                .monoStyle(size: TypeScale.small)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .panel(radius: Radius.sm)
    }
}

/// A Markdown table as data: a mono grid, the header heavier with a hairline under it.
private struct TableBlock: View {
    let header: [[MarkdownText.Span]]
    let rows: [[[MarkdownText.Span]]]

    var body: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Space.m, verticalSpacing: Space.xs) {
            GridRow {
                ForEach(Array(header.enumerated()), id: \.offset) { _, cell in
                    Text(MarkdownText.plain(cell))
                        .font(Typeface.monoMedium.font(size: TypeScale.small))
                }
            }
            Hairline()
                .gridCellUnsizedAxes(.horizontal)
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                GridRow {
                    ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                        Text(MarkdownText.plain(cell))
                            .font(Typeface.monoRegular.font(size: TypeScale.small))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
        }
        .textSelection(.enabled)
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .panel(radius: Radius.sm)
    }
}
