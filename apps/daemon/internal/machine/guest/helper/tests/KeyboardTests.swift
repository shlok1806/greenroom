import Foundation

func testClicksCarryStateOneTwoThreeInPairs() {
    expectEqual(clickSequence(count: 1), [ClickEvent(down: true, state: 1), ClickEvent(down: false, state: 1)])
    expectEqual(clickSequence(count: 2).map(\.state), [1, 1, 2, 2], "a double click")
    expectEqual(clickSequence(count: 3).map(\.state), [1, 1, 2, 2, 3, 3], "a triple click selects a paragraph")
    expectEqual(clickSequence(count: 3).map(\.down), [true, false, true, false, true, false])
    expectEqual(clickSequence(count: 9).count, 6, "never more than three clicks")
    expectEqual(clickSequence(count: 0).count, 2, "never fewer than one")
}

func testModifiersAreKnownByTheirNames() {
    expect(unknownModifier(["cmd", "Shift", "option", "ctrl", "fn"]) == nil, "the names flags() reads")
    expectEqual(unknownModifier(["cmd", "hyper"]), "hyper")
}

func testThePaceDefaultsTo20msAndIsBounded() {
    expectEqual(typingPaceMs(nil), 20)
    expectEqual(typingPaceMs(0), 0, "0 is allowed: it shows a field that drops keys")
    expectEqual(typingPaceMs(-5), 0)
    expectEqual(typingPaceMs(250), 100)
}

func testAUSKeyboardTypesLettersDigitsAndShiftedSymbols() {
    expectEqual(usKeyStroke(for: "a"), KeyStroke(code: 0, shift: false))
    expectEqual(usKeyStroke(for: "A"), KeyStroke(code: 0, shift: true))
    expectEqual(usKeyStroke(for: "1"), KeyStroke(code: 18, shift: false))
    expectEqual(usKeyStroke(for: "!"), KeyStroke(code: 18, shift: true))
    expectEqual(usKeyStroke(for: "@"), KeyStroke(code: 19, shift: true))
    expectEqual(usKeyStroke(for: "?"), KeyStroke(code: 44, shift: true))
    expectEqual(usKeyStroke(for: "\""), KeyStroke(code: 39, shift: true))
    expectEqual(usKeyStroke(for: "~"), KeyStroke(code: 50, shift: true))
    expectEqual(usKeyStroke(for: " "), KeyStroke(code: 49, shift: false))
    expectEqual(usKeyStroke(for: "\n"), KeyStroke(code: 36, shift: false), "a line end is return")
    expectEqual(usKeyStroke(for: "\r\n"), KeyStroke(code: 36, shift: false), "a CRLF is one character and one return")
    expectEqual(usKeyStroke(for: "\t"), KeyStroke(code: 48, shift: false))
    // Every printable ASCII character has a key.
    for scalar in 32...126 {
        let character = Character(UnicodeScalar(UInt8(scalar)))
        expect(usKeyStroke(for: character) != nil, "no key for \(character)")
    }
}

func testACharacterWithNoKeyRefusesTheWholeText() {
    switch usKeyStrokes(for: "Café 1") {
    case .success:
        expect(false, "é has no key on a US keyboard")
    case let .failure(unmapped):
        expectEqual(unmapped.character, "é")
        expectEqual(unmapped.index, 3)
        expect(unmapped.message.contains("character 4"), unmapped.message)
        expect(unmapped.message.contains("U+00E9"), unmapped.message)
        expect(unmapped.message.contains("via \"unicode\""), "says what to do instead: \(unmapped.message)")
    }
    switch usKeyStrokes(for: "Hi!") {
    case let .success(strokes):
        expectEqual(strokes, [KeyStroke(code: 4, shift: true), KeyStroke(code: 34, shift: false), KeyStroke(code: 18, shift: true)])
    case .failure:
        expect(false, "Hi! types")
    }
    if case .success = usKeyStrokes(for: "👍") { expect(false, "an emoji has no key") }
}

func testAReplacedValueMustBeTheWholeText() {
    expect(readBackMatches(typed: "120", readBack: "120", replace: true, secret: false), "the same")
    expect(!readBackMatches(typed: "120", readBack: "12", replace: true, secret: false), "a dropped key (docs/14 case 5)")
    expect(!readBackMatches(typed: "120", readBack: "1120", replace: true, secret: false), "the old text stayed")
    expect(readBackMatches(typed: "a\nb", readBack: "a\rb", replace: true, secret: false), "line ends as a field reports them")
    expect(readBackMatches(typed: "note", readBack: "note\n", replace: true, secret: false, submittedReturn: true),
           "the return of submit in a text area")
    expect(!readBackMatches(typed: "note", readBack: "note\n", replace: true, secret: false), "a line end nobody typed")
    expect(readBackMatches(typed: "  ", readBack: "  ", replace: true, secret: false), "white space alone is typed and read back")
}

func testAnAppendedValueMustContainTheText() {
    expect(readBackMatches(typed: "world", readBack: "hello world", replace: false, secret: false))
    expect(!readBackMatches(typed: "world", readBack: "hello wrld", replace: false, secret: false))
    expect(!readBackMatches(typed: "World", readBack: "hello world", replace: false, secret: false), "case counts")
}

func testASecretIsComparedByLengthOnly() {
    expect(readBackMatches(typed: "hunter2", readBack: "•••••••", replace: true, secret: true), "the same length")
    expect(!readBackMatches(typed: "hunter2", readBack: "••••••", replace: true, secret: true), "a key dropped")
    expect(readBackMatches(typed: "pw", readBack: "•••••", replace: false, secret: true), "appended: at least as long")
    expectEqual(secretText(7), "<secret, 7 chars>", "the daemon's SecretText")
    expectEqual(secretText(-1), "<secret, 0 chars>")
}
