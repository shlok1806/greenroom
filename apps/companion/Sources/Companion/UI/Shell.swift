import AppKit
import SwiftUI

/// The window (companion ADR 0019): the runs sidebar and one run pane. A run is always open
/// when there is one; with none, or no Greenroom, the pane says what to do.
struct CompanionShell: View {
    @Bindable var shell: ShellModel
    @AppStorage("appearance", store: AppDefaults.shared) private var appearance = "system"
    @State private var width: CGFloat = Metrics.windowDefault.width
    /// The run that was open when Greenroom came back without it, until dismissed. Held here:
    /// the store forgets it as soon as another run opens, which happens at once.
    @State private var goneNotice: String?

    private var store: RunStore { shell.store }

    var body: some View {
        let windowClass = WindowClass.of(width: width)
        ZStack {
            if let summary = shell.summary, shell.driving {
                TakeControlView(shell: shell, summary: summary)
            } else {
                HStack(spacing: 0) {
                    if shell.sidebarHidden {
                        RunsRail(show: shell.toggleSidebar)
                    } else {
                        RunsSidebar(board: store.board, connection: store.connection, selected: store.selectedRunId,
                                    width: windowClass.sidebar, select: { shell.select(run: $0) },
                                    openSettings: { shell.settingsOpen = true },
                                    tasks: { [store] in store.searchTasks() },
                                    searchRequest: shell.searchRequest,
                                    updateBadge: MoreBadge.of(store.updates.summary))
                    }
                    VStack(spacing: 0) {
                        banners
                        pane(windowClass)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            if shell.detailsOpen, let summary = shell.summary {
                ZStack(alignment: .top) {
                    Palette.scrim.opacity(0.18).onTapGesture { shell.detailsOpen = false }.accessibilityHidden(true)
                    RunDetailsView(shell: shell, summary: summary)
                        .background(RoundedRectangle(cornerRadius: Corner.sheet).fill(Palette.bgRaised))
                        .overlay(RoundedRectangle(cornerRadius: Corner.sheet).strokeBorder(Palette.border, lineWidth: 1))
                        .shadow(color: .black.opacity(Elevation.raisedOpacity), radius: Elevation.raisedRadius / 2, y: Elevation.raisedY)
                        .padding(.top, 96)
                }
            }
            if shell.paletteOpen {
                PaletteOverlay(shell: shell)
            }
            // Drawn over the window, not a system sheet: a sheet whose presenter goes away
            // leaves the window unable to take a click (companion CLAUDE.md).
            if shell.settingsOpen {
                ZStack(alignment: .top) {
                    Palette.scrim.opacity(0.18).onTapGesture { shell.settingsOpen = false }.accessibilityHidden(true)
                    SettingsSheet(shell: shell).padding(.top, Gap.x48)
                }
            }
        }
        .overlay { DropdownLayer(center: shell.dropdowns) }
        .coordinateSpace(name: DropdownLayer.space)
        .ignoresSafeArea()
        .background(Palette.bg)
        .background(ShellWindowConfigurator())
        .background(OnScreenReader())
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .preferredColorScheme(appearance == "dark" ? .dark : (appearance == "light" ? .light : nil))
        .navigationTitle(shell.summary?.name ?? "Greenroom Companion")
        .onChange(of: store.board?.runs.count) { _, _ in shell.selectDefaultRunIfNeeded() }
        .onAppear { shell.selectDefaultRunIfNeeded() }
        .onChange(of: store.goneRun) { _, gone in if let gone { goneNotice = gone } }
    }

    /// Over the run pane: Greenroom not answering while what was loaded still shows (the old
    /// window's OfflineBanner), and a run that went away.
    @ViewBuilder
    private var banners: some View {
        switch store.connection {
        case .offline(hasData: true):
            StaleDataBanner(words: nil) { _ = await store.resync() }
        case .refused(let words, hasData: true):
            StaleDataBanner(words: words) { _ = await store.resync() }
        default:
            EmptyView()
        }
        if let goneNotice {
            NoticeBanner(text: "\(goneNotice) is no longer on Greenroom") { self.goneNotice = nil }
        }
    }

    @ViewBuilder
    private func pane(_ windowClass: WindowClass) -> some View {
        switch store.connection {
        case .connecting:
            ConnectingPane(address: store.daemonAddress)
        case .offline(hasData: false):
            DaemonOfflineView(refusal: nil) { _ = await store.resync() }
        case .refused(let words, hasData: false):
            DaemonOfflineView(refusal: words) { _ = await store.resync() }
        default:
            if let summary = shell.summary {
                if shell.evidenceOpen {
                    EvidenceViewer(shell: shell, summary: summary)
                } else {
                    RunPane(shell: shell, summary: summary, windowClass: windowClass)
                }
            } else if store.summariesMissing {
                CenteredMessage(title: "Update Greenroom", text: "This Greenroom is older than the app and does not say how its runs are going. Update it from Settings.", command: nil) { EmptyView() }
            } else if let board = store.board, board.runs.isEmpty {
                NoRunsView(address: store.client.baseURL.appending(path: "mcp").absoluteString)
            } else {
                Palette.bg
            }
        }
    }
}

/// Puts the window's traffic lights in the sidebar's 52 pt title row and keeps them there
/// (AppKit moves them back on many passes).
struct ShellWindowConfigurator: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView { Placer() }
    func updateNSView(_ view: NSView, context: Context) { (view as? Placer)?.place() }

    final class Placer: NSView {
        private var observers: [NSObjectProtocol] = []

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            observers.forEach(NotificationCenter.default.removeObserver)
            observers = []
            guard let window else { return }
            window.titlebarAppearsTransparent = true
            window.titleVisibility = .hidden
            window.styleMask.insert(.fullSizeContentView)
            window.isMovableByWindowBackground = false
            let names: [Notification.Name] = [
                NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification,
                NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification,
                NSWindow.didExitFullScreenNotification, NSWindow.didChangeScreenNotification,
                NSWindow.didUpdateNotification,
            ]
            window.titlebarSeparatorStyle = .none
            for name in names {
                observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                    MainActor.assumeIsolated { self?.place() }
                })
            }
            place()
            DispatchQueue.main.async { [weak self] in self?.place() }
        }

