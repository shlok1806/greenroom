import AppKit
import SwiftUI

/// The window: runs on the left, the open run (or why there is none) on the right.
struct RootView: View {
    let store: RunStore

    @AppStorage("stagePane") private var pane: StagePane = .screen
    @AppStorage("showsConversation") private var showsConversation = true
    @AppStorage("selectedRunId") private var savedSelection = ""
    @State private var columns: NavigationSplitViewVisibility = .all
    /// Set when a narrow window folded the sidebar away, so widening brings it back.
    @State private var autoCollapsed = false
    @State private var windowWidth: Double = 0
    /// The sidebar's width as last laid out, which a person may have dragged past its ideal.
    @State private var sidebarWidth = RunLayout.sidebarIdeal

    /// Below this the sidebar folds away, so the stage and the conversation keep their room.
    private var foldWidth: Double {
        RunLayout.sidebarFoldWidth(sidebar: sidebarWidth, showsConversation: showsConversation)
    }

    var body: some View {
        NavigationSplitView(columnVisibility: $columns) {
            SidebarView(store: store)
                .navigationSplitViewColumnWidth(
                    min: RunLayout.sidebarMinimum, ideal: RunLayout.sidebarIdeal, max: RunLayout.sidebarMaximum
                )
                .onGeometryChange(for: Double.self) { $0.size.width } action: { width in
                    if width > 0 { sidebarWidth = width }
                }
        } detail: {
            detail
        }
        // The SSE socket can look alive after sleep while the daemon restarted.
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.resync() }
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

    /// The empty states keep a toolbar too, so the title bar is one height in every state.
    private var emptyToolbar: some ToolbarContent {
        ToolbarItem(placement: .primaryAction) {
            Button {
                Task { await store.resync() }
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
                    .labelStyle(.titleAndIcon)
            }
            .help("Read the runs again (\(Keys.refresh))")
        }
    }

    @ViewBuilder
    private var detail: some View {
        switch store.connection {
        case .offline(hasData: false):
            OfflineView(store: store)
                .toolbar(removing: .title)
                .toolbar { emptyToolbar }
        case .connecting where store.runs.isEmpty:
            ConnectingView(address: store.daemonAddress)
                .toolbar(removing: .title)
                .toolbar { emptyToolbar }
        default:
            VStack(spacing: 0) {
                if case .offline = store.connection {
                    OfflineBanner(store: store)
                }
                if let runId = store.selectedRunId {
                    RunView(store: store, runId: runId, pane: $pane, showsConversation: $showsConversation,
                            makeRoom: makeRoom)
                } else if store.runs.isEmpty {
                    WelcomeView(address: store.daemonAddress)
                        .toolbar(removing: .title)
                        .toolbar { emptyToolbar }
                } else {
                    NoSelectionView(store: store)
                        .toolbar(removing: .title)
                        .toolbar { emptyToolbar }
                }
            }
        }
    }

    /// Folds only while shrinking (or on first layout), so a sidebar shown by hand in a
    /// narrow window stays.
    private func fold(width: Double, previous: Double) {
        let shrankPast = width < foldWidth && (previous == 0 || previous >= foldWidth)
        if shrankPast, columns != .detailOnly {
            autoCollapsed = true
            withAnimation(.snappy) { columns = .detailOnly }
        } else if width >= foldWidth, autoCollapsed {
            autoCollapsed = false
            withAnimation(.snappy) { columns = .all }
        }
    }

