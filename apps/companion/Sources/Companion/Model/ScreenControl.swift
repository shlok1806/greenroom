import Foundation

/// Maps a point on the letterboxed (`scaledToFit`) picture to a fraction of the
/// guest's screen (ADR 0009). Top-left origin throughout.
enum ScreenGeometry {
    /// The centred rectangle a `scaledToFit` image occupies in `view`.
    static func fitted(image: CGSize, in view: CGSize) -> CGRect {
        guard image.width > 0, image.height > 0, view.width > 0, view.height > 0 else {
            return .zero
        }
        let scale = min(view.width / image.width, view.height / image.height)
        let size = CGSize(width: image.width * scale, height: image.height * scale)
        return CGRect(
            x: ((view.width - size.width) / 2).rounded(),
            y: ((view.height - size.height) / 2).rounded(),
            width: size.width,
            height: size.height
        )
    }

    /// `nil` when `point` is on the letterbox rather than the picture.
    static func fraction(at point: CGPoint, image: CGSize, view: CGSize) -> CGPoint? {
        let rect = fitted(image: image, in: view)
        guard rect.width > 0, rect.height > 0, rect.contains(point) else { return nil }
        return CGPoint(
            x: (point.x - rect.minX) / rect.width,
            y: (point.y - rect.minY) / rect.height
        )
    }

    /// The other way: where a screen fraction (a step's click) sits on the picture in
    /// `view`. `nil` before there is a picture to place it on. Fractions outside 0 to 1
    /// are clamped onto the picture's edge.
    static func point(atFraction fraction: CGPoint, image: CGSize, view: CGSize) -> CGPoint? {
        let rect = fitted(image: image, in: view)
        guard rect.width > 0, rect.height > 0 else { return nil }
        return CGPoint(
            x: rect.minX + min(max(fraction.x, 0), 1) * rect.width,
            y: rect.minY + min(max(fraction.y, 0), 1) * rect.height
        )
    }

    /// Clamped onto the picture instead of refused: a drag's moves and release
    /// must arrive wherever the pointer went, or the guest's button stays held.
    static func clampedFraction(at point: CGPoint, image: CGSize, view: CGSize) -> CGPoint? {
        let rect = fitted(image: image, in: view)
        guard rect.width > 0, rect.height > 0 else { return nil }
        return CGPoint(
            x: min(max((point.x - rect.minX) / rect.width, 0), 1),
            y: min(max((point.y - rect.minY) / rect.height, 0), 1)
        )
    }
}

/// A key press without AppKit, so `KeyTranslator` is testable.
struct KeyStroke: Equatable, Sendable {
    var characters: String
    var charactersIgnoringModifiers: String
    var keyCode: UInt16
    var command = false
    var shift = false
    var option = false
    var control = false
    var function = false

    var modifiers: [String] {
        var mods: [String] = []
        if command { mods.append("cmd") }
        if shift { mods.append("shift") }
        if option { mods.append("alt") }
        if control { mods.append("ctrl") }
        if function { mods.append("fn") }
        return mods
    }

    /// Command-A must arrive as the A key plus command to mean Select All.
    var isShortcut: Bool { command || control }
}

enum KeyTranslator {
    /// Keys with no character to type, by virtual key code. Everything else
    /// types its characters, so any keyboard layout works unmodelled.
    static let named: [UInt16: String] = [
        36: "return", 76: "enter", 48: "tab", 51: "delete", 53: "escape",
        117: "forwarddelete", 115: "home", 119: "end", 116: "pageup", 121: "pagedown",
        123: "left", 124: "right", 125: "down", 126: "up",
        122: "f1", 120: "f2", 99: "f3", 118: "f4", 96: "f5", 97: "f6", 98: "f7",
        100: "f8", 101: "f9", 109: "f10", 103: "f11", 111: "f12",
    ]

    static func action(for stroke: KeyStroke) -> InputAction? {
        if let name = named[stroke.keyCode] {
            return InputAction(type: .key, key: name, mods: nilIfEmpty(stroke.modifiers))
        }
        if stroke.isShortcut {
            let name = stroke.charactersIgnoringModifiers.lowercased()
            guard !name.isEmpty else { return nil }
            return InputAction(type: .key, key: name, mods: nilIfEmpty(stroke.modifiers))
        }
        guard !stroke.characters.isEmpty else { return nil }
        // Shift is already in the characters.
        return InputAction(type: .type, text: stroke.characters)
    }

    private static func nilIfEmpty(_ mods: [String]) -> [String]? {
        mods.isEmpty ? nil : mods
    }
}

/// Trims a queue of actions without losing anything the guest can observe.
enum InputBatch {
    /// A scroll from an AppKit wheel event. AppKit's positive `scrollingDeltaY` scrolls up (the
    /// content moves down) and positive `scrollingDeltaX` scrolls left; the daemon's deltas are
    /// the other way round, positive down and right, as its tools describe (daemon issue #51).
    static func scroll(x: Double, y: Double, appKitDeltaX: Double, appKitDeltaY: Double) -> InputAction {
        InputAction(type: .scroll, x: x, y: y, deltaX: -appKitDeltaX, deltaY: -appKitDeltaY)
    }

    static func coalesced(_ actions: [InputAction]) -> [InputAction] {
        var out: [InputAction] = []
        for action in actions {
            guard let last = out.last else {
                out.append(action)
                continue
            }
            // Only the last of a run of moves matters.
            if action.type == .move, last.type == .move {
                out[out.count - 1] = action
                continue
            }
            if action.type == .type, last.type == .type,
               let text = action.text, let held = last.text {
                out[out.count - 1] = InputAction(type: .type, text: held + text)
                continue
            }
            if action.type == .scroll, last.type == .scroll, action.x == last.x, action.y == last.y {
                out[out.count - 1] = InputAction(
                    type: .scroll,
                    x: last.x,
                    y: last.y,
                    deltaX: (last.deltaX ?? 0) + (action.deltaX ?? 0),
                    deltaY: (last.deltaY ?? 0) + (action.deltaY ?? 0)
                )
                continue
            }
            out.append(action)
        }
        return out
    }
}
