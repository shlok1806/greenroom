import SwiftUI

/// The inspector column at the right of the player (redesign 7): three tabs, Checks (the
/// default: the checklist, the first failure selected), Activity (the verifier's work as task
/// rows, or every step as a log) and Message (the conversation and the composer). It never
/// replaces the player: the picture shrinks to the room left. Selecting a check or a step
/// seeks the player; while the recording plays, the step under the playhead is the selected
/// row and scrolls into view.
struct InspectorColumn: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 2) {
                ForEach(InspectorTab.allCases, id: \.self) { tab in
                    InspectorTabButton(title: tab.title, badge: nil, on: shell.inspectorTab == tab) {
                        shell.show(tab)
                    }
                    .cloneScope("Tab[\(InspectorTab.allCases.firstIndex(of: tab) ?? 0)]")
                    .help(help(tab))
                    .accessibilityIdentifier("inspector.\(tab.rawValue)")
                }
                Spacer(minLength: Gap.x4)
                IconButton(icon: .sidebar, name: "Hide the inspector (Z)") { shell.toggleZoom() }
                    .cloneScope("Icon button")
            }
            .padding(.leading, Gap.x12)
            .padding(.trailing, Gap.x8)
            .frame(height: 44)
            .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
            .cloneScope("Tabs")

            switch shell.inspectorTab {
            case .checks:
                ChecksColumn(shell: shell, summary: summary).cloneScope("Checks")
            case .activity:
                ActivitySteps(shell: shell, summary: summary)
            case .message:
                ConversationTab(shell: shell, summary: summary)
            }
        }
        .background(Palette.bg)
    }

    /// A figure beside a tab: the failed checks, the steps, the messages.
    private func badge(_ tab: InspectorTab) -> String? {
        switch tab {
        case .checks:
            let failed = summary.checks.items.filter { $0.state == .fail }.count
            return failed > 0 ? "\(failed)" : nil
        case .activity:
            let count = (shell.store.steps[summary.runId] ?? []).count
            return count > 0 ? "\(count)" : nil
        case .message:
            return nil
        }
    }

    private func help(_ tab: InspectorTab) -> String {
        switch tab {
        case .checks:
            let items = summary.checks.items
            let failed = items.filter { $0.state == .fail }.count
            return "Checks: \(items.count) in all, \(failed) failed (J and K move between them)"
        case .activity:
            return "Activity: \((shell.store.steps[summary.runId] ?? []).count) steps the verifier took (A)"
        case .message:
            return "The conversation, and a message or a new task for the verifier (M)"
        }
    }
}

/// A tab: its title, a figure, and a 2 pt rule under it while it shows.
struct InspectorTabButton: View {
    var title: String
    var badge: String?
    var on: Bool
    var action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Text(title).textStyle(on ? .bodyEmphasis : .body).foregroundStyle(on ? Palette.text : Palette.textSecondary)
                    .clonePart("Text")
                if let badge {
                    Text(badge).textStyle(.caption).monospacedDigit().foregroundStyle(Palette.textSecondary)
                }
            }
            .padding(.horizontal, Gap.x8)
            .frame(height: 30)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(hovering && !on ? Palette.bgHover : .clear))
            .overlay(alignment: .bottom) {
                if on { Rectangle().fill(Palette.text).frame(height: 2).offset(y: 7) }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(on ? [.isSelected, .isButton] : .isButton)
    }
}

/// Activity: the verifier's work as task rows grouped by check, or every step as a log line;
/// linked to the player both ways.
struct ActivitySteps: View {
    @Bindable var shell: ShellModel
    var summary: Summary
    @AppStorage("activityLogs", store: AppDefaults.shared) private var logs = false
    @AppStorage("activityFailuresOnly", store: AppDefaults.shared) private var failuresOnly = false
    @State private var open: [String: Bool] = [:]
    /// Rows and tool calls that arrive while Activity shows fade up (Beautiful UI).
    @State private var rowArrivals = Arrivals()
    @State private var chipArrivals = Arrivals()

    private var steps: [Step] { shell.store.steps[summary.runId] ?? [] }

