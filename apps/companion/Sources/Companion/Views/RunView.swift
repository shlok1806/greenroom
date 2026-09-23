import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// What the run's main column shows. The conversation sits beside either.
enum StagePane: String, CaseIterable, Identifiable {
    case screen
    case steps

    var id: String { rawValue }

    var title: String {
        switch self {
        case .screen: "Screen"
        case .steps: "Steps"
        }
    }
}

/// What the menu bar can do to the open run (companion ADR 0001).
struct RunCommands {
    var pane: Binding<StagePane>
    var showsConversation: Binding<Bool>
    var capture: (() -> Void)?
    var export: (() -> Void)?
    var destroy: (() -> Void)?
    var control: (() -> Void)?
    var controlTitle = "Take Control"
    var nextFailure: (() -> Void)?
    var previousFailure: (() -> Void)?
}

extension FocusedValues {
    @Entry var runCommands: RunCommands?
}

/// One run: a header naming it with its state in one line, the stage (Screen or Steps),
/// and the conversation in a trailing column.
struct RunView: View {
    let store: RunStore
    let runId: String
    @Binding var pane: StagePane
    @Binding var showsConversation: Bool
    /// Folds the sidebar, for a conversation asked for where it does not fit beside the stage.
    var makeRoom: () -> Void = {}

    @State private var confirmingDestroy = false
    @State private var capturing = false
    @State private var savingRecording = false
    @State private var failureCursor: Int?
    @AppStorage("conversationWidth") private var conversationWidth = RunLayout.conversationIdeal
    @State private var detailSize: CGSize = .zero

    private var facts: RunFacts { store.facts(runId) }
    private var pilot: ControlPilot { store.pilot(for: runId) }
    private var driving: Bool { pilot.active }

    private var canCapture: Bool { facts.machineReady && !capturing }
    private var canExport: Bool { !(store.frames[runId] ?? []).isEmpty && !savingRecording }
    private var canDestroy: Bool { store.details[runId]?.machine != nil }

    /// The conversation's width beside the stage; nil when the window is too narrow for both.
    private var conversationFit: Double? {
        RunLayout.conversation(conversationWidth, in: detailSize.width)
    }

    /// Whether the conversation is beside the stage: asked for, and room for it.
    private var conversationShown: Bool { showsConversation && conversationFit != nil }

    /// What the toolbar and the menu toggle. Asking for a conversation that has no room
    /// folds the sidebar to make it.
    private var conversationToggle: Binding<Bool> {
        Binding(get: { conversationShown }, set: { show in
            showsConversation = show
            if show, conversationFit == nil { makeRoom() }
        })
    }

