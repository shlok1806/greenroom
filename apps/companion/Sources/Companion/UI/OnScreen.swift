import AppKit
import SwiftUI

/// Whether anyone can see the run window (companion ADR 0021): false while it is minimised,
/// the app is hidden, the window is on another Space or wholly covered. Every clock in the
/// window (`Clocked`, the periodic `TimelineView`s) and the live screen stop while it is
/// false, and pick up from the time when it turns true. One object rather than an
/// environment value, so the runs table's own hosting views see it too.
@Observable
@MainActor
final class OnScreen {
    static let shared = OnScreen()

    var visible = true
}

/// Follows the run window's `occlusionState` into `OnScreen.shared`. Placed once, at the
/// shell's root; a window that is not the run window (a test's, the snapshot harness's)
/// changes nothing.
struct OnScreenReader: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView { Probe() }
    func updateNSView(_ view: NSView, context: Context) {}

    final class Probe: NSView {
        private var observer: NSObjectProtocol?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            if let observer { NotificationCenter.default.removeObserver(observer) }
            observer = nil
            guard let window, AppDelegate.isRunWindow(window) else { return }
            observer = NotificationCenter.default.addObserver(forName: NSWindow.didChangeOcclusionStateNotification,
                                                              object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.update() }
            }
            update()
        }

        private func update() {
            guard let window else { return }
            let visible = window.occlusionState.contains(.visible)
            if OnScreen.shared.visible != visible { OnScreen.shared.visible = visible }
        }
    }
}