    var body: some View {
        let t = shell.timeline()
        let currentStep = shell.currentStep(t)
        let sections = ActivityLayout.sections(messages: shell.store.messages[summary.runId] ?? [], steps: steps,
                                               checks: summary.checks.items, working: summary.state == .checking)
        let shownSections = failuresOnly ? sections.compactMap { section -> ActivityLayout.Section? in
            let rows = section.rows.filter { $0.glyph == .failed }
            return rows.isEmpty ? nil : ActivityLayout.Section(title: section.title, rows: rows)
        } : sections
        let currentRow = logs ? currentStep.map { "l\($0)" } : ActivityLayout.row(holding: currentStep, in: shownSections)
        let failures = steps.filter(\.failed).count
        let rowIDs = sections.flatMap(\.rows).map(\.id)
        let seqs = steps.map(\.seq)
        VStack(spacing: 0) {
            header(failures: failures)
            list(shownSections, current: currentRow, step: currentStep)
            // The raw record of the step at the playhead, one click away.
            if let seq = currentStep, let step = steps.first(where: { $0.seq == seq }) {
                StepRecordFold(step: step, total: steps.count)
            }
        }
        .onAppear { resetArrivals(sections) }
        .onChange(of: summary.runId) { _, _ in resetArrivals(sections) }
        .onChange(of: rowIDs) { _, ids in rowArrivals.note(ids) }
        .onChange(of: seqs) { _, seqs in chipArrivals.note(seqs.map(String.init)) }
    }

    private func header(failures: Int) -> some View {
        HStack(spacing: Gap.x8) {
            SegmentedControl(options: [(false, "Tasks"), (true, "Log")], selection: $logs)
                .help("Tasks: the verifier's work grouped by check. Log: every step, one line each")
            Spacer()
            failuresToggle(failures)
        }
        .padding(.horizontal, Gap.x12)
        .padding(.vertical, 6)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }

    private func failuresToggle(_ failures: Int) -> some View {
        Button { failuresOnly.toggle() } label: {
            HStack(spacing: 4) {
                StatusGlyph(kind: .failed, color: failuresOnly ? .fail : .tertiary, size: 12)
                Text("\(failures)").textStyle(.captionEmphasis).monospacedDigit()
                    .foregroundStyle(failuresOnly ? Palette.fail : Palette.textSecondary)
            }
            .padding(.horizontal, 8)
            .frame(height: 24)
            .background(RoundedRectangle(cornerRadius: Corner.control).fill(failuresOnly ? Palette.failSubtle : .clear))
            .overlay(RoundedRectangle(cornerRadius: Corner.control).strokeBorder(failuresOnly ? .clear : Palette.border, lineWidth: 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(failuresOnly ? "Showing only the \(failures) failed steps. Show all" : "Show only the \(failures) failed steps")
        .accessibilityLabel(failuresOnly ? "Only failed steps, on" : "Only failed steps, off")
        .accessibilityIdentifier("activity.failuresOnly")
    }

    private func scrollerLabel(_ metrics: ScrollMetrics, step: Int?) -> String? {
        let total = steps.count
        guard metrics.content > metrics.viewport * 1.2, total > 0 else { return nil }
        if let step { return "step \(step) of \(total)" }
        return "\(total) steps"
    }

    private func list(_ sections: [ActivityLayout.Section], current: String?, step: Int?) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                if logs {
                    logLines(current: step)
                } else {
                    taskRows(sections, current: current)
                }
            }
            .visibleScroller { metrics in scrollerLabel(metrics, step: step) }
            .onChange(of: current) { _, row in
                guard let row else { return }
                withAnimation(Motion.easeOut(Motion.settle)) { proxy.scrollTo(row) }
            }
            .onChange(of: steps.count) { _, _ in followLive(proxy) }
            .onAppear {
                if let current { proxy.scrollTo(current, anchor: .center) }
            }
        }
    }

    private func followLive(_ proxy: ScrollViewProxy) {
        if shell.playhead == nil, shell.showsLive { proxy.scrollTo("end", anchor: .bottom) }
    }

    private func resetArrivals(_ sections: [ActivityLayout.Section]) {
        rowArrivals.reset(sections.flatMap(\.rows).map(\.id))
        chipArrivals.reset(steps.map { String($0.seq) })
    }

