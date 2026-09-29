import Foundation
import Markdown

/// A message's Markdown as the transcript draws it (spec, Type; companion ADR 0009).
/// swift-markdown parses; this file is the only one that imports it, and it hands the
/// views plain values of our own: blocks, and runs of styled text in them. The views
/// draw those in the app's two faces (`MarkdownView`), never at a larger size.
enum MarkdownText {
    /// A block of a message, top to bottom.
    enum Block: Equatable, Sendable {
        /// Never drawn larger than reading text, only heavier and wider (ADR 0008).
        case heading(level: Int, [Span])
        case paragraph([Span])
        /// A fenced or indented block, in the mono face. JSON is pretty-printed.
        case code(language: String?, text: String)
        /// Each item is its own blocks; `start` numbers an ordered list.
        case list(ordered: Bool, start: Int, items: [Item])
        /// Drawn with a `│` rule down its side.
        case quote([Block])
        /// A GitHub table, drawn as a mono grid.
        case table(header: [[Span]], rows: [[[Span]]])
        /// A thematic break, drawn as a hairline.
        case rule
    }

    struct Item: Equatable, Sendable {
        /// `true` or `false` for a task list's checkbox, nil for a plain item.
        var checked: Bool?
        var blocks: [Block]
    }

    /// A run of text with one style.
    struct Span: Equatable, Sendable {
        var text: String
        var style: Style = []
        /// A step this run cites ("step 16"): drawn as an evidence chip that seeks it.
        var step: Int?
        /// A link's destination, as written.
        var link: String?
        /// While a message streams in (`StreamReveal.cut`), the word this run is part of
        /// when that word is still fading in; nil for settled text.
        var arriving: Int?

        init(_ text: String, _ style: Style = [], step: Int? = nil, link: String? = nil) {
            self.text = text
            self.style = style
            self.step = step
            self.link = link
        }
    }

    struct Style: OptionSet, Hashable, Sendable {
        let rawValue: Int
        static let emphasis = Style(rawValue: 1 << 0)
        static let strong = Style(rawValue: 1 << 1)
        static let code = Style(rawValue: 1 << 2)
        static let strikethrough = Style(rawValue: 1 << 3)
    }

    /// The blocks of `text`. A citation of a step becomes a chip only when `steps` holds
    /// it (the run's record); nil when the record is not loaded yet, which cites none.
    /// A single line break stays a line break: agents write lists and quoted screen text
    /// one per line without Markdown's blank lines, as a comment on GitHub reads them.
    static func blocks(_ text: String, steps: Set<Int>? = nil) -> [Block] {
        // Smart punctuation off: it would turn an agent's "--" into a dash it never wrote.
        let document = Document(parsing: text, options: [.disableSmartOpts])
        var converter = Converter(steps: steps)
        return document.children.compactMap { converter.block($0) }
    }

    /// Schemes a link may open. Messages quote untrusted screen and web text, so a link
    /// to a file, an app's own scheme or anything else stays words.
    static let openableSchemes: Set<String> = ["http", "https", "mailto"]

    /// A link's destination as a URL a click may open, or nil when it must stay words.
    static func openableURL(_ destination: String) -> URL? {
        guard let url = URL(string: destination), let scheme = url.scheme?.lowercased(),
              openableSchemes.contains(scheme)
        else { return nil }
        return url
    }

    /// The text of spans, as a person would read it aloud.
    static func plain(_ spans: [Span]) -> String {
        spans.map(\.text).joined()
    }

    /// The blocks' words as plain lines, for a place that shows a few lines of them.
    static func plain(_ blocks: [Block]) -> String {
        blocks.map { block -> String in
            switch block {
            case .heading(_, let spans), .paragraph(let spans): plain(spans)
            case .code(_, let text): text
            case .list(_, _, let items): items.map { "• " + plain($0.blocks) }.joined(separator: "\n")
            case .quote(let inner): plain(inner)
            case .table(let header, let rows): ([header] + rows).map { $0.map(plain).joined(separator: "  ") }.joined(separator: "\n")
            case .rule: ""
            }
        }
        .filter { !$0.isEmpty }
        .joined(separator: "\n")
    }

