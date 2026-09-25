import SwiftUI

// PROTOTYPE: reading text (revision 18/20). Anything read as a sentence - verifier
// replies, human notes, the task, the verdict reason, questions, empty-state copy, and
// markdown paragraphs - is Mona Sans, off the character grid, wrapped naturally by
// SwiftUI at 14-15 pt with roughly 1.5 line height. Chrome and data (ids, times, code,
// commands, key hints, section labels) stay on the mono grid (`GridText`); this file
// never draws those.

enum ProseBlock: Sendable, Identifiable {
    case paragraph(id: Int, runs: [ProseRun])
    case heading(id: Int, level: Int, text: String)
    case bullet(id: Int, runs: [ProseRun])
    case code(id: Int, lang: String, text: String)

    var id: Int {
        switch self {
        case .paragraph(let id, _), .heading(let id, _, _), .bullet(let id, _), .code(let id, _, _): id
        }
    }
}

struct ProseRun: Sendable, Hashable {
    var text: String
    var bold = false
    var code = false
}

/// A hand-rolled markdown subset, parsed straight to blocks a real `Text` can wrap
/// (never onto the grid). Headings are semibold Mona Sans, never bigger than 18 pt
/// (decision 5: headings never larger, just heavier).
enum Prose {
    static func parse(_ text: String) -> [ProseBlock] {
        var out: [ProseBlock] = []
        var paragraph: [String] = []
        let lines = text.components(separatedBy: "\n")
        var i = 0
        var nextID = 0
        func take() -> Int { defer { nextID += 1 }; return nextID }
        func flush() {
            guard !paragraph.isEmpty else { return }
            out.append(.paragraph(id: take(), runs: inline(paragraph.joined(separator: " "))))
            paragraph = []
        }
        while i < lines.count {
            let raw = lines[i]
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                flush()
                let lang = String(line.dropFirst(3))
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                    code.append(lines[i]); i += 1
                }
                out.append(.code(id: take(), lang: lang, text: code.joined(separator: "\n")))
            } else if line.hasPrefix("#") {
                flush()
                let level = line.prefix { $0 == "#" }.count
                out.append(.heading(id: take(), level: level, text: line.dropFirst(level).trimmingCharacters(in: .whitespaces)))
            } else if line.hasPrefix("- ") || line.hasPrefix("* ") {
                flush()
                out.append(.bullet(id: take(), runs: inline(String(line.dropFirst(2)))))
            } else if line.isEmpty {
                flush()
            } else {
                paragraph.append(line)
            }
            i += 1
        }
        flush()
        return out
    }

    /// `**bold**` and `` `code` `` spans.
    static func inline(_ text: String) -> [ProseRun] {
        var out: [ProseRun] = []
        var buf = ""
        var bold = false
        var chars = Array(text)[...]
        func flushBuf() {
            if !buf.isEmpty { out.append(ProseRun(text: buf, bold: bold)) }
            buf = ""
        }
        while let ch = chars.first {
            if ch == "*", chars.dropFirst().first == "*" {
                flushBuf(); bold.toggle(); chars = chars.dropFirst(2)
            } else if ch == "`" {
                flushBuf()
                chars = chars.dropFirst()
                var code = ""
                while let c = chars.first, c != "`" { code.append(c); chars = chars.dropFirst() }
                chars = chars.dropFirst()
                out.append(ProseRun(text: code, code: true))
            } else {
                buf.append(ch)
                chars = chars.dropFirst()
            }
        }
        flushBuf()
        return out
    }
}

/// A block of reading text: paragraphs, headings, bullets, fenced code. Used for message
/// bodies, the verdict reason, questions, notes, the task and empty-state copy.
struct ProseText: View {
    @Environment(PrototypeModel.self) private var model
    let text: String
    var size: CGFloat = 14.5
    var ink: Ink = .fg

    var body: some View {
        let palette = model.palette
        let color = palette.color(ink)
        VStack(alignment: .leading, spacing: Space.sm) {
            ForEach(Prose.parse(text)) { block in
                ProseBlockView(block: block, size: size, color: color)
            }
        }
    }
}

struct ProseBlockView: View {
    @Environment(PrototypeModel.self) private var model
    let block: ProseBlock
    let size: CGFloat
    let color: Color

    var body: some View {
        let palette = model.palette
        switch block {
        case .paragraph(_, let runs):
            runsText(runs)
                .lineSpacing(size * 0.5) // ~1.5x line height
                .fixedSize(horizontal: false, vertical: true)
        case .heading(_, _, let title):
            // Mona Sans Expanded semibold: a wider, heavier cut for headings, never a
            // bigger size than reading text plus a couple of points.
            Text(title)
                .font(FontCache.prose(.expandedSemibold, size: size + 2))
                .foregroundStyle(color)
                .fixedSize(horizontal: false, vertical: true)
        case .bullet(_, let runs):
            HStack(alignment: .top, spacing: Space.xs + 2) {
                Text("•").font(FontCache.prose(.medium, size: size)).foregroundStyle(color.opacity(0.55))
                runsText(runs).lineSpacing(size * 0.5).fixedSize(horizontal: false, vertical: true)
            }
        case .code(_, let lang, let text):
            VStack(alignment: .leading, spacing: Space.xs) {
                if !lang.isEmpty {
                    Text(lang.uppercased())
                        .font(FontCache.font(.neon, .medium, size: 10))
                        .foregroundStyle(palette.color(.dim))
                }
                Text(text)
                    .font(FontCache.font(.neon, .regular, size: 12.5))
                    .foregroundStyle(palette.color(.fg))
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(Space.sm)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 6, style: .continuous).fill(palette.color(.selection).opacity(0.6)))
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(palette.color(.alpha(.role(.border), 0.5)), lineWidth: 1))
        }
    }

    private func runsText(_ runs: [ProseRun]) -> Text {
        runs.reduce(Text("")) { acc, run in
            let piece: Text
            if run.code {
                piece = Text(run.text)
                    .font(FontCache.font(.neon, .regular, size: size - 1.5))
                    .foregroundColor(color)
            } else {
                piece = Text(run.text)
                    .font(FontCache.prose(run.bold ? .semibold : .regular, size: size))
                    .foregroundColor(color)
            }
            return acc + piece
        }
    }
}

/// A speaker's mono uppercase label and thin coloured left edge (revision 20): who is
/// talking is shown this way now, never by a typeface.
struct SpeakerEdge: ViewModifier {
    @Environment(PrototypeModel.self) private var model
    let color: Color

    func body(content: Content) -> some View {
        HStack(alignment: .top, spacing: 0) {
            Rectangle().fill(color).frame(width: 2)
            content.padding(.leading, Space.sm)
        }
    }
}

extension View {
    func speakerEdge(_ color: Color) -> some View { modifier(SpeakerEdge(color: color)) }
}