    /// The conversation was asked for beside a sidebar shown by hand, with no room for
    /// both: the sidebar gives way first, and comes back once the window is wide enough.
    private func makeRoom() {
        guard columns != .detailOnly else { return }
        autoCollapsed = true
        withAnimation(.snappy) { columns = .detailOnly }
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

// MARK: - Empty and error states

private struct ConnectingView: View {
    let address: String

    var body: some View {
        VStack(spacing: Space.m) {
            ProgressView()
            Text("Connecting to the greenroom daemon at \(address)")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Nothing loaded and nothing answering: say what to do, not just that it failed.
private struct OfflineView: View {
    let store: RunStore

    @State private var retrying = false

    var body: some View {
        VStack(spacing: Space.l) {
            Image(systemName: "server.rack")
                .font(.system(size: 34))
                .foregroundStyle(.secondary)
            VStack(spacing: Space.xs) {
                Text("The greenroom daemon is not running")
                    .font(.title3.weight(.semibold))
                Text("Nothing answered at \(store.daemonAddress). Start it in a terminal and this window connects on its own.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            CommandBlock(command: "greenroom serve")
                .frame(maxWidth: 320)
            Button {
                retrying = true
                Task {
                    await store.resync()
                    retrying = false
                }
            } label: {
                if retrying {
                    ProgressView().controlSize(.small)
                } else {
                    Text("Try Again")
                }
            }
            .disabled(retrying)
            .keyboardShortcut(.defaultAction)
        }
        .frame(maxWidth: 420)
        .padding(Space.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Runs are still readable while the daemon is away; one quiet line says they may be stale.
private struct OfflineBanner: View {
    let store: RunStore

    var body: some View {
        HStack(spacing: Space.s) {
            Image(systemName: "bolt.horizontal.circle")
                .foregroundStyle(.secondary)
            Text("The daemon at \(store.daemonAddress) is not answering. This is what was last loaded.")
                .lineLimit(1)
                .truncationMode(.tail)
                .help(store.lastError ?? "")
            Spacer(minLength: Space.s)
            Button("Reconnect") { Task { await store.resync() } }
                .controlSize(.small)
        }
        .font(.callout)
        .padding(.horizontal, Space.l)
        .padding(.vertical, 6)
        .background(.fill.quinary)
        .overlay(alignment: .bottom) { Divider() }
    }
}

/// First run of the app, or a daemon with no history: what this window is for and how
/// to get a run into it.
private struct WelcomeView: View {
    let address: String

    private var mcpCommand: String {
        "claude mcp add --transport http greenroom http://\(address)/mcp"
    }

    var body: some View {
        GeometryReader { geometry in
        ScrollView {
            VStack(alignment: .leading, spacing: Space.xl) {
                VStack(alignment: .leading, spacing: Space.s) {
                    Text("Watch your agents work")
                        .font(.title.weight(.semibold))
                    Text("greenroom gives your coding agent (Claude Code, or any agent that speaks MCP) its own disposable Mac. Its verifier, greenroom's own agent, checks the work and proposes a verdict. You watch, answer and decide here.")
                        .font(.body)
                        .foregroundStyle(.secondary)
                }

                VStack(alignment: .leading, spacing: Space.s) {
                    Text("Connect your agent")
                        .font(.headline)
                    Text("The greenroom daemon is running at \(address). Add it to Claude Code once, then ask your agent to verify a change on a greenroom machine:")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    CommandBlock(command: mcpCommand)
                    Link(destination: URL(string: "https://github.com/shlok1806/greenroom#readme")!) {
                        Label("Setup guide", systemImage: "book")
                    }
                    .font(.callout)
                }

                VStack(alignment: .leading, spacing: Space.m) {
                    feature("display", "Watch the screen", "Follow the machine live, or scrub back through its recording.")
                    feature("checkmark.seal", "Judge the verdict", "See what the verifier cites, open each step, then accept or dispute.")
                    feature("bubble.left.and.text.bubble.right", "Talk to the verifier", "Answer its questions, or hand it more to check.")
                    feature("cursorarrow.click.2", "Take control", "Drive the machine's mouse and keyboard yourself when an agent is stuck.")
                }
            }
            .frame(maxWidth: 480, alignment: .leading)
            .padding(Space.xxl)
            .frame(maxWidth: .infinity, minHeight: geometry.size.height)
        }
        }
    }

    private func feature(_ symbol: String, _ title: String, _ detail: String) -> some View {
        HStack(alignment: .top, spacing: Space.m) {
            Image(systemName: symbol)
                .font(.title3)
                .foregroundStyle(.secondary)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.headline)
                Text(detail)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
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
        VStack(spacing: Space.l) {
            Image(systemName: "rectangle.stack")
                .font(.system(size: 34))
                .foregroundStyle(.secondary)
            Text("No run open")
                .font(.title3.weight(.semibold))
            if let suggestion {
                let facts = store.facts(suggestion.runId)
                Button(facts.needsYou ? "Open the Run That Needs You" : facts.isAlive ? "Open the Running Run" : "Open the Newest Run") {
                    store.selectedRunId = suggestion.runId
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                Text(RunTitle.short(task: suggestion.task, runId: suggestion.runId))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .frame(maxWidth: 420)
            }
            Grid(alignment: .leading, horizontalSpacing: Space.m, verticalSpacing: 4) {
                shortcut("↑ ↓", "Move through runs")
                shortcut("\(Keys.screen)  \(Keys.steps)", "Screen or steps")
                shortcut(Keys.conversation, "Show or hide the conversation")
                shortcut(Keys.nextFailure, "Next step that errored")
            }
            .font(.callout)
            .padding(.top, Space.s)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func shortcut(_ keys: String, _ action: String) -> some View {
        GridRow {
            Text(keys).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
            Text(action)
        }
    }
}
