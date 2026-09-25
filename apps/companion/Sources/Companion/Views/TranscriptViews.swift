import SwiftUI

// The transcript's cards (spec, Components; companion ADR 0009): a spoken message in its
// sender's group, the verdict's one line in the history, the question card, the tool-call
// group and its rows, lifecycle events and the working row. What each says comes from
// `Model/TranscriptCards.swift`; these only draw it. The column around them is
// `ConversationView`'s.

/// A lifecycle event or an accept, as one quiet mono line.
struct EventLine: View {
    let message: Message
    let verdicts: [Message]

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(message.kind == .accept ? "✓" : "·")
                .accessibilityHidden(true)
            // The time runs on after the words, so a wrapped line keeps it beside them.
            (Text(text) + Text("  " + Chrome.shortTime(message.at)).monospacedDigit())
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
                .help(Chrome.stamp(message.at))
        }
        .monoStyle(size: TypeScale.monoSmall)
        .foregroundStyle(.secondary)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.leading, Space.m)
        .textSelection(.enabled)
    }

    private var text: String {
        guard message.kind == .accept else { return Chrome.eventText(message.text) }
        let target = verdicts.first { $0.seq == message.replyTo }
        let outcome = target?.verdict.map { " \($0)" } ?? ""
        return "\(message.from.displayName) accepted the\(outcome) verdict"
    }
}

/// One spoken message (ADR 0008 `MessageGroup`): the sender's name in words once per
/// turn, then the words in the reading face, with a thin coloured edge down the side
/// saying who spoke a second way. Messages are not bubbles and all sit on the left.
struct MessageRow: View {
    let store: RunStore
    let runId: String
    let message: Message
    let showsSender: Bool

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            if showsSender { header }
            content
        }
        .padding(.leading, Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .overlay(alignment: .leading) {
            RoundedRectangle(cornerRadius: 1)
                .fill(edge)
                .frame(width: 2)
                .accessibilityHidden(true)
        }
    }

    private var edge: Color {
        message.kind == .dispute ? theme.color(.attention) : theme.edge(message.from)
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(message.from.displayName)
                .readingStyle(.readingSemiBold, size: TypeScale.readingSmall)
            if let kind = kindLabel {
                Text(kind)
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
            }
            Text(Chrome.shortTime(message.at))
                .monoStyle(size: TypeScale.monoSmall)
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .help("\(Chrome.stamp(message.at)) · message \(message.seq)")
        }
        .accessibilityElement(children: .combine)
    }

    /// Only kinds that change how to read the message get named.
    private var kindLabel: String? {
        switch message.kind {
        case .task: "task"
        case .question: "question"
        case .answer: "answer"
        case .dispute: "dispute"
        case .verdict: "verdict"
        case .unknown(let raw): raw
        default: nil
        }
    }

    @ViewBuilder
    private var content: some View {
        switch message.kind {
        case .verdict:
            VerdictHistoryLine(store: store, runId: runId, message: message)
        case .question:
            QuestionCard(store: store, runId: runId, message: message)
        default:
            MessageBody(store: store, runId: runId, text: message.text)
        }
    }
}

/// A message's words: its Markdown, with the steps it cites as chips that seek them.
struct MessageBody: View {
    let store: RunStore
    let runId: String
    let text: String
    var size: CGFloat = TypeScale.reading

    var body: some View {
        MarkdownView(text: TranscriptText.clean(text), steps: store.stepNumbers(runId), size: size) { step in
            store.requestSeek(runId: runId, step: step)
        }
    }
}

extension RunStore {
    /// The step numbers in a run's record, or nil before it is read: what an evidence chip
    /// may cite.
    func stepNumbers(_ runId: String) -> Set<Int>? {
        steps[runId].map { Set($0.map(\.seq)) }
    }
}