    var body: some View {
        // A hand-made split, not `.inspector` or `HSplitView`: inside a split view both
        // add hundreds of points to the window's minimum width, so it could not shrink.
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                RunHeader(
                    store: store,
                    runId: runId,
                    facts: facts,
                    failureCursor: failureCursor,
                    showFailure: showFailure
                )
                Divider()
                // Hidden conversation: the verdict must still be read, not shrink to a chip.
                if !conversationShown {
                    VerdictCard(store: store, runId: runId, facts: facts, compact: true,
                                maxHeight: RunLayout.verdictCardMaximum(column: detailSize.height))
                        .id(VerdictCard.identity(runId: runId, verdict: facts.verdict))
                        .padding(.horizontal, Space.l)
                        .padding(.top, Space.m)
                }
                stage
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            // No hard minimum: one would add to the window's own, and showing the sidebar
            // in a narrow window would then widen the window past the screen (issue #64).
            // `conversationFit` keeps the stage at `stageMinimum` or takes the conversation away.
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            if conversationShown, let width = conversationFit {
                ColumnDivider(width: $conversationWidth)
                // `.id(runId)` rebuilds per run: message seqs restart at 1, so a half-typed
                // draft, answer or dispute would otherwise post into the next run.
                ConversationView(store: store, runId: runId)
                    .id(runId)
                    // First pick: the stage keeps its minimum through `conversationFit`.
                    .frame(minWidth: RunLayout.conversationMinimum, idealWidth: width, maxWidth: width)
                    .frame(maxHeight: .infinity)
                    .layoutPriority(1)
                    .transition(.move(edge: .trailing))
            }
        }
        .onGeometryChange(for: CGSize.self) { $0.size } action: { detailSize = $0 }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .navigationTitle(RunTitle.short(task: store.run(runId)?.task, runId: runId))
        // The header names the run; the toolbar title would say it twice.
        .toolbar(removing: .title)
        .toolbar { toolbar }
        .confirmationDialog(
            "Destroy this machine?",
            isPresented: $confirmingDestroy,
            titleVisibility: .visible
        ) {
            Button("Destroy Machine", role: .destructive) {
                Task { await store.destroy(runId: runId) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The machine is deleted and the run ends. The coding agent is told in the conversation.")
        }
        .task(id: runId) {
            failureCursor = nil
            await store.select(runId)
        }
        .onChange(of: store.seekRequest) {
            guard let request = store.seekRequest, request.runId == runId else { return }
            pane = request.inSteps ? .steps : .screen
        }
        .focusedSceneValue(\.runCommands, RunCommands(
            pane: $pane,
            showsConversation: conversationToggle,
            capture: canCapture ? { Task { await capture() } } : nil,
            export: canExport ? { Task { await saveRecording() } } : nil,
            destroy: canDestroy ? { confirmingDestroy = true } : nil,
            control: facts.machineReady && !pilot.busy ? { toggleControl() } : nil,
            controlTitle: driving ? "Give Back Control" : "Take Control",
            nextFailure: facts.failures.isEmpty ? nil : { showFailure(1) },
            previousFailure: facts.failures.isEmpty ? nil : { showFailure(-1) }
        ))
    }

    @ViewBuilder
    private var stage: some View {
        switch pane {
        case .screen:
            // Resets itself by hand, since it has a lease to give back first.
            ScreenView(store: store, runId: runId)
        case .steps:
            StepsView(store: store, runId: runId)
                .id(runId)
        }
    }

    /// Cycles the failed steps, opening each in Steps.
    private func showFailure(_ delta: Int) {
        let failures = facts.failures
        guard !failures.isEmpty else { return }
        let next: Int
        if let cursor = failureCursor, let at = failures.firstIndex(of: cursor) {
            next = (at + delta + failures.count) % failures.count
        } else {
            next = delta > 0 ? 0 : failures.count - 1
        }
        failureCursor = failures[next]
        store.requestSeek(runId: runId, step: failures[next], inSteps: true)
    }

    /// Taking control needs the Screen: switch to it first, then take the lease.
    private func toggleControl() {
        if driving {
            Task { await pilot.release() }
        } else {
            pane = .screen
            Task { await pilot.take() }
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .principal) {
            Picker("View", selection: $pane) {
                ForEach(StagePane.allCases) { item in
                    Text(item.title).tag(item)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help("Show the screen (\(Keys.screen)) or the steps (\(Keys.steps))")
        }
        // Only what applies to this run is offered; a finished run has no machine to
        // capture, drive or destroy.
        // Words beside every icon: a camera, a film strip and a sidebar do not explain
        // themselves. Only what applies to this run is offered.
        ToolbarItemGroup(placement: .primaryAction) {
            if facts.machineReady {
                Button {
                    Task { await capture() }
                } label: {
                    Label("Screenshot", systemImage: "camera")
                        .labelStyle(.titleAndIcon)
                }
                .disabled(!canCapture)
                .help("Capture the machine's screen now (\(Keys.capture)). It lands in the run as a step.")
            }
            if !(store.frames[runId] ?? []).isEmpty {
                Button {
                    Task { await saveRecording() }
                } label: {
                    if savingRecording {
                        ProgressView().controlSize(.small)
                    } else {
                        Label("Export", systemImage: "square.and.arrow.down")
                            .labelStyle(.titleAndIcon)
                    }
                }
                .disabled(!canExport)
                .help("Save the run's recording as a movie (\(Keys.export))")
            }
        }
        if facts.machineReady {
            // The one way to take and give back the screen.
            ToolbarItem(placement: .primaryAction) {
                ControlButton(driving: driving, busy: pilot.busy, action: toggleControl)
            }
        }
        if canDestroy {
            ToolbarItem(placement: .primaryAction) {
                Button(role: .destructive) {
                    confirmingDestroy = true
                } label: {
                    Label("Destroy...", systemImage: "trash")
                        .labelStyle(.titleAndIcon)
                        .foregroundStyle(Palette.failure)
                }
                .help("Destroy the machine and end the run (\(Keys.destroy)). Asks first.")
            }
        }
        ToolbarItem(placement: .primaryAction) {
            Button {
                withAnimation(.snappy(duration: 0.2)) { conversationToggle.wrappedValue.toggle() }
            } label: {
                Label("Conversation", systemImage: "sidebar.trailing")
                    .labelStyle(.titleAndIcon)
            }
            .help("\(conversationShown ? "Hide" : "Show") the conversation (\(Keys.conversation))")
        }
    }

    private func capture() async {
        capturing = true
        await store.screenshot(runId: runId)
        capturing = false
    }

    private func saveRecording() async {
        savingRecording = true
        let data = await store.recording(runId: runId)
        savingRecording = false
        guard let data else { return }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(runId).mp4"
        panel.allowedContentTypes = [.mpeg4Movie]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try data.write(to: url)
        } catch {
            store.lastError = error.localizedDescription
        }
    }
}

/// "Take Control" and, while driving, "Give Back": a button with words, never a switch
/// whose on state reads as off.
struct ControlButton: View {
    let driving: Bool
    let busy: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label(driving ? "Give Back" : "Take Control", systemImage: driving ? "hand.raised.fill" : "cursorarrow.click.2")
                .labelStyle(.titleAndIcon)
        }
        .buttonStyle(.bordered)
        .tint(driving ? Palette.driving : nil)
        .disabled(busy)
        .help(driving
            ? "Give the mouse and keyboard back to the agents"
            : "Drive the machine with this window's mouse and keyboard (\(Keys.control)). The conversation records it.")
    }
}

