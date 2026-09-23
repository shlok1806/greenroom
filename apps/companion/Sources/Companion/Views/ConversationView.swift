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

/// The run's conversation and the human's seat in it (ADR 0006): the verdict pinned on
/// top, the transcript, and the composer.
struct ConversationView: View {
    let store: RunStore
    let runId: String

    @State private var atBottom = true
    /// Whether the newest message has been scrolled to at least once.
    @State private var anchored = false
    @State private var userScrolled = false
    @AppStorage("showsToolCalls") private var showsToolCalls = true

    private var messages: [Message] { store.messages[runId] ?? [] }
    private var items: [TranscriptLayout.Item] { TranscriptLayout.items(messages, toolCalls: showsToolCalls) }
    private var awaitingVerifier: Bool { RunStore.awaitingVerifier(messages) }

    private static let workingRowId = -1

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            VerdictCard(store: store, runId: runId, facts: store.facts(runId))
                .padding(Space.m)
                .background(Color(nsColor: .windowBackgroundColor))
            Divider()
            transcript
            Divider()
            Composer(store: store, runId: runId) { atBottom = true }
        }
        .navigationTitle("Conversation")
    }

    /// Names the column and holds its one filter: the verifier's tool calls, which a
    /// long run has hundreds of.
    private var header: some View {
        HStack(spacing: Space.s) {
            Text("Conversation")
                .font(.title3.weight(.semibold))
            let count = store.facts(runId).messageCount
            if count > 0 {
                Text(count == 1 ? "1 message" : "\(Chrome.count(count)) messages")
                    .font(.callout.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: Space.s)
            Toggle("Tool calls", isOn: $showsToolCalls)
                .toggleStyle(.checkbox)
                .controlSize(.small)
                .font(.callout)
                .help("Show the verifier's tool calls between its messages")
        }
        .padding(.horizontal, Space.m)
        .padding(.top, Space.m)
        .padding(.bottom, Space.s)
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Space.m) {
                    ForEach(items) { item in
                        row(item).id(item.id)
                    }
                    if awaitingVerifier {
                        WorkingRow().id(Self.workingRowId)
                    }
                    // A lazy stack guesses the height of rows it has not drawn, so "at the
                    // end" is whether this last line is on screen, not a scroll offset.
                    Color.clear
                        .frame(height: 1)
                        .onScrollVisibilityChange(threshold: 0.01) { atBottom = $0 }
                }
                .scrollTargetLayout()
                .padding(.horizontal, Space.m)
                .padding(.vertical, Space.m)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .overlayScrollers()
            .scrollTargetBehavior(.viewAligned(limitBehavior: .never))
            .contentMargins(.top, Space.s, for: .scrollContent)
            .defaultScrollAnchor(.bottom)
            // A scroll-edge fade: whatever row sits at the top edge dissolves into the
            // surface under the pinned card instead of showing as a cut-off half line.
            .overlay(alignment: .top) {
                LinearGradient(
                    colors: [Color(nsColor: .windowBackgroundColor), Color(nsColor: .windowBackgroundColor).opacity(0)],
                    startPoint: .top, endPoint: .bottom
                )
                .frame(height: 22)
                .allowsHitTesting(false)
            }
            // "Jump to latest" is for someone who scrolled away, never shown unasked.
            .onScrollPhaseChange { _, phase in
                if phase == .interacting { userScrolled = true }
            }
            .onChange(of: messages.count) {
                guard let last = items.last else { return }
                // The first batch always anchors: before it arrives the
                // empty list reads as "not at the bottom".
                guard atBottom || !anchored else { return }
                anchored = true
                atBottom = true
                // After the new row has laid out, or the scroll stops short of it.
                Task {
                    try? await Task.sleep(for: .milliseconds(50))
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }
            .onChange(of: awaitingVerifier) {
                guard atBottom, awaitingVerifier else { return }
                withAnimation { proxy.scrollTo(Self.workingRowId, anchor: .bottom) }
            }
            .onAppear {
                atBottom = true
                if let last = items.last {
                    anchored = true
                    proxy.scrollTo(last.id, anchor: .bottom)
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if !atBottom, anchored, userScrolled, items.count > 3 {
                    Button {
                        atBottom = true
                        if let last = items.last {
                            withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                        }
                    } label: {
                        Label("Jump to latest", systemImage: "arrow.down")
                            .font(.caption.weight(.medium))
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(.regularMaterial, in: Capsule())
                            .overlay(Capsule().strokeBorder(Palette.hairline))
                            .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
                    }
                    .buttonStyle(.plain)
                    .padding(.bottom, Space.s)
                    .padding(.trailing, Space.l)
                    .help("Scroll to the newest message")
                    .transition(.opacity)
                }
            }
            .overlay {
                if messages.isEmpty {
                    ContentUnavailableView {
                        Label("No conversation yet", systemImage: "bubble.left.and.bubble.right")
                    } description: {
                        Text("The coding agent opens it with the task. You can message the verifier at any time.")
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func row(_ item: TranscriptLayout.Item) -> some View {
        switch item {
        case .message(let message, let showsSender):
            MessageRow(store: store, runId: runId, message: message, showsSender: showsSender)
        case .toolCalls(let calls):
            ToolCallGroup(store: store, runId: runId, calls: calls)
        case .event(let message):
            EventLine(message: message, verdicts: messages.filter { $0.kind == .verdict }, steps: store.steps[runId] ?? [])
        case .day(let day, _):
            HStack(spacing: Space.s) {
                Rectangle().fill(Palette.hairline).frame(height: 1)
                Text(Chrome.day(day))
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .fixedSize()
                Rectangle().fill(Palette.hairline).frame(height: 1)
            }
            .padding(.vertical, Space.xs)
        }
    }
}

// MARK: - Rows

/// A lifecycle event or a closed verdict, as a quiet centred line.
private struct EventLine: View {
    let message: Message
    let verdicts: [Message]
    let steps: [Step]

    var body: some View {
        HStack(spacing: Space.s) {
            line
            HStack(spacing: 5) {
                if message.kind == .accept {
                    Image(systemName: "checkmark.circle")
                }
                Text(text)
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
                Text(Chrome.shortTime(message.at))
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
                    .help(Chrome.stamp(message.at))
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .layoutPriority(1)
            line
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, Space.xxs)
        .textSelection(.enabled)
    }

    private var line: some View {
        Rectangle().fill(Palette.hairline).frame(height: 1).frame(minWidth: 12)
    }

    private var text: String {
        guard message.kind == .accept else { return Chrome.eventText(message.text) }
        let target = verdicts.first { $0.seq == message.replyTo }
        let outcome = target?.verdict.map { " \($0)" } ?? ""
        return "\(message.from.displayName) accepted the\(outcome) verdict"
    }
}

/// One spoken message: avatar and name once per turn, then the body. The human's own
/// words sit on the right, like any conversation.
private struct MessageRow: View {
    let store: RunStore
    let runId: String
    let message: Message
    let showsSender: Bool

    private var mine: Bool { message.from == .human }

    var body: some View {
        if mine {
            VStack(alignment: .trailing, spacing: Space.xs) {
                if showsSender { header }
                bubble
                    .frame(maxWidth: 320, alignment: .trailing)
            }
            .frame(maxWidth: .infinity, alignment: .trailing)
            .padding(.leading, Space.xxl)
        } else {
            HStack(alignment: .top, spacing: Space.s) {
                if showsSender {
                    SenderAvatar(from: message.from)
                } else {
                    Color.clear.frame(width: 22, height: 1)
                }
                VStack(alignment: .leading, spacing: Space.xs) {
                    if showsSender { header }
                    bubble
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(message.from.displayName)
                .font(.callout.weight(.semibold))
            if let kind = kindLabel {
                Text(kind)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
            }
            Text(Chrome.shortTime(message.at))
                .font(.caption.monospacedDigit())
                .foregroundStyle(.tertiary)
                .help("\(Chrome.stamp(message.at)) · message \(message.seq)")
        }
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
    private var bubble: some View {
        switch message.kind {
        case .verdict:
            VerdictMessage(store: store, runId: runId, message: message)
        case .question:
            QuestionBubble(store: store, runId: runId, message: message)
        default:
            MessageText(text: TranscriptText.clean(message.text))
                .padding(.horizontal, Space.m)
                .padding(.vertical, Space.s)
                .background(fill, in: RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous))
                .overlay(alignment: .leading) {
                    if message.kind == .dispute {
                        RoundedRectangle(cornerRadius: 1.5).fill(.orange).frame(width: 3).padding(.vertical, Space.s)
                    }
                }
        }
    }

    private var fill: AnyShapeStyle {
        switch message.from {
        case .human: AnyShapeStyle(Color.accentColor.opacity(0.16))
        case .coder where message.kind == .task: AnyShapeStyle(.fill.tertiary)
        default: AnyShapeStyle(.fill.quaternary)
        }
    }
}

/// A verdict in the history, one line: the card above holds the live one in full, so it
/// is never shown twice. An earlier one opens to its words.
private struct VerdictMessage: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var open = false

    private var isLatest: Bool { store.verdict(runId)?.seq == message.seq }
    private var tint: Color { Palette.outcome(message.verdict) }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            Button {
                withAnimation(.snappy(duration: 0.18)) { open.toggle() }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: Chrome.outcomeSymbol(message.verdict))
                        .foregroundStyle(tint)
                    Text(isLatest ? "Verdict: \(Chrome.outcomeTitle(message.verdict))" : "Earlier verdict: \(Chrome.outcomeTitle(message.verdict))")
                        .font(.callout.weight(.semibold))
                        .foregroundStyle(isLatest ? tint : .secondary)
                    Text(isLatest ? "in full above" : "superseded")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 0)
                    Image(systemName: open ? "chevron.up" : "chevron.down")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.tertiary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(open ? "Hide what the verifier wrote" : "Show what the verifier wrote")
            if open {
                MessageText(text: TranscriptText.clean(message.text))
                    .foregroundStyle(.secondary)
                let cited = VerdictCard.byStep((message.evidence ?? []).map(Evidence.parse))
                if !cited.isEmpty {
                    HStack(spacing: Space.xs) {
                        Text(isLatest ? "Cites" : "Cited by this earlier \(Chrome.outcomeTitle(message.verdict)) verdict, superseded:")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        ForEach(cited, id: \.self) { item in
                            EvidenceLink(store: store, runId: runId, item: item) {
                                if let step = item.step { store.requestSeek(runId: runId, step: step) }
                            }
                        }
                    }
                }
            }
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.fill.quinary, in: RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous).strokeBorder(tint.opacity(isLatest ? 0.35 : 0.15)))
    }
}

/// The verifier asking the person something, answered in place.
private struct QuestionBubble: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var answer = ""
    @State private var sending = false

    private var answered: Bool {
        (store.messages[runId] ?? []).contains { $0.kind == .answer && $0.replyTo == message.seq }
    }

    private var canSend: Bool { !sending && !isBlank(answer) }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            MessageText(text: TranscriptText.clean(message.text))
            if !answered {
                HStack(spacing: Space.s) {
                    TextField("Answer the verifier", text: $answer)
                        .textFieldStyle(.roundedBorder)
                        .sendOnReturn(enabled: canSend, submit)
                    Button("Reply", action: submit)
                        .disabled(!canSend)
                }
            } else {
                Label("Answered", systemImage: "checkmark")
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, Space.m)
        .padding(.vertical, Space.s)
        .background(.fill.quaternary, in: RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous)
                .strokeBorder(answered ? AnyShapeStyle(.clear) : AnyShapeStyle(Color.blue.opacity(0.45)), lineWidth: 1)
        )
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

/// The verifier's tool calls in a row, one line each. A line seeks the Screen to its
/// step; its chevron shows what the verifier saw. Long runs fold to the last few.
private struct ToolCallGroup: View {
    let store: RunStore
    let runId: String
    let calls: [Message]

    @State private var showsAll = false

    private static let folded = 3

    private var visible: [Message] {
        showsAll || calls.count <= Self.folded + 1 ? calls : Array(calls.suffix(Self.folded))
    }

    var body: some View {
        HStack(alignment: .top, spacing: Space.s) {
            Color.clear.frame(width: 22, height: 1)
            VStack(alignment: .leading, spacing: 0) {
                if calls.count > Self.folded + 1 {
                    Button {
                        withAnimation(.snappy(duration: 0.2)) { showsAll.toggle() }
                    } label: {
                        HStack(spacing: 6) {
                            Image(systemName: showsAll ? "chevron.down" : "chevron.right")
                                .font(.caption2.weight(.semibold))
                                .frame(width: 10)
                            Text(showsAll ? "Hide earlier tool calls" : "\(calls.count - Self.folded) earlier tool calls")
                        }
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, Space.s)
                        .padding(.vertical, 5)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    Divider()
                }
                ForEach(Array(visible.enumerated()), id: \.element.seq) { position, call in
                    if position > 0 { Divider().padding(.leading, 30) }
                    ToolCallLine(store: store, runId: runId, message: call)
                }
            }
            .background(.fill.quinary, in: RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Radius.card, style: .continuous).strokeBorder(Palette.hairline.opacity(0.7)))
        }
    }
}

private struct ToolCallLine: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var expanded = false

    private var step: Step? {
        guard let number = message.step else { return nil }
        return (store.steps[runId] ?? []).first { $0.seq == number }
    }

    private var entry: ToolCatalog.Entry {
        ToolCatalog.entry(for: step?.tool ?? ToolCatalog.tool(ofProgress: message.text) ?? "")
    }

    /// The step's own summary when the record is held; else the first line of the
    /// message after the tool name, which is raw JSON.
    private var detail: String {
        if let step { return StepSummary.line(for: step) }
        let first = message.text.split(separator: "\n").first.map(String.init) ?? ""
        let rest = first.split(separator: " ", maxSplits: 1).dropFirst().first.map(String.init) ?? ""
        let flat = StepSummary.oneLine(rest)
        return flat == "{}" ? "" : flat
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Button {
                    withAnimation(.snappy(duration: 0.18)) { expanded.toggle() }
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.tertiary)
                        .frame(width: 16, height: 18)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide what the verifier saw" : "Show what the verifier saw")

                Button {
                    if let number = message.step { store.requestSeek(runId: runId, step: number) }
                } label: {
                    HStack(spacing: 6) {
                        Image(systemName: step?.outcome.isFailure == true ? "exclamationmark.triangle.fill" : entry.symbol)
                            .font(.caption)
                            .foregroundStyle(step?.outcome.isFailure == true ? AnyShapeStyle(Color.red) : AnyShapeStyle(.secondary))
                            .frame(width: 14)
                        Text(entry.title)
                            .font(.callout)
                            .fixedSize()
                        Text(detail)
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                        Spacer(minLength: Space.xs)
                        if let number = message.step {
                            Text("step \(number)")
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(message.step == nil)
                .help(message.step.map { "Show step \($0) on the screen" } ?? "")
            }
            .padding(.horizontal, Space.xs + 2)
            .padding(.vertical, 5)
            .hoverHighlight(radius: Radius.chip + 2)

            if expanded {
                Text(message.text)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 30)
                    .padding(.trailing, Space.s)
                    .padding(.bottom, Space.s)
            }
        }
    }
}

