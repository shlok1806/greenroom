import SwiftUI

private func isBlank(_ text: String) -> Bool {
    text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

private extension View {
    /// Return and Cmd-Return send; Shift-Return is the field's own newline.
    /// Caught with `.onKeyPress` because a vertical `TextField` would insert
    /// the newline itself. A blank draft swallows Return without a newline.
    func sendOnReturn(enabled: Bool, _ action: @escaping () -> Void) -> some View {
        onKeyPress(phases: .down) { press in
            guard press.key == .return else { return .ignored }
            guard press.modifiers.isEmpty || press.modifiers == .command else { return .ignored }
            if enabled { action() }
            return .handled
        }
    }
}

/// A one-line reply box: a field and a button that both send the trimmed text.
/// The text stays until the daemon has taken it.
private struct ReplyField: View {
    let prompt: String
    let button: String
    @Binding var text: String
    let send: (String) async -> Bool

    @State private var sending = false

    private var canSend: Bool { !sending && !isBlank(text) }

    var body: some View {
        HStack {
            TextField(prompt, text: $text)
                .textFieldStyle(.roundedBorder)
                .sendOnReturn(enabled: canSend, submit)
            Button(button, action: submit)
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(!canSend)
        }
    }

    private func submit() {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !sending else { return }
        sending = true
        Task {
            if await send(trimmed) { text = "" }
            sending = false
        }
    }
}

/// The run's conversation and the human's seat in it (ADR 0006). The column
/// is capped at a readable measure.
struct TranscriptView: View {
    let store: RunStore
    let runId: String

    @State private var draft = ""
    @State private var draftKind: MessageKind = .note
    @State private var sending = false
    @FocusState private var composing: Bool
    @State private var atBottom = true
    /// Whether the newest message has been scrolled to at least once.
    @State private var anchored = false

    private var messages: [Message] { store.messages[runId] ?? [] }

    private var awaitingVerifier: Bool { RunStore.awaitingVerifier(messages) }

    private static let thinkingRowId = -1

    private static let bottomSlack: CGFloat = 40
    private static let measure: CGFloat = 820

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(messages) { message in
                            MessageRow(store: store, runId: runId, message: message)
                                .id(message.seq)
                        }
                        if awaitingVerifier {
                            ThinkingRow()
                                .id(Self.thinkingRowId)
                        }
                    }
                    .frame(maxWidth: Self.measure, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                // Follow new messages only for someone already at the end.
                .onScrollGeometryChange(for: Bool.self) { geometry in
                    geometry.contentOffset.y + geometry.containerSize.height
                        >= geometry.contentSize.height - Self.bottomSlack
                } action: { _, isAtBottom in
                    atBottom = isAtBottom
                }
                .onChange(of: messages.count) {
                    guard let last = messages.last else { return }
                    // The first batch always anchors: before it arrives the
                    // empty list reads as "not at the bottom".
                    guard atBottom || !anchored else { return }
                    anchored = true
                    atBottom = true
                    withAnimation { proxy.scrollTo(last.seq, anchor: .bottom) }
                }
                .onChange(of: awaitingVerifier) {
                    guard atBottom, awaitingVerifier else { return }
                    withAnimation { proxy.scrollTo(Self.thinkingRowId, anchor: .bottom) }
                }
                .onAppear {
                    atBottom = true
                    if let last = messages.last {
                        anchored = true
                        proxy.scrollTo(last.seq, anchor: .bottom)
                    }
                }
                .overlay {
                    if messages.isEmpty {
                        ContentUnavailableView(
                            "No conversation yet",
                            systemImage: "bubble.left.and.bubble.right",
                            description: Text("The coding agent opens the conversation with the task. "
                                + "You can add a note from here at any time.")
                        )
                    }
                }
            }
            Divider()
            composer
        }
        .onAppear { composing = true }
    }

    private var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            Picker("Kind", selection: $draftKind) {
                Text("note").tag(MessageKind.note)
                Text("task").tag(MessageKind.task)
            }
            .labelsHidden()
            .frame(width: 90)

            TextField("Message the verifier", text: $draft, axis: .vertical)
                .lineLimit(1...5)
                .textFieldStyle(.roundedBorder)
                .focused($composing)
                .sendOnReturn(enabled: canSend, send)

            Button("Send") { send() }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(!canSend)
        }
        .padding(12)
    }

    private var canSend: Bool { !sending && !isBlank(draft) }

    /// The draft stays in the field until the daemon has taken it.
    private func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        sending = true
        atBottom = true
        let kind = draftKind
        Task {
            if await store.send(runId: runId, kind: kind, text: text) { draft = "" }
            sending = false
        }
    }
}