    /// Every step cited in the text, in order, once each.
    static func citedSteps(_ blocks: [Block]) -> [Int] {
        var seen = Set<Int>()
        var out: [Int] = []
        func visit(_ spans: [Span]) {
            for step in spans.compactMap(\.step) where seen.insert(step).inserted { out.append(step) }
        }
        func visit(_ block: Block) {
            switch block {
            case .heading(_, let spans), .paragraph(let spans): visit(spans)
            case .list(_, _, let items): items.flatMap(\.blocks).forEach(visit)
            case .quote(let inner): inner.forEach(visit)
            case .table(let header, let rows):
                header.forEach(visit)
                rows.flatMap { $0 }.forEach(visit)
            case .code, .rule: break
            }
        }
        blocks.forEach(visit)
        return out
    }

    /// JSON laid out one key per line with sorted keys, or nil when `text` is not a JSON
    /// object or array. Numbers print as the agent wrote them in value (`0.2`, never
    /// `0.20000000000000001`), and a key is followed by `": "`, as people write JSON.
    static func prettyJSON(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = trimmed.first, first == "{" || first == "[",
              let value = try? JSONDecoder().decode(JSONValue.self, from: Data(trimmed.utf8))
        else { return nil }
        return layOut(value, indent: "")
    }

    private static func layOut(_ value: JSONValue, indent: String) -> String {
        let inner = indent + "  "
        switch value {
        case .null: return "null"
        case .bool(let flag): return flag ? "true" : "false"
        case .int(let number): return String(number)
        case .double(let number): return number.isFinite ? String(number) : "null"
        case .string(let words): return quoted(words)
        case .array(let items):
            guard !items.isEmpty else { return "[]" }
            return "[\n" + items.map { inner + layOut($0, indent: inner) }.joined(separator: ",\n") + "\n\(indent)]"
        case .object(let fields):
            guard !fields.isEmpty else { return "{}" }
            let lines = fields.keys.sorted().map { key in
                inner + quoted(key) + ": " + layOut(fields[key] ?? .null, indent: inner)
            }
            return "{\n" + lines.joined(separator: ",\n") + "\n\(indent)}"
        }
    }

