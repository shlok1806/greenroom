// Sidebar row states (running / failed / done) ported from Beautiful UI TaskRows (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the runs sidebar. Wide: sections and two-line rows. Medium: a strip of glyphs.

/// Where the block cursor may go, in window points, and what it covers.
struct CellSpot: Equatable {
    var x: CGFloat
    var y: CGFloat
    var ch: Character = " "
    var voice: Voice = .chrome
    var blink = false
}

struct CursorPrefs: PreferenceKey {
    static let defaultValue: [String: CellSpot] = [:]
    static func reduce(value: inout [String: CellSpot], nextValue: () -> [String: CellSpot]) {
        value.merge(nextValue()) { $1 }
    }
}

extension CellRect {
    /// The window point of an inner cell (inside the border).
    func inner(_ col: Int, _ row: Int) -> (CGFloat, CGFloat) { (G.w(self.col + 1 + col), G.h(self.row + 1 + row)) }
}

struct RunsPane: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect
    let compact: Bool

    private struct Row { var line: GridLine; var run: String? }

    var body: some View {
        let rows = compact ? stripRows : listRows
        let inner = rect.cols - 2
        let focusRow = rows.firstIndex { $0.run == model.runCursor && ($0.line.first?.text.isEmpty == false) } ?? 0
        let visible = Array(rows.prefix(max(0, rect.rows - 2)))
        PaneBox(rect: rect, title: compact ? [] : [Span("RUNS", .chrome, .medium, ink: .dim)],
                right: compact ? [] : [Span("\(Synthetic.runs.count)", ink: .dim)],
                focused: model.focus == .runs, tint: true) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(visible.enumerated()), id: \.offset) { _, row in
                    GridText(line: row.line.fitted(inner), width: inner)
                        .contentShape(Rectangle())
                        .onTapGesture {
                            guard let id = row.run else { return }
                            model.runCursor = id
                            model.focus = .runs
                            model.open()
                        }
                }
            }
        }
        .preference(key: CursorPrefs.self, value: focusSpot(focusRow))
    }

    private func focusSpot(_ row: Int) -> [String: CellSpot] {
        let (x, y) = rect.inner(0, row)
        return ["focus.runs": CellSpot(x: x, y: y, ch: " ")]
    }

    private func status(_ run: ProtoRun) -> (glyph: Span, word: GridLine) {
        let state = model.runState(run)
        let glyph: Span = switch state {
        case .live: Span(FX.spinner(model.now + Double(run.id.count) * 0.13, frozen: model.reduceMotion), ink: .role(.live))
        case .booting: Span("◌", ink: .dim)
        case .needsYou: Span("?", .chrome, .bold, ink: .role(.needsYou))
        case .pass: Span("✓", .chrome, .bold, ink: .role(.pass))
        case .fail: Span("✗", .chrome, .bold, ink: .role(.failure))
        case .ended: Span("·", ink: .dim)
        }
        var word = state.word
        if let t = model.runStateChangedAt[run.id], model.now - t < 0.32, !model.reduceMotion {
            word = FX.decode(word, progress: (model.now - t) / 0.32, seed: FX.seed(model.now))
        }
        if state == .booting {
            return (glyph, FX.shimmer(word, now: model.now, weight: .regular, ink: .fg, frozen: model.reduceMotion))
        }
        return (glyph, [Span(word, .chrome, .medium, ink: state.ink)])
    }

    private var listRows: [Row] {
        let runs = Synthetic.runs
        let needs = runs.filter { model.runState($0) == .needsYou }
        let running = runs.filter { [.live, .booting].contains(model.runState($0)) }
        let rest = runs.filter { !needs.map(\.id).contains($0.id) && !running.map(\.id).contains($0.id) }
        var out: [Row] = []
        func section(_ title: String, _ list: [ProtoRun], ink: Ink = .dim) {
            guard !list.isEmpty else { return }
            if !out.isEmpty { out.append(Row(line: [], run: nil)) }
            let w = rect.cols - 2
            let count = "\(list.count)"
            out.append(Row(line: [Span(" "), Span(title, .chrome, .medium, ink: ink), Span(" "),
                                  Span(String(repeating: "─", count: max(0, w - title.count - count.count - 4)), ink: .role(.border)),
                                  Span(" "), Span(count, ink: .dim)], run: nil))
            for run in list {
                let (glyph, word) = status(run)
                let open = run.id == model.selectedRun
                let cursorOn = run.id == model.runCursor && model.focus == .runs
                // Revision 21-22: the selected run is a brand-olive edge and faint wash;
                // the keyboard cursor is the neutral selection tint (they can combine).
                let back: Ink? = cursorOn ? .selection : (open ? .alpha(.brand, 0.12) : nil)
                var l1: GridLine = [Span(open ? "▎" : " ", .chrome, ink: open ? .brand : .fg), glyph, Span(" "),
                                    Span(run.title, .chrome, open ? .bold : .regular, ink: .fg)]
                // Revision 18c: title + one line (status word + time) - no extra facts.
                var l2: GridLine = [Span("   ")] + word + [Span(" · \(run.started)", ink: .dim)]
                l1 = l1.fitted(w).map { var s = $0; s.back = s.back ?? back; return s }
                l2 = l2.fitted(w).map { var s = $0; s.back = s.back ?? back; return s }
                if open { l2[0].text = "▎" + l2[0].text.dropFirst() }
                out.append(Row(line: l1, run: run.id))
                out.append(Row(line: l2, run: run.id))
            }
        }
        section("needs you", needs, ink: .role(.needsYou))
        section("running", running)
        section("today", rest.filter { $0.day == .today })
        section("yesterday", rest.filter { $0.day == .yesterday })
        return out
    }

    private var stripRows: [Row] {
        var out: [Row] = []
        for run in Synthetic.runs {
            let (glyph, _) = status(run)
            let open = run.id == model.selectedRun
            let back: Ink? = run.id == model.runCursor && model.focus == .runs ? .selection : nil
            var g = glyph
            g.back = back
            out.append(Row(line: [Span(open ? "▎" : " ", ink: .fg, back: back), g, Span(" ", back: back)], run: run.id))
            out.append(Row(line: [], run: nil))
        }
        return out
    }
}
