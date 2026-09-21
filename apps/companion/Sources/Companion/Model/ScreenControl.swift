import Foundation

/// Where the picture really is inside the Screen tab, and what a point on it
/// means to the machine.
///
/// The frame is drawn with `scaledToFit`, so it is letterboxed: the view is
/// rarely the same shape as the guest's display, and a click at the top left
/// of the *view* is usually not a click at the top left of the *screen*. This
/// is the one place that arithmetic lives, and it is a pure value type so it
/// can be tested without a window (ADR 0009).
enum ScreenGeometry {
    /// The rectangle a `scaledToFit` image occupies in a view of `view`
    /// points, centred, in a top-left origin space.
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

    /// Where `point` is on the guest's screen, as a fraction of it, or `nil`
    /// when the point is on the letterbox rather than on the picture. `point`
    /// is in the view's own space, with y growing downwards.
    static func fraction(at point: CGPoint, image: CGSize, view: CGSize) -> CGPoint? {
        let rect = fitted(image: image, in: view)
        guard rect.width > 0, rect.height > 0, rect.contains(point) else { return nil }
        return CGPoint(
            x: (point.x - rect.minX) / rect.width,
            y: (point.y - rect.minY) / rect.height
        )
    }

    /// The same, but pulled onto the nearest edge of the picture instead of
    /// refused. This is for the events that have to arrive wherever the
    /// pointer went: a drag that leaves the window still ends somewhere, and
    /// a release the guest never hears leaves its mouse button held down for
    /// the rest of the run.
    static func clampedFraction(at point: CGPoint, image: CGSize, view: CGSize) -> CGPoint? {
        let rect = fitted(image: image, in: view)
        guard rect.width > 0, rect.height > 0 else { return nil }
        return CGPoint(
            x: min(max((point.x - rect.minX) / rect.width, 0), 1),
            y: min(max((point.y - rect.minY) / rect.height, 0), 1)
        )
    }
}

/// One key press as the app saw it, with no AppKit in it, so the translation
/// below can be tested without building an `NSEvent`.
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

    /// A shortcut is a key with a name, not text to type: command-A has to
    /// arrive as the A key with the command flag or no application will read
    /// it as Select All.
    var isShortcut: Bool { command || control }
}

/// Turns a key press into the action the daemon posts.
enum KeyTranslator {
    /// Keys that have no character to type, by virtual key code. Anything not
    /// listed types its characters instead, which is what makes accented and
    /// non-Latin input work without the app knowing any keyboard layout.
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
        // Shift is already in the characters; passing it again would give the
        // guest a second shift it never needs.
        return InputAction(type: .type, text: stroke.characters)
    }

    private static func nilIfEmpty(_ mods: [String]) -> [String]? {
        mods.isEmpty ? nil : mods
    }
}

/// How a queue of actions is trimmed before it goes over the wire.
///
/// A drag is hundreds of `move`s a second and a typed word is one `type` per
/// key; the machine only needs where the pointer ended up and what was typed.
/// Trimming here keeps a batch small without losing anything an application
/// in the guest can tell the difference about, and it is pure, so the rules
/// are tested rather than trusted.
enum InputBatch {
    static func coalesced(_ actions: [InputAction]) -> [InputAction] {
        var out: [InputAction] = []
        for action in actions {
            guard let last = out.last else {
                out.append(action)
                continue
            }
            // Only the last of a run of moves matters: the pointer passes
            // through the others faster than anything can notice.
            if action.type == .move, last.type == .move {
                out[out.count - 1] = action
                continue
            }
            // Consecutive typing is one string.
            if action.type == .type, last.type == .type,
               let text = action.text, let held = last.text {
                out[out.count - 1] = InputAction(type: .type, text: held + text)
                continue
            }
            // A scroll in the same place adds up.
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
