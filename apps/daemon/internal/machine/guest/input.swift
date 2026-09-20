// greenroom-input: the one thing inside a guest that moves its mouse and
// presses its keys.
//
// The daemon compiles this file inside the machine once (see input.go), then
// calls the binary with one base64-encoded JSON argument, so no text the
// person types ever passes through a shell:
//
//     greenroom-input --version
//     greenroom-input --json-base64 <base64 of {"actions":[...]}>
//
// It writes one JSON object to stdout and exits 0, or writes an error object
// and exits 1. Coordinates are pixels on the main display; the daemon turns
// the fractions the companion sends into pixels before it gets here.
//
// The events go in through CGEvent at the HID tap, which needs the
// Accessibility and PostEvent permissions. The greenroom image grants both to
// the Tart guest agent, and this binary inherits them because the agent
// starts it (docs/02-spike.md).

import CoreGraphics
import Foundation

let version = "greenroom-input 1"

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

/// Every event this process posts goes through here, so the HID tap is named
/// once and a caller cannot post to a different one by accident.
func post(_ event: CGEvent?) {
    event?.post(tap: .cghidEventTap)
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
        post(down)
        post(up)
    }
}

func press(key name: String, mods: [String]?) throws {
    guard let code = keyCode(for: name) else {
        throw Failure("unknown key \(name)")
    }
    let modifiers = flags(mods)
    guard let down = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: true),
          let up = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: false)
    else { throw Failure("cannot build a key event for \(name)") }
    down.flags = modifiers
    up.flags = modifiers
    post(down)
    post(up)
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
        click(at: point(action), button: button, down: down, up: up, times: action.clicks ?? 1)
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

let arguments = Array(CommandLine.arguments.dropFirst())
if arguments.first == "--version" {
    print(version)
    exit(0)
}

guard arguments.count == 2, arguments[0] == "--json-base64",
      let payload = Data(base64Encoded: arguments[1])
else {
    emit(["error": "usage: greenroom-input --json-base64 <base64 json>"], to: FileHandle.standardError)
    exit(1)
}

do {
    let request = try JSONDecoder().decode(Request.self, from: payload)
    for action in request.actions {
        try run(action)
    }
    // A release that is never posted leaves the guest with a stuck button, so
    // a batch that ends mid-drag is the caller's business, not a leak here:
    // the daemon always sends the release in the same batch or a later one.
    emit([
        "ok": true,
        "actions": request.actions.count,
        "screen": ["width": Int(bounds.width), "height": Int(bounds.height)],
    ], to: FileHandle.standardOutput)
} catch let failure as Failure {
    emit(["error": failure.message], to: FileHandle.standardError)
    exit(1)
} catch {
    emit(["error": "\(error)"], to: FileHandle.standardError)
    exit(1)
}
