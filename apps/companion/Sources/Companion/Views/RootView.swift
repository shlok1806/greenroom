import AppKit
import SwiftUI

/// The window: the top bar, then the runs on the left and the open run (or why there is
/// none) on the right. A hand-made split, not a `NavigationSplitView`: that brings the
/// system's sidebar material, toolbar and divider, which the window's own chrome replaces
/// (ADR 0004, 0008).
struct RootView: View {
    let store: RunStore

    @AppStorage("stagePane") private var pane: StagePane = .screen
    @AppStorage("showsConversation") private var showsConversation = true
    @AppStorage("selectedRunId") private var savedSelection = ""
    @AppStorage("sidebarWidth") private var sidebarWidth = RunLayout.sidebarIdeal
    @State private var sidebarShown = true
    /// Set when a narrow window folded the sidebar away, so widening brings it back.
    @State private var autoCollapsed = false
    @State private var windowWidth: Double = 0

    /// Posted to show the sidebar by hand (the snapshot harness, as a person would).
    static let showSidebarNotification = Notification.Name("greenroom.showSidebar")

    /// Below this the sidebar folds away, so the stage and the conversation keep their room.
    private var foldWidth: Double {
        RunLayout.sidebarFoldWidth(sidebar: sidebarWidth, showsConversation: showsConversation)
    }

    var body: some View {
        ThemedRoot {
            VStack(spacing: 0) {
                // The top bar is drawn over this room, from the preference the open view sets.
                Color.clear.frame(height: TopBar.height)
                HStack(spacing: 0) {
                    if sidebarShown {
                        SidebarView(store: store)
                            .frame(width: RunLayout.clampSidebar(sidebarWidth))
                            .transition(.move(edge: .leading))
                        SidebarDivider(width: $sidebarWidth)
                    }
                    detail
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .overlayPreferenceValue(TopBarItemsKey.self, alignment: .top) { items in
                TopBar(items: items)
            }
            .ignoresSafeArea()
        }
        .focusedSceneValue(\.sidebarShown, $sidebarShown)
        // The SSE socket can look alive after sleep while the daemon restarted.
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.resync() }
        }
        .onReceive(NotificationCenter.default.publisher(for: Self.showSidebarNotification)) { _ in
            autoCollapsed = false
            sidebarShown = true
        }
        .onChange(of: store.selectedRunId) {
            if let selected = store.selectedRunId { savedSelection = selected }
        }
        .onChange(of: store.runs.isEmpty) {
            restoreSelection()
        }
        .onGeometryChange(for: Double.self) { $0.size.width } action: { width in
            let previous = windowWidth
            windowWidth = width
            fold(width: width, previous: previous)
        }
        .onChange(of: showsConversation) { fold(width: windowWidth, previous: 0) }
    }

    /// The empty states offer Refresh in the top bar, where the run's actions go.
    private var refresh: some View {
        Button("Refresh") {
            Task { await store.resync() }
        }
        .help("Read the runs again (\(Keys.refresh))")
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
                    RunView(store: store, runId: runId, pane: $pane, showsConversation: $showsConversation,
                            makeRoom: makeRoom)
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

    /// Folds only while shrinking (or on first layout), so a sidebar shown by hand in a
    /// narrow window stays.
    private func fold(width: Double, previous: Double) {
        let shrankPast = width < foldWidth && (previous == 0 || previous >= foldWidth)
        if shrankPast, sidebarShown {
            autoCollapsed = true
            withAnimation(.snappy) { sidebarShown = false }
        } else if width >= foldWidth, autoCollapsed {
            autoCollapsed = false
            withAnimation(.snappy) { sidebarShown = true }
        }
    }

    /// The conversation was asked for beside a sidebar shown by hand, with no room for
    /// both: the sidebar gives way first, and comes back once the window is wide enough.
    private func makeRoom() {
        guard sidebarShown else { return }
        autoCollapsed = true
        withAnimation(.snappy) { sidebarShown = false }
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

/// Whether the runs sidebar is shown, for View > Hide Sidebar. Spelled out rather than
/// `@Entry`: that macro's plugin ships only with Xcode.
private struct SidebarShownKey: FocusedValueKey {
    typealias Value = Binding<Bool>
}

extension FocusedValues {
    var sidebarShown: Binding<Bool>? {
        get { self[SidebarShownKey.self] }
        set { self[SidebarShownKey.self] = newValue }
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

private struct ConnectingView: View {
    let address: String

    var body: some View {
        HStack(spacing: Space.s) {
            Spinner()
            Text("Connecting to greenroom at \(address)")
                .monoStyle()
        }
        .foregroundStyle(.secondary)
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
        .keyboardShortcut(.defaultAction)
    }
}

/// Nothing loaded and nothing answering: say what to do, not just that it failed.
private struct OfflineView: View {
    let store: RunStore

    var body: some View {
        EmptyState(
            title: "greenroom is not running",
            message: "Nothing answered at \(store.daemonAddress). Start it in a terminal and this window connects on its own."
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
            message: "The daemon at \(store.daemonAddress) is running and answered with an error:"
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
        if let words { return "\(words.hasSuffix(".") ? words : words + ".") This is what was last loaded." }
        return "The daemon at \(store.daemonAddress) is not answering. This is what was last loaded."
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
                        Text("greenroom gives your coding agent (Claude Code, or any agent that speaks MCP) its own disposable Mac. Its verifier, greenroom's own agent, checks the work and proposes a verdict. You watch, answer and decide here.")
                            .readingStyle()
                            .foregroundStyle(.secondary)
                    }

                    VStack(alignment: .leading, spacing: Space.s) {
                        SectionLabel(title: "Connect your agent")
                        Text("The greenroom daemon is running at \(address). Add it to Claude Code once, then ask your agent to verify a change on a greenroom machine:")
                            .readingStyle()
                            .foregroundStyle(.secondary)
                        CommandBlock(command: mcpCommand)
                        Button("Setup guide") { openURL(URL(string: "https://github.com/shlok1806/greenroom#readme")!) }
                            .buttonStyle(.textLink)
                    }

                    VStack(alignment: .leading, spacing: Space.m) {
                        SectionLabel(title: "What you can do")
                        feature("Watch the screen", "Follow the machine live, or scrub back through its recording.")
                        feature("Judge the verdict", "See what the verifier cites, open each step, then accept or dispute.")
                        feature("Talk to the verifier", "Answer its questions, or hand it more to check.")
                        feature("Take control", "Drive the machine's mouse and keyboard yourself when an agent is stuck.")
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
            message: store.goneRun.map { "\u{201C}\($0)\u{201D} is no longer on the daemon at \(store.daemonAddress)." }
                ?? "Pick a run on the left, or open the one that most wants you."
        ) {
            if let suggestion {
                let facts = store.facts(suggestion.runId)
                VStack(alignment: .leading, spacing: Space.s) {
                    Button(facts.needsYou ? "Open the Run That Needs You" : facts.isAlive ? "Open the Running Run" : "Open the Newest Run") {
                        store.selectedRunId = suggestion.runId
                    }
                    .buttonStyle(.primary)
                    .keyboardShortcut(.defaultAction)
                    Text(RunTitle.short(task: suggestion.task, runId: suggestion.runId))
                        .readingStyle(size: TypeScale.readingSmall)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Grid(alignment: .leading, horizontalSpacing: Space.m, verticalSpacing: Space.xs) {
                shortcut("↑ ↓", "Move through runs")
                shortcut("\(Keys.screen)  \(Keys.steps)", "Screen or steps")
                shortcut(Keys.conversation, "Show or hide the conversation")
                shortcut(Keys.nextFailure, "Next step that errored")
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
