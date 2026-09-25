import SwiftUI

// PROTOTYPE: adaptive layout by cell columns (decision 10). Every rect is whole cells.

/// Revision 18b: a 4/8 pt spacing grid for the parts of the UI that are no longer
/// cell-quantized (card padding, prose blocks, the message list).
enum Space {
    static let xs: CGFloat = 4
    static let sm: CGFloat = 8
    static let md: CGFloat = 12
    static let lg: CGFloat = 16
    static let xl: CGFloat = 24
}

/// A quiet panel (revision 18b) sized by its own content, for pieces that float free of
/// the cell grid: cards, prose blocks. `accent` draws a slightly heavier coloured border
/// (a verdict's pass/fail, a question's needs-you) in place of the plain hairline.
struct QuietCard<Content: View>: View {
    @Environment(PrototypeModel.self) private var model
    var accent: Ink?
    var radius: CGFloat = 8
    @ViewBuilder var content: () -> Content

    var body: some View {
        let palette = model.palette
        content()
            .padding(Space.md)
            .background(RoundedRectangle(cornerRadius: radius, style: .continuous).fill(palette.color(.panel)))
            .overlay(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .strokeBorder(palette.color(accent ?? .alpha(.role(.border), 0.55)), lineWidth: accent == nil ? 1 : 1.5)
            )
    }
}

struct PaneLayout: Equatable {
    var mode: LayoutMode
    var runs: CellRect
    var screen: CellRect
    var steps: CellRect
    var transcript: CellRect
    /// The screen's well inside its pane, in points relative to the pane.
    var well: CGRect

    func rect(_ p: Pane) -> CellRect {
        switch p {
        case .runs: runs
        case .screen: screen
        case .steps: steps
        case .transcript: transcript
        }
    }

    static func make(body: CellRect, mode: LayoutMode, zoomed: Pane?, narrowPane: Pane) -> PaneLayout {
        var l = PaneLayout(mode: mode, runs: .zero, screen: .zero, steps: .zero, transcript: .zero, well: .zero)
        // Revision 18b: generous whitespace between quiet panels, not a 1-cell seam.
        let gut = 2
        switch mode {
        case .wide:
            let runsW = 32
            let trW = max(60, min(84, Int(Double(body.cols) * 0.36)))
            let midW = body.cols - runsW - trW - gut * 2
            l.runs = CellRect(col: body.col, row: body.row, cols: runsW, rows: body.rows)
            l.transcript = CellRect(col: body.maxCol - trW, row: body.row, cols: trW, rows: body.rows)
            let screenRows = screenRowsFor(cols: midW, maxRows: body.rows - 12)
            l.screen = CellRect(col: l.runs.maxCol + gut, row: body.row, cols: midW, rows: screenRows)
            let stepsRow = l.screen.maxRow + gut
            l.steps = CellRect(col: l.screen.col, row: stepsRow, cols: midW, rows: max(4, body.maxRow - stepsRow))
        case .medium:
            let runsW = 5
            let trW = max(50, min(70, Int(Double(body.cols) * 0.4)))
            let midW = body.cols - runsW - trW - gut * 2
            l.runs = CellRect(col: body.col, row: body.row, cols: runsW, rows: body.rows)
            l.transcript = CellRect(col: body.maxCol - trW, row: body.row, cols: trW, rows: body.rows)
            let screenRows = screenRowsFor(cols: midW, maxRows: body.rows - 4)
            l.screen = CellRect(col: l.runs.maxCol + gut, row: body.row, cols: midW, rows: screenRows)
            let stepsRow = l.screen.maxRow + gut
            l.steps = CellRect(col: l.screen.col, row: stepsRow, cols: midW, rows: max(4, body.maxRow - stepsRow))
        case .narrow:
            for p in Pane.allCases {
                l.set(p, p == narrowPane ? body : CellRect(col: body.col, row: body.row, cols: body.cols, rows: body.rows))
            }
        }
        if let z = zoomed {
            l.set(z, body)
        }
        l.well = wellRect(l.screen)
        return l
    }

    mutating func set(_ p: Pane, _ r: CellRect) {
        switch p {
        case .runs: runs = r
        case .screen: screen = r
        case .steps: steps = r
        case .transcript: transcript = r
        }
    }

