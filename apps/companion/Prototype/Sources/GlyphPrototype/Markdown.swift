import Foundation

// PROTOTYPE: a hand-rolled markdown subset rendered onto the grid, Glamour-style.
// Headings are bold and coloured, never bigger; code blocks are boxed; bullets are `•`.
// The real app parses with swift-markdown (decision 13) and renders the same way.

enum Markdown {
    static let keywords: Set<String> = ["var", "let", "func", "return", "struct", "import", "if", "else", "for", "in", "Double", "Int", "String"]

    static func render(_ text: String, voice: Voice, width: Int, ink: Ink = .fg) -> [GridLine] {
        var out: [GridLine] = []
        var paragraph: [String] = []
        let lines = text.components(separatedBy: "\n")
        var i = 0

        func flushParagraph() {
            guard !paragraph.isEmpty else { return }
            let joined = paragraph.joined(separator: " ")
            out += wrap(inline(joined, voice: voice, ink: ink), width: width)
            paragraph = []
        }
        func gap() {
            if let last = out.last, !last.plain.trimmingCharacters(in: .whitespaces).isEmpty { out.append([]) }
        }

        while i < lines.count {
            let raw = lines[i]
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                flushParagraph()
                gap()
                let lang = String(line.dropFirst(3))
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                    code.append(lines[i])
                    i += 1
                }
                out += codeBox(code, lang: lang, width: width)
                out.append([])
            } else if line.hasPrefix("#") {
                flushParagraph()
                gap()
                let level = line.prefix { $0 == "#" }.count
                let title = line.dropFirst(level).trimmingCharacters(in: .whitespaces)
                out.append([
                    Span(String(repeating: "#", count: level) + " ", voice, .bold, ink: .alpha(.role(.heading), 0.55)),
                    Span(title, voice, .bold, ink: .role(.heading)),
                ])
                out.append([])
            } else if line.hasPrefix("- ") || line.hasPrefix("* ") {
                flushParagraph()
                let body = String(line.dropFirst(2))
                out += wrap(inline(body, voice: voice, ink: ink), width: width,
                            first: [Span("  • ", voice, ink: .dim)], hang: [Span("    ")])
            } else if line.isEmpty {
                flushParagraph()
                gap()
            } else {
                paragraph.append(line)
            }
            i += 1
        }
        flushParagraph()
        while let last = out.last, last.plain.trimmingCharacters(in: .whitespaces).isEmpty { out.removeLast() }
        return out
    }

    /// `**bold**` and `` `code` `` spans. Inline code is Krypton on the selection colour.
    static func inline(_ text: String, voice: Voice, ink: Ink) -> GridLine {
        var out: GridLine = []
        var buf = ""
        var bold = false
        var chars = Array(text)[...]
        func flush() {
            if !buf.isEmpty { out.append(Span(buf, voice, bold ? .bold : .regular, ink: ink)) }
            buf = ""
        }
        while let ch = chars.first {
            if ch == "*", chars.dropFirst().first == "*" {
                flush()
                bold.toggle()
                chars = chars.dropFirst(2)
            } else if ch == "`" {
                flush()
                chars = chars.dropFirst()
                var code = ""
                while let c = chars.first, c != "`" { code.append(c); chars = chars.dropFirst() }
                chars = chars.dropFirst()
                out.append(Span(code, .tool, .regular, ink: .fg, back: .selection))
            } else {
                buf.append(ch)
                chars = chars.dropFirst()
            }
        }
        flush()
        return out
    }

    static func codeBox(_ code: [String], lang: String, width: Int) -> [GridLine] {
        let inner = max(4, width - 4)
        let label = lang.isEmpty ? "" : "─ \(lang) "
        let border: Ink = .role(.border)
        var out: GridLine = [Span("╭" + label, .tool, ink: border),
                             Span(String(repeating: "─", count: max(0, width - 2 - label.count)) + "╮", .tool, ink: border)]
        var lines: [GridLine] = [out]
        for raw in code {
            var body = highlight(raw)
            body = body.fitted(inner)
            out = [Span("│ ", ink: border)] + body + [Span(" │", ink: border)]
            lines.append(out)
        }
        lines.append([Span("╰" + String(repeating: "─", count: max(0, width - 2)) + "╯", ink: border)])
        return lines
    }

    /// Just enough colour to read code: keywords bold, numbers in the heading colour.
    static func highlight(_ line: String) -> GridLine {
        var out: GridLine = []
        var word = ""
        func flush() {
            guard !word.isEmpty else { return }
            if keywords.contains(word) {
                out.append(Span(word, .tool, .bold, ink: .fg))
            } else if word.first?.isNumber == true {
                out.append(Span(word, .tool, ink: .role(.heading)))
            } else {
                out.append(Span(word, .tool, ink: .fg))
            }
            word = ""
        }
        for ch in line {
            if ch.isLetter || ch.isNumber || ch == "_" || ch == "." && word.first?.isNumber == true {
                word.append(ch)
            } else {
                flush()
                out.append(Span(String(ch), .tool, ink: .fg))
            }
        }
        flush()
        return out
    }
}
