import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// One run: a header naming it with its state in one line, the stage (the screen with the
/// steps under it) and the conversation beside it, arranged by `PaneLayout` for the
/// window's width class and zoom. Every pane stays in the tree in every arrangement.
struct RunView: View {
    let store: RunStore
    let runId: String
    /// The part of the stage with the keys (`KeyboardModel.stage`).
    @Binding var stage: StagePane
    @Binding var showsConversation: Bool

    @State private var capturing = false
    @State private var savingRecording = false
    @State private var failureCursor: Int?
    /// The step at the screen's playhead, for the one-row steps track.
    @State private var playhead: Int?
    @AppStorage("conversationWidth", store: AppDefaults.shared) private var conversationWidth = RunLayout.conversationIdeal
    @AppStorage("composerFocusRequest", store: AppDefaults.shared) private var composerFocusRequest = 0
    @State private var detailSize: CGSize = .zero
    /// The verdict this view has shown for its run, so a new one can be told from one
    /// that was already there (`VerdictLanding.lands`).
    @State private var verdictOnScreen: VerdictLanding.OnScreen?
    @Environment(\.keyboard) private var keyboard
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var facts: RunFacts { store.facts(runId) }
    private var pilot: ControlPilot { store.pilot(for: runId) }
    private var driving: Bool { pilot.active }

    private var canCapture: Bool { facts.machineReady && !capturing }
    private var canExport: Bool { !(store.frames[runId] ?? []).isEmpty && !savingRecording }
    private var canDestroy: Bool { store.details[runId]?.machine != nil }

    /// The window's arrangement; a view hosted alone (tests) works one out from its own width.
    private var layout: PaneLayout {
        if let keyboard { return keyboard.layout }
        return PaneLayout(widthClass: .of(width: detailSize.width), focus: .stage, stage: stage, runOpen: true,
                          conversationShown: showsConversation)
    }

    private var frames: PaneLayout.RunFrames {
        layout.run(in: detailSize, conversationWidth: conversationWidth)
    }

    /// Whether the conversation is on screen beside (or, narrow, instead of) the stage.
    private var conversationVisible: Bool { layout.conversationShown(fits: frames.conversationFits) }

    /// The conversation is asked for but has no room beside the stage: asking for it in a
    /// window too narrow shows it the way the width class does.
    private var conversationToggle: Binding<Bool> {
        Binding(get: { showsConversation && frames.conversationFits }, set: { show in
            showsConversation = show
            if show, layout.widthClass == .narrow { keyboard?.pane = .conversation }
        })
    }

    /// Keyboard focus is drawn only where more than one pane shows.
    private var showsFocus: Bool { layout.widthClass != .narrow && layout.zoom == nil }

    private func focused(_ pane: FocusPane, _ part: StagePane? = nil) -> Bool {
        guard showsFocus, let keyboard, keyboard.pane == pane else { return false }
        return part == nil || keyboard.stage == part
    }

    var body: some View {
        let layout = layout
        RunPanesLayout(layout: layout, conversationWidth: conversationWidth) {
            stageColumn(layout)
                .paneShown(layout.stageShown)
                .keyboardPane(.stage, active: layout.stageShown)
            ColumnDivider(width: $conversationWidth)
                .paneShown(conversationVisible && layout.stageShown)
            // `.id(runId)` rebuilds per run: message seqs restart at 1, so a half-typed
            // draft, answer or dispute would otherwise post into the next run. Always in
            // the tree otherwise, so a hidden conversation keeps its draft.
            ConversationView(store: store, runId: runId, visible: conversationVisible, focused: focused(.conversation))
                .id(runId)
                .focusRule(focused(.conversation))
                .paneShown(conversationVisible)
                .keyboardPane(.conversation, active: conversationVisible)
        }
        .onGeometryChange(for: CGSize.self) { $0.size } action: { detailSize = $0 }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .navigationTitle(RunTitle.short(task: store.run(runId)?.task, runId: runId))
        .topBar(leading: { paneSwitch }, trailing: { actions })
        .task(id: runId) {
            failureCursor = nil
            verdictOnScreen = VerdictLanding.OnScreen(runId: runId, seq: store.facts(runId).verdict?.seq)
            await store.select(runId)
        }
        .onChange(of: facts.verdict?.seq) { _, seq in
            verdictChanged(to: seq)
        }
        .onChange(of: store.seekRequest) {
            guard let request = store.seekRequest, request.runId == runId else { return }
            stage = request.inSteps ? .steps : .screen
            // One pane at a time: the stage comes forward to show what was asked for.
            if layout.widthClass == .narrow || (layout.zoom != nil && layout.zoom != .screen && layout.zoom != .steps) {
                keyboard?.pane = .stage
            }
        }
        .onPreferenceChange(PlayheadStepKey.self) { step in
            MainActor.assumeIsolated { playhead = step }
        }
        .offersActions(.run, offered, refresh: runId) { perform($0) }
    }

