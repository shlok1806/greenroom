import SwiftUI

// PROTOTYPE: box-drawing, block and braille characters drawn as cell sprites, the way
// Ghostty and kitty draw them: the glyph is still a character on the grid, but its lines
// are computed from the cell so they always meet their neighbours, at any line height.

enum SpriteDraw {
    /// A 1 px line on whole-point cells, centred on a pixel so it stays crisp.
    static func mid(_ a: CGFloat, _ len: CGFloat) -> CGFloat { a + (len / 2).rounded(.down) + 0.5 }

    struct Arms: OptionSet {
        let rawValue: Int
        static let up = Arms(rawValue: 1)
        static let down = Arms(rawValue: 2)
        static let left = Arms(rawValue: 4)
        static let right = Arms(rawValue: 8)
    }

    static func arms(_ s: UInt32) -> (Arms, heavy: Bool)? {
        switch s {
        case 0x2500: ([.left, .right], false)
        case 0x2501: ([.left, .right], true)
        case 0x2502: ([.up, .down], false)
        case 0x2503: ([.up, .down], true)
        case 0x250C: ([.down, .right], false)
        case 0x2510: ([.down, .left], false)
        case 0x2514: ([.up, .right], false)
        case 0x2518: ([.up, .left], false)
        case 0x251C: ([.up, .down, .right], false)
        case 0x2524: ([.up, .down, .left], false)
        case 0x252C: ([.left, .right, .down], false)
        case 0x2534: ([.left, .right, .up], false)
        case 0x253C: ([.left, .right, .up, .down], false)
        case 0x2574: ([.left], false)
        case 0x2575: ([.up], false)
        case 0x2576: ([.right], false)
        case 0x2577: ([.down], false)
        case 0x2578: ([.left], true)
        case 0x257A: ([.right], true)
        default: nil
        }
    }

    /// Draws one sprite character into `r`.
    static func draw(_ ch: Character, in r: CGRect, color: Color, context: inout GraphicsContext) {
        guard let s = ch.unicodeScalars.first?.value else { return }
        let cx = mid(r.minX, r.width)
        let cy = mid(r.minY, r.height)
        if let (arms, heavy) = arms(s) {
            var p = Path()
            if arms.contains(.left) { p.move(to: CGPoint(x: r.minX, y: cy)); p.addLine(to: CGPoint(x: cx, y: cy)) }
            if arms.contains(.right) { p.move(to: CGPoint(x: cx, y: cy)); p.addLine(to: CGPoint(x: r.maxX, y: cy)) }
            if arms.contains(.up) { p.move(to: CGPoint(x: cx, y: r.minY)); p.addLine(to: CGPoint(x: cx, y: cy)) }
            if arms.contains(.down) { p.move(to: CGPoint(x: cx, y: cy)); p.addLine(to: CGPoint(x: cx, y: r.maxY)) }
            // extend joins by half a line so corners are square, not notched
            context.stroke(p, with: .color(color), style: StrokeStyle(lineWidth: heavy ? 2 : 1, lineCap: .square))
            return
        }
        switch s {
        case 0x256D, 0x256E, 0x256F, 0x2570: // ╭ ╮ ╯ ╰
            let rad = min(r.width, r.height) / 2
            var p = Path()
            let right = s == 0x256D || s == 0x2570
            let down = s == 0x256D || s == 0x256E
            let hx = right ? r.maxX : r.minX
            let vy = down ? r.maxY : r.minY
            p.move(to: CGPoint(x: hx, y: cy))
            p.addArc(tangent1End: CGPoint(x: cx, y: cy), tangent2End: CGPoint(x: cx, y: vy), radius: rad)
            p.addLine(to: CGPoint(x: cx, y: vy))
            context.stroke(p, with: .color(color), style: StrokeStyle(lineWidth: 1))
        case 0x2504, 0x2508, 0x254C: // dashed horizontal
            var p = Path()
            let dash = r.width / 4
            var x = r.minX + dash / 2
            while x < r.maxX { p.move(to: CGPoint(x: x, y: cy)); p.addLine(to: CGPoint(x: min(r.maxX, x + dash), y: cy)); x += dash * 2 }
            context.stroke(p, with: .color(color), lineWidth: 1)
        case 0x2580: context.fill(Path(CGRect(x: r.minX, y: r.minY, width: r.width, height: (r.height / 2).rounded())), with: .color(color))
        case 0x2584:
            let h = (r.height / 2).rounded()
            context.fill(Path(CGRect(x: r.minX, y: r.maxY - h, width: r.width, height: h)), with: .color(color))
        case 0x2581...0x2587: // lower eighths
            let h = (r.height * CGFloat(s - 0x2580) / 8).rounded()
            context.fill(Path(CGRect(x: r.minX, y: r.maxY - h, width: r.width, height: h)), with: .color(color))
        case 0x2588: context.fill(Path(r), with: .color(color))
        case 0x2589...0x258F: // left eighths
            let w = (r.width * CGFloat(0x2590 - s) / 8).rounded()
            context.fill(Path(CGRect(x: r.minX, y: r.minY, width: w, height: r.height)), with: .color(color))
        case 0x2590:
            let w = (r.width / 2).rounded()
            context.fill(Path(CGRect(x: r.maxX - w, y: r.minY, width: w, height: r.height)), with: .color(color))
        case 0x2591, 0x2592, 0x2593:
            let a = [0.22, 0.45, 0.7][Int(s - 0x2591)]
            context.fill(Path(r), with: .color(color.opacity(a)))
        case 0x2594:
            context.fill(Path(CGRect(x: r.minX, y: r.minY, width: r.width, height: (r.height / 8).rounded(.up))), with: .color(color))
        case 0x2596...0x259F:
            let quads: [UInt32: [Int]] = [
                0x2596: [2], 0x2597: [3], 0x2598: [0], 0x2599: [0, 2, 3], 0x259A: [0, 3],
                0x259B: [0, 1, 2], 0x259C: [0, 1, 3], 0x259D: [1], 0x259E: [1, 2], 0x259F: [1, 2, 3],
            ]
            let hw = (r.width / 2).rounded(), hh = (r.height / 2).rounded()
            for q in quads[s] ?? [] {
                let x = q % 2 == 0 ? r.minX : r.minX + hw
                let y = q < 2 ? r.minY : r.minY + hh
                context.fill(Path(CGRect(x: x, y: y, width: q % 2 == 0 ? hw : r.width - hw, height: q < 2 ? hh : r.height - hh)), with: .color(color))
            }
        case 0x2800...0x28FF:
            drawBraille(UInt8(s - 0x2800), in: r, color: color, context: &context)
        default:
            break
        }
    }

