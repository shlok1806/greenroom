import AppKit
import SwiftUI

/// Turns the person's mouse and keyboard into actions for the machine (ADR 0009).
/// AppKit because SwiftUI gestures have no press/release, right button,
/// scroll wheel, click count or key codes.
struct InputSurface: NSViewRepresentable {
    /// In pixels; with the view's bounds it locates the letterbox.
    var imageSize: CGSize
    /// While false the surface takes no events at all.
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

/// Flipped, so its points already match `ScreenGeometry`'s top-left origin.
final class InputSurfaceView: NSView {
    var imageSize: CGSize = .zero
    var active = false
    var onActions: ([InputAction]) -> Void = { _ in }

    private var tracking: NSTrackingArea?

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { active }

    /// The click that focuses the window still goes to the guest.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { active }

    func setActive(_ newValue: Bool) {
        guard newValue != active else { return }
        active = newValue
        updateTrackingAreas()
        window?.invalidateCursorRects(for: self)
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
        if active {
            addCursorRect(bounds, cursor: .crosshair)
        } else {
            super.resetCursorRects()
        }
    }

    // MARK: - Mouse

    /// `nil` on the letterbox, unless `clamped`: a drag's moves and release
    /// must arrive even after the pointer leaves the picture.
    private func fraction(_ event: NSEvent, clamped: Bool = false) -> (x: Double, y: Double)? {
        let point = convert(event.locationInWindow, from: nil)
        let at = clamped
            ? ScreenGeometry.clampedFraction(at: point, image: imageSize, view: bounds.size)
            : ScreenGeometry.fraction(at: point, image: imageSize, view: bounds.size)
        return at.map { (Double($0.x), Double($0.y)) }
    }

    private func send(_ action: InputAction) {
        onActions([action])
    }

    override func mouseDown(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.mouseDown(with: event) }
        send(InputAction(type: .down, x: at.x, y: at.y, button: "left", clicks: event.clickCount))
    }

    override func mouseDragged(with event: NSEvent) {
        guard active, let at = fraction(event, clamped: true) else { return super.mouseDragged(with: event) }
        send(InputAction(type: .move, x: at.x, y: at.y))
    }

    override func mouseUp(with event: NSEvent) {
        guard active, let at = fraction(event, clamped: true) else { return super.mouseUp(with: event) }
        send(InputAction(type: .up, x: at.x, y: at.y, button: "left", clicks: event.clickCount))
    }

    override func mouseMoved(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.mouseMoved(with: event) }
        send(InputAction(type: .move, x: at.x, y: at.y))
    }

    override func rightMouseDown(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.rightMouseDown(with: event) }
        send(InputAction(type: .down, x: at.x, y: at.y, button: "right", clicks: event.clickCount))
    }

    override func rightMouseDragged(with event: NSEvent) {
        guard active, let at = fraction(event, clamped: true) else { return super.rightMouseDragged(with: event) }
        send(InputAction(type: .move, x: at.x, y: at.y))
    }

    override func rightMouseUp(with event: NSEvent) {
        guard active, let at = fraction(event, clamped: true) else { return super.rightMouseUp(with: event) }
        send(InputAction(type: .up, x: at.x, y: at.y, button: "right", clicks: event.clickCount))
    }

    /// The guest helper knows left, right and middle; buttons 4 and up have nowhere to go.
    private static let middleButton = 2

    override func otherMouseDown(with event: NSEvent) {
        guard active, event.buttonNumber == Self.middleButton, let at = fraction(event) else {
            return super.otherMouseDown(with: event)
        }
        send(InputAction(type: .down, x: at.x, y: at.y, button: "middle", clicks: event.clickCount))
    }

    override func otherMouseDragged(with event: NSEvent) {
        guard active, event.buttonNumber == Self.middleButton, let at = fraction(event, clamped: true) else {
            return super.otherMouseDragged(with: event)
        }
        send(InputAction(type: .move, x: at.x, y: at.y))
    }

    override func otherMouseUp(with event: NSEvent) {
        guard active, event.buttonNumber == Self.middleButton, let at = fraction(event, clamped: true) else {
            return super.otherMouseUp(with: event)
        }
        send(InputAction(type: .up, x: at.x, y: at.y, button: "middle", clicks: event.clickCount))
    }

    override func scrollWheel(with event: NSEvent) {
        guard active, let at = fraction(event) else { return super.scrollWheel(with: event) }
        send(InputBatch.scroll(
            x: at.x,
            y: at.y,
            appKitDeltaX: Double(event.scrollingDeltaX),
            appKitDeltaY: Double(event.scrollingDeltaY)
        ))
    }

    // MARK: - Keyboard

    /// Command shortcuts reach the view hierarchy here before the menu bar,
    /// so while driving, Cmd-Q or Cmd-W go to the guest, not to this app.
    /// Switching "Take control" off with the mouse is the way out.
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard active, event.type == .keyDown, window?.firstResponder === self,
              let action = KeyTranslator.action(for: InputSurfaceView.stroke(from: event)) else {
            return super.performKeyEquivalent(with: event)
        }
        send(action)
        return true
    }

    override func keyDown(with event: NSEvent) {
        guard active, let action = KeyTranslator.action(for: InputSurfaceView.stroke(from: event)) else {
            return super.keyDown(with: event)
        }
        send(action)
    }

    /// Every action that needs a modifier names it itself.
    override func flagsChanged(with event: NSEvent) {}

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