private struct MessageRow: View {
    let store: RunStore
    let runId: String
    let message: Message

    private var isSystemEvent: Bool { message.from == .system && message.kind == .event }

    var body: some View {
        Group {
            if isSystemEvent {
                eventRow
            } else {
                spokenRow
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Machine ready, destroyed, budget spent: not speech.
    private var eventRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Image(systemName: "info.circle")
            Text(message.text)
                .textSelection(.enabled)
            Spacer(minLength: 8)
            Text(Chrome.timeOfDay(message.at))
                .monospacedDigit()
                .help(Chrome.stamp(message.at))
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    private var spokenRow: some View {
        VStack(alignment: .leading, spacing: 6) {
            header
            switch message.kind {
            case .verdict:
                VerdictBody(store: store, runId: runId, message: message)
            case .question:
                QuestionBody(store: store, runId: runId, message: message)
            case .progress:
                ProgressBody(store: store, runId: runId, message: message)
            default:
                MessageText(text: message.text)
            }
        }
        .padding(bubble == nil ? 0 : 10)
        .background {
            if let bubble {
                RoundedRectangle(cornerRadius: 8).fill(bubble)
            }
        }
    }

    private var bubble: AnyShapeStyle? {
        switch message.from {
        case .verifier:
            switch message.kind {
            case .reply, .question: return AnyShapeStyle(.quaternary)
            default: return nil
            }
        case .human:
            return AnyShapeStyle(Color.accentColor.opacity(0.12))
        case .coder, .system, .unknown:
            return nil
        }
    }

    private var header: some View {
        HStack(spacing: 6) {
            Text(message.from.text)
                .font(.caption.weight(.semibold))
                .foregroundStyle(tint)
            if message.kind != .reply {
                Text(message.kind.text)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let replyTo = message.replyTo {
                Text("re \(replyTo)")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.tertiary)
            }
            Spacer()
            Text(Chrome.timeOfDay(message.at))
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
                .help("\(Chrome.stamp(message.at)) · \(Chrome.relative(message.at))")
            Text("#\(message.seq)")
                .font(.caption2.monospaced())
                .foregroundStyle(.quaternary)
        }
    }

    /// Only the human is coloured, so the verdict stays the strongest colour.
    private var tint: Color {
        switch message.from {
        case .human: return .accentColor
        case .coder, .verifier: return .primary
        case .system, .unknown: return .secondary
        }
    }
}

private struct MessageText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // By position: block text can repeat.
            ForEach(Array(RichText.blocks(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .heading(let title, let level):
                    Text(title)
                        .font(level <= 2 ? .headline : .subheadline.weight(.semibold))
                        .textSelection(.enabled)
                        .padding(.top, 2)
                case .code(let body):
                    ScrollView(.horizontal) {
                        Text(body)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .padding(8)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.quinary, in: RoundedRectangle(cornerRadius: 4))
                case .prose(let body):
                    Text(inline(body))
                        .font(.body)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Preserving whitespace keeps the agent's line breaks, and so its lists.
    private func inline(_ body: String) -> AttributedString {
        (try? AttributedString(
            markdown: body,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(body)
    }
}

private struct ThinkingRow: View {
    var body: some View {
        HStack(spacing: 6) {
            ProgressView()
                .controlSize(.small)
            Text("verifier is working")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A verdict is a proposal; a human accepts or disputes it.
private struct VerdictBody: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var disputing = false
    @State private var reason = ""
    @State private var accepting = false

    private var state: VerdictState? { store.verdict(runId) }

    private var isLatest: Bool { state?.seq == message.seq }

    private var canClose: Bool {
        guard let state, isLatest else { return false }
        return state.status.isOpen
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "gavel")
                    .foregroundStyle(Chrome.color(forVerdict: message.verdict))
                Text(message.verdict ?? "verdict")
                    .font(.headline)
                    .foregroundStyle(Chrome.color(forVerdict: message.verdict))
                if isLatest, let state {
                    Text(state.status.text)
                        .font(.caption)
                        .foregroundStyle(Chrome.color(for: state.status))
                }
            }
            MessageText(text: message.text)
            if let evidence = message.evidence, !evidence.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    SectionCaption(title: "Evidence")
                    ForEach(evidence, id: \.self) { item in
                        Text(item)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                    }
                }
            }
            if canClose {
                HStack(spacing: 8) {
                    Button("Accept") {
                        accepting = true
                        Task {
                            await store.send(runId: runId, kind: .accept, text: "accepted", replyTo: message.seq)
                            accepting = false
                        }
                    }
                    .disabled(accepting)
                    Button(disputing ? "Cancel" : "Dispute") { disputing.toggle() }
                        .disabled(accepting)
                }
                if disputing {
                    ReplyField(prompt: "Why the verdict is wrong, with evidence", button: "Send dispute", text: $reason) { text in
                        let sent = await store.send(runId: runId, kind: .dispute, text: text, replyTo: message.seq)
                        if sent { disputing = false }
                        return sent
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        .overlay(alignment: .leading) {
            Rectangle()
                .fill(Chrome.color(forVerdict: message.verdict))
                .frame(width: 3)
                .clipShape(RoundedRectangle(cornerRadius: 2))
        }
    }
}

private struct QuestionBody: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var answer = ""

    private var answered: Bool {
        (store.messages[runId] ?? []).contains { $0.kind == .answer && $0.replyTo == message.seq }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: "questionmark.bubble")
                    .foregroundStyle(.secondary)
                MessageText(text: message.text)
            }
            if !answered {
                ReplyField(prompt: "Answer", button: "Reply", text: $answer) { text in
                    await store.send(runId: runId, kind: .answer, text: text, replyTo: message.seq)
                }
            }
        }
    }
}

/// One verifier tool call, collapsed to a line. The chevron expands; the line
/// seeks the Screen tab to its step (ADR 0008).
private struct ProgressBody: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var expanded = false

    /// The step's own summary when the record is held; else the first line of
    /// the message, which is the tool name and raw JSON.
    private var headline: String {
        if let number = message.step,
           let step = (store.steps[runId] ?? []).first(where: { $0.seq == number }) {
            let summary = StepSummary.line(for: step)
            return summary.isEmpty ? step.tool : "\(step.tool)  \(summary)"
        }
        let first = message.text.split(separator: "\n").first.map(String.init) ?? "progress"
        guard let step = message.step else { return StepSummary.oneLine(first) }
        let prefix = "step \(step):"
        let rest = first.hasPrefix(prefix) ? String(first.dropFirst(prefix.count)) : first
        return StepSummary.oneLine(rest)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Button {
                    expanded.toggle()
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide what the verifier wrote" : "Show what the verifier wrote")

                Button {
                    if let step = message.step {
                        store.requestSeek(runId: runId, step: step)
                    }
                } label: {
                    HStack(spacing: 6) {
                        if let step = message.step {
                            Text("\(step)")
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.tertiary)
                                .frame(minWidth: 26, alignment: .trailing)
                        }
                        Text(headline)
                            .font(.system(.caption, design: .monospaced))
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(message.step == nil)
                .help("Show the screen at this step")
            }
            .foregroundStyle(.secondary)

            if expanded {
                Text(message.text)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(.leading, 16)
            }
        }
    }
}