// MARK: - Layout

enum RunLayout {
    /// The design spec's window: least and default size.
    static let windowMinimum = CGSize(width: 820, height: 560)
    static let windowDefault = CGSize(width: 1320, height: 840)

    /// The first window's size on a screen whose visible frame (less the menu bar and
    /// Dock) is `visible`: the default, cut down to fit. Never under the minimum, which
    /// the window keeps anyway.
    static func defaultWindowSize(visible: CGSize) -> CGSize {
        CGSize(
            width: max(min(windowDefault.width, visible.width), windowMinimum.width),
            height: max(min(windowDefault.height, visible.height), windowMinimum.height)
        )
    }

    static let sidebarMinimum: Double = 240
    static let sidebarIdeal: Double = 290
    static let sidebarMaximum: Double = 380
    /// The design spec's least stage: the player bar, the evidence row and the Steps
    /// table's Detail column need it.
    static let stageMinimum: Double = 440
    static let conversationMinimum: Double = 300
    static let conversationIdeal: Double = 380
    static let conversationMaximum: Double = 560
    static let divider: Double = 1
    /// The most of its column the pinned verdict card takes, so the column's header,
    /// transcript and composer stay on screen when its evidence is open.
    static let verdictCardShare: Double = 0.6
    /// However short the column, the card's reasons keep this much room to scroll in.
    static let verdictBodyMinimum: Double = 72

    static func clamp(_ width: Double) -> Double {
        min(max(width, conversationMinimum), conversationMaximum)
    }

    /// The width a person chose, given back when the window is too narrow for it: the
    /// stage keeps its minimum first. Nil when the conversation does not fit beside the
    /// stage at all; the window then shows the verdict above the stage (the spec's
    /// order: the sidebar folds first, then the conversation gives way).
    static func conversation(_ chosen: Double, in available: Double) -> Double? {
        guard available > 0 else { return clamp(chosen) }
        let room = available - stageMinimum - divider
        guard room >= conversationMinimum else { return nil }
        return min(clamp(chosen), room)
    }

