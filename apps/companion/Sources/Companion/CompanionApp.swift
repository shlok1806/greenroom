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
        CompanionApp.main()
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
        // The spec's default, but never wider or taller than the screen it opens on: a
        // plain `defaultSize` opened 1320 pt wide on a 1024 pt screen (issue #64).
        .defaultWindowPlacement { _, context in
            WindowPlacement(size: RunLayout.defaultWindowSize(visible: context.defaultDisplay.visibleRect.size))
        }
        .commands {
            SidebarCommands()
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

    var body: some Commands {
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
