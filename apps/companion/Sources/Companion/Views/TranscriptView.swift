import SwiftUI

/// Nothing but whitespace, so nothing worth sending.
private func isBlank(_ text: String) -> Bool {
    text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

/// Return sends, Shift-Return makes a newline, Cmd-Return sends too.
///
/// A `TextField` with `axis: .vertical` treats Return as a newline of its own,
/// so the key has to be caught before the field's own handling. `.onKeyPress`
/// on the field runs while it has focus and ahead of the text view, and
/// returning `.ignored` hands Shift-Return straight back to it.
private struct SendOnReturn: ViewModifier {
    let enabled: Bool
    let action: () -> Void

    func body(content: Content) -> some View {
        content.onKeyPress(phases: .down) { press in
            guard press.key == .return else { return .ignored }
            // Shift-Return is the field's own newline. Cmd-Return sends, like
            // the Send button's shortcut: the shortcut gets first refusal on
            // it, and this catches it when the button is not in the responder
            // chain to take it.
            guard press.modifiers.isEmpty || press.modifiers == .command else { return .ignored }
            // A blank draft sends nothing, and must not leave a stray newline
            // behind either.
            guard enabled else { return .handled }
            action()
            return .handled
        }
    }
}

private extension View {
    /// Makes Return in a text field send, the way every chat composer does.
    func sendOnReturn(enabled: Bool, _ action: @escaping () -> Void) -> some View {
        modifier(SendOnReturn(enabled: enabled, action: action))
    }
}

/// The run's one conversation, and the human's seat in it (ADR 0006).
struct TranscriptView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var draft = ""
    @State private var draftKind: MessageKind = .note
    @FocusState private var composing: Bool
    @State private var atBottom = true

    private var messages: [Message] { store.messages[runId] ?? [] }

    /// The verifier owes the transcript an answer, so say so at the bottom.
    private var awaitingVerifier: Bool { RunStore.awaitingVerifier(messages) }

    private static let thinkingRowId = -1

    /// How near the end still counts as being at the end, in points.
    private static let bottomSlack: CGFloat = 40

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        ForEach(messages) { message in
                            MessageRow(store: store, runId: runId, message: message)
                                .id(message.seq)
                        }
                        if awaitingVerifier {
                            ThinkingRow()
                                .id(Self.thinkingRowId)
                        }
                    }
                    .padding(12)
                }
                // Follow the conversation only for someone already at the end
                // of it. Someone who has scrolled back to read is not dragged
                // away by a message landing.
                .onScrollGeometryChange(for: Bool.self) { geometry in
                    geometry.contentOffset.y + geometry.containerSize.height
                        >= geometry.contentSize.height - Self.bottomSlack
                } action: { _, isAtBottom in
                    atBottom = isAtBottom
                }
                .onChange(of: messages.count) {
                    guard atBottom, let last = messages.last else { return }
                    withAnimation { proxy.scrollTo(last.seq, anchor: .bottom) }
                }
                .onChange(of: awaitingVerifier) {
                    guard atBottom, awaitingVerifier else { return }
                    withAnimation { proxy.scrollTo(Self.thinkingRowId, anchor: .bottom) }
                }
                .onAppear {
                    atBottom = true
                    if let last = messages.last { proxy.scrollTo(last.seq, anchor: .bottom) }
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
                .sendOnReturn(enabled: !isBlank(draft), send)

            Button("Send") { send() }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(isBlank(draft))
        }
        .padding(10)
    }

    private func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        draft = ""
        // Your own message always brings you back to the end.
        atBottom = true
        let kind = draftKind
        Task { await store.send(runId: runId, kind: kind, text: text) }
    }
}

/// One message. The verifier's own voice reads as a bubble, a human's as the
/// same bubble in the accent colour, and a system event as the quietest line on
/// screen. A verdict, a question and a run of progress each keep their shape.
private struct MessageRow: View {
    @Bindable var store: RunStore
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