    /// Below this window width the sidebar, `sidebar` wide, folds away, so the stage
    /// keeps its minimum and the conversation fits beside it. The width is clamped to the
    /// sidebar's own range, so an unmeasured (or folded) sidebar counts as its minimum.
    static func sidebarFoldWidth(sidebar: Double, showsConversation: Bool) -> Double {
        let sidebar = min(max(sidebar, sidebarMinimum), sidebarMaximum)
        return sidebar + divider + stageMinimum + (showsConversation ? divider + conversationMinimum : 0)
    }

    /// The tallest the pinned verdict card may be in a column this tall; its body
    /// scrolls inside that. Nil before the column has been measured.
    static func verdictCardMaximum(column height: Double) -> Double? {
        guard height > 0 else { return nil }
        return (height * verdictCardShare).rounded(.down)
    }

    /// How tall the card's scrolling body is: all of its `natural` height, or what a card
    /// capped at `card` leaves after its headline and actions (`chrome`). Nil means no
    /// limit yet (nothing measured, no cap).
    static func verdictBody(natural: Double?, card: Double?, chrome: Double) -> Double? {
        guard let card else { return natural }
        let room = max(card - chrome, verdictBodyMinimum)
        return min(natural ?? verdictBodyMinimum, room)
    }
}

/// The line between the stage and the conversation. Dragging it resizes the
/// conversation; double-clicking puts it back to its usual width.
private struct ColumnDivider: View {
    @Binding var width: Double

    @State private var start: Double?

    var body: some View {
        Rectangle()
            .fill(Palette.hairline)
            .frame(width: 1)
            .frame(maxHeight: .infinity)
            .overlay {
                // A wider grip than the line, as NSSplitView gives.
                Color.clear
                    .frame(width: 9)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside { NSCursor.resizeLeftRight.push() } else { NSCursor.pop() }
                    }
                    .gesture(
                        DragGesture(minimumDistance: 1, coordinateSpace: .global)
                            .onChanged { value in
                                let origin = start ?? RunLayout.clamp(width)
                                start = origin
                                width = RunLayout.clamp(origin - value.translation.width)
                            }
                            .onEnded { _ in start = nil }
                    )
                    .onTapGesture(count: 2) { width = RunLayout.conversationIdeal }
            }
            .accessibilityHidden(true)
    }
}

// MARK: - Header

/// The run's short name, its task, and one status line that says where it is, who is
/// acting and when anything last happened. Machine facts are in the info popover.
private struct RunHeader: View {
    let store: RunStore
    let runId: String
    let facts: RunFacts
    let failureCursor: Int?
    let showFailure: (Int) -> Void

    @State private var expanded = false

    private var summary: RunSummary? { store.run(runId) }

    /// The whole first task once the transcript is in, else the list's clipped copy.
    private var task: String? {
        RunTitle.task(in: store.messages[runId] ?? []) ?? summary?.task
    }

    private var title: String { RunTitle.short(task: task, runId: runId) }
    private var fullTask: String { RunTitle.subtitle(task: task, alive: facts.isAlive) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(title)
                    .font(.title3.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(fullTask)
                Spacer(minLength: Space.s)
                if !facts.failures.isEmpty {
                    FailureNavigator(failures: facts.failures, cursor: failureCursor, alive: facts.isAlive, show: showFailure)
                }
            }
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(fullTask)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(expanded ? nil : 1)
                    .truncationMode(.tail)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: expanded)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Button(expanded ? "Less" : "Details") {
                    withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
                }
                .buttonStyle(.link)
                .font(.callout)
                .fixedSize()
                .help("The whole task, and the machine, image and run id")
            }
            if expanded {
                RunInfo(store: store, runId: runId, facts: facts)
                    .transition(.opacity)
            }
            RunStatusLine(store: store, runId: runId, facts: facts)
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.m)
    }
}