    private func taskRows(_ sections: [ActivityLayout.Section], current: String?) -> some View {
        LazyVStack(alignment: .leading, spacing: 0) {
            if sections.isEmpty {
                Text(failuresOnly ? "No step failed." : "Nothing yet. The verifier's steps show here as it works.")
                    .textStyle(.body).foregroundStyle(Palette.textSecondary).padding(Gap.x24)
            }
            ForEach(sections) { section in
                Text(AgentMarkdown.inline(section.title)).textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                    .lineLimit(2)
                    .padding(.horizontal, Gap.x16)
                    .padding(.top, Gap.x16)
                    .padding(.bottom, Gap.x4)
                    .accessibilityAddTraits(.isHeader)
                ForEach(section.rows) { row in
                    TaskRowView(row: row, expanded: Binding(
                        get: { open[row.id] ?? row.opensItself },
                        set: { open[row.id] = $0 }),
                        current: row.id == current,
                        onSelect: { if let first = row.steps.first { shell.seek(toStep: first) } },
                        onChip: { shell.seek(toStep: $0) },
                        chipHelp: chipHelp,
                        chipEntrance: { seq in
                            let id = String(seq)
                            return (chipArrivals.isNew(id), chipArrivals.delay(id, stagger: AgentMotion.taskRowStagger))
                        })
                        .padding(.horizontal, Gap.x8)
                        .fadeUp(rowArrivals.isNew(row.id), duration: AgentMotion.taskRow,
                                delay: rowArrivals.delay(row.id, stagger: AgentMotion.taskRowStagger))
                        .id(row.id)
                }
            }
            if summary.state == .checking {
                ThinkingView(live: true).padding(.horizontal, Gap.x16).padding(.vertical, Gap.x8)
            }
            Color.clear.frame(height: 1).id("end")
        }
        .padding(.trailing, Gap.x8)
        .padding(.bottom, Gap.x16)
    }

    private func logLines(current: Int?) -> some View {
        let shown = failuresOnly ? steps.filter(\.failed) : steps
        return LazyVStack(alignment: .leading, spacing: 0) {
            if shown.isEmpty {
                Text(failuresOnly ? "No step failed." : "No steps yet.")
                    .textStyle(.body).foregroundStyle(Palette.textSecondary).padding(Gap.x24)
            }
            ForEach(shown) { step in
                Button { shell.seek(toStep: step.seq) } label: {
                    HStack(alignment: .firstTextBaseline, spacing: Gap.x8) {
                        Text("\(step.seq)").font(.system(size: 11, design: .monospaced)).foregroundStyle(Palette.textSecondary)
                            .frame(width: 30, alignment: .trailing)
                        Text(StepSummary.phrase(for: step, in: steps) + (step.failure.map { ", failed: \($0)" } ?? ""))
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(step.failed ? Palette.fail : Palette.text)
                            .lineLimit(2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Text(String(format: "%.1fs", Double(step.durationMs) / 1000))
                            .font(.system(size: 11, design: .monospaced)).foregroundStyle(Palette.textSecondary)
                    }
                    .padding(.horizontal, Gap.x8)
                    .padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: Corner.control).fill(step.seq == current ? Palette.bgSelected : .clear))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(chipHelp(step.seq))
                .id("l\(step.seq)")
            }
            Color.clear.frame(height: 1).id("end")
        }
        .padding(.horizontal, Gap.x8)
        .padding(.vertical, Gap.x8)
        .padding(.trailing, Gap.x8)
    }

    private func chipHelp(_ seq: Int) -> String { StepHelp.text(seq, in: steps) }
}

/// A step's words on hover: tool, time, how long, the error in full.
enum StepHelp {
    static func text(_ seq: Int, in steps: [Step]) -> String {
        guard let step = steps.first(where: { $0.seq == seq }) else { return "Step \(seq)" }
        var words = "Step \(seq): \(StepSummary.phrase(for: step, in: steps)). \(ToolCatalog.entry(for: step.tool).title), "
            + "\(step.at.formatted(date: .omitted, time: .standard)), \(String(format: "%.1f s", Double(step.durationMs) / 1000))"
        if let failure = step.failure { words += ". Failed: \(failure)" }
        if step.isRisky { words += ". A command that can destroy data or change the Mac for good" }
        return words + ". Click to show it on the player; its raw call opens at the foot of Activity."
    }
}

