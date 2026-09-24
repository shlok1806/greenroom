import SwiftUI

// PROTOTYPE: adaptive layout by cell columns (decision 10). Every rect is whole cells.

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
        let gut = 1
        switch mode {
        case .wide:
            let runsW = 32
            let trW = max(60, min(84, Int(Double(body.cols) * 0.36)))
            let midW = body.cols - runsW - trW - gut * 2
            l.runs = CellRect(col: body.col, row: body.row, cols: runsW, rows: body.rows)
            l.transcript = CellRect(col: body.maxCol - trW, row: body.row, cols: trW, rows: body.rows)
            let screenRows = screenRowsFor(cols: midW, maxRows: body.rows - 11)
            l.screen = CellRect(col: l.runs.maxCol + gut, row: body.row, cols: midW, rows: screenRows)
            l.steps = CellRect(col: l.screen.col, row: l.screen.maxRow - 1, cols: midW, rows: body.rows - screenRows + 1)
        case .medium:
            let runsW = 5
            let trW = max(50, min(70, Int(Double(body.cols) * 0.4)))
            let midW = body.cols - runsW - trW - gut * 2
            l.runs = CellRect(col: body.col, row: body.row, cols: runsW, rows: body.rows)
            l.transcript = CellRect(col: body.maxCol - trW, row: body.row, cols: trW, rows: body.rows)
            let screenRows = screenRowsFor(cols: midW, maxRows: body.rows - 3)
            l.screen = CellRect(col: l.runs.maxCol + gut, row: body.row, cols: midW, rows: screenRows)
            l.steps = CellRect(col: l.screen.col, row: l.screen.maxRow - 1, cols: midW, rows: max(4, body.maxRow - l.screen.maxRow + 1))
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

/// A pane: a box-drawn border with titles cut into its top edge.
struct PaneBox<Content: View>: View {
    @Environment(PrototypeModel.self) private var model
    var rect: CellRect
    var title: GridLine
    var right: GridLine = []
    var focused: Bool
    var borderInk: Ink?
    var top: BoxTop = .rounded
    var drawProgress: Double = 1
    @ViewBuilder var content: () -> Content

    var body: some View {
        let palette = model.palette
        let ink: Ink = borderInk ?? (focused ? .fg : .role(.border))
        ZStack(alignment: .topLeading) {
            BoxFrame(color: palette.color(ink), top: top, progress: drawProgress)
            content()
                .frame(width: G.w(max(0, rect.cols - 2)), height: G.h(max(0, rect.rows - 2)), alignment: .topLeading)
                .clipped()
                .offset(x: G.cellW, y: G.cellH)
            if rect.cols > 8 {
                let t = title.isEmpty ? [] : [Span(" ", back: .bg)] + title.map { var s = $0; s.back = s.back ?? .bg; return s } + [Span(" ", back: .bg)]
                GridText(line: t.fitted(min(t.cellCount, max(0, rect.cols - 4 - right.cellCount - 2))))
                    .offset(x: G.w(2))
                if !right.isEmpty {
                    let r = [Span(" ", back: .bg)] + right.map { var s = $0; s.back = s.back ?? .bg; return s } + [Span(" ", back: .bg)]
                    GridText(line: r)
                        .offset(x: G.w(rect.cols - 2 - r.cellCount))
                }
            }
        }
        .frame(width: G.w(rect.cols), height: G.h(rect.rows), alignment: .topLeading)
    }
}
