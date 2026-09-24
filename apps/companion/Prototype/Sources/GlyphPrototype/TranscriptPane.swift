// Working indicator (loader + shimmer + elapsed) ported from Beautiful UI LoadingState (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the transcript. Verdict card pinned on top, messages in their voices, the
// working indicator, the question card and the composer at the bottom.

struct TranscriptPane: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect

    var body: some View {
        let inner = rect.cols - 2
        let height = rect.rows - 2
        let verdictCard = model.verdict != nil && model.isScripted ? VerdictCardModel.lines(model, width: inner) : nil
        let questionCard = model.questionOpen ? QuestionCardModel.lines(model, width: inner) : nil
        let composer = composerLines(inner)
        let cardRows = verdictCard.map { $0.count + 2 + 1 } ?? 0
        let qRows = questionCard.map { $0.count + 2 } ?? 0
        let msgRows = max(0, height - cardRows - qRows - composer.count)
        let built = messageLines(width: inner)
        let window = scrollWindow(built, room: msgRows)

        PaneBox(rect: rect, title: [Span("transcript", .chrome, .bold)],
                right: [Span("\(model.visibleMessages.count) messages", ink: .dim)],
                focused: model.focus == .transcript) {
            VStack(alignment: .leading, spacing: 0) {
                if let verdictCard {
                    VerdictCardView(lines: verdictCard, cols: inner)
                    Color.clear.frame(height: G.cellH)
                }
                GridLines(lines: window.lines, width: inner)
                    .frame(height: G.h(msgRows), alignment: .top)
                    .contentShape(Rectangle())
                    .onTapGesture { model.focus = .transcript }
                if let questionCard {
                    QuestionCardView(lines: questionCard, cols: inner)
                }
                GridLines(lines: composer, width: inner)
                    .onTapGesture { model.composerActive = true; model.focus = .transcript }
            }
        }
        .preference(key: CursorPrefs.self, value: spots(built: built, window: window, cardRows: cardRows, msgRows: msgRows,
                                                          qRows: qRows, composer: composer, inner: inner))
    }

    // MARK: messages

    struct Built {
        var lines: [GridLine] = []
        var starts: [Int: Int] = [:]
        var agentEnd: (row: Int, col: Int, voice: Voice)?
        var workingRow: Int?
    }

    private func messageLines(width: Int) -> Built {
        var b = Built()
        let msgs = model.visibleMessages
        var prev: ProtoMessage?
        let bodyW = width - 2
        if model.bootStart != nil, model.isScripted {
            b.lines.append([])
            b.workingRow = b.lines.count + 1
            let t = model.now - (model.bootStart ?? 0)
            b.lines += FX.working("Booting macos-15-xcode", elapsed: t * 7.1, now: model.now, frozen: model.reduceMotion, indent: 1)
            return b
        }
        for m in msgs {
            let typed = model.typed(m)
            let text = typed.map { String(m.text.prefix($0)) } ?? m.text
            let first = prev == nil
            switch m.kind {
            case .event:
                if !first { b.lines.append([]) }
                b.starts[m.id] = b.lines.count
                let label = " \(m.text) "
                let side = max(2, (width - label.count) / 2)
                b.lines.append([Span(String(repeating: "─", count: side), ink: .role(.border)), Span(label, ink: .dim),
                                Span(String(repeating: "─", count: max(0, width - side - label.count)), ink: .role(.border))])
                prev = m
                continue
            case .progress:
                if prev?.from != m.from || prev?.kind == .event {
                    if !first { b.lines.append([]) }
                    b.lines.append(header(m, width: width))
                }
                b.starts[m.id] = b.lines.count
                if let s = Synthetic.steps.first(where: { $0.id == m.step }) {
                    var chip = model.toolChip(s, width: width, expanded: model.expandedMessages.contains(m.id))
                    if model.selectedMessage == m.id, model.focus == .transcript {
                        chip[0] = chip[0].map { var x = $0; x.back = .selection; return x }
                    }
                    b.lines += chip
                }
            case .verdict:
                b.lines.append([])
                b.starts[m.id] = b.lines.count
                let pass = model.verdict == .pass
                b.lines += wrap([Span(pass ? "✓ " : "✗ ", .chrome, .bold, ink: .role(pass ? .pass : .failure)),
                                 Span("verdict ", .verifier, .bold), Span(pass ? "pass" : "fail", .verifier, .bold, ink: .role(pass ? .pass : .failure)),
                                 Span(" · " + (pass ? Synthetic.passReason : m.text), .verifier, ink: .dim)],
                                width: width, hang: [Span("  ")])
            default:
                if !first { b.lines.append([]) }
                b.starts[m.id] = b.lines.count
                if prev?.from != m.from || prev?.kind == .progress || prev?.kind == .event || m.kind == .question || m.kind == .task {
                    b.lines.append(header(m, width: width))
                }
                let voice = m.from.voice
                var body = Markdown.render(text, voice: voice, width: bodyW)
                if m.kind == .question, model.questionOpen {
                    body = [[Span(String(m.text.prefix(max(8, bodyW - 16))) + "…", voice, ink: .dim)],
                            [Span("pinned below ↓ · 1-3 answers", ink: .role(.needsYou))]]
                }
                if body.isEmpty { body = [[]] }
                let selected = model.selectedMessage == m.id && model.focus == .transcript
                let gutterInk: Ink = selected ? .fg : (m.kind == .question ? .role(.needsYou) : .alpha(.role(.border), 0.0))
                for l in body { b.lines.append([Span("▎", ink: gutterInk), Span(" ")] + l) }
                if typed != nil {
                    let last = body.last ?? []
                    b.agentEnd = (b.lines.count - 1, 2 + last.cellCount, voice)
                }
            }
            prev = m
        }
        if case .thinking(let since, let who) = model.activity {
            b.lines.append([])
            b.workingRow = b.lines.count + 1
            let label = who == .coder ? "coder is working" : "verifier is thinking"
            let elapsed = (model.playT - since) * Synthetic.clockScale
            b.lines += FX.working(label, elapsed: elapsed, now: model.now, frozen: model.reduceMotion, indent: 1)
        }
        return b
    }

    private func header(_ m: ProtoMessage, width: Int) -> GridLine {
        var left: GridLine = [Span(m.from.name, m.from.voice, .bold, ink: .fg)]
        let tag: String? = switch m.kind {
        case .task: "task"
        case .question: "question"
        case .answer: "answer"
        case .note: "note"
        default: nil
        }
        if let tag {
            left += [Span(" · ", ink: .dim), Span(tag, .chrome, .regular, ink: m.kind == .question ? .role(.needsYou) : .dim)]
        }
        return spread(left, [Span(Synthetic.clock(m.t), ink: .dim)], width: width)
    }

    // MARK: scrolling

    struct Window { var lines: [GridLine]; var first: Int }

    private func scrollWindow(_ b: Built, room: Int) -> Window {
        let total = b.lines.count
        var first = max(0, total - room - model.transcriptBack)
        let following = model.followAgent && model.activity != .idle
        if !following, model.focus == .transcript, let sel = model.selectedMessage, let r = b.starts[sel] {
            if r < first { first = max(0, r - 1) }
            if r >= first + room { first = r - room + 2 }
            first = max(0, min(first, max(0, total - room)))
        }
        return Window(lines: Array(b.lines.dropFirst(first).prefix(room)), first: first)
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

    // MARK: cursor

    private func spots(built: Built, window: Window, cardRows: Int, msgRows: Int, qRows: Int,
                       composer: [GridLine], inner: Int) -> [String: CellSpot] {
        var out: [String: CellSpot] = [:]
        let top = cardRows
        func at(_ row: Int, _ col: Int, _ lines: [GridLine], _ lineIdx: Int, _ voice: Voice = .chrome) -> CellSpot {
            let (x, y) = rect.inner(col, row)
            let ch = char(lines, lineIdx, col)
            return CellSpot(x: x, y: y, ch: ch, voice: voice)
        }
        // focus: selected message, else the composer prompt
        let composerRow = top + msgRows + qRows + composer.count - 1
        if let sel = model.selectedMessage, let r = built.starts[sel], r >= window.first, r < window.first + msgRows {
            out["focus.transcript"] = at(top + r - window.first, 0, built.lines, r)
        } else {
            out["focus.transcript"] = at(composerRow, 0, composer, composer.count - 1)
        }
        if model.composerActive || model.scriptedComposer != nil {
            let last = composer.last ?? []
            let col = min(inner - 1, last.cellCount)
            let (x, y) = rect.inner(col, composerRow)
            out["composer"] = CellSpot(x: x, y: y, ch: " ", voice: .human, blink: model.composerActive)
        }
        if let e = built.agentEnd, e.row >= window.first, e.row < window.first + msgRows {
            let (x, y) = rect.inner(min(inner - 1, e.col), top + e.row - window.first)
            out["agent.transcript"] = CellSpot(x: x, y: y, ch: " ", voice: e.voice)
        } else if let w = built.workingRow, w >= window.first, w < window.first + msgRows {
            let (x, y) = rect.inner(8, top + w - window.first)
            out["agent.transcript"] = CellSpot(x: x, y: y, ch: char(built.lines, w, 8), blink: true)
        }
        if model.questionOpen {
            let (x, y) = rect.inner(1, top + msgRows + 1 + QuestionCardModel.optionRow(model, width: inner) + model.questionOption)
            out["focus.question"] = CellSpot(x: x, y: y, ch: "›")
        }
        return out
    }

    private func char(_ lines: [GridLine], _ row: Int, _ col: Int) -> Character {
        guard lines.indices.contains(row) else { return " " }
        let s = Array(lines[row].plain)
        return s.indices.contains(col) ? s[col] : " "
    }
}