        /// Close, minimise and zoom at 20 pt from the left, centred on the 52 pt title row
        /// (the old window's `WindowConfigurator` did the same for its 44 pt bar).
        func place() {
            guard let window, !window.styleMask.contains(.fullScreen) else { return }
            let buttons: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
            for (index, type) in buttons.enumerated() {
                guard let button = window.standardWindowButton(type), let container = button.superview else { continue }
                let middle = container.isFlipped ? Metrics.toolbarHeight / 2 : container.bounds.height - Metrics.toolbarHeight / 2
                let origin = NSPoint(x: TrafficLights.inset + CGFloat(index) * TrafficLights.pitch,
                                     y: (middle - button.frame.height / 2).rounded())
                if button.frame.origin != origin { button.setFrameOrigin(origin) }
            }
        }
    }
}

/// Where the window's close, minimise and zoom buttons sit: 20 apart, from 20 in.
enum TrafficLights {
    static let inset: CGFloat = 20
    static let pitch: CGFloat = 20
    /// The room they take, with a little air after the zoom button.
    static let width: CGFloat = inset + 3 * pitch + Gap.x4
}

/// The runs sidebar folded away (Ctrl-Cmd-S): a strip just wide enough for the traffic
/// lights, so the run's toolbar never sits under them, and the button that brings the runs back.
struct RunsRail: View {
    var show: () -> Void

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                // The title row the traffic lights sit in.
                Color.clear.frame(height: Metrics.toolbarHeight)
                IconButton(icon: .sidebar, name: "Show runs (⌃⌘S)", action: show)
                Spacer(minLength: 0)
            }
            .frame(width: TrafficLights.width)
            Rectangle().fill(Palette.border).frame(width: 1)
        }
        .frame(maxHeight: .infinity)
        .background(Palette.bgSidebar)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Runs, hidden")
    }
}

/// The first read of Greenroom: where the app is looking, while it looks.
struct ConnectingPane: View {
    var address: String

    var body: some View {
        VStack(spacing: Gap.x8) {
            HStack(spacing: Gap.x8) {
                StatusGlyph(kind: .checking, color: .accent)
                Text("Connecting to Greenroom").textStyle(.bodyEmphasis).foregroundStyle(Palette.text)
            }
            Text(address).textStyle(.caption).foregroundStyle(Palette.textSecondary).textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.bg)
        .accessibilityElement(children: .combine)
    }
}

/// Greenroom stopped answering (or refused) while runs are loaded: they still show, maybe
/// stale, and one amber line says so, in Greenroom's words when it gave any.
struct StaleDataBanner: View {
    /// Greenroom's error when it answered with one; nil when nothing answered.
    var words: String?
    var reconnect: () async -> Void
    @State private var trying = false

    static func text(words: String?) -> String {
        guard let words, !words.isEmpty else { return "Greenroom is not answering. Showing what was last loaded." }
        return "\(words.hasSuffix(".") ? words : words + ".") Showing what was last loaded."
    }

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: .warning, color: .wait)
            Text(Self.text(words: words)).textStyle(.body).foregroundStyle(Palette.text).lineLimit(1).truncationMode(.tail)
                .help(Self.text(words: words))
            Spacer(minLength: Gap.x8)
            Button("Reconnect") {
                trying = true
                Task {
                    await reconnect()
                    trying = false
                }
            }
            .buttonStyle(ActionButtonStyle(kind: .plain, loading: trying))
            .disabled(trying)
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 40)
        .background(Palette.waitSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

/// One dismissible amber line over the run pane.
struct NoticeBanner: View {
    var text: String
    var dismiss: () -> Void

    var body: some View {
        HStack(spacing: Gap.x8) {
            StatusGlyph(kind: .warning, color: .wait)
            Text(text).textStyle(.body).foregroundStyle(Palette.text).lineLimit(1).help(text)
            Spacer(minLength: Gap.x8)
            IconButton(icon: .close, name: "Dismiss", action: dismiss)
        }
        .padding(.leading, Gap.x24)
        .padding(.trailing, Gap.x12)
        .frame(height: 40)
        .background(Palette.waitSubtle)
        .overlay(alignment: .bottom) { Rectangle().fill(Palette.border).frame(height: 1) }
    }
}

extension RunStore {
    /// Each run's task, for the sidebar's search: the first task message where the run's
    /// conversation is held, else the task the run list carries (clipped by Greenroom).
    func searchTasks() -> [String: String] {
        var out: [String: String] = [:]
        for run in runs {
            if let task = run.task, !task.isEmpty { out[run.runId] = task }
        }
        for (runId, held) in messages {
            if let task = held.first(where: { $0.kind == .task })?.text, !task.isEmpty { out[runId] = task }
        }
        return out
    }
}
