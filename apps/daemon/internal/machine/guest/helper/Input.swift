// Posting input through CGEvent at the HID tap (ADR 0009), shared by every mode.

import AppKit
import CoreGraphics
import Foundation

// MARK: - Wire types

struct Action: Decodable {
    var type: String
    var x: Double?
    var y: Double?
    var button: String?
    var clicks: Int?
    var deltaX: Double?
    var deltaY: Double?
    var text: String?
    var key: String?
    var mods: [String]?
    var ms: Int?
    /// For focus: the application to bring to the front, a name or bundle id (daemon ADR 0009).
    var app: String?
    /// For focus, and for a click by element: the application's process id. A click with a pid
    /// lands on a point of the element that app owns (`w` and `h` are the element's size).
    var pid: Int?
    var w: Double?
    var h: Double?
}

struct Request: Decodable {
    var actions: [Action]
}

// MARK: - Keys

/// US-ANSI virtual key codes. A shortcut has to name a key, not a character:
/// command-A is the key at code 0 with the command flag, and no amount of
/// unicode on the event will make an application see it.
let namedKeys: [String: CGKeyCode] = [
    "return": 36, "enter": 76, "tab": 48, "space": 49, "delete": 51, "backspace": 51,
    "forwarddelete": 117, "escape": 53, "esc": 53, "left": 123, "right": 124,
    "down": 125, "up": 126, "home": 115, "end": 119, "pageup": 116, "pagedown": 121,
    "capslock": 57, "help": 114,
    "f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
    "f9": 101, "f10": 109, "f11": 103, "f12": 111,
]

let characterKeys: [String: CGKeyCode] = [
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
    "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
    "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "9": 25, "7": 26, "8": 28, "0": 29,
    "=": 24, "-": 27, "]": 30, "[": 33, "'": 39, ";": 41, "\\": 42, ",": 43, "/": 44,
    ".": 47, "`": 50,
    "o": 31, "u": 32, "i": 34, "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46,
]

func keyCode(for name: String) -> CGKeyCode? {
    let lower = name.lowercased()
    if let code = namedKeys[lower] { return code }
    if let code = characterKeys[lower] { return code }
    return nil
}

func flags(_ names: [String]?) -> CGEventFlags {
    var out: CGEventFlags = []
    for raw in names ?? [] {
        switch raw.lowercased() {
        case "cmd", "command", "meta": out.insert(.maskCommand)
        case "shift": out.insert(.maskShift)
        case "alt", "option", "opt": out.insert(.maskAlternate)
        case "ctrl", "control": out.insert(.maskControl)
        case "fn", "function": out.insert(.maskSecondaryFn)
        default: break
        }
    }
    return out
}

// MARK: - Posting

let display = CGMainDisplayID()
let bounds = CGDisplayBounds(display)
let source = CGEventSource(stateID: .combinedSessionState)

/// Where the pointer is, as this process believes it. A drag is a press, some
/// moves and a release, and every one of those events has to carry a position,
/// so the last one is remembered rather than read back from the system.
var cursor = CGPoint(x: bounds.midX, y: bounds.midY)

func clamp(_ point: CGPoint) -> CGPoint {
    CGPoint(
        x: min(max(point.x, bounds.minX), bounds.maxX - 1),
        y: min(max(point.y, bounds.minY), bounds.maxY - 1)
    )
}

func point(_ action: Action) -> CGPoint {
    guard let x = action.x, let y = action.y else { return cursor }
    return clamp(CGPoint(x: x, y: y))
}

func mouseButton(_ name: String?) -> (CGMouseButton, CGEventType, CGEventType, CGEventType) {
    switch (name ?? "left").lowercased() {
    case "right": return (.right, .rightMouseDown, .rightMouseUp, .rightMouseDragged)
    case "middle", "center": return (.center, .otherMouseDown, .otherMouseUp, .otherMouseDragged)
    default: return (.left, .leftMouseDown, .leftMouseUp, .leftMouseDragged)
    }
}

/// How many events this process has posted.
var posted: UInt32 = 0

/// Every event this process posts goes through here, so the HID tap is named
/// once and a caller cannot post to a different one by accident. It also counts
/// them for `settle`.
func post(_ event: CGEvent?) {
    guard let event else { return }
    event.post(tap: .cghidEventTap)
    posted &+= 1
}

