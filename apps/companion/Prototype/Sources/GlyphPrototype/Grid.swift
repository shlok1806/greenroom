import AppKit
import CoreText
import SwiftUI

// PROTOTYPE: the character-cell grid. Every size in the window is a whole number of cells.

/// Registers the bundled Monaspace faces for this process. Called once at launch.
enum PrototypeFonts {
    static func register() {
        let urls = (Bundle.module.urls(forResourcesWithExtension: "otf", subdirectory: nil) ?? [])
            + (Bundle.module.urls(forResourcesWithExtension: "otf", subdirectory: "Fonts") ?? [])
        for url in urls {
            var error: Unmanaged<CFError>?
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, &error)
        }
    }
}

/// The cell size, from Monaspace Neon 13 pt. Monaspace advances are 0.62 em (8.06 pt);
/// the cell rounds that to a whole point and every text run carries the difference as
/// tracking, so columns land on whole points and box lines stay crisp at 1x.
struct GridMetrics: Sendable, Equatable {
    var fontSize: CGFloat
    var advance: CGFloat
    var cellW: CGFloat
    var cellH: CGFloat

    var tracking: CGFloat { cellW - advance }

    static let standard: GridMetrics = {
        let size: CGFloat = 13
        let font = CTFontCreateWithName("MonaspaceNeon-Regular" as CFString, size, nil)
        var glyph = CGGlyph(0)
        var chars: [UniChar] = [48]
        CTFontGetGlyphsForCharacters(font, &chars, &glyph, 1)
        var adv = CGSize.zero
        CTFontGetAdvancesForGlyphs(font, .horizontal, &glyph, &adv, 1)
        let advance = adv.width > 1 ? adv.width : size * 0.62
        let natural = CTFontGetAscent(font) + CTFontGetDescent(font) + CTFontGetLeading(font)
        return GridMetrics(
            fontSize: size,
            advance: advance,
            cellW: advance.rounded(),
            cellH: max(18, (natural * 1.1).rounded())
        )
    }()

    func w(_ cols: Int) -> CGFloat { CGFloat(cols) * cellW }
    func h(_ rows: Int) -> CGFloat { CGFloat(rows) * cellH }
    func size(_ cols: Int, _ rows: Int) -> CGSize { CGSize(width: w(cols), height: h(rows)) }
}

/// Resolved lazily, after `PrototypeFonts.register()` has run.
let G = GridMetrics.standard

/// A rectangle on the grid, in cells.
struct CellRect: Equatable, Sendable {
    var col: Int
    var row: Int
    var cols: Int
    var rows: Int

    static let zero = CellRect(col: 0, row: 0, cols: 0, rows: 0)
    var maxCol: Int { col + cols }
    var maxRow: Int { row + rows }
}

// MARK: - Faces

/// One face per voice (decision 5). All share Neon's metrics, so they mix on one line.
enum Face: String, Sendable, CaseIterable {
    case neon = "Neon"
    case xenon = "Xenon"
    case radon = "Radon"
    case argon = "Argon"
    case krypton = "Krypton"
}

enum Weight: String, Sendable {
    case regular = "Regular"
    case medium = "Medium"
    case bold = "Bold"
}

enum FontCache {
    static func font(_ face: Face, _ weight: Weight = .regular, size: CGFloat = 13) -> Font {
        Font.custom("Monaspace\(face.rawValue)-\(weight.rawValue)", fixedSize: size)
    }
}

/// Who is speaking, which picks the face. `human` resolves to Radon or Argon at render.
enum Voice: Sendable, Equatable {
    case chrome, coder, verifier, human, tool
}

// MARK: - Styled spans

/// A colour by ANSI meaning, resolved against the live theme at render time.
indirect enum Ink: Sendable, Hashable {
    case fg, dim, bg, cursor, cursorText, selection
    case role(Role)
    case slot(Int)
    case alpha(Ink, Double)
}

struct Span: Sendable, Hashable {
    var text: String
    var voice: Voice = .chrome
    var weight: Weight = .regular
    var ink: Ink = .fg
    var back: Ink?
    var italic = false
    var underline = false