private struct WorkingRow: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(alignment: .center, spacing: Space.s) {
            SenderAvatar(from: .verifier)
            HStack(spacing: Space.s) {
                if reduceMotion {
                    Image(systemName: "ellipsis")
                } else {
                    Image(systemName: "ellipsis")
                        .symbolEffect(.variableColor.iterative, options: .repeating)
                }
                Text("Verifier is working")
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            .padding(.horizontal, Space.m)
            .padding(.vertical, 6)
            .background(.fill.quaternary, in: RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous))
        }
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Text

struct MessageText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            // By position: block text can repeat.
            ForEach(Array(RichText.blocks(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .heading(let title, let level):
                    Text(title)
                        .font(level <= 2 ? .headline : .subheadline.weight(.semibold))
                        .textSelection(.enabled)
                        .padding(.top, 2)
                case .code(let body):
                    // Wrapped, not scrolled: a column this narrow would show a scroller per block.
                    Text(body)
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(Space.s)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.fill.quinary, in: RoundedRectangle(cornerRadius: Radius.chip + 2))
                case .prose(let body):
                    Text(inline(body))
                        .font(.body)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    /// Preserving whitespace keeps the agent's line breaks, and so its lists.
    private func inline(_ body: String) -> AttributedString {
        (try? AttributedString(
            markdown: body,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(body)
    }
}

// MARK: - Composer

/// The human seat: a note (the verifier reads it and answers) or a task (new work).
/// The draft stays in the field until the daemon has taken it.
private struct Composer: View {
    let store: RunStore
    let runId: String
    let didSend: () -> Void

    @State private var draft = ""
    @State private var kind: MessageKind = .note
    @State private var sending = false
    @FocusState private var focused: Bool

    private var canSend: Bool { !sending && !offline && !isBlank(draft) }

    @AppStorage("composerFocusRequest") private var focusRequest = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: Space.s) {
                Picker("Send as", selection: $kind) {
                    Text("Message").tag(MessageKind.note)
                    Text("New task").tag(MessageKind.task)
                }
                .pickerStyle(.menu)
                .labelsHidden()
                .controlSize(.small)
                .fixedSize()
                .help("A message is answered by the verifier and read by the coding agent; a new task hands the verifier more to check")
                Text(hint)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(alignment: .bottom, spacing: Space.s) {
                TextField(placeholder, text: $draft, axis: .vertical)
                    .disabled(offline)
                    .textFieldStyle(.plain)
                    .font(.body)
                    .lineLimit(1...6)
                    .focused($focused)
                    .sendOnReturn(enabled: canSend, send)
                    .padding(.vertical, 3)

                Button(action: send) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 20))
                        .symbolRenderingMode(.hierarchical)
                        .foregroundStyle(canSend ? AnyShapeStyle(Color.accentColor) : AnyShapeStyle(.tertiary))
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(!canSend)
                .help("Send (Return)")
                .accessibilityLabel("Send")
            }
            .padding(.horizontal, Space.s + 2)
            .padding(.vertical, 5)
            .background(.background, in: RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Radius.bubble, style: .continuous)
                    .strokeBorder(focused ? AnyShapeStyle(Color.accentColor.opacity(0.6)) : AnyShapeStyle(Palette.hairline),
                                  lineWidth: focused ? 1.5 : 1)
            )
        }
        .padding(Space.m)
        // "Write a Message" in an idle run's header puts the cursor here.
        .onChange(of: focusRequest) { focused = true }
    }

    private var facts: RunFacts { store.facts(runId) }
    private var offline: Bool {
        if case .offline = store.connection { return true }
        return false
    }

    private var placeholder: String {
        if offline { return "Can't send while the daemon is not answering" }
        return kind == .task ? "Describe what the verifier should check" : "Message the verifier and the coding agent"
    }

    /// Who reads it and when, for the state the run is in.
    private var hint: String {
        if kind == .task { return "The verifier starts on it and reports back here" }
        switch facts.phase {
        case .booting: return "The verifier answers once the machine is up"
        case .idle: return "The verifier answers now. The coding agent reads it when it next checks in."
        case .ended, .failed: return "The machine is gone; the verifier answers from the record"
        case .live: return "The verifier answers; the coding agent reads it too"
        }
    }

    private func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        sending = true
        didSend()
        let sentKind = kind
        Task {
            if await store.send(runId: runId, kind: sentKind, text: text) { draft = "" }
            sending = false
        }
    }
}
