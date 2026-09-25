// Working indicator (loader + shimmer + elapsed) ported from Beautiful UI LoadingState (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the transcript. Verdict card pinned on top, messages in a real scrolling
// list (revision 18b drops the manual grid-row window here), the working indicator, the
// question card and the composer at the bottom. Headers, tool chips, event dividers and
// the composer stay on the mono grid (chrome and data); message bodies are Mona Sans
// reading text (revision 18/20) with a thin coloured left edge instead of a typeface.

struct TranscriptPane: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect

    var body: some View {
        let innerCols = rect.cols - 2
        let innerW = G.w(innerCols)
        // The message list scrolls, so it reserves a couple of cells for the scrollbar
        // and right-margin breathing room; the rest (cards, composer) use the full width.
        let listCols = max(20, innerCols - 2)
        let listW = G.w(listCols)
        let showVerdictCard = model.verdict != nil && model.isScripted
        let showQuestion = model.questionOpen
        let composer = composerLines(innerCols)
        let rows = buildRows(width: listCols)

        PaneBox(rect: rect, title: [Span("TRANSCRIPT", .chrome, .medium, ink: .dim)],
                right: [Span("\(model.visibleMessages.count) messages", ink: .dim)],
                focused: model.focus == .transcript) {
            VStack(alignment: .leading, spacing: 0) {
                if showVerdictCard {
                    VerdictCardView(width: innerW)
                        .padding(.bottom, Space.sm)
                }
                MessageList(rows: rows, width: listW)
                    .frame(maxHeight: .infinity)
                if showQuestion {
                    QuestionCardView(width: innerW)
                        .padding(.top, Space.sm)
                }
                GridLines(lines: composer, width: innerCols)
                    .padding(.top, Space.sm)
                    .contentShape(Rectangle())
                    .onTapGesture { model.composerActive = true; model.focus = .transcript }
                    .background(cursorPin(composer, innerCols))
            }
            .coordinateSpace(name: "transcriptContent")
        }
    }

    // MARK: rows

    enum Row: Identifiable {
        case divider(id: Int, line: GridLine)
        case chip(id: Int, lines: [GridLine], selected: Bool, action: () -> Void)
        case verdictLine(id: Int, line: GridLine)
        case bubble(id: Int, header: GridLine?, edge: Ink, text: String, selected: Bool, action: () -> Void)
        case working(id: Int, lines: [GridLine])

        var id: Int {
            switch self {
            case .divider(let id, _), .chip(let id, _, _, _), .verdictLine(let id, _),
                 .bubble(let id, _, _, _, _, _), .working(let id, _): id
            }
        }
    }

    private func buildRows(width: Int) -> [Row] {
        var out: [Row] = []
        let msgs = model.visibleMessages
        var prev: ProtoMessage?
        // Negative, disjoint from `ProtoMessage.id` (>= 0): a bubble row's id IS the
        // message id, so `ScrollViewReader.scrollTo(model.selectedMessage)` finds it.
        var nextID = 0
        func take() -> Int { defer { nextID += 1 }; return -(nextID + 1) }

        if model.bootStart != nil, model.isScripted {
            let t = model.now - (model.bootStart ?? 0)
            out.append(.working(id: take(), lines: FX.working("Booting macos-15-xcode", elapsed: t * 7.1, now: model.now, frozen: model.reduceMotion, indent: 1)))
            return out
        }

        for m in msgs {
            let typed = model.typed(m)
            let text = typed.map { String(m.text.prefix($0)) } ?? m.text
            switch m.kind {
            case .event:
                let label = " \(m.text) "
                let side = max(2, (width - label.count) / 2)
                out.append(.divider(id: take(), line: [
                    Span(String(repeating: "─", count: side), ink: .role(.border)), Span(label, ink: .dim),
                    Span(String(repeating: "─", count: max(0, width - side - label.count)), ink: .role(.border)),
                ]))
            case .progress:
                guard let s = Synthetic.steps.first(where: { $0.id == m.step }) else { continue }
                let expanded = model.expandedMessages.contains(m.id)
                let chip = model.toolChip(s, width: width, expanded: expanded)
                let selected = model.selectedMessage == m.id && model.focus == .transcript
                out.append(.chip(id: take(), lines: chip, selected: selected, action: {
                    model.focus = .transcript
                    model.selectedMessage = m.id
                    if model.expandedMessages.contains(m.id) { model.expandedMessages.remove(m.id) } else { model.expandedMessages.insert(m.id) }
                }))
            case .verdict:
                let pass = model.verdict == .pass
                let reason = pass ? Synthetic.passReason : m.text
                out.append(.verdictLine(id: take(), line: [
                    Span(pass ? "✓ " : "✗ ", .chrome, .bold, ink: .role(pass ? .pass : .failure)),
                    Span(pass ? "Pass" : "Fail", .chrome, .bold, ink: .role(pass ? .pass : .failure)),
                    Span("  " + reason, .tool, ink: .dim),
                ]))
            default:
                let showHeader = prev?.from != m.from || prev?.kind == .progress || prev?.kind == .event || m.kind == .question || m.kind == .task
                var body = text
                if m.kind == .question, model.questionOpen {
                    body = String(m.text.prefix(max(8, width - 16))) + "…\n\npinned below · 1-3 answers"
                }
                let selected = model.selectedMessage == m.id && model.focus == .transcript
                let edge: Ink = m.kind == .question ? .role(.needsYou) : .alpha(.role(.border), 0.8)
                out.append(.bubble(id: m.id, header: showHeader ? header(m, width: width) : nil, edge: edge,
                                   text: body, selected: selected, action: {
                    model.focus = .transcript
                    model.selectedMessage = m.id
                }))
            }
            prev = m
        }
        if case .thinking(let since, let who) = model.activity {
            let label = who == .coder ? "coder is working" : "verifier is thinking"
            let elapsed = (model.playT - since) * Synthetic.clockScale
            out.append(.working(id: take(), lines: FX.working(label, elapsed: elapsed, now: model.now, frozen: model.reduceMotion, indent: 1)))
        }
        return out
    }

    private func header(_ m: ProtoMessage, width: Int) -> GridLine {
        var left: GridLine = [Span(m.from.name.uppercased(), .chrome, .bold, ink: .fg)]
        let tag: String? = switch m.kind {
        case .task: "task"
        case .question: "question"
        case .answer: "answer"
        case .note: "note"
        default: nil
        }
        if let tag {
            left += [Span("  ", ink: .dim), Span(tag, .chrome, .regular, ink: m.kind == .question ? .role(.needsYou) : .dim)]
        }
        return spread(left, [Span(Synthetic.clock(m.t), ink: .dim)], width: width)
    }

    // MARK: composer

    private func composerLines(_ inner: Int) -> [GridLine] {
        let rule: GridLine = [Span(String(repeating: "─", count: inner), ink: .role(.border))]
        if model.machineDestroyed {
            return [rule, [Span("  machine destroyed · nothing will answer", ink: .dim)]]
        }
        if model.composerActive {
            let parts = model.composer.components(separatedBy: "\n")
            var out = [rule]
            for (i, p) in parts.suffix(4).enumerated() {
                out.append([Span(i == 0 ? "› " : "  ", .chrome, .bold, ink: .fg), Span(p, .human, ink: .fg)])
            }
            return out
        }
        if let scripted = model.scriptedComposer {
            return [rule, [Span("› ", .chrome, .bold, ink: .fg), Span(scripted, .human, ink: .fg)]]
        }
        return [rule, [Span("› ", ink: .dim), Span("/ to write to the verifier", ink: .dim)]]
    }

    /// Publishes the composer's insertion point via measured geometry, not row maths:
    /// the message list above it is a real scroll view now, so its height is unknown.
    private func cursorPin(_ lines: [GridLine], _ inner: Int) -> some View {
        GeometryReader { g in
            Color.clear.preference(key: CursorPrefs.self, value: pin(g, lines, inner))
        }
    }

    private func pin(_ g: GeometryProxy, _ lines: [GridLine], _ inner: Int) -> [String: CellSpot] {
        var out: [String: CellSpot] = [:]
        let f = g.frame(in: .named("transcriptContent"))
        let lastIdx = lines.count - 1
        guard lastIdx >= 0 else { return out }
        let lastLine = lines[lastIdx]
        let col = min(inner - 1, lastLine.cellCount)
        if model.composerActive || model.scriptedComposer != nil {
            out["composer"] = CellSpot(x: f.minX + G.w(col), y: f.minY + G.h(lastIdx), ch: " ", voice: .human, blink: model.composerActive)
        }
        if model.selectedMessage == nil, model.focus == .transcript {
            out["focus.transcript"] = CellSpot(x: f.minX, y: f.minY, ch: "›")
        }
        return out
    }
}

