import AppKit
import SwiftUI

/// The window: the top bar, then the runs and the open run (or why there is none),
/// arranged by `PaneLayout` for the window's width class and zoom (ADR 0004 decision 8),
/// then the hint bar. Hand-made rather than a `NavigationSplitView`: that brings the
/// system's sidebar material, toolbar and divider, which the window's own chrome replaces
/// (ADR 0004, 0008).
struct RootView: View {
    let store: RunStore

    @State private var keyboard: KeyboardModel
    @AppStorage("stagePane", store: AppDefaults.shared) private var savedStage: StagePane = .screen
    @AppStorage("showsConversation", store: AppDefaults.shared) private var showsConversation = true
    @AppStorage("selectedRunId", store: AppDefaults.shared) private var savedSelection = ""
    @AppStorage("sidebarWidth", store: AppDefaults.shared) private var sidebarWidth = RunLayout.sidebarIdeal
    @State private var windowHeight: Double = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// The app passes the window's keyboard, which `KeyRouter` feeds; a view hosted alone
    /// (the harness, tests) makes its own.
    init(store: RunStore, keyboard: KeyboardModel? = nil) {
        self.store = store
        _keyboard = State(initialValue: keyboard ?? KeyboardModel(store: store))
    }

    /// Posted to show the runs column by hand (the snapshot harness, as a person would).
    static let showSidebarNotification = Notification.Name("greenroom.showSidebar")

    private var settle: Animation? { PaneMotion.settle(reduceMotion: reduceMotion) }

