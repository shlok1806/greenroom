import SwiftUI

private func isBlank(_ text: String) -> Bool {
    text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

/// The draft with a line break where the cursor is (replacing any selected text), and
/// the cursor after it. At the end when the field reports no cursor.
func insertingNewline(into text: String, at selection: TextSelection?) -> (text: String, selection: TextSelection) {
    var range = text.endIndex..<text.endIndex
    if case .selection(let selected)? = selection?.indices,
       selected.lowerBound >= text.startIndex, selected.upperBound <= text.endIndex {
        range = selected
    }
    let offset = text.distance(from: text.startIndex, to: range.lowerBound) + 1
    var out = text
    out.replaceSubrange(range, with: "\n")
    return (out, TextSelection(insertionPoint: out.index(out.startIndex, offsetBy: offset)))
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
    @State private var columnHeight: Double = 0
    /// The keyboard's row (an item's id): `j` and `k` move it, `⏎` shows its step.
    @State private var cursor: Int?
    @Environment(\.keyboard) private var keyboard

    private var messages: [Message] { store.messages[runId] ?? [] }
    private var items: [TranscriptLayout.Item] { TranscriptLayout.items(messages, toolCalls: showsToolCalls) }
    private var awaitingVerifier: Bool {
        RunStore.verifierIsWorking(messages, verifierListens: store.facts(runId).verifierListens)
    }

    private static let workingRowId = -1
    /// The 1 pt line after the last row. Scrolling to it, not to the last message, shows the
    /// whole of a tall last bubble: scrolled to its own id it stopped a line short.
    private static let endRowId = -2

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(spacing: 0) {
            header
            VerdictCard(store: store, runId: runId, facts: store.facts(runId),
                        maxHeight: RunLayout.verdictCardMaximum(column: columnHeight))
                .id(VerdictCard.identity(runId: runId, verdict: store.verdict(runId)))
                .padding(.horizontal, Space.m)
                .padding(.bottom, Space.m)
                // Before the transcript, which scrolls in whatever is left.
                .layoutPriority(1)
            Hairline()
            transcript
            Hairline()
            Composer(store: store, runId: runId) { atBottom = true }
        }
        .onGeometryChange(for: Double.self) { $0.size.height } action: { columnHeight = $0 }
        // No navigation title: the column's header names it, and a title here would name
        // the whole window "Conversation" (the run view names it after the run).
    }

    /// Names the column and holds its one filter: the verifier's tool calls, which a
    /// long run has hundreds of.
    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text("Conversation")
                .headingStyle()
            let count = store.facts(runId).messageCount
            if count > 0 {
                Text(count == 1 ? "1 message" : "\(Chrome.count(count)) messages")
                    .monoStyle(size: TypeScale.monoSmall)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: Space.s)
            Toggle("Tool calls", isOn: $showsToolCalls)
                .help("Show the verifier's tool calls between its messages")
        }
        .padding(.horizontal, Space.m)
        .padding(.top, Space.m)
        .padding(.bottom, Space.m)
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Space.l) {
                    ForEach(items) { item in
                        row(item)
                            .background {
                                if cursorShown, cursor == item.id {
                                    RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                                        .fill(theme.highlight)
                                        .padding(-Space.xs)
                                }
                            }
                            .id(item.id)
                    }
                    if awaitingVerifier {
                        WorkingRow().id(Self.workingRowId)
                    }
                    // A lazy stack guesses the height of rows it has not drawn, so "at the
                    // end" is whether this last line is on screen, not a scroll offset.
                    Color.clear
                        .frame(height: 1)
                        .onScrollVisibilityChange(threshold: 0.01) { atBottom = $0 }
                        .id(Self.endRowId)
                }
                .scrollTargetLayout()
                .padding(.horizontal, Space.m)
                .padding(.vertical, Space.m)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .overlayScrollers()
            .scrollTargetBehavior(.viewAligned(limitBehavior: .never))
            .contentMargins(.top, Space.s, for: .scrollContent)
            // Opens at the newest message and follows it, but a short transcript sits at the
            // top: anchored to the bottom for alignment too, two events sat under a tall gap.
            .defaultScrollAnchor(.top, for: .alignment)
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(.bottom, for: .sizeChanges)
            // A scroll-edge fade: whatever row sits at the top edge dissolves into the
            // surface under the pinned card instead of showing as a cut-off half line.
            .overlay(alignment: .top) {
                LinearGradient(
                    colors: [theme.background, theme.background.opacity(0)],
                    startPoint: .top, endPoint: .bottom
                )
                .frame(height: Space.l)
                .allowsHitTesting(false)
            }
            // "Jump to latest" is for someone who scrolled away, never shown unasked.
            .onScrollPhaseChange { _, phase in
                if phase == .interacting { userScrolled = true }
            }
            .onChange(of: messages.count) {
                guard !items.isEmpty else { return }
                // The first batch always anchors: before it arrives the
                // empty list reads as "not at the bottom".
                guard atBottom || !anchored else { return }
                anchored = true
                atBottom = true
                // After the new row has laid out, or the scroll stops short of it.
                Task {
                    try? await Task.sleep(for: .milliseconds(50))
                    withAnimation { proxy.scrollTo(Self.endRowId, anchor: .bottom) }
                }
            }
            .onChange(of: awaitingVerifier) {
                guard atBottom, awaitingVerifier else { return }
                withAnimation { proxy.scrollTo(Self.workingRowId, anchor: .bottom) }
            }
            .onAppear {
                atBottom = true
                if !items.isEmpty {
                    anchored = true
                    proxy.scrollTo(Self.endRowId, anchor: .bottom)
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if !atBottom, anchored, userScrolled, items.count > 3 {
                    Button("↓ Jump to latest") {
                        atBottom = true
                        if !items.isEmpty {
                            withAnimation { proxy.scrollTo(Self.endRowId, anchor: .bottom) }
                        }
                    }
                    .buttonStyle(.quiet(small: true))
                    .padding(.bottom, Space.s)
                    .padding(.trailing, Space.l)
                    .help("Scroll to the newest message")
                    .transition(.opacity)
                }
            }
            .overlay {
                if messages.isEmpty {
                    QuietEmpty(title: "No conversation yet",
                               message: "The coding agent opens it with the task. You can message the verifier at any time.")
                }
            }
            .offersActions(.conversation, offered, refresh: runId) { perform($0, proxy: proxy) }
        }
    }

    // MARK: - Keys

    /// The cursor shows only while the conversation has the keyboard.
    private var cursorShown: Bool { keyboard?.pane == .conversation }

    /// Rows a person reads: a day's label is not one.
    private var stops: [TranscriptLayout.Item] {
        items.filter { if case .day = $0 { false } else { true } }
    }

    private var offered: Set<ActionID> {
        guard !stops.isEmpty else { return [] }
        var ids: Set<ActionID> = [.moveDown, .moveUp, .latest]
        if let cursor, let item = stops.first(where: { $0.id == cursor }), Self.step(of: item) != nil { ids.insert(.open) }
        return ids
    }

    /// The step a row points at: a tool call's, or the first a verdict cites.
    static func step(of item: TranscriptLayout.Item) -> Int? {
        switch item {
        case .toolCalls(let calls): return calls.last?.step
        case .message(let message, _) where message.kind == .verdict:
            return (message.evidence ?? []).lazy.compactMap { Evidence.parse($0).step }.first
        default: return nil
        }
    }

    private func perform(_ id: ActionID, proxy: ScrollViewProxy) {
        let stops = stops
        switch id {
        case .moveDown, .moveUp:
            let delta = id == .moveDown ? 1 : -1
            let at = cursor.flatMap { cursor in stops.firstIndex { $0.id == cursor } }
            // From nothing, down starts at the top and up at the newest.
            let next = at.map { min(max($0 + delta, 0), stops.count - 1) } ?? (delta > 0 ? 0 : stops.count - 1)
            cursor = stops[next].id
            withAnimation(.snappy(duration: 0.18)) { proxy.scrollTo(stops[next].id, anchor: .center) }
        case .open:
            guard let cursor, let item = stops.first(where: { $0.id == cursor }), let step = Self.step(of: item) else { return }
            store.requestSeek(runId: runId, step: step)
        case .latest:
            cursor = stops.last?.id
            atBottom = true
            withAnimation { proxy.scrollTo(Self.endRowId, anchor: .bottom) }
        default:
            break
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
            SectionLabel(title: Chrome.day(day))
                .frame(maxWidth: .infinity)
                .padding(.vertical, Space.xs)
        }
    }
}

