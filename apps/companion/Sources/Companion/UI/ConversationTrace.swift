import SwiftUI

// The Message tab's work between messages (redesign 7): the verifier's tool calls since the
// message before, as Beautiful UI's ThinkingState "Steps" (`components/primitives/
// ThinkingState.tsx`, MIT, spec `thinking-state.json`, docs/22 C31) whose trace is the calls as
// ToolChips (C11, the Figma file's chip). While the verifier works, the header's word is what
// it does now under the shimmer and the trace is open, each call fading up as it arrives;
// when its turn ends the word becomes "12 tool calls, 0:42" with the original's `fade-in 350ms
// ease-out`, and the trace folds unless a call failed. See ACKNOWLEDGEMENTS.md.

/// The conversation as it reads: messages, and the tool calls between them. Pure.
enum ConversationLayout {
    enum Item: Hashable, Identifiable {
        case message(Message)
        /// The calls after the message `after` (nil: before the first), and whether the
        /// verifier is making them now.
        case tools(after: Int?, steps: [Step], live: Bool)

        var id: String {
            switch self {
            case .message(let message): "m\(message.seq)"
            case .tools(let after, _, _): "t\(after.map(String.init) ?? "start")"
            }
        }
    }

    /// `messages` in order, each followed by the steps taken from it until the next; the calls
    /// after the last message are live while the verifier `working`, shown even before the
    /// first call so the header says what it does.
    static func items(messages: [Message], steps: [Step], working: Bool) -> [Item] {
        let messages = messages.sorted { $0.seq < $1.seq }
        let steps = steps.sorted { $0.seq < $1.seq }
        var items: [Item] = []
        let before = steps.filter { step in messages.first.map { step.at < $0.at } ?? true }
        if !before.isEmpty || (messages.isEmpty && working) {
            items.append(.tools(after: nil, steps: before, live: messages.isEmpty && working))
        }
        for (index, message) in messages.enumerated() {
            items.append(.message(message))
            let last = index == messages.count - 1
            let end = last ? Date.distantFuture : messages[index + 1].at
            let mine = steps.filter { $0.at >= message.at && $0.at < end }
            if !mine.isEmpty || (last && working) {
                items.append(.tools(after: message.seq, steps: mine, live: last && working))
            }
        }
        return items
    }

    /// The header once the calls are done: "12 tool calls, 2 failed, 0:42".
    static func doneLabel(_ steps: [Step]) -> String {
        let failed = steps.filter { $0.error != nil }.count
        var words = steps.count == 1 ? "1 tool call" : "\(steps.count) tool calls"
        if failed > 0 { words += ", \(failed) failed" }
        if let first = steps.first, let last = steps.last {
            let span = last.at.timeIntervalSince(first.at) + Double(last.durationMs) / 1000
            words += ", " + Clock.elapsed(Int(span.rounded()))
        }
        return words
    }
}

/// One group of tool calls, drawn as the Thinking state with its trace.
struct ToolTraceView: View {
    var steps: [Step]
    /// Every step of the run, for the chips' words.
    var allSteps: [Step]
    var live: Bool
    /// What the verifier does now, under the shimmer while live.
    var now: String
    var arrivals: Arrivals
    var onChip: (Int) -> Void
    var chipHelp: (Int) -> String

    @State private var userOpen: Bool?
    @State private var hovering = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var failed: Int { steps.filter { $0.error != nil }.count }
    private var open: Bool { userOpen ?? (live || failed > 0) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                userOpen = !open
            } label: {
                HStack(spacing: Gap.x8) {
                    IconView(icon: .think, size: 14)
                        .foregroundStyle(live ? Palette.textSecondary : Palette.textTertiary)
                    label
                    if !steps.isEmpty {
                        IconView(icon: .chevronRight, size: 12)
                            .foregroundStyle(Palette.textSecondary)
                            .rotationEffect(.degrees(open ? 90 : 0))
                            .animation(reduceMotion ? nil : Curve.tailwindDefault.animation(Motion.settle), value: open)
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, Gap.x4)
                .background(RoundedRectangle(cornerRadius: Corner.row).fill(hovering && !steps.isEmpty ? Palette.bgHover : .clear))
                .animation(reduceMotion ? nil : Curve.linear.animation(0.1), value: hovering)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(steps.isEmpty)
            .onHover { hovering = $0 }
            .padding(.horizontal, -6)
            .accessibilityLabel(live ? now : ConversationLayout.doneLabel(steps))
            .accessibilityValue(steps.isEmpty ? "" : (open ? "expanded" : "collapsed"))
            .accessibilityIdentifier(live ? "conversation.thinking" : "conversation.tools")

            Reveal(open: open && !steps.isEmpty) {
                ChipFlowLayout(spacing: 6) {
                    ForEach(steps) { step in
                        let id = String(step.seq)
                        Button { onChip(step.seq) } label: { ToolChip(chip: ActivityLayout.chip(step, in: allSteps)) }
                            .buttonStyle(.plain)
                            .help(chipHelp(step.seq))
                            .fadeUp(arrivals.isNew(id), duration: AgentMotion.traceRow,
                                    delay: arrivals.delay(id, stagger: AgentMotion.traceStagger))
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.leading, 16)
                // The trace's rail: 1 pt, 5 in from the header's glyph (`ml-[5px] pl-4`).
                .overlay(alignment: .leading) {
                    Rectangle().fill(Palette.border).frame(width: 1).padding(.leading, 6)
                }
                .padding(.top, 6)
            }
        }
    }

    @ViewBuilder
    private var label: some View {
        if live {
            ShimmerText(text: now)
                .lineLimit(1)
                .transition(.identity)
        } else {
            Text(ConversationLayout.doneLabel(steps))
                .textStyle(.body)
                .foregroundStyle(failed > 0 ? Palette.fail : Palette.textSecondary)
                .lineLimit(1)
                .transition(reduceMotion ? .identity : .opacity.animation(AgentMotion.labelCurve.animation(AgentMotion.labelFadeIn)))
        }
    }
}
