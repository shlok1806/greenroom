// What the acting ops post, decided before anything is posted (daemon ADR 0006, catalog I2 and
// I9, point 7): the click states of a press, the keys a US keyboard sends for a text, the pace
// between typed characters and whether what an element shows afterwards is what was typed.

import Foundation

// MARK: - Clicks (I2)

/// One mouse event of a press: a button going down or up, with the click state it carries.
struct ClickEvent: Equatable {
    let down: Bool
    let state: Int
}

/// The most clicks one press makes: a triple click selects a paragraph, nothing needs four.
let maxPressCount = 3

/// The events of a press of `count` clicks: each pair carries click state 1, 2, 3 in turn, so an
/// app sees one double or triple click and not separate single clicks.
func clickSequence(count: Int) -> [ClickEvent] {
    let clicks = min(max(count, 1), maxPressCount)
    return (1...clicks).flatMap { [ClickEvent(down: true, state: $0), ClickEvent(down: false, state: $0)] }
}

/// The mouse buttons a press takes.
let pressButtons: Set<String> = ["left", "right", "middle"]

/// The modifier names a request may hold, in the words `flags` reads (Input.swift).
let modifierNames: Set<String> = ["cmd", "command", "meta", "shift", "alt", "option", "opt", "ctrl", "control", "fn", "function"]

/// The first modifier name that is not one, nil when all are.
func unknownModifier(_ names: [String]) -> String? {
    names.first { !modifierNames.contains($0.lowercased()) }
}

// MARK: - Pace (point 7)

/// The least time between two typed characters unless the request says, and its bounds. A busy
/// app drops keys that arrive faster than a hand types; 20 ms is still 50 characters a second.
let defaultPaceMs = 20
let maxPaceMs = 100

func typingPaceMs(_ requested: Int?) -> Int {
    guard let requested else { return defaultPaceMs }
    return min(max(requested, 0), maxPaceMs)
}

// MARK: - A US keyboard (I9)

/// A key as a US keyboard sends it: its virtual key code, and whether shift is held.
struct KeyStroke: Equatable {
    let code: UInt16
    let shift: Bool
}

/// The keys of the US ANSI layout, unshifted and shifted: what `type {via: "keys"}` posts.
private let usUnshifted: [Character: UInt16] = [
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
    "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
    "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25, "7": 26,
    "-": 27, "8": 28, "0": 29, "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35,
    "l": 37, "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44,
    "n": 45, "m": 46, ".": 47, "`": 50,
    " ": 49, "\t": 48, "\n": 36, "\r": 36, "\r\n": 36,
]

private let usShifted: [Character: Character] = [
    "!": "1", "@": "2", "#": "3", "$": "4", "%": "5", "^": "6", "&": "7", "*": "8", "(": "9", ")": "0",
    "_": "-", "+": "=", "{": "[", "}": "]", "|": "\\", ":": ";", "\"": "'", "<": ",", ">": ".",
    "?": "/", "~": "`",
]

/// The key a US keyboard types a character with, nil for one it has no key for (an accented
/// letter, an emoji, anything an input method makes).
func usKeyStroke(for character: Character) -> KeyStroke? {
    if let code = usUnshifted[character] { return KeyStroke(code: code, shift: false) }
    if let base = usShifted[character], let code = usUnshifted[base] { return KeyStroke(code: code, shift: true) }
    if character.isASCII, character.isUppercase, let lower = character.lowercased().first,
       let code = usUnshifted[lower] {
        return KeyStroke(code: code, shift: true)
    }
    return nil
}

/// A character a US keyboard has no key for, and where it is in the text (0-based, in
/// characters), for the `bad_request` that refuses the text before anything is typed.
struct UnmappedCharacter: Error, Equatable {
    let character: Character
    let index: Int

    var message: String {
        let scalars = character.unicodeScalars.map { String(format: "U+%04X", $0.value) }.joined(separator: " ")
        return "character \(index + 1) of the text, \"\(character)\" (\(scalars)), has no key on a US keyboard, so nothing was typed; type it with via \"unicode\" (the default)"
    }
}

/// The keys of a whole text, or the first character that has none.
func usKeyStrokes(for text: String) -> Result<[KeyStroke], UnmappedCharacter> {
    var strokes: [KeyStroke] = []
    for (index, character) in text.enumerated() {
        guard let stroke = usKeyStroke(for: character) else {
            return .failure(UnmappedCharacter(character: character, index: index))
        }
        strokes.append(stroke)
    }
    return .success(strokes)
}

// MARK: - Read-back (point 7, I15)

/// How a secure field's text is said: its length only.
func secretText(_ count: Int) -> String {
    "<secret, \(max(count, 0)) chars>"
}

/// Line ends as a text view reports them: a field may give back "\r" for the "\n" typed.
private func normalizedLines(_ text: String) -> String {
    text.replacingOccurrences(of: "\r\n", with: "\n").replacingOccurrences(of: "\r", with: "\n")
}

/// Whether what an element shows after typing is what was typed. With `replace` the whole value
/// must be the text, else it must contain it. A secure field is compared by length alone: with
/// `replace` the same length, else at least as long. `submittedReturn` forgives the one line end
/// the return key adds to a text area.
func readBackMatches(typed: String, readBack: String, replace: Bool, secret: Bool, submittedReturn: Bool = false) -> Bool {
    if secret {
        return replace ? readBack.count == typed.count : readBack.count >= typed.count
    }
    let want = normalizedLines(typed)
    var got = normalizedLines(readBack)
    if replace {
        if got == want { return true }
        if submittedReturn, got.hasSuffix("\n") {
            got.removeLast()
            return got == want
        }
        return false
    }
    return want.isEmpty || got.contains(want)
}
