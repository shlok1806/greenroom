import AppKit
import SwiftUI

/// The layer that turns a person's mouse and keyboard into actions for the
/// machine (ADR 0009).
///
/// It is AppKit rather than SwiftUI gestures for one reason: driving a screen
/// needs the raw events. A SwiftUI `DragGesture` cannot tell a press from a
/// release, has no right button, no scroll wheel, no click count and no key
/// codes. An `NSView` has all of them.
///
/// It knows nothing about the daemon. It converts a point in itself into a
/// fraction of the guest's screen (`ScreenGeometry`) and hands the actions to
/// its owner.
struct InputSurface: NSViewRepresentable {
    /// The size of the frame being shown, in pixels. The surface is the same
    /// rectangle as the picture's container, so this is what says where the
    /// letterbox is.
    var imageSize: CGSize
    /// False while the app is only watching: the surface then takes no events
    /// at all, and clicks fall through to the scrubber as before.
    var active: Bool
    var onActions: ([InputAction]) -> Void

    func makeNSView(context: Context) -> InputSurfaceView {
        let view = InputSurfaceView()
        view.imageSize = imageSize
        view.active = active
        view.onActions = onActions
        return view
    }

    func updateNSView(_ view: InputSurfaceView, context: Context) {
        view.imageSize = imageSize
        view.onActions = onActions
        view.setActive(active)
    }
}

/// The view itself. Top-left origin (`isFlipped`), so a point in it is in the
/// same direction as a point on the guest's screen and the arithmetic in
/// `ScreenGeometry` needs no flipping.
final class InputSurfaceView: NSView {
    var imageSize: CGSize = .zero
    var active = false
    var onActions: ([InputAction]) -> Void = { _ in }

    private var tracking: NSTrackingArea?

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { active }

    /// A click that gives the surface focus is still a click on the machine:
    /// the person aimed at something in the guest, not at this window.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { active }

    func setActive(_ newValue: Bool) {
        guard newValue != active else { return }
        active = newValue
        updateTrackingAreas()
        if active {
            window?.makeFirstResponder(self)
        } else if window?.firstResponder === self {
            window?.makeFirstResponder(nil)
        }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if active { window?.makeFirstResponder(self) }
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
            self.tracking = nil
        }
        guard active else { return }
        let area = NSTrackingArea(
            rect: .zero,
            options: [.activeInKeyWindow, .inVisibleRect, .mouseMoved, .mouseEnteredAndExited],
            owner: self,
            userInfo: nil
        )
        addTrackingArea(area)
        tracking = area
    }

    override func resetCursorRects() {
        // A crosshair says "this click goes to the machine", which is the one
        // thing a person needs to know before they click.
        if active {
            addCursorRect(bounds, cursor: .crosshair)
        } else {
            super.resetCursorRects()
        }
    }

    // MARK: - Where a point is

    /// The event's position as a fraction of the guest's screen, or nil when
    /// it landed on the letterbox rather than on the picture.
    private func fraction(_ event: NSEvent) -> (x: Double, y: Double)? {
        let point = convert(event.locationInWindow, from: nil)
        guard let at = ScreenGeometry.fraction(at: point, image: imageSize, view: bounds.size) else {
            return nil
        }
        return (Double(at.x), Double(at.y))
    }

    /// The same, for the events that must arrive even when the pointer has
    /// left the picture: the moves and the release of a drag.
    private func dragFraction(_ event: NSEvent) -> (x: Double, y: Double)? {
        let point = convert(event.locationInWindow, from: nil)
        guard let at = ScreenGeometry.clampedFraction(at: point, image: imageSize, view: bounds.size) else {
            return nil
        }
        return (Double(at.x), Double(at.y))
    }

    private func send(_ actions: [InputAction]) {
        guard active, !actions.isEmpty else { return }
        onActions(actions)
    }

    // MARK: - Mouse

    override func mouseDown(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.mouseDown(with: event) }
        send([InputAction(type: .down, x: at.x, y: at.y, button: "left", clicks: event.clickCount)])
    }

    override func mouseDragged(with event: NSEvent) {
        guard active, let at = dragFraction(event) else { return super.mouseDragged(with: event) }
        send([InputAction(type: .move, x: at.x, y: at.y)])
    }

    override func mouseUp(with event: NSEvent) {
        guard active, let at = dragFraction(event) else { return super.mouseUp(with: event) }
        send([InputAction(type: .up, x: at.x, y: at.y, button: "left", clicks: event.clickCount)])
    }

    override func mouseMoved(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.mouseMoved(with: event) }
        // Hovering matters: menus open, tooltips appear, buttons highlight.
        send([InputAction(type: .move, x: at.x, y: at.y)])
    }

    override func rightMouseDown(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.rightMouseDown(with: event) }
        send([InputAction(type: .down, x: at.x, y: at.y, button: "right", clicks: event.clickCount)])
    }

    override func rightMouseUp(with event: NSEvent) {
        guard active, let at = dragFraction(event) else { return super.rightMouseUp(with: event) }
        send([InputAction(type: .up, x: at.x, y: at.y, button: "right", clicks: event.clickCount)])
    }

    override func scrollWheel(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.scrollWheel(with: event) }
        send([InputAction(
            type: .scroll,
            x: at.x,
            y: at.y,
            deltaX: Double(event.scrollingDeltaX),
            deltaY: Double(event.scrollingDeltaY)
        )])
    }

    // MARK: - Keyboard

    override func keyDown(with event: NSEvent) {
        guard active, let action = KeyTranslator.action(for: InputSurfaceView.stroke(from: event)) else {
            return super.keyDown(with: event)
        }
        send([action])
    }

    /// Modifier keys on their own change nothing in the guest until another
    /// key or a click carries them, and every action that needs them names
    /// them itself.
    override func flagsChanged(with event: NSEvent) {}

    /// Reads an `NSEvent` into the value type the translator works on, which
    /// is what lets the translation be tested without AppKit.
    static func stroke(from event: NSEvent) -> KeyStroke {
        let flags = event.modifierFlags
        return KeyStroke(
            characters: event.characters ?? "",
            charactersIgnoringModifiers: event.charactersIgnoringModifiers ?? "",
            keyCode: event.keyCode,
            command: flags.contains(.command),
            shift: flags.contains(.shift),
            option: flags.contains(.option),
            control: flags.contains(.control),
            function: flags.contains(.function)
        )
    }
}