// MARK: - Rows

/// A lifecycle event or a closed verdict, as one quiet mono line.
private struct EventLine: View {
    let message: Message
    let verdicts: [Message]
    let steps: [Step]

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.s) {
            Text(message.kind == .accept ? "✓" : "·")
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
/// saying who spoke a second way.
private struct MessageRow: View {
    let store: RunStore
    let runId: String
    let message: Message
    let showsSender: Bool

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xs) {
            if showsSender { header }
            bubble
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
    @Environment(\.theme) private var theme

    private var isLatest: Bool { store.verdict(runId)?.seq == message.seq }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            Button {
                withAnimation(.snappy(duration: 0.18)) { open.toggle() }
            } label: {
                HStack(spacing: Space.s) {
                    Text(open ? "▾" : "▸").foregroundStyle(.secondary)
                    Text("\(Chrome.outcomeGlyph(message.verdict)) \(isLatest ? "Verdict" : "Earlier verdict"): \(Chrome.outcomeTitle(message.verdict))")
                        .fontWeight(.semibold)
                        .foregroundStyle(isLatest ? theme.outcome(message.verdict) : theme.dim)
                    Text(isLatest ? "in full above" : "superseded")
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 0)
                }
                .monoStyle(size: TypeScale.monoSmall)
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
                            .readingStyle(size: TypeScale.small)
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
    }
}

