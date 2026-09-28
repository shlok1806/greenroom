import AppKit
import SwiftUI

/// The window (companion ADR 0019): the runs sidebar and one run pane. A run is always open
/// when there is one; with none, or no Greenroom, the pane says what to do.
struct CompanionShell: View {
    @Bindable var shell: ShellModel
    @AppStorage("appearance", store: AppDefaults.shared) private var appearance = "system"
    @State private var width: CGFloat = Metrics.windowDefault.width

    private var store: RunStore { shell.store }

    var body: some View {
        let windowClass = WindowClass.of(width: width)
        ZStack {
            if let summary = shell.summary, shell.driving {
                TakeControlView(shell: shell, summary: summary)
            } else {
                HStack(spacing: 0) {
                    RunsSidebar(board: store.board, connection: store.connection, selected: store.selectedRunId,
                                width: windowClass.sidebar, select: { shell.select(run: $0) },
                                openSettings: { shell.settingsOpen = true })
                    pane(windowClass)
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
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .preferredColorScheme(appearance == "dark" ? .dark : (appearance == "light" ? .light : nil))
        .navigationTitle(shell.summary?.name ?? "Greenroom Companion")
        .onChange(of: store.board?.runs.count) { _, _ in shell.selectDefaultRunIfNeeded() }
        .onAppear { shell.selectDefaultRunIfNeeded() }
    }

    @ViewBuilder
    private func pane(_ windowClass: WindowClass) -> some View {
        switch store.connection {
        case .offline(hasData: false):
            DaemonOfflineView(refusal: nil)
        case .refused(let words, hasData: false):
            DaemonOfflineView(refusal: words)
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
                let origin = NSPoint(x: 20 + CGFloat(index) * 20, y: (middle - button.frame.height / 2).rounded())
                if button.frame.origin != origin { button.setFrameOrigin(origin) }
            }
        }
    }
}
