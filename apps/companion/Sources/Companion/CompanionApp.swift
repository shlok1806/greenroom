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

@main
struct CompanionApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    private var store: RunStore { delegate.store }

    var body: some Scene {
        WindowGroup {
            RootView(store: store)
                .frame(minWidth: 900, minHeight: 560)
                .task { store.start() }
        }
        .commands {
            CommandGroup(after: .toolbar) {
                Button("Refresh") {
                    Task { await store.resync() }
                }
                .keyboardShortcut("r", modifiers: .command)
            }
        }
    }
}

struct RootView: View {
    let store: RunStore

    var body: some View {
        NavigationSplitView {
            SidebarView(store: store)
                .navigationSplitViewColumnWidth(min: 220, ideal: 260)
        } detail: {
            if let runId = store.selectedRunId {
                RunView(store: store, runId: runId)
            } else {
                ContentUnavailableView(
                    "Pick a run",
                    systemImage: "sidebar.left",
                    description: Text("Runs are listed newest first.")
                )
            }
        }
        // The SSE socket can look alive after sleep while the daemon restarted.
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.resync() }
        }
    }
}