/// The verifier asking the person something, answered in place.
private struct QuestionBubble: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var answer = ""
    @State private var sending = false
    @FocusState private var focused: Bool

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
                        .textFieldStyle(.plain)
                        .font(Typeface.readingRegular.font(size: TypeScale.readingSmall))
                        .focused($focused)
                        .padding(.horizontal, Space.s)
                        .frame(height: 28)
                        .fieldFrame(focused: focused, radius: Radius.sm)
                        .sendOnReturn(enabled: canSend, submit)
                        .typingField(focused: focused, sends: true)
                    Button("Reply", action: submit)
                        .buttonStyle(.primary)
                        .disabled(!canSend)
                }
            } else {
                Text("✓ Answered")
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
            }
        }
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

/// The verifier's tool calls in a row, one plain-words line each, in a quiet panel. A
/// line seeks the Screen to its step; its caret shows the raw call. Long runs fold to
/// the last few.
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
        VStack(alignment: .leading, spacing: 0) {
            if calls.count > Self.folded + 1 {
                Button {
                    withAnimation(.snappy(duration: 0.2)) { showsAll.toggle() }
                } label: {
                    HStack(spacing: Space.s) {
                        Text(showsAll ? "▾" : "▸")
                        Text(showsAll ? "Hide earlier tool calls" : "\(calls.count - Self.folded) earlier tool calls")
                    }
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, Space.s)
                    .padding(.vertical, Space.xs + 2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                Hairline()
            }
            ForEach(Array(visible.enumerated()), id: \.element.seq) { position, call in
                if position > 0 { Hairline() }
                ToolCallLine(store: store, runId: runId, message: call)
            }
        }
        .panel(radius: Radius.md)
    }
}

private struct ToolCallLine: View {
    let store: RunStore
    let runId: String
    let message: Message

    @State private var expanded = false
    @Environment(\.theme) private var theme

    private var step: Step? {
        guard let number = message.step else { return nil }
        return (store.steps[runId] ?? []).first { $0.seq == number }
    }

    /// The step's own words when the record is held; else the same rules applied to the
    /// tool call the message carries.
    private var phrase: String {
        if let step { return StepSummary.phrase(for: step, in: store.steps[runId] ?? []) }
        return StepSummary.phrase(ofProgress: message.text)
    }

    private var failed: Bool { step?.outcome.isFailure == true }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Space.s) {
                Button {
                    withAnimation(.snappy(duration: 0.18)) { expanded.toggle() }
                } label: {
                    Text(expanded ? "▾" : "▸")
                        .foregroundStyle(.secondary)
                        .frame(width: 14, height: 20)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(expanded ? "Hide the raw tool call" : "Show the raw tool call and what came back")

                Button {
                    if let number = message.step { store.requestSeek(runId: runId, step: number) }
                } label: {
                    HStack(spacing: Space.s) {
                        Text(failed ? "✗" : "✓")
                            .foregroundStyle(failed ? theme.color(.failure, on: .surface) : theme.dim(on: .surface))
                        Text(phrase)
                            .foregroundStyle(failed ? theme.color(.failure, on: .surface) : theme.foreground)
                            .lineLimit(1)
                            .truncationMode(.tail)
                        Spacer(minLength: Space.xs)
                        if let number = message.step {
                            Text("step \(number)")
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(message.step == nil)
                .help(message.step.map { "Show step \($0) on the screen" } ?? "")
            }
            .monoStyle(size: TypeScale.small)
            .padding(.horizontal, Space.s)
            .padding(.vertical, Space.xs + 2)
            .hoverHighlight(radius: Radius.sm)

            if expanded {
                Text(message.text)
                    .monoStyle(size: TypeScale.monoSmall)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, Space.s + 14 + Space.s)
                    .padding(.trailing, Space.s)
                    .padding(.bottom, Space.s)
            }
        }
    }
}