    /// Machine ready, destroyed, budget spent: it happened, it is not speech.
    private var eventRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Image(systemName: "info.circle")
            Text(message.text)
                .textSelection(.enabled)
            Spacer(minLength: 8)
            Text(Chrome.relative(message.at))
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
                Text(message.text)
                    .textSelection(.enabled)
                    .font(.body)
            }
        }
        .padding(bubble == nil ? 0 : 10)
        .background {
            if let bubble {
                RoundedRectangle(cornerRadius: 8).fill(bubble)
            }
        }
    }

    /// The agent's voice and the human's stand apart from system events and
    /// from the coder, without any left/right alignment: this is a transcript.
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

    /// A `reply` is plain speech, so its kind adds nothing to read.
    private var showsKind: Bool { message.kind != .reply }

    private var header: some View {
        HStack(spacing: 6) {
            Text(message.from.text)
                .font(.caption.weight(.semibold))
                .foregroundStyle(tint)
            if showsKind {
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
            Text(Chrome.relative(message.at))
                .font(.caption2)
                .foregroundStyle(.tertiary)
            Text("#\(message.seq)")
                .font(.caption2.monospaced())
                .foregroundStyle(.tertiary)
        }
    }

    private var tint: Color {
        switch message.from {
        case .coder: return .blue
        case .human: return .purple
        case .verifier: return .teal
        case .system, .unknown: return .secondary
        }
    }
}

/// The verifier has the turn and has not answered yet.
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

/// A verdict is a proposal. A human closes it.
private struct VerdictBody: View {
    @Bindable var store: RunStore
    let runId: String
    let message: Message

    @State private var disputing = false
    @State private var reason = ""

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
            Text(message.text)
                .textSelection(.enabled)
            if let evidence = message.evidence, !evidence.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Evidence")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
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
                        Task { await store.send(runId: runId, kind: .accept, text: "accepted", replyTo: message.seq) }
                    }
                    Button(disputing ? "Cancel" : "Dispute") { disputing.toggle() }
                }
                if disputing {
                    HStack {
                        TextField("Why the verdict is wrong, with evidence", text: $reason)
                            .textFieldStyle(.roundedBorder)
                            .sendOnReturn(enabled: !isBlank(reason), sendDispute)
                        Button("Send dispute") { sendDispute() }
                            .keyboardShortcut(.return, modifiers: .command)
                            .disabled(isBlank(reason))
                    }
                }
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }

    private func sendDispute() {
        let text = reason.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        reason = ""
        disputing = false
        Task { await store.send(runId: runId, kind: .dispute, text: text, replyTo: message.seq) }
    }
}

/// A question ends the verifier's turn. Answering starts the next one.
private struct QuestionBody: View {
    @Bindable var store: RunStore
    let runId: String
    let message: Message

    @State private var answer = ""

    private var answered: Bool {
        (store.messages[runId] ?? []).contains { $0.kind == .answer && $0.replyTo == message.seq }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "questionmark.bubble")
                    .foregroundStyle(.teal)
                Text(message.text)
                    .textSelection(.enabled)
            }
            if !answered {
                HStack {
                    TextField("Answer", text: $answer)
                        .textFieldStyle(.roundedBorder)
                        .sendOnReturn(enabled: !isBlank(answer), sendAnswer)
                    Button("Reply") { sendAnswer() }
                        .keyboardShortcut(.return, modifiers: .command)
                        .disabled(isBlank(answer))
                }
            }
        }
    }

    private func sendAnswer() {
        let text = answer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        answer = ""
        Task { await store.send(runId: runId, kind: .answer, text: text, replyTo: message.seq) }
    }
}

/// One verifier tool call. Collapsed to a line, because there are many.
/// Clicking it also jumps the Screen tab to the frame at or after its step
/// (ADR 0008).
private struct ProgressBody: View {
    @Bindable var store: RunStore
    let runId: String
    let message: Message

    @State private var expanded = false

    private var headline: String {
        let tool = message.text.split(separator: "\n").first.map(String.init) ?? "progress"
        if let step = message.step { return "step \(step): \(tool)" }
        return tool
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                expanded.toggle()
                if let step = message.step {
                    store.requestSeek(runId: runId, step: step)
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                    Text(headline)
                        .font(.caption.monospaced())
                        .lineLimit(1)
                }
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)

            if expanded {
                Text(message.text)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .padding(.leading, 16)
            }
        }
    }
}
