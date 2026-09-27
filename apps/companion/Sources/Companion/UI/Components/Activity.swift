import SwiftUI

// Agent-state components, ported from Beautiful UI (MIT, Shane Levine,
// github.com/slev12397/beautiful-ui): `components/primitives/TaskRows.tsx`,
// `ThinkingState.tsx`, `ToolChips.tsx` and `components/atoms/Shimmer.tsx`. The structure and
// motion follow the originals (a row that expands its detail with the grid-rows reveal and a
// rotating chevron, a status badge that pops in, a shimmer across a working label, chips of
// tool calls); the sizes, colours and durations are the Figma file's (Task row 6:72, Tool chip
// 3:230, Thinking 6:31), which restyled them. See ACKNOWLEDGEMENTS.md.

/// One tool call in plain words: "Click 25%", "Screenshot", "Read "Each pays"".
struct ToolChipModel: Hashable, Sendable, Identifiable {
    enum State: Hashable, Sendable { case running, done, error(String) }

    var id: Int
    var systemImage: String
    var label: String
    /// "0.4s", "now".
    var meta: String
    var state: State
}

struct ToolChip: View {
    var chip: ToolChipModel

    var body: some View {
        HStack(spacing: 6) {
            if chip.state == .running {
                StatusGlyph(kind: .checking, color: .accent, size: 12)
            } else {
                Image(systemName: chip.systemImage).font(.system(size: 10)).frame(width: 12, height: 12)
                    .foregroundStyle(isError ? Palette.fail : Palette.textSecondary)
            }
            Text(chip.label).textStyle(.caption).foregroundStyle(isError ? Palette.fail : Palette.text).lineLimit(1)
            Text(metaText).textStyle(.caption).foregroundStyle(isError ? Palette.fail : Palette.textSecondary).lineLimit(1)
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: 24)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(isError ? Palette.failSubtle : Palette.bgHover))
        .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(isError ? Palette.fail.opacity(0.4) : Palette.border, lineWidth: 1))
        .accessibilityElement(children: .combine)
    }

    private var isError: Bool { if case .error = chip.state { true } else { false } }

    private var metaText: String {
        if case .error(let why) = chip.state { return why }
        return chip.meta
    }
}

/// A step the verifier took, named by what it learned, with its tool calls inside.
struct TaskRowModel: Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var glyph: GlyphKind
    var color: ToneColor
    /// "0:18", "now".
    var meta: String
    var chips: [ToolChipModel]
    /// The verifier's words under the chips, if it said something at this step.
    var note: String?
    /// Opened by itself: the failed row.
    var opensItself: Bool
}

/// Beautiful UI's Task Rows: collapsed by default; the failing one opens itself.
struct TaskRowView: View {
    var row: TaskRowModel
    @Binding var expanded: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: expanded ? Gap.x8 : 0) {
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: Gap.x8) {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(Palette.textSecondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .frame(width: 12, height: 12)
                        .opacity(row.chips.isEmpty && row.note == nil ? 0 : 1)
                    StatusGlyph(kind: row.glyph, color: row.color)
                    Text(row.title).textStyle(.body).foregroundStyle(Palette.text)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(row.meta).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(row.chips.isEmpty && row.note == nil)
            .accessibilityLabel("\(row.title), \(row.meta)")
            .accessibilityValue(expanded ? "expanded" : "collapsed")

            if expanded {
                VStack(alignment: .leading, spacing: Gap.x8) {
                    if !row.chips.isEmpty {
                        ChipFlowLayout(spacing: 6) {
                            ForEach(row.chips) { ToolChip(chip: $0) }
                        }
                    }
                    if let note = row.note {
                        Text(note).textStyle(.body).foregroundStyle(Palette.text)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.leading, 44)
                .transition(reduceMotion ? .identity : .opacity.combined(with: .offset(y: -4)))
            }
        }
        .padding(Gap.x8)
        .animation(Motion.change(Motion.settle, reduce: reduceMotion), value: expanded)
    }
}

/// Beautiful UI's Thinking state: "Thinking" with a slow shimmer while live; "Thought for 4s"
/// once done, one click from its trace.
struct ThinkingView: View {
    var live: Bool
    var doneText = "Thought"

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "sparkle").font(.system(size: 11)).foregroundStyle(Palette.textSecondary)
            if live {
                ShimmerText(text: "Thinking")
            } else {
                Text(doneText).textStyle(.body).foregroundStyle(Palette.textSecondary)
                Image(systemName: "chevron.right").font(.system(size: 9, weight: .semibold)).foregroundStyle(Palette.textSecondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// Beautiful UI's Shimmer: a band of light that crosses a working label. Stops under Reduce
/// Motion, leaving the word.
struct ShimmerText: View {
    var text: String
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var phase: CGFloat = -1

    var body: some View {
        Text(text)
            .textStyle(.body)
            .foregroundStyle(Palette.textSecondary)
            .overlay {
                if !reduceMotion {
                    GeometryReader { geo in
                        LinearGradient(colors: [.clear, Palette.bg.opacity(0.65), .clear], startPoint: .leading, endPoint: .trailing)
                            .frame(width: geo.size.width / 2)
                            .offset(x: phase * geo.size.width)
                    }
                    .mask(Text(text).textStyle(.body))
                    .allowsHitTesting(false)
                }
            }
            .onAppear {
                guard !reduceMotion else { return }
                withAnimation(.easeInOut(duration: Motion.shimmer).repeatForever(autoreverses: false)) { phase = 1.5 }
            }
    }
}

/// Lays chips out left to right, wrapping.
struct ChipFlowLayout: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var (x, y, lineHeight, widest) = (CGFloat(0), CGFloat(0), CGFloat(0), CGFloat(0))
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                y += lineHeight + spacing
                x = 0
                lineHeight = 0
            }
            x += size.width + spacing
            widest = max(widest, x - spacing)
            lineHeight = max(lineHeight, size.height)
        }
        return CGSize(width: proposal.width ?? widest, height: y + lineHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var (x, y, lineHeight) = (bounds.minX, bounds.minY, CGFloat(0))
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x > bounds.minX, x + size.width > bounds.maxX {
                y += lineHeight + spacing
                x = bounds.minX
                lineHeight = 0
            }
            sub.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }
    }
}

/// The message composer: one field and Send. Disabled with its reason when nothing will answer.
struct ComposerView: View {
    @Binding var text: String
    var placeholder = "Message the verifier"
    var sending = false
    /// Why the composer cannot send now; nil when it can.
    var disabledReason: String?
    var send: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: Gap.x8) {
            TextField(disabledReason ?? placeholder, text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .textStyle(.body)
                .foregroundStyle(Palette.text)
                .lineLimit(1...5)
                .focused($focused)
                .disabled(disabledReason != nil)
                .onSubmit(sendIfReady)
                .accessibilityLabel("Message the verifier")
            Button(sending ? "Sending" : "Send", action: sendIfReady)
                .buttonStyle(ActionButtonStyle(kind: .primary, loading: sending))
                .disabled(!canSend && !sending)
                .allowsHitTesting(canSend)
        }
        .padding(.leading, Gap.x12)
        .padding(.trailing, Gap.x4)
        .padding(.vertical, Gap.x4)
        .background(RoundedRectangle(cornerRadius: Corner.row).fill(Palette.bgRaised))
        .overlay(RoundedRectangle(cornerRadius: Corner.row)
            .strokeBorder(focused ? Palette.focusRing : Palette.border, lineWidth: focused ? 2 : 1))
    }

    private var canSend: Bool {
        disabledReason == nil && !sending && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func sendIfReady() {
        if canSend { send() }
    }
}
