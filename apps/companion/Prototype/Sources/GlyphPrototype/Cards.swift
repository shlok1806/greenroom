// Verdict and question card structure ported from Beautiful UI ApprovalCard (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the verdict and question cards as quiet, naturally-sized panels (revision
// 18b) - no box-drawn border, no pre-computed row budget. The PASS/FAIL block letters
// and the decode-in are the kept glyph moment (decision 5: headings never bigger, just
// heavier); the reason and the question itself are Mona Sans reading text.

@MainActor
enum VerdictCardModel {
    static let landDuration = 0.45
}

struct VerdictCardView: View {
    @Environment(PrototypeModel.self) private var model
    let width: CGFloat

    private struct Computed {
        var role: Role
        var letterLines: [GridLine]
        var reasonText: String
    }

    /// Plain (non-`@ViewBuilder`) work: the decode-in noise and block-letter settling
    /// are imperative loops, which a `@ViewBuilder` body cannot host directly.
    private func compute() -> Computed {
        let m = model
        let pass = m.verdict == .pass
        let role: Role = pass ? .pass : .failure
        let t = m.verdictLandedAt.map { m.now - $0 } ?? 10
        let landing = !m.reduceMotion && t < VerdictCardModel.landDuration
        let word = pass ? "PASS" : "FAIL"
        var letterRows = BlockLetters.rows(word)
        if landing {
            let seed = FX.seed(m.now)
            letterRows = letterRows.enumerated().map { r, row in
                String(row.enumerated().map { i, ch -> Character in
                    let settle = Double(i) / Double(max(1, row.count)) * VerdictCardModel.landDuration * 0.8
                    if t >= settle || (ch == " " && t > settle * 0.5) { return ch }
                    return BlockLetters.noise[abs(i &* 31 &+ r &* 17 &+ seed) % BlockLetters.noise.count]
                })
            }
        }
        let letterLines: [GridLine] = letterRows.map { [Span($0, .chrome, .bold, ink: .role(role))] }
        let reason = pass ? Synthetic.passReason : Synthetic.verdictReason
        let reasonText = landing ? FX.decode(reason, progress: t / VerdictCardModel.landDuration, seed: FX.seed(m.now)) : reason
        return Computed(role: role, letterLines: letterLines, reasonText: reasonText)
    }

    var body: some View {
        let palette = model.palette
        let c = compute()

        QuietCard(accent: .role(c.role)) {
            VStack(alignment: .leading, spacing: Space.md) {
                HStack(alignment: .top, spacing: Space.md) {
                    VStack(alignment: .leading, spacing: 0) { GridLines(lines: c.letterLines) }
                    Spacer(minLength: Space.sm)
                    VStack(alignment: .trailing, spacing: 3) {
                        reviewStatus
                        metaLine("verifier · \(Synthetic.clock(Synthetic.verdictAt))")
                        metaLine("\(Synthetic.evidence.count) evidence steps")
                    }
                }
                ProseText(text: c.reasonText)
                evidenceRow
                Divider().overlay(palette.color(.alpha(.role(.border), 0.5)))
                footer
            }
        }
    }

    private var reviewStatus: some View {
        let (text, ink): (String, Ink) = switch model.review {
        case .none: ("needs review", .role(.needsYou))
        case .accepted: ("accepted by you", .role(.pass))
        case .disputed: ("disputed by you", .role(.needsYou))
        }
        return metaLine(text, ink: ink)
    }

    private func metaLine(_ text: String, ink: Ink = .dim) -> some View {
        Text(text).font(FontCache.font(.neon, .medium, size: 11)).foregroundStyle(model.palette.color(ink))
    }

    private var evidenceRow: some View {
        FlowChips {
            ForEach(Synthetic.evidence, id: \.self) { id in
                if let s = Synthetic.steps.first(where: { $0.id == id }) {
                    chip("#\(id) \(s.verb)", failed: s.failed)
                }
            }
        }
    }

    private func chip(_ text: String, failed: Bool) -> some View {
        Text(text)
            .font(FontCache.font(.neon, .medium, size: 11))
            .foregroundStyle(failed ? model.palette.color(.role(.failure)) : model.palette.color(.fg))
            .padding(.horizontal, Space.xs + 2).padding(.vertical, 3)
            .background(RoundedRectangle(cornerRadius: 4, style: .continuous).fill(model.palette.color(.selection)))
    }

