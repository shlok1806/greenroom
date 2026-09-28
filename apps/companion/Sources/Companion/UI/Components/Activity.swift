import SwiftUI

// Agent-state components, ported from Beautiful UI (slev12397/beautiful-ui at 44a274e, MIT,
// (c) 2026 Shane Levine): `components/primitives/TaskRows.tsx`, `ThinkingState.tsx`,
// `ToolChips.tsx` and `components/atoms/Shimmer.tsx`. Behaviour and motion are the originals'
// (a row opens its detail in place, height and opacity together, under a turning chevron; a
// working label shimmers; tool calls are chips), held to the browser's sampled frames by
// `MotionTests`. Sizes, colours and the durations the design names are the Figma file's (Task
// row 6:72, Tool chip 3:230, Thinking 6:31, Composer), which restyled them (docs/22 C08, C11,
// C18, C31, C32). See ACKNOWLEDGEMENTS.md.

/// One tool call in plain words: "Click 25%", "Screenshot", "Read "Each pays"".
struct ToolChipModel: Hashable, Sendable, Identifiable {
    enum State: Hashable, Sendable { case running, done, error(String) }

    var id: Int
    var icon: Icon
    var label: String
    /// "0.4s", "now".
    var meta: String
    var state: State
}

/// 24 tall, 8 at the sides, 6 between the pieces, radius 6, a 1 pt border, Caption.
struct ToolChip: View {
    var chip: ToolChipModel

    var body: some View {
        HStack(spacing: 6) {
            if chip.state == .running {
                StatusGlyph(kind: .checking, color: .accent, size: 12)
            } else {
                IconView(icon: chip.icon, size: 12).foregroundStyle(Palette.textSecondary)
            }
            Text(chip.label).textStyle(.caption).foregroundStyle(Palette.text).lineLimit(1)
            Text(metaText).textStyle(.caption).foregroundStyle(isError ? Palette.fail : Palette.textSecondary).lineLimit(1)
        }
        .padding(.horizontal, Gap.x8)
        .frame(height: 24)
        .background(RoundedRectangle(cornerRadius: Corner.control).fill(isError ? Palette.failSubtle : Palette.bgHover))
        .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(isError ? Palette.fail.opacity(0.35) : Palette.border, lineWidth: 1))
        .accessibilityElement(children: .combine)
    }

    private var isError: Bool { if case .error = chip.state { true } else { false } }

    private var metaText: String {
        if case .error(let why) = chip.state { return why }
        return chip.state == .running ? "now" : chip.meta
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
    /// The steps this row covers, oldest first: where selecting it seeks the picture.
    var steps: [Int] = []
}

/// Beautiful UI's Task Rows, as the Figma file draws them: 8 all round, a 12 pt chevron, the
/// glyph, the title and the time 8 apart; the detail 8 below, 44 in. Collapsed by default; the
/// failing one opens itself.
struct TaskRowView: View {
    var row: TaskRowModel
    @Binding var expanded: Bool
    /// The row under the recording's playhead (redesign 7): drawn selected.
    var current = false
    /// Selecting the row seeks the picture to its first step.
    var onSelect: (() -> Void)?
    /// A chip seeks the picture to its step.
    var onChip: ((Int) -> Void)?
    /// A chip's words on hover: the tool, the time, the error.
    var chipHelp: ((Int) -> String)?
    /// Whether a chip arrived while the row showed, and its delay: it fades up (ToolChips).
    var chipEntrance: ((Int) -> (active: Bool, delay: Double))?

    @State private var hovering = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var opens: Bool { !row.chips.isEmpty || row.note != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                if opens { expanded.toggle() }
                onSelect?()
            } label: {
                HStack(spacing: Gap.x8) {
                    IconView(icon: .chevronRight, size: 12)
                        .foregroundStyle(Palette.textSecondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        // Beautiful UI turns the chevron on Tailwind's default curve; the Figma
                        // file's settle is the time.
                        .animation(reduceMotion ? nil : Curve.tailwindDefault.animation(Motion.settle), value: expanded)
                        .opacity(opens ? 1 : 0)
                    StatusGlyph(kind: row.glyph, color: row.color)
                    Text(AgentMarkdown.inline(row.title)).textStyle(.body).foregroundStyle(Palette.text)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(row.meta).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(!opens && onSelect == nil)
            .accessibilityLabel("\(row.title), \(row.meta)")
            .accessibilityAddTraits(current ? .isSelected : [])
            .accessibilityValue(opens ? (expanded ? "expanded" : "collapsed") : "")

            Reveal(open: expanded && opens) {
                VStack(alignment: .leading, spacing: Gap.x8) {
                    if !row.chips.isEmpty {
                        ChipFlowLayout(spacing: 6) {
                            ForEach(row.chips) { chip in
                                let entrance: (active: Bool, delay: Double) = chipEntrance?(chip.id) ?? (active: false, delay: 0)
                                Group {
                                    if let onChip {
                                        Button { onChip(chip.id) } label: { ToolChip(chip: chip) }
                                            .buttonStyle(.plain)
                                            .help(chipHelp?(chip.id) ?? chip.label)
                                    } else {
                                        ToolChip(chip: chip)
                                    }
                                }
                                .fadeUp(entrance.active, duration: AgentMotion.toolChip, delay: entrance.delay)
                            }
                        }
                    }
                    if let note = row.note {
                        AgentMarkdown(text: note)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.leading, 44)
                .padding(.top, Gap.x8)
            }
        }
        .padding(Gap.x8)
        .background(RoundedRectangle(cornerRadius: Corner.control)
            .fill(current ? Palette.bgSelected : (hovering && (opens || onSelect != nil) ? Palette.bgHover : .clear)))
        .overlay(alignment: .leading) {
            if current { RoundedRectangle(cornerRadius: 1).fill(Palette.accent).frame(width: 2).padding(.vertical, 6) }
        }
        .onHover { hovering = $0 }
    }
}

/// Opens its content in place: the height goes from nothing to the content's own and the
/// opacity with it, the content clipped (CSS `grid-template-rows: 0fr` to `1fr`, docs/22
/// section 3.6), over the design's settle on its one curve. The content stays in the tree, so
/// closing runs the same curve back. Instant under Reduce Motion.
struct Reveal<Content: View>: View {
    var open: Bool
    var duration = Motion.settle
    @ViewBuilder var content: () -> Content

    @State private var height: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        content()
            .fixedSize(horizontal: false, vertical: true)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
            .frame(height: open ? height : 0, alignment: .top)
            .clipped()
            .opacity(open ? 1 : 0)
            .allowsHitTesting(open)
            .accessibilityHidden(!open)
            .animation(Motion.change(duration, reduce: reduceMotion), value: open)
    }
}

/// Beautiful UI's Thinking state: the word with a slow shimmer while live; "Thought for 4s"
/// once done, one click from its trace. The icon 14, 6 before the word.
struct ThinkingView: View {
    var live: Bool
    var liveText = "Thinking"
    var doneText = "Thought"