    init(_ text: String, _ voice: Voice = .chrome, _ weight: Weight = .regular, ink: Ink = .fg, back: Ink? = nil, italic: Bool = false, underline: Bool = false) {
        self.text = text
        self.voice = voice
        self.weight = weight
        self.ink = ink
        self.back = back
        self.italic = italic
        self.underline = underline
    }

    func with(_ text: String) -> Span {
        var s = self
        s.text = text
        return s
    }
}

typealias GridLine = [Span]

extension Array where Element == Span {
    var cellCount: Int { reduce(0) { $0 + $1.text.count } }
    var plain: String { map(\.text).joined() }

    /// Pads or clips to exactly `width` cells.
    func fitted(_ width: Int, pad: Span = Span(" ")) -> GridLine {
        var out: GridLine = []
        var used = 0
        for span in self where used < width {
            let room = width - used
            if span.text.count <= room {
                out.append(span)
                used += span.text.count
            } else {
                let cut = room > 1 ? String(span.text.prefix(room - 1)) + "…" : String(span.text.prefix(room))
                out.append(span.with(cut))
                used = width
            }
        }
        if used < width { out.append(pad.with(String(repeating: " ", count: width - used))) }
        return out
    }

    /// Clips to `count` visible cells (for the type effect).
    func prefixCells(_ count: Int) -> GridLine {
        var out: GridLine = []
        var left = count
        for span in self {
            if left <= 0 { break }
            if span.text.count <= left {
                out.append(span)
                left -= span.text.count
            } else {
                out.append(span.with(String(span.text.prefix(left))))
                left = 0
            }
        }
        return out
    }
}

/// Left and right parts laid into one line of `width` cells.
func spread(_ left: GridLine, _ right: GridLine, width: Int) -> GridLine {
    let room = width - right.cellCount - 1
    if room < 4 { return left.fitted(width) }
    let l = left.cellCount > room ? left.fitted(room) : left
    let gap = max(1, width - l.cellCount - right.cellCount)
    return l + [Span(String(repeating: " ", count: gap))] + right
}

// MARK: - Word wrap on the grid

/// Greedy word wrap by cells, keeping styles. `hang` is repeated at the start of every
/// line after the first (a hanging indent).
func wrap(_ line: GridLine, width: Int, first: GridLine = [], hang: GridLine = []) -> [GridLine] {
    struct Cell { var ch: Character; var style: Int }
    let styles = line
    var cells: [Cell] = []
    for (i, span) in line.enumerated() {
        for ch in span.text { cells.append(Cell(ch: ch, style: i)) }
    }
    func spans(_ run: ArraySlice<Cell>) -> GridLine {
        var out: GridLine = []
        var buf = ""
        var style = -1
        for c in run {
            if c.style != style {
                if !buf.isEmpty { out.append(styles[style].with(buf)) }
                buf = ""
                style = c.style
            }
            buf.append(c.ch)
        }
        if !buf.isEmpty, style >= 0 { out.append(styles[style].with(buf)) }
        return out
    }
    var result: [GridLine] = []
    var index = 0
    var isFirst = true
    while index < cells.count || result.isEmpty {
        let prefix = isFirst ? first : hang
        let avail = max(1, width - prefix.cellCount)
        if index >= cells.count { result.append(prefix); break }
        var end = min(cells.count, index + avail)
        if end < cells.count {
            // back up to the last space inside the line
            var cut = end
            while cut > index, cells[cut].ch != " " { cut -= 1 }
            if cut > index { end = cut }
        }
        var lineCells = cells[index..<end]
        while let last = lineCells.last, last.ch == " " { lineCells = lineCells.dropLast() }
        result.append(prefix + spans(lineCells))
        index = end
        while index < cells.count, cells[index].ch == " " { index += 1 }
        isFirst = false
    }
    return result
}

// MARK: - Rendering a line

/// Characters Monaspace lacks or that must fill the whole cell: drawn as sprites
/// (the way Ghostty and kitty draw box, block and braille glyphs), or, for the
/// few symbol glyphs from a fallback font, boxed into exactly one cell.
enum Sprite {
    static func isSprite(_ ch: Character) -> Bool {
        guard let s = ch.unicodeScalars.first?.value else { return false }
        return (0x2500...0x257F).contains(s) || (0x2580...0x259F).contains(s) || (0x2800...0x28FF).contains(s)
    }