/// The conversation, oldest first, and the composer (a message, a new task, an answer or a
/// rejection's reason). The old window's transcript, in the redesign's type.
struct ConversationTab: View {
    @Bindable var shell: ShellModel
    var summary: Summary

    private var messages: [Message] {
        (shell.store.messages[summary.runId] ?? []).filter { message in
            message.kind != .progress && !message.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }

    private var steps: [Step] { shell.store.steps[summary.runId] ?? [] }

    private var items: [ConversationLayout.Item] {
        ConversationLayout.items(messages: messages, steps: steps, working: summary.state == .checking)
    }

    /// Messages, groups and calls that arrive while the tab shows fade up.
    @State private var messageArrivals = Arrivals()
    @State private var chipArrivals = Arrivals()
    /// Bumped as the newest message streams taller, to keep it in view.
    @State private var streamGrowth = 0

    private func resetArrivals() {
        messageArrivals.reset(items.map(\.id))
        chipArrivals.reset(steps.map { String($0.seq) })
    }

    var body: some View {
        let ids = items.map(\.id)
        let seqs = steps.map(\.seq)
        VStack(spacing: 0) {
            conversation
                .onAppear { resetArrivals() }
                .onChange(of: summary.runId) { _, _ in resetArrivals() }
                .onChange(of: ids) { _, ids in messageArrivals.note(ids) }
                .onChange(of: seqs) { _, seqs in chipArrivals.note(seqs.map(String.init)) }
            composer
        }
    }

    private var conversation: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Gap.x12) {
                    if items.isEmpty {
                        Text("No messages yet.").textStyle(.body).foregroundStyle(Palette.textSecondary)
                    }
                    ForEach(items) { item in row(item) }
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(Gap.x16)
                .padding(.trailing, Gap.x8)
            }
            .defaultScrollAnchor(.bottom)
            .visibleScroller { metrics in scrollerLabel(metrics) }
            .onChange(of: messages.count) { _, _ in
                shell.noteMessages(messages, runId: summary.runId)
                proxy.scrollTo("end", anchor: .bottom)
            }
            .onChange(of: steps.count) { _, _ in followLive(proxy) }
            .onChange(of: streamGrowth) { _, _ in proxy.scrollTo("end", anchor: .bottom) }
            .onAppear { shell.noteMessages(messages, runId: summary.runId) }
        }
    }

    private func scrollerLabel(_ metrics: ScrollMetrics) -> String? {
        metrics.content > metrics.viewport * 1.2 ? "\(messages.count) messages" : nil
    }

    private func followLive(_ proxy: ScrollViewProxy) {
        if shell.playhead == nil { proxy.scrollTo("end", anchor: .bottom) }
    }

    @ViewBuilder
    private func row(_ item: ConversationLayout.Item) -> some View {
        let isNew = messageArrivals.isNew(item.id)
        switch item {
        case .message(let message):
            let streaming = shell.streamStart(runId: summary.runId, seq: message.seq) != nil
            messageBlock(message).fadeUp(isNew, duration: AgentMotion.toolChip).id(message.seq)
                // A message streaming in grows line by line: the conversation follows it down.
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { _ in
                    if streaming, message.seq == messages.last?.seq { streamGrowth += 1 }
                }
        case .tools(_, let group, let live):
            // Beautiful UI's Thinking state; its trace, the calls as tool chips.
            trace(group, live: live).fadeUp(isNew, duration: AgentMotion.toolChip).id(item.id)
        }
    }

    private func messageBlock(_ message: Message) -> MessageBlock {
        let runId = summary.runId
        let known = Set(steps.map(\.seq))
        return MessageBlock(message: message, start: shell.streamStart(runId: runId, seq: message.seq), steps: known,
                            onStep: { shell.seek(toStep: $0) },
                            finished: { shell.finishStream(runId: runId, seq: message.seq) })
    }

    private func trace(_ group: [Step], live: Bool) -> ToolTraceView {
        let all = steps
        return ToolTraceView(steps: group, allSteps: all, live: live, now: summary.now ?? "Thinking", arrivals: chipArrivals,
                             onChip: { shell.seek(toStep: $0) }, chipHelp: { StepHelp.text($0, in: all) })
    }

    @ViewBuilder
    private var composer: some View {
        let mode = shell.composer ?? .message
        VStack(alignment: .leading, spacing: Gap.x8) {
            if mode == .message || mode == .task {
                SegmentedControl(options: [(ComposerMode.message, "Message"), (ComposerMode.task, "New task")],
                                 selection: Binding(get: { mode }, set: { shell.openComposer($0) }))
                .disabled(!summary.machine.isUp)
                .help("A message is a note the verifier answers; a new task starts it on new work")
            } else {
                HStack {
                    Text(mode == .reject ? "Reject the verdict" : (mode == .answer ? "Answer the question" : "Ask for a re-check"))
                        .textStyle(.captionEmphasis).foregroundStyle(Palette.textSecondary)
                    Spacer()
                    Button("Cancel") { shell.openComposer(.message) }.buttonStyle(ActionButtonStyle(kind: .plain))
                }
            }
            ComposerView(text: $shell.composerDraft, placeholder: mode.placeholder, sendTitle: mode.send, sending: shell.sending,
                         disabledReason: disabledReason(mode), focusRequest: shell.composerFocus, send: {
                             if shell.composer == nil { shell.composer = mode }
                             shell.sendComposer()
                         })
        }
        .padding(Gap.x12)
        .overlay(alignment: .top) { Rectangle().fill(Palette.border).frame(height: 1) }
    }

    private func disabledReason(_ mode: ComposerMode) -> String? {
        if mode == .recheck, let verdict = shell.store.verdict(summary.runId),
           let newer = VerdictReview.newerTask(than: verdict.seq, in: shell.store.messages[summary.runId] ?? []) {
            // A newer task makes this verdict stale: a re-check would answer the wrong task (#89).
            return VerdictReview.staleNote(newerTask: newer, verifierListens: summary.machine.isUp)
        }
        if mode == .message || mode == .task || mode == .answer, shell.store.connection != .online {
            return "Greenroom is not answering. Nothing will be sent."
        }
        guard mode == .message || mode == .answer || mode == .task else { return nil }
        if summary.state == .paused, mode == .message, summary.primaryAction?.id == SummaryAction.continue {
            return nil
        }
        return summary.machine.isUp ? nil : "The verifier stopped with the Mac. Nothing will answer."
    }
}