    var body: some View {
        let layout = keyboard.layout
        ThemedRoot {
            ZStack(alignment: .top) {
                VStack(spacing: 0) {
                    // The top bar is drawn over this room, from the preference the open view sets.
                    Color.clear.frame(height: TopBar.height)
                    WindowPanesLayout(layout: layout, runsWidth: sidebarWidth) {
                        runs(layout)
                            .paneShown(layout.runs != .hidden)
                        runsLine(layout)
                            .paneShown(layout.runs == .column || layout.runs == .strip)
                        detail
                            .paneShown(layout.detailShown)
                    }
                    // Any height, clipped: a run whose panes want more room than a short
                    // window has must not push the hint bar off the window.
                    .frame(minHeight: 0, maxHeight: .infinity, alignment: .top)
                    .clipped()
                    .overlay(alignment: .leading) {
                        if layout.runsOverlay {
                            runsOverlay
                        }
                    }
                    // The help opens over the panes, above the hint bar, and never squeezes
                    // them: at most `layout.helpMaxShare` of the window.
                    .overlay(alignment: .bottom) {
                        if keyboard.helpOpen {
                            KeyHelpPanel(keyboard: keyboard, maximumHeight: helpHeight)
                                .transition(.move(edge: .bottom).combined(with: .opacity))
                        }
                    }
                    .animation(settle, value: layout)
                    .animation(settle, value: keyboard.helpOpen)
                    HintBarView(keyboard: keyboard)
                        .houseLightsLit()
                }
                .overlayPreferenceValue(TopBarItemsKey.self, alignment: .top) { items in
                    TopBar(items: items)
                }
                // Over the panes and the top bar, under the palette: dark everywhere but
                // the holes the lit views report, while the person drives.
                .overlayPreferenceValue(HouseLightsHolesKey.self) { holes in
                    GeometryReader { proxy in
                        HouseLightsScrim(keyboard: keyboard, holes: holes.map { (proxy[$0.anchor], $0.radius) })
                    }
                }
                if keyboard.paletteOpen {
                    palette
                }
            }
            .ignoresSafeArea()
        }
        .environment(\.keyboard, keyboard)
        .focusedSceneValue(\.actionState, keyboard.state())
        // The width picks the class; crossing a threshold settles the panes on a spring.
        .onGeometryChange(for: CGSize.self) { $0.size } action: { size in
            windowHeight = size.height
            let widthClass = WidthClass.of(width: size.width)
            guard widthClass != keyboard.widthClass else { return }
            withAnimation(settle) {
                keyboard.widthClass = widthClass
                keyboard.settleFocus()
            }
        }
        // The SSE socket can look alive after sleep while the daemon restarted.
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.resync() }
        }
        .onReceive(NotificationCenter.default.publisher(for: Self.showSidebarNotification)) { _ in
            keyboard.sidebarShown = true
        }
        .onChange(of: store.selectedRunId) { old, new in
            if let new { savedSelection = new } else { keyboard.zoom.restore() }
            if old == nil, new != nil { keyboard.settleFocus(runRestored: true) }
            keyboard.confirmingDestroy = nil
        }
        .onChange(of: store.runs.isEmpty) {
            restoreSelection()
        }
        .onChange(of: savedStage, initial: true) { if keyboard.stage != savedStage { keyboard.stage = savedStage } }
        .onChange(of: keyboard.stage) { if savedStage != keyboard.stage { savedStage = keyboard.stage } }
        .onChange(of: showsConversation, initial: true) { keyboard.conversationShown = showsConversation }
    }

    private var helpHeight: Double {
        let share = DesignData.shared.tokens.layout.helpMaxShare
        return max((windowHeight * share).rounded(.down), 160)
    }

    /// The runs: a column, a strip of marks, or (narrow, zoomed) the whole window.
    @ViewBuilder
    private func runs(_ layout: PaneLayout) -> some View {
        if layout.runs == .strip {
            RunsStrip(store: store) { keyboard.pane = .sidebar }
        } else {
            SidebarView(store: store, onOpen: keyboard.openedRunByClick)
                .focusRule(layout.runs == .column && keyboard.pane == .sidebar && layout.zoom == nil)
                .keyboardPane(.sidebar, active: layout.runs != .hidden)
        }
    }

    /// After the runs: a column's edge drags; a strip's is a plain line.
    @ViewBuilder
    private func runsLine(_ layout: PaneLayout) -> some View {
        if layout.runs == .column {
            SidebarDivider(width: $sidebarWidth)
        } else {
            Hairline(axis: .vertical)
                .frame(maxHeight: .infinity)
        }
    }

    /// The whole list over the run, from the strip, while the runs have the keys. A click
    /// beside it, esc, or opening a run puts it away.
    private var runsOverlay: some View {
        HStack(spacing: 0) {
            SidebarView(store: store, onOpen: keyboard.openedRunByClick)
                .frame(width: RunLayout.clampSidebar(sidebarWidth))
                .focusRule(true)
                .keyboardPane(.sidebar)
                .overlay(alignment: .trailing) { Hairline(axis: .vertical).frame(maxHeight: .infinity) }
                // Lifted over a run only: with none open there is nothing under it to dim,
                // and a scrim over an empty page read as a broken window (audit P6).
                .shadow(color: .black.opacity(store.selectedRunId == nil ? 0 : 0.18), radius: 12, x: 4)
            Color.black.opacity(store.selectedRunId == nil ? 0 : 0.12)
                .contentShape(Rectangle())
                .onTapGesture { if store.selectedRunId != nil { keyboard.pane = .stage } }
                .accessibilityHidden(true)
        }
        .transition(.move(edge: .leading).combined(with: .opacity))
    }

    /// Cmd-K: over everything, on a scrim that closes it when clicked.
    private var palette: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.28)
                .contentShape(Rectangle())
                .onTapGesture { keyboard.closePalette() }
                .accessibilityHidden(true)
            CommandPalette(keyboard: keyboard)
                .padding(.top, TopBar.height + Space.xxl)
                .padding(.horizontal, Space.l)
        }
        .transition(.opacity)
    }

    /// The empty states offer Refresh in the top bar, where the run's actions go.
    private var refresh: some View {
        Button("Refresh") {
            Task { await store.resync() }
        }
        .help("Read the runs again (\(ActionRegistry.label(.refresh)))")
    }

    @ViewBuilder
    private var detail: some View {
        switch store.connection {
        case .offline(hasData: false):
            OfflineView(store: store)
                .topBar { refresh }
        case .refused(let words, hasData: false):
            RefusedView(store: store, words: words)
                .topBar { refresh }
        case .connecting where store.runs.isEmpty:
            ConnectingView(address: store.daemonAddress)
                .topBar { refresh }
        default:
            VStack(spacing: 0) {
                switch store.connection {
                case .offline: OfflineBanner(store: store, words: nil)
                case .refused(let words, _): OfflineBanner(store: store, words: words)
                case .connecting, .online: EmptyView()
                }
                if let runId = store.selectedRunId {
                    RunView(store: store, runId: runId,
                            stage: Binding(get: { keyboard.stage }, set: { keyboard.stage = $0 }),
                            showsConversation: $showsConversation)
                } else if store.runs.isEmpty {
                    WelcomeView(address: store.daemonAddress)
                        .topBar { refresh }
                } else {
                    NoSelectionView(store: store)
                        .topBar { refresh }
                }
            }
        }
    }

    /// The last run a person looked at, else the one that needs them, else the newest,
    /// once the list first arrives.
    private func restoreSelection() {
        guard store.selectedRunId == nil, let newest = store.runs.first else { return }
        let saved = store.runs.first { $0.runId == savedSelection }
        let needing = store.runs.first { store.facts($0.runId).needsYou }
        store.selectedRunId = (saved ?? needing ?? newest).runId
    }
}