/// The login session's count of input events of every kind, ours included.
func sessionEvents() -> UInt32 {
    CGEventSource.counterForEventType(.combinedSessionState, eventType: CGEventType(rawValue: ~0)!)
}

/// Waits until the window server has applied every event posted since `base`
/// was read, or throws after `limit`.
///
/// A post only queues the event. The window server then checks, by the
/// poster's audit token, that the process may post (TCC PostEvent), and only
/// then applies it. Right after boot that check can run over 100 ms late;
/// by then a one-shot helper has exited, the check finds no process, and the
/// event is dropped without an error anywhere: the pointer stayed where boot
/// left it, (10,10), and e2e_input_test.go failed about one run in ten. So a
/// batch is not done until the session's event counter has moved past it.
/// Other sources only make the counter reach the target sooner, never later.
func settle(since base: UInt32, want: UInt32, limit: TimeInterval = 3) throws {
    let deadline = Date().addingTimeInterval(limit)
    while sessionEvents() &- base < want {
        if Date() >= deadline {
            throw Failure("the machine applied \(sessionEvents() &- base) of \(want) input events within \(Int(limit)) s")
        }
        usleep(2_000)
    }
}

/// Runs a batch and returns once the window server has applied all of it. A
/// batch that fails part way still waits for what it posted, then reports the
/// failure.
///
/// `before`, when given, runs before each action with its index and may throw
/// to stop the batch there (the agent's cancel and pause checks, daemon ADR
/// 0005 point 14); what was already posted still settles. The one-shot modes
/// pass none.
func perform(_ actions: [Action], before: ((Int) throws -> Void)? = nil) throws {
    let base = sessionEvents()
    let first = posted
    do {
        for (index, action) in actions.enumerated() {
            try before?(index)
            try run(action)
        }
    } catch {
        try? settle(since: base, want: posted &- first)
        throw error
    }
    try settle(since: base, want: posted &- first)
}

func move(to target: CGPoint, dragging button: CGMouseButton?) {
    cursor = target
    let type: CGEventType
    switch button {
    case .some(.left): type = .leftMouseDragged
    case .some(.right): type = .rightMouseDragged
    case .some(.center): type = .otherMouseDragged
    default: type = .mouseMoved
    }
    post(CGEvent(
        mouseEventSource: source,
        mouseType: type,
        mouseCursorPosition: target,
        mouseButton: button ?? .left
    ))
}

func mouse(_ type: CGEventType, at target: CGPoint, button: CGMouseButton, clickState: Int) {
    cursor = target
    guard let event = CGEvent(
        mouseEventSource: source,
        mouseType: type,
        mouseCursorPosition: target,
        mouseButton: button
    ) else { return }
    event.setIntegerValueField(.mouseEventClickState, value: Int64(clickState))
    post(event)
}

/// A double click is one click event with clickState 2, not two clicks: an
/// application reads the field rather than timing the presses.
func click(at target: CGPoint, button: CGMouseButton, down: CGEventType, up: CGEventType, times: Int) {
    for n in 1...max(times, 1) {
        mouse(down, at: target, button: button, clickState: n)
        mouse(up, at: target, button: button, clickState: n)
    }
}

/// Which button, if any, is held. Held state is what turns a move into a drag.
var held: CGMouseButton?

func type(text: String) {
    // One event per character, with the character set as the event's unicode
    // string. This types what the person typed without caring which keyboard
    // layout the guest has.
    //
    // A one-millisecond gap follows both the key-down and the key-up: posted
    // back to back with no gap at all, a whole string arrives at the window
    // server faster than a freshly-focused app's run loop can drain its event
    // queue, and it silently drops everything after the first character or
    // two -- confirmed end to end (e2e_input_test.go) by typing into Terminal
    // right after opening it: only the first couple of characters landed and
    // the rest, including the trailing return, never did. The delay costs
    // nothing a person would notice and it is what makes every character
    // actually arrive.
    for character in text {
        let units = Array(String(character).utf16)
        guard let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false)
        else { continue }
        units.withUnsafeBufferPointer { buffer in
            guard let base = buffer.baseAddress else { return }
            down.keyboardSetUnicodeString(stringLength: units.count, unicodeString: base)
            up.keyboardSetUnicodeString(stringLength: units.count, unicodeString: base)
        }
        // Text is never a shortcut, whatever modifier state the source holds.
        down.flags = []
        up.flags = []
        post(down)
        usleep(1_000)
        post(up)
        usleep(1_000)
    }
}

