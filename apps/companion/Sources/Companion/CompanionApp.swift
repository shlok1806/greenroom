import AppKit
import SwiftUI

/// This matters only when the app is run unbundled, as `swift run` does: a
/// bare SwiftPM executable has no app bundle, so macOS treats it as a
/// background process and the window shows but never takes keyboard focus.
/// Making it a regular app and activating it fixes typing into the composer.
/// The bundle `scripts/bundle.sh` builds needs none of this, and is unharmed
/// by it.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
}

/// greenroom's companion: watch runs, see the screen, talk to the agents. It
/// speaks to the daemon's HTTP API and to nothing else (ADR 0007).
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
                    Task {
                        await store.refresh()
                        if let runId = store.selectedRunId {
                            await store.select(runId)
                        }
                    }
                }
                .keyboardShortcut("r", modifiers: .command)
            }
        }
    }
}

struct RootView: View {
    @Bindable var store: RunStore

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
        // A daemon that restarted while the window was elsewhere, or in the
        // background, is not caught by the SSE reconnect alone: the socket
        // can look alive after a sleep/wake. Coming to the foreground always
        // resyncs (RunStore.resyncPlan).
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await store.resync() }
        }
        // Quitting gives back any screen this app was driving (ADR 0009).
        // The daemon expires a lease on its own, so this only shortens the
        // minute a machine would otherwise wait.
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willTerminateNotification)) { _ in
            Task { await store.releaseAllControl() }
        }
    }
}