/// What the keys can do now, for the menu bar's enabled items. Spelled out rather than
/// `@Entry`: that macro's plugin ships only with Xcode.
private struct ActionStateKey: FocusedValueKey {
    typealias Value = ActionState
}

extension FocusedValues {
    var actionState: ActionState? {
        get { self[ActionStateKey.self] }
        set { self[ActionStateKey.self] = newValue }
    }
}

/// The hairline between the runs and the run. Dragging it resizes the runs;
/// double-clicking puts them back to their usual width.
private struct SidebarDivider: View {
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
                                let origin = start ?? RunLayout.clampSidebar(width)
                                start = origin
                                width = RunLayout.clampSidebar(origin + value.translation.width)
                            }
                            .onEnded { _ in start = nil }
                    )
                    .onTapGesture(count: 2) { width = RunLayout.sidebarIdeal }
            }
    }
}

// MARK: - Empty and error states

/// An empty state's words: a heading and a sentence or two in the reading face,
/// centred in the detail at a comfortable measure.
private struct EmptyState<Extra: View>: View {
    let title: String
    let message: String
    @ViewBuilder var extra: () -> Extra

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            VStack(alignment: .leading, spacing: Space.s) {
                Text(title)
                    .headingStyle(size: TypeScale.title)
                Text(message)
                    .readingStyle()
                    .foregroundStyle(.secondary)
            }
            extra()
        }
        .frame(maxWidth: 440, alignment: .leading)
        .padding(Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The first read of the daemon: the loader, timed from when it began, and where.
private struct ConnectingView: View {
    let address: String

    @State private var since = Date()

    var body: some View {
        VStack(spacing: Space.s) {
            Loader(label: "Connecting to greenroom", since: since)
            Text(address)
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// "Try Again", ticking while the read is out.
private struct RetryButton: View {
    let store: RunStore

    @State private var retrying = false

    var body: some View {
        Button {
            retrying = true
            Task {
                await store.resync()
                retrying = false
            }
        } label: {
            HStack(spacing: Space.s) {
                if retrying { Spinner(size: TypeScale.readingSmall) }
                Text(retrying ? "Trying" : "Try Again")
            }
        }
        .buttonStyle(.primary)
        .disabled(retrying)
    }
}

/// Nothing loaded and nothing answering: say what to do, not just that it failed.
private struct OfflineView: View {
    let store: RunStore

    var body: some View {
        EmptyState(
            title: "greenroom is not running",
            message: "Nothing answers at \(store.daemonAddress). Start it and this window connects."
        ) {
            CommandBlock(command: "greenroom serve")
            RetryButton(store: store)
        }
    }
}

/// The daemon is running and answered with an error: its words, never "start it".
private struct RefusedView: View {
    let store: RunStore
    let words: String

    var body: some View {
        EmptyState(
            title: "greenroom refused the request",
            message: "greenroom at \(store.daemonAddress) answered with an error:"
        ) {
            Text(words)
                .monoStyle()
                .textSelection(.enabled)
                .padding(Space.m)
                .frame(maxWidth: .infinity, alignment: .leading)
                .panel()
            if let advice = ConnectionState.advice(for: words) {
                Text(advice)
                    .readingStyle(size: TypeScale.readingSmall)
                    .foregroundStyle(.secondary)
            }
            RetryButton(store: store)
        }
    }
}

/// Runs are still readable while the daemon is away or refusing; one quiet line says
/// they may be stale, and why in the daemon's words when it gave any.
private struct OfflineBanner: View {
    let store: RunStore
    /// The daemon's error when it answered with one; nil when nothing answered.
    let words: String?

    private var text: String {
        if let words { return "\(words.hasSuffix(".") ? words : words + ".") Showing what was last loaded." }
        return "greenroom is not answering. Showing what was last loaded."
    }

    var body: some View {
        HStack(spacing: Space.s) {
            Spinner(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
            Text(text)
                .readingStyle(size: TypeScale.readingSmall)
                .lineLimit(1)
                .truncationMode(.tail)
                .help(words ?? store.lastError ?? "")
            Spacer(minLength: Space.s)
            Button("Reconnect") { Task { await store.resync() } }
                .buttonStyle(.quiet(small: true))
        }
        .padding(.horizontal, Space.l)
        .padding(.vertical, Space.s)
        .overlay(alignment: .bottom) { Hairline() }
    }
}

/// First run of the app, or a daemon with no history: what this window is for and how
/// to get a run into it.
private struct WelcomeView: View {
    let address: String

    @Environment(\.openURL) private var openURL

    private var mcpCommand: String {
        "claude mcp add --transport http greenroom http://\(address)/mcp"
    }

    var body: some View {
        GeometryReader { geometry in
            ScrollView {
                VStack(alignment: .leading, spacing: Space.xl) {
                    VStack(alignment: .leading, spacing: Space.m) {
                        Wordmark(size: TypeScale.title)
                        Text("Watch your agents work")
                            .headingStyle(size: TypeScale.title)
                        Text("greenroom gives your coding agent its own disposable Mac. A verifier checks the work and proposes a verdict. You watch, answer and decide here.")
                            .readingStyle()
                            .foregroundStyle(.secondary)
                    }

                    VStack(alignment: .leading, spacing: Space.s) {
                        SectionLabel(title: "Connect your agent")
                        Text("greenroom is running at \(address). Add it to Claude Code once, then ask your agent to verify a change on a greenroom machine:")
                            .readingStyle()
                            .foregroundStyle(.secondary)
                        CommandBlock(command: mcpCommand)
                        Button("Setup guide") { openURL(URL(string: "https://github.com/shlok1806/greenroom#readme")!) }
                            .buttonStyle(.textLink)
                    }

                    VStack(alignment: .leading, spacing: Space.m) {
                        SectionLabel(title: "What you can do")
                        feature("Watch the screen", "Follow the machine live, or scrub through its recording.")
                        feature("Judge the verdict", "Open the steps it cites, then accept or dispute.")
                        feature("Talk to the verifier", "Answer its questions, or hand it more to check.")
                        feature("Take control", "Drive the machine yourself when an agent is stuck.")
                    }
                }
                .frame(maxWidth: 520, alignment: .leading)
                .padding(Space.xl)
                .frame(maxWidth: .infinity, minHeight: geometry.size.height)
            }
        }
    }

    private func feature(_ title: String, _ detail: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).readingStyle(.readingSemiBold)
            Text(detail)
                .readingStyle(size: TypeScale.readingSmall)
                .foregroundStyle(.secondary)
        }
    }
}

/// No run open: point at the one that needs the person, and at the keys.
private struct NoSelectionView: View {
    let store: RunStore

    var body: some View {
        let needing = store.runs.first { store.facts($0.runId).needsYou }
        let live = store.runs.first { store.facts($0.runId).isAlive }
        let suggestion = needing ?? live ?? store.runs.first
        EmptyState(
            title: "No run open",
            message: store.goneRun.map { "\u{201C}\($0)\u{201D} is no longer on greenroom." }
                ?? "Pick a run on the left."
        ) {
            if let suggestion {
                let facts = store.facts(suggestion.runId)
                VStack(alignment: .leading, spacing: Space.s) {
                    Button(facts.needsYou ? "Open the Run That Needs You" : facts.isAlive ? "Open the Running Run" : "Open the Newest Run") {
                        store.selectedRunId = suggestion.runId
                    }
                    .buttonStyle(.primary)
                    Text(RunTitle.short(task: suggestion.task, runId: suggestion.runId))
                        .readingStyle(size: TypeScale.readingSmall)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Grid(alignment: .leading, horizontalSpacing: Space.m, verticalSpacing: Space.xs) {
                shortcut("j k", "Move through runs")
                shortcut(ActionRegistry.label(.open), "Open the suggested run")
                shortcut(ActionRegistry.label(.palette), "Commands")
                shortcut(ActionRegistry.label(.help), "All keys")
            }
            .padding(.top, Space.s)
        }
    }

    private func shortcut(_ keys: String, _ action: String) -> some View {
        GridRow {
            Text(keys)
                .monoStyle(size: TypeScale.monoSmall)
                .foregroundStyle(.secondary)
                .gridColumnAlignment(.trailing)
            Text(action)
                .readingStyle(size: TypeScale.readingSmall)
        }
    }
}
