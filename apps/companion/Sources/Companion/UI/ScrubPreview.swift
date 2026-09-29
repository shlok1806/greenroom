import AppKit
import SwiftUI

/// The scrub bar's hover preview (companion ADR 0023): whether the frame-and-line card shows
/// above the bar, where, and every way it goes away. Pure; `ScrubPreviewTests` hold each rule.
///
/// It shows while the pointer is over the track or a press on it is held. It hides when the
/// pointer leaves (unless a drag holds it), when a drag ends away from the track, and on
/// `dismissed`: the window stopped being key, the app went to the back, the window went off
/// screen, a scroll arrived or the run changed. Once hidden it shows again only on the next
/// pointer move over the track, so nothing the pointer did before can leave it standing.
struct ScrubPreview: Equatable, Sendable {
    enum Event: Equatable, Sendable {
        /// The pointer moved over the track, `x` points from its leading edge.
        case hovered(x: CGFloat, width: CGFloat)
        /// The pointer left the track.
        case exited
        /// A press or drag on the track, at `x`.
        case dragged(x: CGFloat, width: CGFloat)
        /// The press or drag ended, over the track or away from it.
        case released(inside: Bool)
        /// Anything that means nobody is pointing at the bar any more.
        case dismissed
    }

    /// Where the card points, in points from the track's leading edge; nil while hidden.
    private(set) var x: CGFloat?
    /// A press on the track is held.
    private(set) var dragging = false

    var shows: Bool { x != nil }

    mutating func handle(_ event: Event) {
        switch event {
        case .hovered(let x, let width):
            self.x = Self.clamp(x, width)
        case .exited:
            if !dragging { x = nil }
        case .dragged(let x, let width):
            dragging = true
            self.x = Self.clamp(x, width)
        case .released(let inside):
            dragging = false
            if !inside { x = nil }
        case .dismissed:
            dragging = false
            x = nil
        }
    }

    /// Whether a point in the track's own space is over it.
    static func inside(_ point: CGPoint, width: CGFloat, height: CGFloat) -> Bool {
        point.x >= 0 && point.x <= width && point.y >= 0 && point.y <= height
    }

    private static func clamp(_ x: CGFloat, _ width: CGFloat) -> CGFloat { min(max(0, x), max(0, width)) }
}

/// Hands `dismiss` every event after which nobody is pointing at the scrub bar: its window
/// resigns key or is minimised, the app resigns active or hides. Placed behind the track;
/// `OnScreen` (off screen), a scroll (`Keys`, the window's one event monitor) and a run
/// change are handled where they happen.
struct ScrubPreviewDismisser: NSViewRepresentable {
    var dismiss: () -> Void

    func makeNSView(context: Context) -> Probe {
        let probe = Probe()
        probe.dismiss = dismiss
        return probe
    }

    func updateNSView(_ probe: Probe, context: Context) { probe.dismiss = dismiss }

    static func dismantleNSView(_ probe: Probe, coordinator: ()) { probe.stop() }

    final class Probe: NSView {
        var dismiss: () -> Void = {}
        private var observers: [NSObjectProtocol] = []

        override func hitTest(_ point: NSPoint) -> NSView? { nil }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            stop()
            guard let window else { return }
            let center = NotificationCenter.default
            let fire: @Sendable (Notification) -> Void = { [weak self] _ in
                MainActor.assumeIsolated { self?.dismiss() }
            }
            observers = [
                center.addObserver(forName: NSWindow.didResignKeyNotification, object: window, queue: .main, using: fire),
                center.addObserver(forName: NSWindow.didMiniaturizeNotification, object: window, queue: .main, using: fire),
                center.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main, using: fire),
                center.addObserver(forName: NSApplication.didHideNotification, object: nil, queue: .main, using: fire),
            ]
        }

        func stop() {
            observers.forEach(NotificationCenter.default.removeObserver)
            observers = []
        }
    }
}