// MARK: - message list

private struct MessageList: View {
    @Environment(PrototypeModel.self) private var model
    let rows: [TranscriptPane.Row]
    let width: CGFloat

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Space.sm) {
                    ForEach(rows) { row in
                        RowView(row: row, width: width)
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding(.vertical, Space.xs)
            }
            .onAppear { proxy.scrollTo("bottom", anchor: .bottom) }
            .onChange(of: rows.count) { _, _ in
                guard model.followAgent else { return }
                withAnimation(model.reduceMotion ? nil : .easeOut(duration: 0.2)) { proxy.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: model.activity) { _, _ in
                guard model.followAgent else { return }
                withAnimation(model.reduceMotion ? nil : .easeOut(duration: 0.2)) { proxy.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: model.selectedRun) { _, _ in proxy.scrollTo("bottom", anchor: .bottom) }
            .onChange(of: model.selectedMessage) { _, id in
                guard let id, !model.followAgent || model.activity == .idle else { return }
                withAnimation(model.reduceMotion ? nil : .easeOut(duration: 0.15)) { proxy.scrollTo(id, anchor: .center) }
            }
        }
    }
}

private struct RowView: View {
    @Environment(PrototypeModel.self) private var model
    let row: TranscriptPane.Row
    let width: CGFloat

    var body: some View {
        let palette = model.palette
        switch row {
        case .divider(_, let line):
            GridText(line: line, width: Int(width / G.cellW))
        case .chip(_, let lines, let selected, let action):
            GridLines(lines: selected ? lines.map { $0.map { var s = $0; s.back = s.back ?? .selection; return s } } : lines,
                      width: Int(width / G.cellW))
                .contentShape(Rectangle())
                .onTapGesture(perform: action)
        case .verdictLine(_, let line):
            let cols = Int(width / G.cellW)
            GridText(line: line.fitted(cols), width: cols)
        case .working(_, let lines):
            GridLines(lines: lines, width: Int(width / G.cellW))
        case .bubble(let id, let header, let edge, let text, let selected, let action):
            VStack(alignment: .leading, spacing: Space.xs + 2) {
                if let header { GridText(line: header, width: Int(width / G.cellW)) }
                ProseText(text: text)
            }
            .speakerEdge(palette.color(edge))
            .padding(.vertical, Space.xs)
            .padding(.trailing, Space.lg)
            .background(selected ? palette.color(.selection).opacity(0.6) : Color.clear)
            .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
            .contentShape(Rectangle())
            .onTapGesture(perform: action)
            .id(id)
        }
    }
}