    @ViewBuilder private var footer: some View {
        let m = model
        if m.review != .none, let until = m.undoUntil {
            HStack(spacing: Space.sm) {
                chipButton("u", "undo", strong: false) { m.undo() }
                metaLine("\(Int((until - m.now).rounded(.up)))s")
            }
        } else if m.review == .accepted {
            metaLine("✓ accepted · the coder has been told", ink: .role(.pass))
        } else if m.review == .disputed {
            metaLine("disputed · the verifier will look again", ink: .role(.needsYou))
        } else {
            HStack(spacing: Space.sm) {
                chipButton("a", "accept", strong: true) { m.accept() }
                chipButton("d", "dispute", strong: false) { m.dispute() }
                Spacer()
                metaLine("esc later")
            }
        }
    }

    private func chipButton(_ key: String, _ label: String, strong: Bool, action: @escaping () -> Void) -> some View {
        let palette = model.palette
        return HStack(spacing: 6) {
            Text(key.uppercased())
                .font(FontCache.font(.neon, .bold, size: 11))
                .foregroundStyle(strong ? palette.color(.brandText) : palette.color(.fg))
                .padding(.horizontal, 6).padding(.vertical, 2)
                .background(RoundedRectangle(cornerRadius: 4, style: .continuous).fill(strong ? palette.color(.brand) : palette.color(.selection)))
            Text(label).font(FontCache.prose(.medium, size: 13)).foregroundStyle(palette.color(.fg))
        }
        .contentShape(Rectangle())
        .onTapGesture(perform: action)
    }
}

@MainActor
enum QuestionCardModel {
    static let options = ["Reading the screen is enough", "Grant assistive access in Settings", "Write an answer…"]
}

struct QuestionCardView: View {
    @Environment(PrototypeModel.self) private var model
    let width: CGFloat

    var body: some View {
        let palette = model.palette
        let q = Synthetic.messages.first { $0.kind == .question }?.text ?? ""
        QuietCard(accent: .role(.needsYou)) {
            VStack(alignment: .leading, spacing: Space.sm) {
                HStack(spacing: 6) {
                    Text("QUESTION").font(FontCache.font(.neon, .medium, size: 11)).foregroundStyle(palette.color(.role(.needsYou)))
                    Text("· verifier asks").font(FontCache.font(.neon, .regular, size: 11)).foregroundStyle(palette.color(.dim))
                    Spacer()
                    Text("1 of 1").font(FontCache.font(.neon, .regular, size: 11)).foregroundStyle(palette.color(.dim))
                }
                ProseText(text: q)
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(QuestionCardModel.options.enumerated()), id: \.offset) { i, o in
                        optionRow(i, o)
                    }
                }
                HStack {
                    Text("↑↓ choose · ⏎ answer").font(FontCache.font(.neon, .regular, size: 11)).foregroundStyle(palette.color(.dim))
                    Spacer()
                    Text("esc skip").font(FontCache.font(.neon, .regular, size: 11)).foregroundStyle(palette.color(.dim))
                }
            }
        }
    }

    private func optionRow(_ i: Int, _ text: String) -> some View {
        let on = i == model.questionOption
        let palette = model.palette
        return HStack(spacing: Space.sm) {
            Text("\(i + 1)")
                .font(FontCache.font(.neon, .bold, size: 11))
                .foregroundStyle(on ? palette.color(.brandText) : palette.color(.fg))
                .frame(width: 18, height: 18)
                .background(Circle().fill(on ? palette.color(.brand) : palette.color(.selection)))
            Text(text)
                .font(FontCache.prose(on ? .medium : .regular, size: 14))
                .foregroundStyle(palette.color(.fg).opacity(on ? 1 : 0.82))
            Spacer(minLength: 0)
        }
        .padding(.vertical, 3)
        .contentShape(Rectangle())
        .onTapGesture { model.questionOption = i; model.answerQuestion() }
    }
}

/// A simple wrapping row of chips, for evidence steps of unpredictable count.
struct FlowChips<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        // The prototype's evidence lists are short (a handful of steps): a plain
        // horizontal row reads cleanly without a custom wrapping layout.
        HStack(spacing: Space.xs) { content }
    }
}