/// "2 failed" with previous and next, cycling the failed steps in the Steps stage.
private struct FailureNavigator: View {
    let failures: [Int]
    let cursor: Int?
    /// A live run is still working through them: "errored", not a verdict on the run.
    let alive: Bool
    let show: (Int) -> Void

    var body: some View {
        HStack(spacing: 0) {
            Button {
                show(1)
            } label: {
                Label(label, systemImage: "exclamationmark.triangle.fill")
                    .labelStyle(.titleAndIcon)
                    .foregroundStyle(alive ? AnyShapeStyle(.secondary) : AnyShapeStyle(Palette.failure))
                    .padding(.horizontal, 6)
            }
            .help("Show the next step that errored (\(Keys.nextFailure))")
            Divider().frame(height: 14)
            Button { show(-1) } label: { Image(systemName: "chevron.up").padding(.horizontal, 4) }
                .help("Previous step that errored (\(Keys.previousFailure))")
            Button { show(1) } label: { Image(systemName: "chevron.down").padding(.horizontal, 4) }
                .help("Next step that errored (\(Keys.nextFailure))")
        }
        .buttonStyle(.plain)
        .font(.callout.weight(.medium))
        .padding(.vertical, 3)
        .background(alive ? AnyShapeStyle(.fill.quaternary) : AnyShapeStyle(Palette.failure.opacity(0.1)), in: Capsule())
        .fixedSize()
    }

    private var label: String {
        if let cursor, let at = failures.firstIndex(of: cursor) {
            return "Error \(at + 1) of \(failures.count): step \(cursor)"
        }
        return failures.count == 1 ? "1 step errored" : "\(failures.count) steps errored"
    }
}

/// Where the run is, whose move it is, and how long since anything happened.
struct RunStatusLine: View {
    let store: RunStore
    let runId: String
    let facts: RunFacts

    var body: some View {
        // Ticks every second while alive, so "8s ago" stays true.
        TimelineView(.periodic(from: .now, by: facts.isAlive ? 1 : 3600)) { tick in
            let parts = parts(now: tick.date)
            // Whole parts drop from the end to fit; none is ever cut mid-word.
            ViewThatFits(in: .horizontal) {
                ForEach((0...parts.count).reversed(), id: \.self) { kept in
                    line(phase: phase(now: tick.date), parts: Array(parts.prefix(kept)))
                }
            }
            .font(.callout)
            .monospacedDigit()
            .frame(maxWidth: .infinity, alignment: .leading)
            .help("Times are your local time (\(Chrome.zone)). The machine's own clock may show UTC.")
        }
        if facts.phase == .idle {
            IdleActions(store: store, runId: runId)
        }
    }

    private func line(phase: some View, parts: [String]) -> some View {
        HStack(spacing: Space.s) {
            phase.fixedSize()
            ForEach(Array(parts.enumerated()), id: \.offset) { _, part in
                Text("·").foregroundStyle(.tertiary)
                Text(part)
                    .foregroundStyle(.secondary)
                    .fixedSize()
            }
        }
    }