    /// The header, the verdict where the conversation is not beside the stage, then the
    /// screen with the steps under it. Zoomed to a part, only that part.
    private func stageColumn(_ layout: PaneLayout) -> some View {
        VStack(spacing: 0) {
            if layout.headerShown {
                RunHeader(
                    store: store,
                    runId: runId,
                    facts: facts,
                    failureCursor: failureCursor,
                    showFailure: showFailure
                )
                Hairline()
                // Conversation not shown: the verdict must still be read, not shrink to a
                // chip. A narrow window keeps it with the conversation, one pane away; above
                // the stage it would leave the screen a thumbnail.
                if !conversationVisible, !(layout.widthClass == .narrow && showsConversation) {
                    VerdictCard(store: store, runId: runId, facts: facts, compact: true,
                                maxHeight: RunLayout.verdictCardMaximum(column: detailSize.height))
                        .id(VerdictCard.identity(runId: runId, verdict: facts.verdict))
                        .padding(.horizontal, Space.l)
                        .padding(.top, Space.l)
                }
            }
            StageBodyLayout(layout: layout) {
                // Resets itself by hand per run, since it has a lease to give back first.
                // `visible` stops the live stream while another pane covers it; the lease
                // stays (Give Back is in the top bar).
                ScreenView(store: store, runId: runId, visible: layout.screenShown, hasKeys: focused(.stage, .screen))
                    .focusRule(focused(.stage, .screen))
                    .paneShown(layout.screenShown)
                    .stagePart(.screen, active: layout.screenShown)
                stepsPane(layout)
                    .focusRule(focused(.stage, .steps))
                    .paneShown(layout.stepsShown)
                    .stagePart(.steps, active: layout.stepsShown)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .clipped()
        }
        // No hard minimum: one would add to the window's own, and showing the runs in a
        // narrow window would then widen the window past the screen (issue #64).
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .background(Color.clear)
    }

    @ViewBuilder
    private func stepsPane(_ layout: PaneLayout) -> some View {
        switch layout.steps {
        case .list:
            StepsView(
                store: store,
                runId: runId,
                focused: focused(.stage, .steps),
                // Opened from the track (medium, narrow): it folds back to the track.
                collapse: layout.widthClass == .wide || layout.zoom == .steps ? nil : { stage = .screen },
                // Beside the screen, the screen's own bar already offers the way back.
                showsEvidenceBar: !layout.screenShown,
                playhead: layout.screenShown ? playhead : nil,
                claimsFocus: !layout.screenShown
            )
            .id(runId)
        case .track:
            StepsTrack(store: store, runId: runId, playhead: playhead) {
                stage = .steps
                keyboard?.pane = .stage
            }
        }
    }

    /// What the run's keys, menu items and palette entries can do now: only what applies.
    private var offered: Set<ActionID> {
        var ids: Set<ActionID> = [.goScreen, .goSteps, .goTranscript, .compose, .toggleConversation]
        if canCapture { ids.insert(.capture) }
        if canExport { ids.insert(.exportRecording) }
        if facts.machineReady, !pilot.busy, !driving { ids.insert(.takeControl) }
        if !facts.failures.isEmpty { ids.formUnion([.nextFailure, .previousFailure]) }
        return ids
    }

    private func perform(_ id: ActionID) {
        switch id {
        case .goScreen:
            stage = .screen
        case .goSteps:
            stage = .steps
        case .goTranscript:
            conversationToggle.wrappedValue = true
        case .compose:
            conversationToggle.wrappedValue = true
            composerFocusRequest += 1
        case .toggleConversation:
            conversationToggle.wrappedValue.toggle()
        case .capture: Task { await capture() }
        case .exportRecording: Task { await saveRecording() }
        case .takeControl: toggleControl()
        case .nextFailure: showFailure(1)
        case .previousFailure: showFailure(-1)
        default: break
        }
    }

    /// A verdict arrived while the run was open (ADR 0006, Verdict lands): its outcome
    /// decodes in, its card draws, VoiceOver hears it once, and a fail moves the run to its
    /// first failing step, through the same actions a key or the card's evidence use.
    private func verdictChanged(to seq: Int?) {
        let onScreen = verdictOnScreen
        verdictOnScreen = VerdictLanding.OnScreen(runId: runId, seq: seq)
        guard let seq, VerdictLanding.lands(onScreen: onScreen, runId: runId, current: seq,
                                            arrivedLive: store.liveVerdicts[runId]),
              let verdict = facts.verdict else { return }
        let cited = (verdict.evidence ?? []).lazy.map(Evidence.parse).compactMap(\.step).first
        let plan = VerdictLanding.plan(
            outcome: verdict.verdict, failures: facts.failures, cited: cited, reduceMotion: reduceMotion,
            typing: keyboard?.responder == .text,
            driving: driving || pilot.busy || keyboard?.responder == .guest
        )
        store.verdictMoment = VerdictMoment(runId: runId, seq: seq, start: Date(), plays: plan.plays)
        AccessibilityNotification.Announcement(plan.announcement).post()
        switch plan.focus {
        case .firstFailure:
            // The registry's next error, from the top: the first step that errored.
            failureCursor = nil
            if let keyboard { keyboard.perform(.nextFailure) } else { showFailure(1) }
        case .cited(let step):
            store.requestSeek(runId: runId, step: step, fromVerdict: true, inSteps: true)
        case nil:
            break
        }
    }

    /// Cycles the failed steps, opening each in the steps.
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

    /// Taking control needs the screen on screen: bring it forward first (the stage, its
    /// screen, no zoom on another pane), then take the lease.
    private func toggleControl() {
        if driving {
            Task { await pilot.release() }
        } else {
            if let keyboard { keyboard.showScreen() } else { stage = .screen }
            Task { await pilot.take() }
        }
    }

    /// A narrow window shows one pane at a time: the switch names them, for the mouse
    /// (`tab` does the same from the keys).
    @ViewBuilder
    private var paneSwitch: some View {
        if layout.widthClass == .narrow, layout.zoom == nil, let keyboard {
            let options: [(FocusPane, String)] = [(.sidebar, "Runs"), (.stage, stage.title)]
                + (showsConversation ? [(.conversation, "Conversation")] : [])
            SegmentedSwitch(options: options, selection: Binding(get: { keyboard.pane }, set: { keyboard.pane = $0 }))
                .help("Switch panes (\(ActionRegistry.label(.nextPane)))")
        }
    }

    /// The run's actions, at the top bar's right edge. Only what applies to this run is
    /// offered; a finished run has no machine to capture, drive or destroy. Words, not
    /// icons: a camera and a film strip do not explain themselves.
    @ViewBuilder
    private var actions: some View {
        if keyboard?.confirmingDestroy == runId {
            DestroyQuestion(
                destroy: { keyboard?.perform(.confirmDestroy, in: .confirm) },
                keep: { keyboard?.perform(.cancelDestroy, in: .confirm) }
            )
        } else {
            HStack(spacing: Space.s) {
                if facts.machineReady {
                    Button("Screenshot") { Task { await capture() } }
                        .disabled(!canCapture)
                        .help("Capture the screen as a step (\(ActionRegistry.label(.capture)))")
                }
                if !(store.frames[runId] ?? []).isEmpty {
                    Button {
                        Task { await saveRecording() }
                    } label: {
                        HStack(spacing: Space.xs) {
                            if savingRecording { Spinner(size: TypeScale.readingSmall) }
                            Text("Export")
                        }
                    }
                    .disabled(!canExport)
                    .help("Save the recording as a movie (\(ActionRegistry.label(.exportRecording)))")
                }
                if canDestroy {
                    Button("Destroy...") { keyboard?.perform(.destroy, in: .run) }
                        .buttonStyle(.quiet(tint: .failure))
                        .help("Destroy the machine and end the run (\(ActionRegistry.label(.destroy)))")
                }
                // Narrow: the pane switch shows the conversation; hiding it there hides nothing.
                if layout.widthClass != .narrow {
                    let shown = conversationToggle.wrappedValue
                    Button(shown ? "Hide Conversation" : "Conversation") {
                        conversationToggle.wrappedValue.toggle()
                    }
                    .help("\(shown ? "Hide" : "Show") the conversation")
                }
                if facts.machineReady {
                    // The one way to take and give back the screen.
                    ControlButton(driving: driving, busy: pilot.busy, action: toggleControl)
                        // Give Back stays lit and clickable while the rest dims.
                        .houseLightsLit(radius: Radius.md)
                }
            }
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

/// The step at the screen's playhead: the newest while following live. The steps track
/// under the screen reads it, so the two show the same moment.
struct PlayheadStepKey: PreferenceKey {
    static var defaultValue: Int? { nil }

    static func reduce(value: inout Int?, nextValue: () -> Int?) {
        value = nextValue() ?? value
    }
}

/// "Take Control" and, while driving, "Give Back": a button with words, never a switch
/// whose on state reads as off.
struct ControlButton: View {
    let driving: Bool
    let busy: Bool
    let action: () -> Void

    var body: some View {
        Group {
            if driving {
                Button("Give Back", action: action)
                    .buttonStyle(.quiet(tint: .driving))
            } else {
                Button("Take Control", action: action)
                    .buttonStyle(.primary)
            }
        }
        .disabled(busy)
        .help(driving
            ? "Give the mouse and keys back to the agents"
            : "Drive the machine yourself (\(ActionRegistry.label(.takeControl))). The conversation records it.")
    }
}

/// "Destroy the machine?" in the top bar, where Destroy was pressed: asked inline, never
/// in a dialog (ADR 0005). The hint bar asks the same, with its keys.
private struct DestroyQuestion: View {
    let destroy: () -> Void
    let keep: () -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: Space.s) {
            Text("Destroy the machine?")
                .font(Typeface.readingSemiBold.font(size: TypeScale.readingSmall))
                .foregroundStyle(theme.color(.failure, on: .chrome))
            Button("Keep It", action: keep)
                .help("Leave the machine running (\(ActionRegistry.label(.cancelDestroy)))")
            Button("Destroy", action: destroy)
                .buttonStyle(.quiet(tint: .failure))
                .help("Delete the machine and end the run (\(ActionRegistry.label(.confirmDestroy)))")
        }
        .fixedSize()
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

    /// A window frame moved and shrunk to fit `visible` (a screen's visible frame). macOS
    /// restores a saved frame, and SwiftUI's split view its saved column widths, without
    /// fitting them to the screen: a window saved 1320 wide came back at x = -148 on a 1024
    /// pt screen after every relaunch (#64). Never below the window's minimum.
    static func fitted(frame: CGRect, visible: CGRect) -> CGRect {
        let width = max(min(frame.width, visible.width), windowMinimum.width)
        let height = max(min(frame.height, visible.height), windowMinimum.height)
        let x = min(max(frame.minX, visible.minX), visible.maxX - width)
        let y = min(max(frame.minY, visible.minY), visible.maxY - height)
        return CGRect(x: x, y: y, width: width, height: height)
    }

    private static var tokens: DesignTokens.Layout { DesignData.shared.tokens.layout }

    static var sidebarMinimum: Double { tokens.runsMinWidth }
    static var sidebarIdeal: Double { tokens.runsWidth }
    static var sidebarMaximum: Double { tokens.runsMaxWidth }
    /// The design spec's least stage: the player bar and the evidence row need it.
    static var stageMinimum: Double { tokens.stageMinWidth }
    static var conversationMinimum: Double { tokens.conversationMinWidth }
    static var conversationIdeal: Double { tokens.conversationWidth }
    static var conversationMaximum: Double { tokens.conversationMaxWidth }
    static let divider: Double = 1
    /// The most of its column the pinned verdict card takes, so the column's header,
    /// transcript and composer stay on screen when its evidence is open.
    static let verdictCardShare: Double = 0.6
    /// However short the column, the card's reasons keep this much room to scroll in.
    static let verdictBodyMinimum: Double = 72

    static func clamp(_ width: Double) -> Double {
        min(max(width, conversationMinimum), conversationMaximum)
    }

    /// A sidebar width a person dragged, kept to the sidebar's range.
    static func clampSidebar(_ width: Double) -> Double {
        min(max(width, sidebarMinimum), sidebarMaximum)
    }

    /// The tallest the pinned verdict card may be in a column this tall; its body
    /// scrolls inside that. Nil before the column has been measured.
    static func verdictCardMaximum(column height: Double) -> Double? {
        guard height > 0 else { return nil }
        // A short column (a narrow window's conversation) keeps a few lines of transcript
        // under the card, not one; the card's reasons still keep their strip to scroll in.
        let leaving = max(height - verdictColumnLeaves, verdictCardLeast)
        return min(height * verdictCardShare, leaving).rounded(.down)
    }

    /// What a short column keeps under the card: its header, a few transcript lines and
    /// the composer.
    static let verdictColumnLeaves: Double = 280
    /// However short the column, the card keeps its headline, a strip of reasons and its
    /// actions.
    static let verdictCardLeast: Double = 200

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
        Hairline(axis: .vertical)
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

/// The run's short name in the heading cut, its task in the reading face, and one mono
/// status line that says where it is, who is acting and when anything last happened.
/// Machine facts are behind Details.
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
        VStack(alignment: .leading, spacing: Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Space.s) {
                Text(title)
                    .headingStyle(size: TypeScale.title)
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
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
                    .lineLimit(expanded ? nil : 1)
                    .truncationMode(.tail)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: expanded)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Button(expanded ? "Less" : "Details") {
                    withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
                }
                .buttonStyle(.textLink)
                .fixedSize()
                .help("Full task and machine details")
            }
            if expanded {
                RunInfo(store: store, runId: runId, facts: facts)
                    .transition(.opacity)
            }
            RunStatusLine(store: store, runId: runId, facts: facts)
        }
        .padding(.horizontal, Space.l)
        .padding(.top, Space.m)
        .padding(.bottom, Space.m)
    }
}