private struct WorkingRow: View {
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

// MARK: - Text

struct MessageText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            // By position: block text can repeat.
            ForEach(Array(RichText.blocks(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .heading(let title, let level):
                    // Never bigger than the reading text, only heavier and wider (ADR 0008).
                    Text(title)
                        .headingStyle(size: level <= 2 ? TypeScale.reading : TypeScale.readingSmall)
                        .textSelection(.enabled)
                        .padding(.top, Space.xs)
                case .code(let body):
                    // Wrapped, not scrolled: a column this narrow would show a scroller per block.
                    Text(body)
                        .monoStyle(size: TypeScale.small)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(Space.s)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .panel(radius: Radius.sm)
                case .prose(let body):
                    Text(inline(body))
                        .readingStyle()
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    /// Preserving whitespace keeps the agent's line breaks, and so its lists. Inline code
    /// sets in the mono face, bold in the reading face's semibold.
    private func inline(_ body: String) -> AttributedString {
        var text = (try? AttributedString(
            markdown: body,
            options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(body)
        for run in text.runs {
            guard let intent = run.inlinePresentationIntent else { continue }
            if intent.contains(.code) {
                text[run.range].font = Typeface.monoRegular.font(size: TypeScale.readingSmall)
            } else if intent.contains(.stronglyEmphasized) {
                text[run.range].font = Typeface.readingSemiBold.font(size: TypeScale.reading)
            }
        }
        return text
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
    /// Where the cursor is, so Shift-Return breaks the line there.
    @State private var selection: TextSelection?
    @State private var kind: MessageKind = .note
    @State private var sending = false
    @FocusState private var focused: Bool

    private var canSend: Bool { !sending && !offline && !isBlank(draft) }

    @AppStorage("composerFocusRequest") private var focusRequest = 0

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            SegmentedSwitch(options: [(MessageKind.note, "Message"), (MessageKind.task, "New task")],
                            selection: $kind, small: true)
                .help("A message is answered by the verifier and read by the coding agent; a new task hands the verifier more to check")
            HStack(alignment: .bottom, spacing: Space.s) {
                TextField(placeholder, text: $draft, selection: $selection, axis: .vertical)
                    .disabled(offline)
                    .textFieldStyle(.plain)
                    .font(Typeface.readingRegular.font(size: TypeScale.reading))
                    .lineLimit(1...6)
                    .focused($focused)
                    .sendOnReturn(enabled: canSend, send) {
                        // While the field is being edited its field editor owns the text: a
                        // newline written into the binding is overwritten by the editor's own
                        // copy (seen in the app, #65), so the break goes in through the editor.
                        if let editor = NSApp.keyWindow?.firstResponder as? NSTextView {
                            editor.insertNewlineIgnoringFieldEditor(nil)
                        } else {
                            (draft, selection) = insertingNewline(into: draft, at: selection)
                        }
                    }
                    .padding(.vertical, Space.xs)
                    .typingField(focused: focused, sends: true)

                Button("Send", action: send)
                    .buttonStyle(.primary(small: true))
                    .disabled(!canSend)
                    .help("Send (\(ActionRegistry.label(.send)))")
            }
            .padding(.leading, Space.m)
            .padding(.trailing, Space.xs)
            .padding(.vertical, Space.xs)
            .fieldFrame(focused: focused)
            // Who reads it and when: under the field, the whole sentence, never clipped.
            Text(hint)
                .readingStyle(size: TypeScale.small)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
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
        if offline { return "The daemon is not answering" }
        // Short enough to stay on one line: a vertical field sizes to its wrapped placeholder.
        return kind == .task ? "What should the verifier check?" : "Message the verifier"
    }

    /// Who reads it and when, for the state the run is in.
    private var hint: String {
        if !facts.verifierListens { return "The verifier stopped when the machine was destroyed; nothing will answer" }
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