/// The modifier keys, in the order a hand presses them, with their flag.
let modifierKeys: [(CGEventFlags, CGKeyCode)] = [
    (.maskCommand, 55), (.maskShift, 56), (.maskAlternate, 58), (.maskControl, 59), (.maskSecondaryFn, 63),
]

/// A shortcut is pressed the way a keyboard sends it: each modifier key
/// down, the key, then the modifiers up. A bare flag on the key event alone
/// leaves the window server believing the modifier is still held, and the
/// next characters typed arrive as command-1, command-2 (issue: typing after
/// command-A was swallowed in SwiftUI text fields).
func press(key name: String, mods: [String]?) throws {
    guard let code = keyCode(for: name) else {
        throw Failure("unknown key \(name)")
    }
    let modifiers = flags(mods)
    let held = modifierKeys.filter { modifiers.contains($0.0) }
    var state: CGEventFlags = []
    for (flag, modifier) in held {
        state.insert(flag)
        guard let event = CGEvent(keyboardEventSource: source, virtualKey: modifier, keyDown: true) else { continue }
        event.flags = state
        post(event)
    }
    guard let down = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: true),
          let up = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: false)
    else { throw Failure("cannot build a key event for \(name)") }
    down.flags = modifiers
    up.flags = modifiers
    post(down)
    usleep(1_000)
    post(up)
    for (flag, modifier) in held.reversed() {
        state.remove(flag)
        guard let event = CGEvent(keyboardEventSource: source, virtualKey: modifier, keyDown: false) else { continue }
        event.flags = state
        post(event)
    }
    usleep(1_000)
}

func scroll(deltaX: Double, deltaY: Double) {
    guard let event = CGEvent(
        scrollWheelEvent2Source: source,
        units: .pixel,
        wheelCount: 2,
        wheel1: Int32(deltaY.rounded()),
        wheel2: Int32(deltaX.rounded()),
        wheel3: 0
    ) else { return }
    // A scroll goes to whatever is under the pointer, so it carries the
    // position the caller last moved to.
    event.location = cursor
    post(event)
}


// MARK: - Running

struct Failure: Error {
    let message: String
    init(_ message: String) { self.message = message }
}

func run(_ action: Action) throws {
    switch action.type.lowercased() {
    case "move":
        move(to: point(action), dragging: held)
    case "click":
        let (button, down, up, _) = mouseButton(action.button)
        var target = point(action)
        if let pid = action.pid {
            target = try ownedPoint(of: action, pid: pid_t(pid), center: target)
        }
        click(at: target, button: button, down: down, up: up, times: action.clicks ?? 1)
    case "focus":
        try focus(action)
    case "down":
        let (button, down, _, _) = mouseButton(action.button)
        // The click count comes from the window the person clicked in, so a
        // double click here is a double click there.
        mouse(down, at: point(action), button: button, clickState: max(action.clicks ?? 1, 1))
        held = button
    case "up":
        let (button, _, up, _) = mouseButton(action.button)
        mouse(up, at: point(action), button: button, clickState: max(action.clicks ?? 1, 1))
        held = nil
    case "scroll":
        if action.x != nil { move(to: point(action), dragging: held) }
        scroll(deltaX: action.deltaX ?? 0, deltaY: action.deltaY ?? 0)
    case "type":
        type(text: action.text ?? "")
    case "key":
        try press(key: action.key ?? "", mods: action.mods)
    case "sleep":
        usleep(UInt32(max(0, min(action.ms ?? 0, 5000)) * 1000))
    default:
        throw Failure("unknown action \(action.type)")
    }
}

func emit(_ object: [String: Any], to handle: FileHandle) {
    let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data("{}".utf8)
    handle.write(data)
    handle.write(Data("\n".utf8))
}