/// One message: who, when and what (Markdown), with a 2 pt edge in the sender's colour. A
/// message that arrived while the conversation was open streams in once.
struct MessageBlock: View {
    var message: Message
    var start: Date?
    var steps: Set<Int>?
    var onStep: ((Int) -> Void)?
    var finished: () -> Void = {}

    var body: some View {
        HStack(alignment: .top, spacing: Gap.x8) {
            RoundedRectangle(cornerRadius: 1).fill(edge).frame(width: 2)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(sender).textStyle(.captionEmphasis).foregroundStyle(Palette.text)
                    Text(kind).textStyle(.caption).foregroundStyle(Palette.textSecondary)
                    Spacer(minLength: 0)
                    // Today's messages say the time; older ones the day too. The whole stamp on hover.
                    Text(Calendar.current.isDateInToday(message.at)
                         ? message.at.formatted(date: .omitted, time: .shortened)
                         : message.at.formatted(.dateTime.month(.abbreviated).day().hour().minute()))
                        .textStyle(.caption).foregroundStyle(Palette.textSecondary)
                        .help("\(message.at.formatted(date: .complete, time: .standard)), message \(message.seq)")
                }
                StreamingMarkdown(text: message.text, start: start, steps: steps, onStep: onStep, finished: finished)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var sender: String {
        switch message.from {
        case .coder: "Coding agent"
        case .human: "You"
        case .verifier: "Verifier"
        case .system: "Greenroom"
        case .unknown(let name): name
        }
    }

    private var kind: String {
        switch message.kind {
        case .task: "task"
        case .question: "question"
        case .verdict: message.verdict.map { "verdict: \($0)" } ?? "verdict"
        case .accept: "accepted"
        case .dispute: "rejected"
        case .answer: "answer"
        default: ""
        }
    }

    private var edge: Color {
        switch message.from {
        case .human: Palette.accent
        case .verifier: Palette.text
        default: Palette.border
        }
    }
}
