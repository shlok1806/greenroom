import AppKit

/// The window's key events, before AppKit hands them to the focused view: each is turned
/// into a `KeyChord` and offered to `KeyboardModel`, which takes it (an action ran, or a
/// sequence waits) or lets it through. The app's one key monitor; with `InputSurface`,
/// the only AppKit event code.
///
/// Driving is untouched: while `InputSurfaceView` has the keyboard every key goes through
/// to it, Cmd-Q included, exactly as before this router existed (ADR 0009). A text field
/// gets every key but the few a typing context owns (Cmd-K, esc).
///
/// In a snapshot (`SnapshotMode`) the window belongs to no one: every real key, click,
/// scroll and hover is swallowed, and only the keys `SnapshotHook` hands to `inject`
/// drive it.
@MainActor
final class KeyRouter {
    private let keyboard: KeyboardModel
    private let snapshot: Bool
    private var monitor: Any?
    private var observer: NSObjectProtocol?

    init(keyboard: KeyboardModel, snapshot: Bool = SnapshotMode.isActive) {
        self.keyboard = keyboard
        self.snapshot = snapshot
    }

    /// What a person's hands can send a window. A snapshot swallows all of it.
    static let personEvents: NSEvent.EventTypeMask = [
        .keyDown, .keyUp, .flagsChanged,
        .leftMouseDown, .leftMouseUp, .leftMouseDragged,
        .rightMouseDown, .rightMouseUp, .rightMouseDragged,
        .otherMouseDown, .otherMouseUp, .otherMouseDragged,
        .mouseMoved, .mouseEntered, .mouseExited, .cursorUpdate,
        .scrollWheel, .magnify, .rotate, .swipe, .smartMagnify, .pressure,
        .tabletPoint, .tabletProximity, .gesture, .beginGesture, .endGesture, .directTouch,
    ]

    func install() {
        guard monitor == nil else { return }
        let mask: NSEvent.EventTypeMask = snapshot ? Self.personEvents : [.keyDown, .leftMouseDown, .rightMouseDown]
        monitor = NSEvent.addLocalMonitorForEvents(matching: mask) { [weak self] event in
            let passes = MainActor.assumeIsolated { self?.passes(event) ?? true }
            return passes ? event : nil
        }
        // After every event the app handles, so the hint bar follows focus moved by a
        // click, a field focusing itself, or the lease being taken.
        observer = NotificationCenter.default.addObserver(forName: NSWindow.didUpdateNotification, object: nil, queue: .main) { [weak self] note in
            let window = note.object as? NSWindow
            MainActor.assumeIsolated {
                guard let self, let window, window.isKeyWindow, AppDelegate.isRunWindow(window) else { return }
                self.follow(window)
            }
        }
    }

    /// Whether an event from the app's queue goes on to AppKit: not when the app takes
    /// it, and in a snapshot never, since no real event may reach the window.
    func passes(_ event: NSEvent) -> Bool {
        if snapshot { return false }
        return !takes(event)
    }

    /// A snapshot's key (`GREENROOM_SNAPSHOT_KEYS`), routed as a typed one would be: the
    /// router first, then the window's own responders. It never passes through the app's
    /// queue, so the monitor's refusal of real events cannot catch it, and the window
    /// need not be key.
    func inject(_ event: NSEvent, into window: NSWindow) {
        if takes(event, in: window) { return }
        window.sendEvent(event)
    }

    /// Whether the app takes the event; false lets AppKit deliver it as usual.
    private func takes(_ event: NSEvent, in target: NSWindow? = nil) -> Bool {
        guard let window = target ?? event.window, AppDelegate.isRunWindow(window) else { return false }
        follow(window)
        switch event.type {
        case .keyDown:
            let chord = KeyChord(stroke: InputSurfaceView.stroke(from: event))
            return keyboard.handle(chord, responder: Self.responder(in: window))
        case .leftMouseDown, .rightMouseDown:
            // Window points, top-left origin, as SwiftUI's global frames are.
            let height = window.contentView?.bounds.height ?? window.frame.height
            keyboard.focusPane(at: CGPoint(x: event.locationInWindow.x, y: height - event.locationInWindow.y))
            return false
        default:
            return false
        }
    }

    private func follow(_ window: NSWindow) {
        let responder = Self.responder(in: window)
        if keyboard.responder != responder { keyboard.responder = responder }
        keyboard.endEditing = { [weak window] in
            guard let window else { return }
            window.makeFirstResponder(nil)
        }
    }

    /// Where the window's keys are going now.
    static func responder(in window: NSWindow) -> KeyResponder {
        switch window.firstResponder {
        case let surface as InputSurfaceView where surface.active: .guest
        case let text as NSTextView where text.isEditable: .text
        case is NSTextField: .text
        default: .other
        }
    }
}