    static let fallback: Set<Character> = ["⏎", "⌘", "⌫", "⌥", "⎋", "⇥", "⏻"]
}

/// One line on the grid: runs of real text, each exactly `n` cells wide, with sprite
/// cells for box, block and braille characters.
struct GridText: View {
    @Environment(PrototypeModel.self) private var model
    let line: GridLine
    var width: Int?
    /// What VoiceOver reads when the drawn text is mid-effect (decode, type).
    var label: String?

    var body: some View {
        let palette = model.palette
        HStack(spacing: 0) {
            ForEach(Array(segments.enumerated()), id: \.offset) { _, seg in
                switch seg {
                case .text(let spans):
                    Text(attributed(spans, palette))
                        .lineLimit(1)
                        .fixedSize()
                        .frame(width: G.w(spans.cellCount), height: G.cellH, alignment: .leading)
                        .background(alignment: .leading) { backs(spans, palette) }
                case .sprites(let chars, let span):
                    SpriteRun(chars: chars, color: palette.color(span.ink))
                        .frame(width: G.w(chars.count), height: G.cellH)
                        .background(span.back.map { palette.color($0) } ?? .clear)
                case .symbol(let ch, let span):
                    Text(String(ch))
                        .font(.system(size: 11, weight: span.weight == .bold ? .bold : .regular))
                        .foregroundStyle(palette.color(span.ink))
                        .frame(width: G.cellW, height: G.cellH)
                        .background(span.back.map { palette.color($0) } ?? .clear)
                }
            }
            if let width, width > line.cellCount {
                Color.clear.frame(width: G.w(width - line.cellCount), height: G.cellH)
            }
        }
        .frame(height: G.cellH)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label ?? line.plain.trimmingCharacters(in: .whitespaces))
    }

    private enum Segment {
        case text(GridLine)
        case sprites([Character], Span)
        case symbol(Character, Span)
    }

    private var segments: [Segment] {
        var out: [Segment] = []
        var current: GridLine = []
        func flush() {
            if !current.isEmpty { out.append(.text(current)) }
            current = []
        }
        for span in line {
            var buf = ""
            for ch in span.text {
                if Sprite.isSprite(ch) || Sprite.fallback.contains(ch) {
                    if !buf.isEmpty { current.append(span.with(buf)); buf = "" }
                    flush()
                    if Sprite.isSprite(ch), case .sprites(let chars, let prev)? = out.last, prev == span {
                        out[out.count - 1] = .sprites(chars + [ch], span)
                    } else {
                        out.append(Sprite.isSprite(ch) ? .sprites([ch], span) : .symbol(ch, span))
                    }
                } else {
                    buf.append(ch)
                }
            }
            if !buf.isEmpty { current.append(span.with(buf)) }
        }
        flush()
        return out
    }

    private func attributed(_ spans: GridLine, _ palette: ThemePalette) -> AttributedString {
        var out = AttributedString()
        for span in spans {
            var a = AttributedString(span.text)
            a.font = FontCache.font(model.face(for: span.voice), span.weight)
            a.foregroundColor = palette.color(span.ink)
            a.tracking = G.tracking
            if span.underline { a.underlineStyle = .single }
            out += a
        }
        return out
    }

    /// Span backgrounds as full-cell blocks, so a highlight is a clean band of cells.
    private func backs(_ spans: GridLine, _ palette: ThemePalette) -> some View {
        HStack(spacing: 0) {
            ForEach(Array(spans.enumerated()), id: \.offset) { _, span in
                (span.back.map { palette.color($0) } ?? Color.clear)
                    .frame(width: G.w(span.text.count), height: G.cellH)
            }
        }
    }
}

/// Lines stacked on the grid, one cell high each.
struct GridLines: View {
    let lines: [GridLine]
    var width: Int?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                GridText(line: line, width: width)
            }
        }
    }
}
