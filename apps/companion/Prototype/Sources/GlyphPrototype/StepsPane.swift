// Thinking-trace header and settle behaviour ported from Beautiful UI ThinkingState (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the steps timeline under the screen. Wide: a duration track plus the trace.
// Medium: a one-row track and the current step.

struct StepsPane: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect
    let compact: Bool
    let joined: Bool

    var body: some View {
        let inner = rect.cols - 2
        let steps = model.visibleSteps
        let failed = steps.filter(\.failed).count
        let right: GridLine = steps.isEmpty ? [] : [
            Span("\(steps.count)", ink: .fg), Span(" steps", ink: .dim),
        ] + (failed > 0 ? [Span(" · ", ink: .dim), Span("\(failed) failed", ink: .role(.failure))] : [])
        let content = compact ? compactLines(inner) : lines(inner)
        PaneBox(rect: rect, title: [Span("STEPS", .chrome, .medium, ink: .dim)], right: right,
                focused: model.focus == .steps, top: joined ? .tee : .rounded) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(content.lines.enumerated()), id: \.offset) { _, row in
                    GridText(line: row.line, width: inner)
                        .contentShape(Rectangle())
                        .onTapGesture {
                            model.focus = .steps
                            guard let id = row.step else { return }
                            if model.selectedStep == id {
                                if model.expandedSteps.contains(id) { model.expandedSteps.remove(id) } else { model.expandedSteps.insert(id) }
                            }
                            model.selectedStep = id
                        }
                }
            }
        }
        .preference(key: CursorPrefs.self, value: content.spots)
    }

    struct Row { var line: GridLine; var step: Int? }
    struct Content { var lines: [Row]; var spots: [String: CellSpot] }

    // MARK: track

    /// One cell per step, height by duration (log scale), coloured by what happened.
    private func track(_ inner: Int) -> (GridLine, GridLine) {
        let steps = model.visibleSteps
        guard !steps.isEmpty else {
            return ([Span(" no steps yet", ink: .dim)], [])
        }
        let bars = Array("▁▂▃▄▅▆▇")
        var line: GridLine = [Span(" ")]
        var marks: GridLine = [Span(" ")]
        let sel = model.selectedStep
        for s in steps.prefix(max(0, inner - 16)) {
            let lv = max(0, min(6, Int((log10(Double(max(s.durationMs, 50))) - 1.6) / (4.6 - 1.6) * 6.99)))
            let ink: Ink
            if model.stepRunning(s) { ink = .role(.live) }
            else if s.failed { ink = .role(.failure) }
            else if s.id == sel { ink = .fg }
            else if model.verdict != nil && Synthetic.evidence.contains(s.id) { ink = .role(.needsYou) }
            else { ink = .alpha(.fg, s.by == .coder ? 0.35 : 0.55) }
            line.append(Span(String(bars[lv]), ink: ink))
            let marked = s.id == sel || (sel == nil && model.stepRunning(s))
            marks.append(Span(marked ? "▔" : " ", ink: .fg))
        }
        let start = Span(Synthetic.clock(0), ink: .dim)
        let end = Span(Synthetic.clock(model.playT), ink: .dim)
        line = spread(line, [end, Span(" ")], width: inner)
        marks = spread(marks, [], width: inner)
        _ = start
        return (line, marks)
    }

    // MARK: trace header

    private func header(_ inner: Int) -> GridLine {
        let steps = model.visibleSteps
        let total = steps.reduce(0) { $0 + $1.durationMs }
        let span = String(format: "%dm %02ds", total / 60_000, (total / 1000) % 60)
        if case .running(let id) = model.activity, let s = steps.first(where: { $0.id == id }) {
            let who = s.by == .coder ? "coder" : "verifier"
            return [Span(" "), Span(FX.spinner(model.now, frozen: model.reduceMotion), ink: .role(.live)), Span(" ")]
                + FX.shimmer("\(who) is running step \(s.id)", now: model.now, frozen: model.reduceMotion)
        }
        if steps.isEmpty { return [Span(" waiting for the first step", ink: .dim)] }
        let open = !model.expandedSteps.isEmpty
        let settled = model.verdict != nil || model.playT >= Synthetic.length
        var word = settled ? "settled" : "idle"
        if case .typing(let id, _) = model.activity, let m = model.visibleMessages.first(where: { $0.id == id }) {
            word = "\(m.from.name) is writing"
        } else if case .thinking(_, let who) = model.activity {
            word = who == .coder ? "coder is working" : "verifier is thinking"
        }
        return [Span(" "), Span(open ? "▾" : "▸", ink: .dim), Span(" "),
                Span(word, ink: .dim),
                Span(" · \(steps.count) steps in \(span) · ⏎ expands a step", ink: .dim)]
    }

    // MARK: wide

    private func lines(_ inner: Int) -> Content {
        let steps = model.visibleSteps
        let (t1, t2) = track(inner)
        var head: [Row] = [Row(line: t1, step: nil), Row(line: t2, step: nil), Row(line: header(inner), step: nil)]
        var body: [Row] = []
        var starts: [Int: Int] = [:]
        for s in steps {
            starts[s.id] = body.count
            let selected = s.id == model.selectedStep && model.focus == .steps
            body.append(Row(line: model.stepRow(s, width: inner, selected: selected), step: s.id))
            if model.expandedSteps.contains(s.id) {
                body += s.detail(width: inner - 8).map { Row(line: [Span("       ")] + $0, step: s.id) }
            }
        }
        let room = max(0, rect.rows - 2 - head.count)
        // Keep the step that matters in view: the selected one, else the running one, else the newest.
        var targetID: Int?
        if let sel = model.selectedStep { targetID = sel }
        if model.followAgent, case .running(let id) = model.activity, model.focus != .steps || model.selectedStep == nil { targetID = id }
        var first = max(0, body.count - room)
        if let id = targetID, let r = starts[id] {
            let end = r + (model.expandedSteps.contains(id) ? 1 + (body[r...].prefix { $0.step == id }.count - 1) : 1)
            if r < first { first = r }
            if end > first + room { first = max(0, end - room) }
            first = min(first, max(0, body.count - room))
            if r < first { first = r }
        }
        let shown = Array(body.dropFirst(first).prefix(room))
        head += shown

        var spots: [String: CellSpot] = [:]
        if let sel = model.selectedStep, let r = starts[sel], r >= first, r < first + room {
            let (x, y) = rect.inner(0, head.count - shown.count + r - first)
            spots["focus.steps"] = CellSpot(x: x, y: y)
        } else {
            let (x, y) = rect.inner(0, 2)
            spots["focus.steps"] = CellSpot(x: x, y: y)
        }
        if case .running(let id) = model.activity, let r = starts[id], r >= first, r < first + room {
            let (x, y) = rect.inner(0, head.count - shown.count + r - first)
            spots["agent.steps"] = CellSpot(x: x, y: y)
        }
        return Content(lines: head, spots: spots)
    }

    // MARK: medium

    /// A one-row track, then as many of the latest steps as fit, newest last.
    private func compactLines(_ inner: Int) -> Content {
        let (t1, _) = track(inner)
        let steps = model.visibleSteps
        let room = max(1, rect.rows - 3)
        let anchor = model.selectedStep.flatMap { id in steps.firstIndex { $0.id == id } } ?? (steps.count - 1)
        let end = min(steps.count, max(anchor + 1, room))
        let shown = steps.isEmpty ? [] : Array(steps[max(0, end - room)..<end])
        var rows = [Row(line: t1, step: nil)]
        if shown.isEmpty { rows.append(Row(line: [Span(" waiting for the first step", ink: .dim)], step: nil)) }
        var spots: [String: CellSpot] = [:]
        for s in shown {
            let selected = s.id == model.selectedStep && model.focus == .steps
            if selected || (model.selectedStep == nil && s.id == shown.last?.id) {
                let (x, y) = rect.inner(0, rows.count)
                spots["focus.steps"] = CellSpot(x: x, y: y)
            }
            if case .running(let id) = model.activity, id == s.id {
                let (x, y) = rect.inner(0, rows.count)
                spots["agent.steps"] = CellSpot(x: x, y: y)
            }
            rows.append(Row(line: model.stepRow(s, width: inner, selected: selected), step: s.id))
        }
        return Content(lines: rows, spots: spots)
    }
}