    /// Rows a screen pane needs so its 4:3 picture fills the width.
    static func screenRowsFor(cols: Int, maxRows: Int) -> Int {
        let w = G.w(cols - 2)
        let h = w * 0.75
        let rows = Int((h / G.cellH).rounded(.up)) + 2
        return max(8, min(maxRows, rows))
    }

    /// The picture inside a screen pane: the inner cells, fitted to 4:3, centred on
    /// whole points.
    static func wellRect(_ r: CellRect) -> CGRect {
        let inner = CGRect(x: G.cellW, y: G.cellH, width: G.w(r.cols - 2), height: G.h(r.rows - 2))
        guard inner.width > 0, inner.height > 0 else { return .zero }
        var w = inner.width
        var h = w * 0.75
        if h > inner.height { h = inner.height; w = h / 0.75 }
        return CGRect(x: (inner.midX - w / 2).rounded(), y: (inner.midY - h / 2).rounded(), width: w.rounded(), height: h.rounded())
    }
}

// MARK: - Cursor targets

/// A cell the block cursor can sit on, and the character under it (drawn inverse).
struct CursorSpot: Equatable {
    var anchor: Anchor<CGRect>
    var ch: Character
    var voice: Voice

    static func == (a: CursorSpot, b: CursorSpot) -> Bool { a.ch == b.ch && a.voice == b.voice }
}

struct CursorKey: PreferenceKey {
    static let defaultValue: [String: CursorSpot] = [:]
    static func reduce(value: inout [String: CursorSpot], nextValue: () -> [String: CursorSpot]) {
        value.merge(nextValue()) { $1 }
    }
}

extension View {
    /// Marks this cell-sized view as a place the cursor can go.
    func cursorSpot(_ id: String, ch: Character = " ", voice: Voice = .chrome) -> some View {
        anchorPreference(key: CursorKey.self, value: .bounds) { [id: CursorSpot(anchor: $0, ch: ch, voice: voice)] }
    }
}

// MARK: - Pane box

/// A pane: a quiet panel (revision 18b) - neutral fill, a thin 1 px hairline, a small
/// radius, and a plain small-caps mono label, never a box-drawn border. The panel that
/// owns keyboard focus gets a short brand-olive accent under its label ("active tab"),
/// never a recoloured border.
struct PaneBox<Content: View>: View {
    @Environment(PrototypeModel.self) private var model
    var rect: CellRect
    var title: GridLine
    var right: GridLine = []
    var focused: Bool
    var borderInk: Ink?
    var top: BoxTop = .rounded
    var drawProgress: Double = 1
    /// Revision 21-22: a very faint olive wash, reserved for chrome (the runs sidebar),
    /// never a content pane - screen, steps and transcript stay neutral.
    var tint = false
    @ViewBuilder var content: () -> Content

    static var radius: CGFloat { 8 }

    var body: some View {
        let palette = model.palette
        let hairline = palette.color(borderInk ?? .alpha(.role(.border), 0.55))
        ZStack(alignment: .topLeading) {
            RoundedRectangle(cornerRadius: Self.radius, style: .continuous)
                .fill(palette.color(.panel))
            if tint {
                RoundedRectangle(cornerRadius: Self.radius, style: .continuous)
                    .fill(palette.color(.chromeTint))
            }
            RoundedRectangle(cornerRadius: Self.radius, style: .continuous)
                .strokeBorder(hairline, lineWidth: 1)
            content()
                .frame(width: G.w(max(0, rect.cols - 2)), height: G.h(max(0, rect.rows - 2)), alignment: .topLeading)
                .clipped()
                .offset(x: G.cellW, y: G.cellH)
            if rect.cols > 8, !title.isEmpty {
                GridText(line: title.fitted(min(title.cellCount, max(0, rect.cols - 4 - right.cellCount - 2)), pad: Span(" ", ink: .dim)))
                    .offset(x: G.w(1) + 4, y: 2)
                if focused {
                    palette.color(.brand)
                        .frame(width: G.w(min(title.cellCount, rect.cols - 4)), height: 2)
                        .offset(x: G.w(1) + 4, y: G.cellH - 3)
                }
                if !right.isEmpty {
                    GridText(line: right)
                        .offset(x: G.w(rect.cols - 1) - G.w(right.cellCount) - 4, y: 2)
                }
            }
        }
        .frame(width: G.w(rect.cols), height: G.h(rect.rows), alignment: .topLeading)
    }
}