    @ViewBuilder
    private func phase(now: Date) -> some View {
        switch facts.phase {
        case .booting:
            HStack(spacing: 5) {
                ProgressView().controlSize(.mini)
                Text("Booting").foregroundStyle(.secondary)
            }
        case .live:
            HStack(spacing: 5) {
                LiveMark()
                Text("Live").foregroundStyle(Palette.live)
            }
            .fontWeight(.semibold)
        case .idle:
            Label("No activity for \(Chrome.span(facts.idle(now: now)))", systemImage: "clock.badge.exclamationmark")
                .foregroundStyle(Palette.attention)
                .fontWeight(.semibold)
                .help("The machine is up but nothing has happened for \(Chrome.span(facts.idle(now: now))).")
        case .ended(let ending):
            Text(Self.endedText(ending, at: facts.ended))
                .foregroundStyle(endedTone(ending))
                .fontWeight(.semibold)
                .help(Self.endedHelp(ending))
        case .failed(let reason):
            Label("Failed to boot", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(Palette.failure)
                .fontWeight(.semibold)
                .help(reason ?? "")
        }
    }

    private func endedTone(_ ending: RunFacts.Ending) -> Color {
        if case .lost = ending { return Palette.failure }
        return .secondary
    }

    /// "Ended 20:35", "Ended 19:29, machine lost", "Ended 20:35, you destroyed it".
    static func endedText(_ ending: RunFacts.Ending, at: Date?) -> String {
        let when = at.map { " \(Chrome.shortTime($0))" } ?? ""
        switch ending {
        case .lost: return "Ended\(when), machine lost"
        case .destroyed(let byYou): return byYou ? "Ended\(when), you destroyed it" : "Ended\(when)"
        case .finished: return "Ended\(when)"
        }
    }

    static func endedHelp(_ ending: RunFacts.Ending) -> String {
        switch ending {
        case .lost(let reason): "The machine's VM went away while the run was going. \(reason ?? "")"
        case .destroyed(let byYou): byYou ? "You destroyed the machine." : "The coding agent destroyed the machine when it was done."
        case .finished: "The machine is gone."
        }
    }

    private func parts(now: Date) -> [String] {
        var out: [String] = []
        if facts.isAlive {
            switch facts.turn {
            case .verifier: out.append("Verifier working")
            case .coder: out.append("Coding agent's turn")
            case .you(let why): out.append(why)
            case .nobody: break
            }
            // Idle already says how long, and booting has only the create step.
            if facts.phase != .idle, facts.phase != .booting, let step = facts.lastStep {
                out.append("last step \(Chrome.span(now.timeIntervalSince(step.at))) ago (\(ToolCatalog.entry(for: step.tool).title))")
            }
        } else if case .you(let why) = facts.turn {
            out.append(why)
        }
        out.append("\(facts.isAlive ? "running" : "ran") \(Chrome.clock(facts.duration(now: now)))")
        out.append(Chrome.plural(facts.stepCount, "step"))
        return out
    }
}

/// A stuck run offers what a person can do about it. There is no way to interrupt the
/// coding agent directly: a message is read on its next check (daemon change listed in
/// companion ADR 0003).
private struct IdleActions: View {
    let store: RunStore
    let runId: String

    @AppStorage("composerFocusRequest") private var focusRequest = 0

    var body: some View {
        HStack(spacing: Space.s) {
            Text("Nothing has happened for a while. The coding agent sees a message the next time it checks in.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Spacer(minLength: Space.s)
            Button("Write a Message") { focusRequest += 1 }
                .controlSize(.small)
                .help("Put the cursor in the conversation's message field")
        }
        .padding(.top, Space.xxs)
    }
}

/// The machine's particulars, out of the way until asked for.
private struct RunInfo: View {
    let store: RunStore
    let runId: String
    let facts: RunFacts

    var body: some View {
        let detail = store.details[runId]
        let summary = store.run(runId)
        Grid(alignment: .leading, horizontalSpacing: Space.m, verticalSpacing: 4) {
            row("Started", "\(Chrome.stamp(facts.started)) (\(Chrome.zone))")
            if let ended = facts.ended { row("Ended", "\(Chrome.stamp(ended)) (\(Chrome.zone))") }
            row("Duration", Chrome.clock(facts.duration(now: Date())))
            row("Image", detail?.image ?? summary?.image ?? "-")
            if let name = detail?.machineName, !name.isEmpty { row("Machine", name) }
            if let ip = detail?.address { row("Address", ip) }
            if let boot = detail?.machine?.bootSeconds { row("Boot", String(format: "%.1f s", boot)) }
            row("Frames", Chrome.count(store.frames[runId]?.count ?? summary?.frames ?? 0))
            row("Messages", Chrome.count(facts.messageCount))
            GridRow {
                Text("Run").foregroundStyle(.secondary)
                HStack {
                    Text(runId).font(.callout.monospaced()).textSelection(.enabled)
                    CopyButton(text: runId, label: "Copy")
                }
            }
        }
        .font(.callout)
        .padding(.vertical, Space.xs)
    }

    private func row(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).foregroundStyle(.secondary)
            Text(value).textSelection(.enabled)
        }
    }
}
