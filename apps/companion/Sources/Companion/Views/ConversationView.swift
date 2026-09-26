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
    /// On screen. A hidden conversation stays in the tree (its draft lives on) but holds
    /// no verdict card, whose reason field would take the keyboard out of sight.
    var visible = true
    /// Has the keyboard: its label takes the brand.
    var focused = false

    @State private var atBottom = true
    /// Whether the newest message has been scrolled to at least once.
    @State private var anchored = false
    @State private var userScrolled = false
    @AppStorage("showsToolCalls", store: AppDefaults.shared) private var showsToolCalls = true
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
                .readingMeasure()
            if visible {
                VerdictCard(store: store, runId: runId, facts: store.facts(runId),
                            maxHeight: RunLayout.verdictCardMaximum(column: columnHeight))
                    .id(VerdictCard.identity(runId: runId, verdict: store.verdict(runId)))
                    .padding(.horizontal, Space.m)
                    .padding(.bottom, Space.m)
                    .readingMeasure()
                    // Before the transcript, which scrolls in whatever is left.
                    .layoutPriority(1)
            }
            Hairline()
            transcript
            Hairline()
            Composer(store: store, runId: runId, visible: visible) { atBottom = true }
                .readingMeasure()
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
                .foregroundStyle(focused ? theme.brandInk(on: .background) : theme.foreground)
                .lineLimit(1)
                .fixedSize()
            let count = store.facts(runId).messageCount
            if count > 0 {
                Text(count == 1 ? "1 message" : "\(Chrome.count(count)) messages")
                    .monoStyle(size: TypeScale.monoSmall)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            Spacer(minLength: Space.s)
            Toggle("Tool calls", isOn: $showsToolCalls)
                .fixedSize()
                .help("Show the verifier's tool calls")
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
                .readingMeasure()
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
                               message: "The coding agent starts it with the task. You can message the verifier any time.")
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
            return message.citedSteps.first
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
        case .plan(let message):
            CheckPlanBlock(message: message)
        case .event(let message):
            EventLine(message: message, verdicts: messages.filter { $0.kind == .verdict })
        case .day(let day, _):
            SectionLabel(title: Chrome.day(day))
                .frame(maxWidth: .infinity)
                .padding(.vertical, Space.xs)
        }
    }
}

// MARK: - Composer

extension View {
    /// Zoomed to the window (or wider than a column ever is), the conversation keeps a
    /// reading measure, centred, rather than lines a window wide; its rules still run
    /// edge to edge.
    fileprivate func readingMeasure() -> some View {
        frame(maxWidth: DesignData.shared.tokens.layout.readingMaxWidth)
            .frame(maxWidth: .infinity)
    }
}

/// The human seat: a note (the verifier reads it and answers) or a task (new work).
/// The draft stays in the field until the daemon has taken it.
private struct Composer: View {
    let store: RunStore
    let runId: String
    /// A hidden composer never takes the keyboard: the typing would be out of sight.
    var visible = true
    let didSend: () -> Void

    @State private var draft = ""
    /// Where the cursor is, so Shift-Return breaks the line there.
    @State private var selection: TextSelection?
    @State private var kind: MessageKind = .note
    @State private var sending = false
    @FocusState private var focused: Bool

    private var canSend: Bool { !sending && !offline && !isBlank(draft) }

    @AppStorage("composerFocusRequest", store: AppDefaults.shared) private var focusRequest = 0

    @Environment(\.theme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            SegmentedSwitch(options: [(MessageKind.note, "Message"), (MessageKind.task, "New task")],
                            selection: $kind, small: true)
                .help("A message gets an answer from the verifier. A new task gives it more to check.")
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
        .onChange(of: focusRequest) { if visible { focused = true } }
    }

    private var facts: RunFacts { store.facts(runId) }
    private var offline: Bool {
        if case .offline = store.connection { return true }
        return false
    }

    private var placeholder: String {
        if offline { return "greenroom is not answering" }
        // Short enough to stay on one line: a vertical field sizes to its wrapped placeholder.
        return kind == .task ? "What should the verifier check?" : "Message the verifier"
    }

    /// Who reads it and when, for the state the run is in.
    private var hint: String {
        if !facts.verifierListens { return "The verifier stopped with the machine. Nothing will answer." }
        if kind == .task { return "The verifier checks it and reports here" }
        switch facts.phase {
        case .booting: return "The verifier answers once the machine is up"
        case .idle: return "The verifier answers now. The coding agent reads it on its next check."
        case .ended, .failed: return "The machine is gone. The verifier answers from the record."
        case .live: return "The verifier answers. The coding agent reads it too."
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