    /// Braille dots on a 2 x 4 lattice inside the cell.
    static func braillePath(_ bits: UInt8, in r: CGRect, into p: inout Path) {
        guard bits != 0 else { return }
        let dot = max(1.5, (r.width / 4).rounded())
        let colX = [r.minX + r.width * 0.3, r.minX + r.width * 0.7]
        let top = r.minY + r.height * 0.14
        let step = (r.height * 0.72) / 3
        let map: [(Int, Int)] = [(0, 0), (0, 1), (0, 2), (1, 0), (1, 1), (1, 2), (0, 3), (1, 3)]
        for i in 0..<8 where bits & (1 << i) != 0 {
            let (cx, row) = map[i]
            let x = (colX[cx] - dot / 2).rounded()
            let y = (top + step * CGFloat(row) - dot / 2).rounded()
            p.addRect(CGRect(x: x, y: y, width: dot, height: dot))
        }
    }

    static func drawBraille(_ bits: UInt8, in r: CGRect, color: Color, context: inout GraphicsContext) {
        var p = Path()
        braillePath(bits, in: r, into: &p)
        context.fill(p, with: .color(color))
    }
}

/// A single sprite character in one cell.
struct SpriteCell: View {
    let ch: Character
    let color: Color

    var body: some View {
        Canvas { context, size in
            SpriteDraw.draw(ch, in: CGRect(origin: .zero, size: size), color: color, context: &context)
        }
        .accessibilityHidden(true)
    }
}

/// A run of sprite characters in one Canvas.
struct SpriteRun: View {
    let chars: [Character]
    let color: Color

    var body: some View {
        Canvas { context, size in
            for (i, ch) in chars.enumerated() {
                let r = CGRect(x: G.w(i), y: 0, width: G.cellW, height: size.height)
                SpriteDraw.draw(ch, in: r, color: color, context: &context)
            }
        }
        .accessibilityHidden(true)
    }
}

// MARK: - Box frames

/// How a box's top edge meets what is above it.
enum BoxTop: Equatable {
    case rounded
    /// Joined under another box: `├──┤`.
    case tee
    /// Nothing drawn; the box above owns this row.
    case open
}