/// A verdict in the history, one line (`VerdictLine`): the pinned card holds the live one
/// in full, so it is never shown twice. It opens to the verifier's words.
struct VerdictHistoryLine: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var open = false
    @Environment(\.theme) private var theme

    private var line: VerdictLine {
        let messages = store.messages[runId] ?? []
        let current = store.verdict(runId)
        let review = current.map { VerdictReview.of($0, messages: messages, verifierListens: store.facts(runId).verifierListens) }
        return VerdictLine.of(message, current: current, review: review)
    }

    var body: some View {
        let line = line
        VStack(alignment: .leading, spacing: Space.s) {
            Button {
                withAnimation(.snappy(duration: 0.18)) { open.toggle() }
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                    Text(open ? "▾" : "▸")
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    Text(line.title)
                        .fontWeight(.semibold)
                        .foregroundStyle(line.outcomeInColour ? theme.outcome(message.verdict) : line.latest ? theme.foreground : theme.dim)
                    Text(line.note)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 0)
                }
                .monoStyle(size: TypeScale.monoSmall)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(open ? "Hide what the verifier wrote" : "Show what the verifier wrote")
            if open {
                MessageBody(store: store, runId: runId, text: message.text)
                    .foregroundStyle(.secondary)
                let cited = VerdictCard.byStep((message.evidence ?? []).map(Evidence.parse))
                if !cited.isEmpty {
                    HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                        Text(line.latest ? "Cites" : "Cited, superseded")
                            .readingStyle(size: TypeScale.readingSmall)
                            .foregroundStyle(.secondary)
                        FlowLayout(spacing: Space.xs) {
                            ForEach(cited, id: \.self) { item in
                                EvidenceLink(store: store, runId: runId, item: item) {
                                    if let step = item.step { store.requestSeek(runId: runId, step: step) }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/// The verifier asking the person something, as a card (Beautiful UI's Approval Card): the
/// question in plain words, the answer field while it is open, and who answered in its
/// place once someone has. Open, its edge takes the attention role.
struct QuestionCard: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var answer = ""
    @State private var sending = false
    @FocusState private var focused: Bool
    @Environment(\.theme) private var theme

    private var state: QuestionCardState {
        QuestionCardState.of(message, in: store.messages[runId] ?? [])
    }

    private var canSend: Bool {
        !sending && !answer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        let state = state
        VStack(alignment: .leading, spacing: Space.m) {
            MessageBody(store: store, runId: runId, text: message.text)
            if state.open {
                HStack(spacing: Space.s) {
                    TextField("Answer the verifier", text: $answer)
                        .textFieldStyle(.plain)
                        .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
                        .focused($focused)
                        .padding(.horizontal, Space.s)
                        .frame(minHeight: 28)
                        .fieldFrame(focused: focused, radius: Radius.sm)
                        .sendOnReturn(enabled: canSend, submit)
                        .typingField(focused: focused, sends: true)
                    Button("Reply", action: submit)
                        .buttonStyle(.primary)
                        .disabled(!canSend)
                        .help("Send your answer to the verifier (\(ActionRegistry.label(.send)))")
                }
            } else if let result = state.result {
                Text("✓ " + result)
                    .readingStyle(.readingMedium, size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .panel(edge: state.open ? theme.color(.attention) : nil)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(state.open ? "Question from the verifier, waiting for an answer" : "Question from the verifier, answered")
    }

    private func submit() {
        let text = answer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        sending = true
        Task {
            if await store.send(runId: runId, kind: .answer, text: text, replyTo: message.seq) { answer = "" }
            sending = false
        }
    }
}

/// The verifier's tool calls in a row (Beautiful UI's Tool Chips), one plain-words line
/// each in a quiet panel. A line seeks the screen to its step; its caret shows the raw
/// call. A long run folds to its last few (`ToolCallFold`).
struct ToolCallGroup: View {
    let store: RunStore
    let runId: String
    let calls: [Message]

    @State private var open = false

    var body: some View {
        let fold = ToolCallFold.visible(calls, open: open)
        VStack(alignment: .leading, spacing: 0) {
            if fold.hidden > 0 || open, calls.count > ToolCallFold.shown + 1 {
                Button {
                    withAnimation(.snappy(duration: 0.2)) { open.toggle() }
                } label: {
                    HStack(spacing: Space.s) {
                        Text(open ? "▾" : "▸")
                            .frame(width: Space.m, alignment: .leading)
                            .accessibilityHidden(true)
                        Text(ToolCallFold.summary(hidden: fold.hidden, open: open))
                        Spacer(minLength: 0)
                    }
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, Space.s)
                    .padding(.vertical, Space.s)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .hoverHighlight(radius: Radius.sm)
                Hairline()
            }
            ForEach(Array(fold.calls.enumerated()), id: \.element.seq) { position, call in
                if position > 0 { Hairline() }
                ToolCallLine(store: store, runId: runId, message: call)
            }
        }
        .panel(radius: Radius.md)
    }
}

/// One tool call: a caret, the state glyph, the sentence, and at most two facts on the
/// right. The caret opens the raw call (tool, input, result) as labelled code blocks.
struct ToolCallLine: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var expanded = false
    @Environment(\.theme) private var theme

    var body: some View {
        let row = ToolCallRow.of(message, steps: store.steps[runId] ?? [])
        let failed = row.state == .failed
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Button {
                    withAnimation(.snappy(duration: 0.18)) { expanded.toggle() }
                } label: {
                    Text(expanded ? "▾" : "▸")
                        .foregroundStyle(.secondary)
                        .frame(width: Space.m, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(expanded ? "Hide the raw tool call" : "Show the raw tool call")
                .help(expanded ? "Hide the raw tool call" : "Show the raw tool call and what came back")

                Button {
                    if let step = row.step { store.requestSeek(runId: runId, step: step) }
                } label: {
                    HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                        Text(failed ? "✗" : "✓")
                            .foregroundStyle(failed ? theme.color(.failure, on: .surface) : theme.dim(on: .surface))
                            .accessibilityLabel(failed ? "Failed" : "Done")
                        Text(row.phrase)
                            .foregroundStyle(failed ? theme.color(.failure, on: .surface) : theme.foreground)
                            .lineLimit(1)
                            .truncationMode(.tail)
                            .layoutPriority(1)
                        Spacer(minLength: Space.s)
                        Text(row.facts.joined(separator: "  "))
                            .monospacedDigit()
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .fixedSize()
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(row.step == nil)
                .help(row.step.map { "Show step \($0) on the screen" } ?? "")
            }
            .monoStyle(size: TypeScale.small)
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.s)
            .hoverHighlight(radius: Radius.sm)

            if expanded {
                VStack(alignment: .leading, spacing: Space.s) {
                    ForEach(row.raw, id: \.label) { part in
                        VStack(alignment: .leading, spacing: Space.xs) {
                            SectionLabel(title: part.label)
                            Text(part.text)
                                .monoStyle(size: TypeScale.monoSmall)
                                .foregroundStyle(part.label == "Error" ? theme.color(.failure, on: .surface) : theme.foreground)
                                .textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                                .padding(.horizontal, Space.s)
                                .padding(.vertical, Space.xs)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(theme.background, in: RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                        }
                    }
                }
                // Under the sentence, past the caret's column.
                .padding(.leading, Space.s + Space.m + Space.s)
                .padding(.trailing, Space.s)
                .padding(.bottom, Space.m)
            }
        }
    }
}

/// While the verifier has the turn.
struct WorkingRow: View {
    var body: some View {
        HStack(spacing: Space.s) {
            Spinner(size: TypeScale.monoSmall)
            Text("Verifier is working")
        }
        .monoStyle(size: TypeScale.monoSmall)
        .foregroundStyle(.secondary)
        .padding(.leading, Space.m)
        .accessibilityElement(children: .combine)
    }
}
