import AppKit
import SwiftUI

/// Owns the store so quitting can give back any screen being driven (ADR 0009).
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let store = RunStore()
    private var quitting = false

    /// An unbundled `swift run` binary is treated as a background process and
    /// its window never takes keyboard focus; this makes it a regular app.
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        #if DEBUG
        SnapshotHook.runIfAsked(store: store)
        #endif
        // The run window is fitted to its screen the first time it shows after launch, when
        // macOS may have restored it off screen, and whenever it moves to another screen.
        for name in [NSWindow.didBecomeKeyNotification, NSWindow.didChangeScreenNotification] {
            NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { [weak self] note in
                // NSWindow is Sendable; windowNumber is read on the main actor, below.
                let window = note.object as? NSWindow
                MainActor.assumeIsolated {
                    guard let self, let window, Self.isRunWindow(window) else { return }
                    if name == NSWindow.didBecomeKeyNotification {
                        guard !self.fittedAfterLaunch else { return }
                        self.fittedAfterLaunch = true
                    }
                    Self.keepOnScreen(window)
                }
            }
        }
    }

    private var fittedAfterLaunch = false

    /// The `main` scene's window. Sheets, panels, alerts and popovers are smaller than the
    /// run window's minimum and must keep their own size.
    static func isRunWindow(_ window: NSWindow) -> Bool {
        guard let id = window.identifier?.rawValue, id == "main" || id.hasPrefix("main-") else { return false }
        return !(window is NSPanel) && window.sheetParent == nil
            && window.styleMask.contains(.titled) && window.styleMask.contains(.resizable)
    }

    /// Fits a restored window that is wider or taller than its screen back onto it (#64).
    static func keepOnScreen(_ window: NSWindow) {
        guard let visible = (window.screen ?? NSScreen.main)?.visibleFrame else { return }
        let fitted = RunLayout.fitted(frame: window.frame, visible: visible)
        if fitted != window.frame { window.setFrame(fitted, display: true) }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    /// The process would exit before an async release ran, so quitting waits
    /// for it, but never more than two seconds on a daemon that is not answering.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard store.holdsControl else { return .terminateNow }
        guard !quitting else { return .terminateLater }
        quitting = true
        Task {
            await store.releaseAllControl()
            finishQuitting()
        }
        Task {
            try? await Task.sleep(for: .seconds(2))
            finishQuitting()
        }
        return .terminateLater
    }

    private func finishQuitting() {
        guard quitting else { return }
        quitting = false
        NSApp.reply(toApplicationShouldTerminate: true)
    }
}

/// The app's one entry point, called from the `CompanionApp` executable. The scene
/// lives here, in the library, so `CompanionSnapshots` can host the same views.
public enum CompanionMain {
    @MainActor
    public static func run() {
        LegacyDefaults.forget(in: .standard)
        BundledFonts.ensureRegistered()
        CompanionApp.main()
    }
}

/// Settings an older build saved that this one must not inherit.
enum LegacyDefaults {
    /// `verdictCardExpanded` opened every verdict card's evidence and survived relaunch
    /// (#52). Nothing reads it now; removing it keeps a stuck install from carrying it on.
    static let keys = ["verdictCardExpanded"]

    static func forget(in defaults: UserDefaults) {
        for key in keys { defaults.removeObject(forKey: key) }
    }
}

struct CompanionApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    private var store: RunStore { delegate.store }

    var body: some Scene {
        // One window: selection lives in the store, so a second window would mirror it.
        Window("Greenroom Companion", id: "main") {
            RootView(store: store)
                .frame(minWidth: RunLayout.windowMinimum.width, minHeight: RunLayout.windowMinimum.height)
                .task { store.start() }
        }
        // Own chrome (ADR 0004, 0008): the content fills the window under a transparent
        // title bar; `RootView` draws the top bar.
        .windowStyle(.hiddenTitleBar)
        // The spec's default, but never wider or taller than the screen it opens on: a
        // plain `defaultSize` opened 1320 pt wide on a 1024 pt screen (issue #64).
        .defaultWindowPlacement { _, context in
            WindowPlacement(size: RunLayout.defaultWindowSize(visible: context.defaultDisplay.visibleRect.size))
        }
        .commands {
            RunMenuCommands(store: store)
        }
    }
}

/// The View and Run menus. They act on the open run through `RunCommands` and
/// `ScreenCommands`, which the run's views publish while they are on screen.
struct RunMenuCommands: Commands {
    let store: RunStore

    @FocusedValue(\.runCommands) private var run
    @FocusedValue(\.screenCommands) private var screen
    @FocusedValue(\.sidebarShown) private var sidebarShown
    @AppStorage(ThemePreference.key) private var theme: ThemePreference = .system

    var body: some Commands {
        // The window has its own sidebar, not a split view's: its toggle, on the same key.
        CommandGroup(before: .toolbar) {
            Button(sidebarShown?.wrappedValue == false ? "Show Sidebar" : "Hide Sidebar") {
                withAnimation(.snappy) { sidebarShown?.wrappedValue.toggle() }
            }
            .keyboardShortcut("s", modifiers: [.control, .command])
            .disabled(sidebarShown == nil)
            Picker("Theme", selection: $theme) {
                ForEach(ThemePreference.allCases, id: \.self) { choice in
                    Text(choice.title).tag(choice)
                }
            }
            Divider()
        }
        CommandGroup(after: .toolbar) {
            Button("Screen") { run?.pane.wrappedValue = .screen }
                .keyboardShortcut("1", modifiers: .command)
                .disabled(run == nil)
            Button("Steps") { run?.pane.wrappedValue = .steps }
                .keyboardShortcut("2", modifiers: .command)
                .disabled(run == nil)
            Button(run?.showsConversation.wrappedValue == false ? "Show Conversation" : "Hide Conversation") {
                run?.showsConversation.wrappedValue.toggle()
            }
            .keyboardShortcut("0", modifiers: [.command, .option])
            .disabled(run == nil)
            Divider()
            Button("Refresh") {
                Task { await store.resync() }
            }
            .keyboardShortcut("r", modifiers: .command)
            Divider()
        }
        CommandMenu("Run") {
            Button("Follow Live") { screen?.goLive?() }
                .keyboardShortcut("l", modifiers: .command)
                .disabled(screen?.goLive == nil)
            Button(run?.controlTitle ?? "Take Control") { run?.control?() }
                .keyboardShortcut("t", modifiers: [.command, .shift])
                .disabled(run?.control == nil)
            Divider()
            Button("Next Failure") { run?.nextFailure?() }
                .keyboardShortcut("'", modifiers: .command)
                .disabled(run?.nextFailure == nil)
            Button("Previous Failure") { run?.previousFailure?() }
                .keyboardShortcut("'", modifiers: [.command, .shift])
                .disabled(run?.previousFailure == nil)
            Divider()
            Button("Capture Screenshot") { run?.capture?() }
                .keyboardShortcut("s", modifiers: [.command, .shift])
                .disabled(run?.capture == nil)
            Button("Export Recording...") { run?.export?() }
                .keyboardShortcut("e", modifiers: [.command, .shift])
                .disabled(run?.export == nil)
            Divider()
            Button("Destroy Machine...") { run?.destroy?() }
                .keyboardShortcut(.delete, modifiers: .command)
                .disabled(run?.destroy == nil)
        }
    }
}