    var body: some View {
        HStack(spacing: 6) {
            IconView(icon: .think, size: 14).foregroundStyle(Palette.textSecondary)
            if live {
                ShimmerText(text: liveText)
            } else {
                Text(doneText).textStyle(.body).foregroundStyle(Palette.textSecondary)
                IconView(icon: .chevronRight, size: 12).foregroundStyle(Palette.textSecondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// Beautiful UI's Shimmer: the label drawn through a gradient twice its width that travels
/// across it (`background-size: 200%`, `background-position` 150% to -50%, linear), dim at the
/// ends and bright in the middle. The design's period is 1.6 s and its colours the secondary
/// text at full and at 35%. Its place is worked out from the motion clock; under Reduce Motion
/// the word stands still in the secondary colour.
struct ShimmerText: View {
    var text: String
    var style: TypeStyle = .body

    @State private var width: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Text(text)
            .textStyle(style)
            .foregroundStyle(reduceMotion ? Palette.textSecondary : Color.clear)
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
            .overlay(alignment: .leading) {
                if !reduceMotion {
                    Clocked { seconds in
                        LinearGradient(stops: ShimmerText.stops, startPoint: .leading, endPoint: .trailing)
                            .frame(width: width * 2)
                            .offset(x: ShimmerText.offset(at: seconds, width: width))
                            .frame(width: width, alignment: .leading)
                            .mask(alignment: .leading) { Text(text).textStyle(style) }
                    }
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
                }
            }
            .accessibilityLabel(text)
    }

    /// The Figma file's gradient: the secondary text at 100%, 35% and 100%.
    static var stops: [Gradient.Stop] {
        [
            .init(color: Palette.textSecondary, location: 0),
            .init(color: Palette.textSecondary.opacity(0.35), location: 0.5),
            .init(color: Palette.textSecondary, location: 1),
        ]
    }

    /// Where the gradient's left edge sits: `background-position-x` p puts it at -W p, and p
    /// runs 1.5 to -0.5 over the period, so the edge travels from -1.5 W to 0.5 W.
    static func offset(at seconds: Double, width: CGFloat, period: Double = Motion.shimmer) -> CGFloat {
        let p = 1.5 - 2 * MotionClock.phase(seconds, period: period)
        return -width * p
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

/// The message composer (Figma: Composer): one field and Send. 6 above, below and after, 12
/// before, 8 between; radius 8, a 1 pt border; focused, the 2 pt focus ring; disabled with its
/// reason, at 60%, when nothing will answer.
struct ComposerView: View {
    @Binding var text: String
    var placeholder = "Message the verifier"
    var sendTitle = "Send"
    var sending = false
    /// Why the composer cannot send now; nil when it can.
    var disabledReason: String?
    /// Bumped to put the keyboard in the field.
    var focusRequest = 0
    var send: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        HStack(alignment: .bottom, spacing: Gap.x8) {
            TextField("", text: $text, prompt: Text(disabledReason ?? placeholder).foregroundStyle(Palette.textSecondary), axis: .vertical)
                .textFieldStyle(.plain)
                .textStyle(.body)
                .foregroundStyle(Palette.text)
                .tint(Palette.accent)
                .lineLimit(1...6)
                .focused($focused)
                .disabled(disabledReason != nil)
                .onSubmit(sendIfReady)
                .accessibilityLabel(disabledReason ?? "Message the verifier")
                .padding(.vertical, 5)
            Button(sending ? "Sending" : sendTitle, action: sendIfReady)
                .buttonStyle(ActionButtonStyle(kind: .primary, loading: sending))
                .disabled(!canSend && !sending)
                .allowsHitTesting(canSend)
        }
        .padding(.leading, Gap.x12)
        .padding([.vertical, .trailing], 6)
        .background(RoundedRectangle(cornerRadius: Corner.row).fill(Palette.bgRaised))
        .overlay(RoundedRectangle(cornerRadius: Corner.row).strokeBorder(Palette.border, lineWidth: 1))
        .focusRing(focused, radius: Corner.row)
        .opacity(disabledReason == nil ? 1 : 0.6)
        .onChange(of: focusRequest) { _, _ in focused = true }
        .onAppear { if focusRequest > 0 { DispatchQueue.main.async { focused = true } } }
    }

    private var canSend: Bool {
        disabledReason == nil && !sending && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func sendIfReady() {
        if canSend { send() }
    }
}
