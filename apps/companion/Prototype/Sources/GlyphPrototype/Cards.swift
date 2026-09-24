// Verdict and question card structure ported from Beautiful UI ApprovalCard (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: approval-card structure in glyphs: a header cut into the border with a step
// counter, one question or outcome, the choices, and a footer of key-labelled actions.

@MainActor
enum VerdictCardModel {
    static let landDuration = 0.45

    static func lines(_ m: PrototypeModel, width: Int) -> [GridLine] {
        let inner = width - 2
        let pass = m.verdict == .pass
        let role: Role = pass ? .pass : .failure
        let t = m.verdictLandedAt.map { m.now - $0 } ?? 10
        let landing = !m.reduceMotion && t < landDuration
        let word = pass ? "PASS" : "FAIL"
        var rows = BlockLetters.rows(word)
        if landing {
            // decode: each column settles left to right, noise until then
            let seed = FX.seed(m.now)
            rows = rows.enumerated().map { r, row in
                String(row.enumerated().map { i, ch -> Character in
                    let settle = Double(i) / Double(max(1, row.count)) * landDuration * 0.8
                    if t >= settle || ch == " " && t > settle * 0.5 { return ch }
                    return BlockLetters.noise[abs(i &* 31 &+ r &* 17 &+ seed) % BlockLetters.noise.count]
                })
            }
        }
        let status: GridLine
        switch m.review {
        case .none: status = [Span("needs review", ink: .role(.needsYou))]
        case .accepted: status = [Span("accepted by you", ink: .role(.pass))]
        case .disputed: status = [Span("disputed by you", ink: .role(.needsYou))]
        }
        let meta: [GridLine] = [
            status,
            [Span("verifier · \(Synthetic.clock(Synthetic.verdictAt))", ink: .dim)],
            [Span("\(Synthetic.evidence.count) evidence steps", ink: .dim)],
        ]
        var out: [GridLine] = []
        for r in 0..<3 {
            let letters: GridLine = [Span(" "), Span(rows[r], .chrome, .bold, ink: .role(role))]
            out.append(spread(letters, meta[r] + [Span(" ")], width: inner))
        }
        out.append([])
        let reason = pass ? Synthetic.passReason : Synthetic.verdictReason
        var reasonText = reason
        if landing { reasonText = FX.decode(reason, progress: t / landDuration, seed: FX.seed(m.now)) }
        out += wrap([Span(reasonText, .verifier, .medium, ink: .fg)], width: inner - 2).map { [Span(" ")] + $0 }
        out.append([])
        var chips: GridLine = [Span(" evidence ", ink: .dim)]
        for id in Synthetic.evidence {
            guard let s = Synthetic.steps.first(where: { $0.id == id }) else { continue }
            chips += [Span(" "), Span(" #\(id) \(s.verb) ", .tool, ink: s.failed ? .role(.failure) : .fg, back: .selection)]
        }
        out += wrap(chips, width: inner, hang: [Span("          ")])
        out.append([])
        out.append(footer(m, inner: inner))
        return out
    }

    static func footer(_ m: PrototypeModel, inner: Int) -> GridLine {
        func key(_ k: String, _ label: String, strong: Bool = false) -> GridLine {
            [Span(" \(k) ", .chrome, .bold, ink: strong ? .cursorText : .fg, back: strong ? .cursor : .selection),
             Span(" \(label)", ink: .fg)]
        }
        if m.review != .none, let until = m.undoUntil {
            let left = Int((until - m.now).rounded(.up))
            return [Span(" ")] + key("u", "undo") + [Span("  \(left)s", ink: .dim)]
        }
        if m.review == .accepted { return [Span(" ✓ accepted · the coder has been told", ink: .dim)] }
        if m.review == .disputed { return [Span(" disputed · the verifier will look again", ink: .dim)] }
        let left: GridLine = [Span(" ")] + key("a", "accept", strong: true) + [Span("   ")] + key("d", "dispute")
        return spread(left, [Span("esc later ", ink: .dim)], width: inner)
    }
}

struct VerdictCardView: View {
    @Environment(PrototypeModel.self) private var model
    let lines: [GridLine]
    let cols: Int

    var body: some View {
        let pass = model.verdict == .pass
        let t = model.verdictLandedAt.map { model.now - $0 } ?? 10
        let progress = model.reduceMotion ? 1 : min(1, t / 0.25)
        PaneBox(rect: CellRect(col: 0, row: 0, cols: cols, rows: lines.count + 2),
                title: [Span("verdict", .chrome, .bold), Span(" · proposed by verifier", ink: .dim)],
                right: [Span("1 of 1", ink: .dim)],
                focused: false, borderInk: .role(pass ? .pass : .failure), drawProgress: progress) {
            GridLines(lines: lines, width: cols - 2)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("Verdict \(pass ? "pass" : "fail"): \(pass ? Synthetic.passReason : Synthetic.verdictReason)")
        }
    }
}

@MainActor
enum QuestionCardModel {
    static let options = ["Reading the screen is enough", "Grant assistive access in Settings", "Write an answer…"]

    static func questionLines(width: Int) -> [GridLine] {
        let q = Synthetic.messages.first { $0.kind == .question }?.text ?? ""
        return wrap([Span(q, .verifier, .regular, ink: .fg)], width: width - 4).map { [Span(" ")] + $0 }
    }

    static func optionRow(_ m: PrototypeModel, width: Int) -> Int { questionLines(width: width).count + 1 }

    static func lines(_ m: PrototypeModel, width: Int) -> [GridLine] {
        var out = questionLines(width: width)
        out.append([])
        for (i, o) in options.enumerated() {
            let on = i == m.questionOption
            out.append([Span(" "), Span(on ? "›" : " ", .chrome, .bold, ink: .fg), Span(" "),
                        Span(" \(i + 1) ", .chrome, .bold, ink: on ? .cursorText : .fg, back: on ? .cursor : .selection),
                        Span("  " + o, .chrome, on ? .medium : .regular, ink: on ? .fg : .alpha(.fg, 0.8))])
        }
        out.append([])
        out.append(spread([Span(" ↑↓ choose  ⏎ answer", ink: .dim)], [Span("esc skip ", ink: .dim)], width: width - 2))
        return out
    }
}

struct QuestionCardView: View {
    @Environment(PrototypeModel.self) private var model
    let lines: [GridLine]
    let cols: Int

    var body: some View {
        PaneBox(rect: CellRect(col: 0, row: 0, cols: cols, rows: lines.count + 2),
                title: [Span("question", .chrome, .bold, ink: .role(.needsYou)), Span(" · verifier asks", ink: .dim)],
                right: [Span("1 of 1", ink: .dim)],
                focused: false, borderInk: .role(.needsYou)) {
            GridLines(lines: lines, width: cols - 2)
        }
    }
}