/// A pane border drawn with box-drawing glyphs on the grid. Sizes itself in whole
/// cells from its frame, so a spring-animated frame redraws in cell steps.
/// `progress` < 1 draws the border tracing itself out from the top-left corner.
struct BoxFrame: View {
    var color: Color
    var top: BoxTop = .rounded
    var bottomOpen = false
    var progress: Double = 1

    var body: some View {
        Canvas { context, size in
            let cols = Int((size.width / G.cellW).rounded(.down))
            let rows = Int((size.height / G.cellH).rounded(.down))
            guard cols >= 2, rows >= 1 else { return }
            let cells = BoxFrame.perimeter(cols: cols, rows: rows, top: top, bottomOpen: bottomOpen)
            let total = Double(cells.count)
            let reach = progress * total / 2
            for (i, cell) in cells.enumerated() {
                let d = min(Double(i), total - Double(i))
                if d > reach + 0.5 { continue }
                let rect = CGRect(x: G.w(cell.col), y: G.h(cell.row), width: G.cellW, height: G.cellH)
                SpriteDraw.draw(cell.ch, in: rect, color: color, context: &context)
            }
        }
        .accessibilityHidden(true)
    }

    struct PCell { var col: Int; var row: Int; var ch: Character }

    /// Perimeter cells in clockwise order from the top-left corner.
    static func perimeter(cols: Int, rows: Int, top: BoxTop, bottomOpen: Bool) -> [PCell] {
        var out: [PCell] = []
        let last = cols - 1
        let bottom = rows - 1
        let tl: Character = top == .tee ? "├" : "╭"
        let tr: Character = top == .tee ? "┤" : "╮"
        if top != .open {
            out.append(PCell(col: 0, row: 0, ch: tl))
            for c in 1..<last { out.append(PCell(col: c, row: 0, ch: "─")) }
            out.append(PCell(col: last, row: 0, ch: tr))
        }
        let firstSide = top == .open ? 0 : 1
        let lastSide = bottomOpen ? bottom : bottom - 1
        if lastSide >= firstSide {
            for r in firstSide...lastSide { out.append(PCell(col: last, row: r, ch: "│")) }
        }
        if !bottomOpen, rows > 1 {
            out.append(PCell(col: last, row: bottom, ch: "╯"))
            for c in stride(from: last - 1, to: 0, by: -1) { out.append(PCell(col: c, row: bottom, ch: "─")) }
            out.append(PCell(col: 0, row: bottom, ch: "╰"))
        }
        if lastSide >= firstSide {
            for r in stride(from: lastSide, through: firstSide, by: -1) { out.append(PCell(col: 0, row: r, ch: "│")) }
        }
        return out
    }
}

// MARK: - Block letters

/// A three-row figlet-style face made of half blocks, for verdict outcomes. Drawn on the
/// grid, never a bigger font (decision 5: headings never larger).
enum BlockLetters {
    static let glyphs: [Character: [String]] = [
        "P": ["█▀▀▄", "█▄▄▀", "█   "],
        "A": ["▄▀▀▄", "█▄▄█", "█  █"],
        "S": ["▄▀▀▀", " ▀▀▄", "▄▄▄▀"],
        "F": ["█▀▀▀", "█▀▀ ", "█   "],
        "I": ["▀█▀", " █ ", "▄█▄"],
        "L": ["█   ", "█   ", "█▄▄▄"],
    ]

    static func rows(_ word: String) -> [String] {
        var out = ["", "", ""]
        for (i, ch) in word.enumerated() {
            let g = glyphs[ch] ?? ["   ", "   ", "   "]
            for r in 0..<3 { out[r] += (i > 0 ? " " : "") + g[r] }
        }
        return out
    }

    /// Random half-block noise for the decode effect.
    static let noise: [Character] = ["▀", "▄", "█", "▌", "▐", "▚", "▞", "▖", "▝", " "]
}

// MARK: - Spinner

/// The tick vocabulary: a braille spinner, drawn as a sprite.
struct Spinner: View {
    @Environment(PrototypeModel.self) private var model
    var ink: Ink = .role(.live)
    static let frames = FX.spinnerFrames

    var body: some View {
        let color = model.palette.color(ink)
        if model.reduceMotion {
            SpriteCell(ch: "⠶", color: color).frame(width: G.cellW, height: G.cellH)
        } else {
            TimelineView(.periodic(from: .now, by: 0.08)) { ctx in
                let i = Int(ctx.date.timeIntervalSinceReferenceDate / 0.08) % Spinner.frames.count
                SpriteCell(ch: Spinner.frames[i], color: color)
            }
            .frame(width: G.cellW, height: G.cellH)
        }
    }
}
