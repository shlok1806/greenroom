import SwiftUI

/// Nothing but whitespace, so nothing worth sending.
private func isBlank(_ text: String) -> Bool {
    text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

/// Return sends, Shift-Return makes a newline, Cmd-Return sends too.
///
/// A `TextField` with `axis: .vertical` treats Return as a newline on its own,
/// so the key has to be caught before the field's own handling. `.onKeyPress`
/// on the field runs while the focus is there and ahead of the text view, and
/// returning `.ignored` hands Shift-Return straight back to it.
private struct SendOnReturn: ViewModifier {
    let enabled: Bool
    let action: () -> Void

    func body(content: Content) -> some View {
        content.onKeyPress(phases: .down) { press in
            guard press.key == .return else { return .ignored }
            // Shift-Return is the field's own newline. Cmd-Return sends, like
            // the Send button's shortcut: that shortcut gets first refusal on
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
///
/// The column has a measure. A message stretched across a wide window is a
/// line of prose two hundred characters long, which nobody reads; capping it
/// and leaving the rest of the window empty is what makes a transcript
/// readable next to the evidence rather than beside it.
struct TranscriptView: View {
    @Bindable var store: RunStore
    let runId: String

    @State private var draft = ""
    @State private var draftKind: MessageKind = .note
    @FocusState private var composing: Bool
    @State private var atBottom = true
    /// Whether the view has ever managed to put the newest message on screen.
    /// `onAppear` runs before `select` has answered, so there is nothing to
    /// scroll to yet; the first batch to arrive wins.
    @State private var anchored = false

    private var messages: [Message] { store.messages[runId] ?? [] }

    private var awaitingVerifier: Bool { RunStore.awaitingVerifier(messages) }

    private static let thinkingRowId = -1

    /// How near the end still counts as the end, in points.
    private static let bottomSlack: CGFloat = 40

    /// The widest a line of the conversation is allowed to get.
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
                // Follow the conversation only for someone who is already at
                // the end of it. Someone who scrolled back to read is not
                // dragged away by a message landing.
                .onScrollGeometryChange(for: Bool.self) { geometry in
                    geometry.contentOffset.y + geometry.containerSize.height
                        >= geometry.contentSize.height - Self.bottomSlack
                } action: { _, isAtBottom in
                    atBottom = isAtBottom
                }
                .onChange(of: messages.count) {
                    guard let last = messages.last else { return }
                    // The first messages to arrive always win: until they do,
                    // the empty list leaves the view at its top, and the
                    // geometry observer has already decided "not at the
                    // bottom" — so waiting for `atBottom` left a run opening
                    // on its oldest message and never following again.
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
                .sendOnReturn(enabled: !isBlank(draft), send)

            Button("Send") { send() }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(isBlank(draft))
        }
        .padding(12)
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
            // The time of day, not "2 days ago": two hundred rows that all
            // read "2 days ago" say nothing about their order or their
            // spacing. The age is a tooltip away.
            Text(Chrome.timeOfDay(message.at))
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
                .help("\(Chrome.stamp(message.at)) · \(Chrome.relative(message.at))")
            Text("#\(message.seq)")
                .font(.caption2.monospaced())
                .foregroundStyle(.quaternary)
        }
    }

    /// Only the human's seat is coloured. The agents are told apart by weight,
    /// so that the strongest colour in the window stays the verdict.
    private var tint: Color {
        switch message.from {
        case .human: return .accentColor
        case .coder, .verifier: return .primary
        case .system, .unknown: return .secondary
        }
    }
}

/// A message's body, with the little Markdown an agent actually writes: a
/// heading is a heading, a fenced block and a table keep their columns, and
/// `**bold**` and `` `code` `` stop being punctuation.
private struct MessageText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // By position, never by content: a message that says the same
            // thing twice must not hand the list two rows with one id.
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

    /// Inline Markdown only: emphasis, code spans and links. Whitespace is
    /// preserved so the agent's own line breaks survive, which is what keeps a
    /// bulleted list looking like one without a list parser.
    private func inline(_ body: String) -> AttributedString {
        (try? AttributedString(
            markdown: body,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(body)
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
            MessageText(text: message.text)
            if let evidence = message.evidence, !evidence.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    Text("EVIDENCE")
                        .font(.system(size: 9, weight: .semibold))
                        .tracking(0.6)
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
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        .overlay(alignment: .leading) {
            // One strong stroke, in the verdict's own colour: this is the row
            // a reviewer is looking for.
            Rectangle()
                .fill(Chrome.color(forVerdict: message.verdict))
                .frame(width: 3)
                .clipShape(RoundedRectangle(cornerRadius: 2))
        }
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
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: "questionmark.bubble")
                    .foregroundStyle(.secondary)
                MessageText(text: message.text)
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

    /// What the step did, at the grain the Steps tab reads it.
    ///
    /// The message's own text is the tool name and the raw JSON it was called
    /// with, which is the debug dump this transcript is trying not to be. When
    /// the app already holds the step's record, the summary comes from there
    /// instead; the message's text is the fallback, and stays behind the
    /// chevron either way.
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
            // Two targets, the way the Steps tab has them: one click cannot
            // both open the row and carry the person off to the Screen tab,
            // because then the thing it opened is on a tab they have left.
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
