import AppKit

/// The window's key events, before AppKit hands them to the focused view: each is turned
/// into a `KeyChord` and offered to `KeyboardModel`, which takes it (an action ran, or a
/// sequence waits) or lets it through. The app's one key monitor; with `InputSurface`,
/// the only AppKit event code.
///
/// Driving is untouched: while `InputSurfaceView` has the keyboard every key goes through
/// to it, Cmd-Q included, exactly as before this router existed (ADR 0009). A text field
/// gets every key but the few a typing context owns (Cmd-K, esc).
@MainActor
final class KeyRouter {
    private let keyboard: KeyboardModel
    private var monitor: Any?
    private var observer: NSObjectProtocol?

    init(keyboard: KeyboardModel) {
        self.keyboard = keyboard
    }

    func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .leftMouseDown, .rightMouseDown]) { [weak self] event in
            let taken = MainActor.assumeIsolated { self?.takes(event) ?? false }
            return taken ? nil : event
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

    /// Whether the app takes the event; false lets AppKit deliver it as usual.
    private func takes(_ event: NSEvent) -> Bool {
        guard let window = event.window, AppDelegate.isRunWindow(window) else { return false }
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
