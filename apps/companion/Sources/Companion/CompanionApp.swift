import AppKit
import SwiftUI

/// An unbundled `swift run` binary is treated as a background process and its
/// window never takes keyboard focus; this makes it a regular app. Harmless
/// in the real bundle.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
}

@main
struct CompanionApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var store = RunStore()

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
        // Give back any screen being driven (ADR 0009).
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in
            Task { await store.releaseAllControl() }
        }
    }
}