/// "2 steps errored" with previous and next, cycling the failed steps in the Steps stage.
private struct FailureNavigator: View {
    let failures: [Int]
    let cursor: Int?
    /// A live run is still working through them: "errored", not a verdict on the run.
    let alive: Bool
    let show: (Int) -> Void

    @Environment(\.theme) private var theme

    var body: some View {
        HStack(spacing: 0) {
            Button { show(1) } label: {
                Text("✗ " + label)
                    .foregroundStyle(alive ? theme.dim(on: .surface) : theme.color(.failure, on: .surface))
                    .padding(.horizontal, Space.s)
            }
            .help("Next error (\(ActionRegistry.label(.nextFailure)))")
            Hairline(axis: .vertical).frame(height: 14)
            Button { show(-1) } label: { Text("↑").padding(.horizontal, Space.s) }
                .help("Previous error (\(ActionRegistry.label(.previousFailure)))")
            Button { show(1) } label: { Text("↓").padding(.horizontal, Space.s) }
                .help("Next error (\(ActionRegistry.label(.nextFailure)))")
        }
        .buttonStyle(.plain)
        .monoStyle(.monoMedium, size: TypeScale.monoSmall)
        .frame(height: 24)
        .panel(radius: Radius.sm)
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

    @Environment(\.theme) private var theme

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
            .monoStyle(size: TypeScale.monoSmall)
            .monospacedDigit()
            .frame(maxWidth: .infinity, alignment: .leading)
            .help("Times are local (\(Chrome.zone)). The machine's clock may show UTC.")
        }
        if facts.phase == .idle {
            IdleActions(store: store, runId: runId)
        }
    }

    private func line(phase: some View, parts: [String]) -> some View {
        HStack(spacing: Space.s) {
            phase.fixedSize()
            ForEach(Array(parts.enumerated()), id: \.offset) { _, part in
                Text("·").foregroundStyle(.secondary)
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
            HStack(spacing: Space.xs) {
                Spinner(size: TypeScale.monoSmall)
                Text("Booting")
            }
        case .live:
            HStack(spacing: Space.xs) {
                LiveMark()
                Text("Live").foregroundStyle(theme.color(.live))
            }
            .fontWeight(.semibold)
        case .idle:
            Text("! No activity for \(Chrome.span(facts.idle(now: now)))")
                .foregroundStyle(theme.color(.attention))
                .fontWeight(.semibold)
                .help("The machine is up. No step or message for \(Chrome.span(facts.idle(now: now))).")
        case .ended(let ending):
            Text(Self.endedText(ending, at: facts.ended))
                .foregroundStyle(endedTone(ending))
                .fontWeight(.semibold)
                .help(Self.endedHelp(ending))
        case .failed(let reason):
            Text("✗ Failed to boot")
                .foregroundStyle(theme.color(.failure))
                .fontWeight(.semibold)
                .help(reason ?? "")
        }
    }

    private func endedTone(_ ending: RunFacts.Ending) -> Color {
        if case .lost = ending { return theme.color(.failure) }
        return theme.foreground
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
        case .lost(let reason): "The machine's VM stopped during the run. \(reason ?? "")"
        case .destroyed(let byYou): byYou ? "You destroyed the machine." : "The coding agent destroyed the machine."
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

    @AppStorage("composerFocusRequest", store: AppDefaults.shared) private var focusRequest = 0
    @Environment(\.keyboard) private var keyboard

    var body: some View {
        HStack(spacing: Space.s) {
            Text("Nothing is happening. The coding agent reads messages when it next checks in.")
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Spacer(minLength: Space.s)
            Button("Write a Message") {
                // The conversation comes forward first (a narrow window shows one pane).
                keyboard?.pane = .conversation
                focusRequest += 1
            }
                .buttonStyle(.quiet(small: true))
                .help("Go to the message field")
        }
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
        Grid(alignment: .leading, horizontalSpacing: Space.m, verticalSpacing: Space.xs) {
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
                label("Run")
                HStack(spacing: Space.s) {
                    Text(runId).textSelection(.enabled)
                    CopyButton(text: runId, label: "Copy")
                }
            }
        }
        .monoStyle(size: TypeScale.monoSmall)
        .padding(.vertical, Space.xs)
    }

    private func label(_ text: String) -> some View {
        Text(text.uppercased())
            .font(Typeface.monoMedium.font(size: TypeScale.label))
            .tracking(0.8)
            .foregroundStyle(.secondary)
    }

    private func row(_ name: String, _ value: String) -> some View {
        GridRow {
            label(name)
            Text(value).textSelection(.enabled)
        }
    }
}