    private static func quoted(_ words: String) -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.withoutEscapingSlashes]
        return (try? encoder.encode(words)).map { String(decoding: $0, as: UTF8.self) } ?? "\"\(words)\""
    }

    // MARK: - Converting swift-markdown's tree

    private struct Converter {
        let steps: Set<Int>?

        mutating func block(_ markup: any Markup) -> Block? {
            switch markup {
            case let heading as Heading:
                return .heading(level: heading.level, spans(heading.children))
            case let paragraph as Paragraph:
                let spans = spans(paragraph.children)
                return spans.isEmpty ? nil : .paragraph(spans)
            case let code as Markdown.CodeBlock:
                return codeBlock(language: code.language, code.code)
            case let html as HTMLBlock:
                return codeBlock(language: "html", html.rawHTML)
            case let list as UnorderedList:
                return .list(ordered: false, start: 1, items: list.listItems.map { item($0) })
            case let list as OrderedList:
                return .list(ordered: true, start: Int(list.startIndex), items: list.listItems.map { item($0) })
            case let quote as BlockQuote:
                return .quote(quote.children.compactMap { block($0) })
            case let table as Markdown.Table:
                let header: [[Span]] = table.head.cells.map { spans($0.children) }
                let rows: [[[Span]]] = table.body.rows.map { row in row.cells.map { spans($0.children) } }
                return .table(header: header, rows: rows)
            case is ThematicBreak:
                return .rule
            default:
                let text = markup.format().trimmingCharacters(in: .newlines)
                return text.isEmpty ? nil : .paragraph([Span(text)])
            }
        }

        private func codeBlock(language: String?, _ raw: String) -> Block {
            let code = raw.hasSuffix("\n") ? String(raw.dropLast()) : raw
            let language = language.flatMap { $0.isEmpty ? nil : $0 }
            if language == nil || language == "json", let pretty = MarkdownText.prettyJSON(code) {
                return .code(language: "json", text: pretty)
            }
            return .code(language: language, text: code)
        }

        private mutating func item(_ item: ListItem) -> Item {
            let checked: Bool? = switch item.checkbox {
            case .checked?: true
            case .unchecked?: false
            case nil: nil
            }
            return Item(checked: checked, blocks: item.children.compactMap { block($0) })
        }

        private func spans(_ children: some Sequence<any Markup>) -> [Span] {
            var out: [Span] = []
            for child in children { append(child, style: [], link: nil, to: &out) }
            return Self.merged(Self.trimmed(out))
        }

        private func append(_ markup: any Markup, style: Style, link: String?, to out: inout [Span]) {
            switch markup {
            case let text as Markdown.Text:
                // Code and links keep their words as written; prose gets its citations.
                if style.contains(.code) || link != nil {
                    out.append(Span(text.string, style, link: link))
                } else {
                    out.append(contentsOf: cited(text.string, style: style))
                }
            case let code as InlineCode:
                out.append(Span(code.code, style.union(.code), link: link))
            case is SoftBreak, is LineBreak:
                out.append(Span("\n", style))
            case let emphasis as Emphasis:
                for child in emphasis.children { append(child, style: style.union(.emphasis), link: link, to: &out) }
            case let strong as Strong:
                for child in strong.children { append(child, style: style.union(.strong), link: link, to: &out) }
            case let strike as Strikethrough:
                for child in strike.children { append(child, style: style.union(.strikethrough), link: link, to: &out) }
            case let target as Markdown.Link:
                for child in target.children { append(child, style: style, link: target.destination ?? link, to: &out) }
            case let image as Markdown.Image:
                // A picture in a message cannot be fetched (the app reads the daemon only):
                // its words stand in for it.
                let words = image.plainText.isEmpty ? (image.source ?? "image") : image.plainText
                out.append(Span(words, style))
            case let html as InlineHTML:
                out.append(Span(html.rawHTML, style.union(.code)))
            default:
                if let plain = markup as? any PlainTextConvertibleMarkup {
                    out.append(Span(plain.plainText, style, link: link))
                }
            }
        }

        /// Prose with each cited step split out as its own span: "step 16" whole, and the
        /// numbers of "steps 4, 5 and 6" one by one. Only steps in the record.
        private func cited(_ text: String, style: Style) -> [Span] {
            guard let steps, let regex = Self.citation else { return [Span(text, style)] }
            var out: [Span] = []
            var cursor = text.startIndex
            for match in regex.matches(in: text, range: NSRange(text.startIndex..., in: text)) {
                guard let whole = Range(match.range, in: text),
                      let numbers = Range(match.range(at: 1), in: text) else { continue }
                let found = Self.numbers(in: text, numbers)
                guard !found.isEmpty, found.allSatisfy({ steps.contains($0.value) }) else { continue }
                if cursor < whole.lowerBound { out.append(Span(String(text[cursor..<whole.lowerBound]), style)) }
                if found.count == 1, let only = found.first {
                    out.append(Span(String(text[whole.lowerBound..<only.range.upperBound]), style, step: only.value))
                    cursor = only.range.upperBound
                } else {
                    var at = whole.lowerBound
                    for number in found {
                        if at < number.range.lowerBound { out.append(Span(String(text[at..<number.range.lowerBound]), style)) }
                        out.append(Span(String(text[number.range]), style, step: number.value))
                        at = number.range.upperBound
                    }
                    cursor = at
                }
            }
            if cursor < text.endIndex { out.append(Span(String(text[cursor...]), style)) }
            return out
        }

        /// "step 26", "Steps 30 and 31", "steps 4, 5 and 6": the same forms
        /// `VerdictCheck.steps(namedIn:)` reads, so a chip and a check agree.
        private static let citation = try? NSRegularExpression(
            pattern: #"\bsteps?\s+(\d+\b(?!\.\d)(?:\s*(?:,\s*(?:and\b|&)?|\band\b|&)\s*\d+\b(?!\.\d))*)"#,
            options: [.caseInsensitive])

        private static func numbers(in text: String, _ range: Range<String.Index>) -> [(value: Int, range: Range<String.Index>)] {
            var out: [(Int, Range<String.Index>)] = []
            var index = range.lowerBound
            while index < range.upperBound {
                guard text[index].isNumber else { index = text.index(after: index); continue }
                let start = index
                while index < range.upperBound, text[index].isNumber { index = text.index(after: index) }
                if let value = Int(text[start..<index]) { out.append((value, start..<index)) }
            }
            return out
        }

        /// No line break at either end of a block: a trailing soft break would draw an
        /// empty last line.
        private static func trimmed(_ spans: [Span]) -> [Span] {
            var spans = spans
            while spans.first?.text == "\n" { spans.removeFirst() }
            while spans.last?.text == "\n" { spans.removeLast() }
            return spans
        }

        /// Neighbours with one style, citation and link join, so the view draws few runs
        /// and tests read whole phrases.
        private static func merged(_ spans: [Span]) -> [Span] {
            var out: [Span] = []
            for span in spans where !span.text.isEmpty {
                if var last = out.last, last.style == span.style, last.link == span.link,
                   last.step == nil, span.step == nil {
                    last.text += span.text
                    out[out.count - 1] = last
                } else {
                    out.append(span)
                }
            }
            return out
        }
    }
}
