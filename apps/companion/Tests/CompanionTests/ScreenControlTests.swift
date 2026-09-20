import XCTest

@testable import Companion

/// The arithmetic and the key mapping behind "take control" (ADR 0009). Every
/// rule here is a pure function on purpose: a wrong fraction is a click in the
/// wrong place on someone's machine, which is not a thing to find out by hand.
final class ScreenControlTests: XCTestCase {

    // MARK: - Where a click lands

    func testAViewTheSameShapeAsTheScreenHasNoLetterbox() {
        let rect = ScreenGeometry.fitted(image: CGSize(width: 1024, height: 768), in: CGSize(width: 512, height: 384))
        XCTAssertEqual(rect, CGRect(x: 0, y: 0, width: 512, height: 384))
    }

    func testAWideViewLetterboxesLeftAndRight() {
        // 1024x768 in a 1000x300 view fits to 400x300, centred.
        let rect = ScreenGeometry.fitted(image: CGSize(width: 1024, height: 768), in: CGSize(width: 1000, height: 300))
        XCTAssertEqual(rect.width, 400, accuracy: 0.001)
        XCTAssertEqual(rect.height, 300, accuracy: 0.001)
        XCTAssertEqual(rect.minX, 300, accuracy: 0.5)
        XCTAssertEqual(rect.minY, 0, accuracy: 0.5)
    }

    func testTheCentreOfThePictureIsTheCentreOfTheScreen() {
        let fraction = ScreenGeometry.fraction(
            at: CGPoint(x: 500, y: 150),
            image: CGSize(width: 1024, height: 768),
            view: CGSize(width: 1000, height: 300)
        )
        XCTAssertEqual(fraction?.x ?? -1, 0.5, accuracy: 0.005)
        XCTAssertEqual(fraction?.y ?? -1, 0.5, accuracy: 0.005)
    }

    func testTheTopLeftOfThePictureIsTheTopLeftOfTheScreen() {
        let fraction = ScreenGeometry.fraction(
            at: CGPoint(x: 300, y: 0),
            image: CGSize(width: 1024, height: 768),
            view: CGSize(width: 1000, height: 300)
        )
        XCTAssertEqual(fraction?.x ?? -1, 0, accuracy: 0.005)
        XCTAssertEqual(fraction?.y ?? -1, 0, accuracy: 0.005)
    }

    /// A click on the black bars is a click on the window, not on the machine.
    func testAClickOnTheLetterboxIsNotAClickOnTheScreen() {
        let fraction = ScreenGeometry.fraction(
            at: CGPoint(x: 10, y: 150),
            image: CGSize(width: 1024, height: 768),
            view: CGSize(width: 1000, height: 300)
        )
        XCTAssertNil(fraction)
    }

    /// A release that the guest never hears leaves its mouse button held down
    /// for the rest of the run, so a drag off the edge is pulled back on.
    func testADragOffTheEdgeIsPulledOntoTheEdge() {
        let at = ScreenGeometry.clampedFraction(
            at: CGPoint(x: -50, y: 900),
            image: CGSize(width: 1024, height: 768),
            view: CGSize(width: 1000, height: 300)
        )
        XCTAssertEqual(at?.x ?? -1, 0, accuracy: 0.001)
        XCTAssertEqual(at?.y ?? -1, 1, accuracy: 0.001)
    }

    func testAClampedPointInsideThePictureIsTheSameAsAnUnclampedOne() {
        let point = CGPoint(x: 500, y: 150)
        let image = CGSize(width: 1024, height: 768)
        let view = CGSize(width: 1000, height: 300)
        XCTAssertEqual(
            ScreenGeometry.clampedFraction(at: point, image: image, view: view),
            ScreenGeometry.fraction(at: point, image: image, view: view)
        )
    }

    func testAViewOrAnImageWithNoSizeYieldsNothing() {
        XCTAssertNil(ScreenGeometry.fraction(at: .zero, image: .zero, view: CGSize(width: 100, height: 100)))
        XCTAssertNil(ScreenGeometry.fraction(at: .zero, image: CGSize(width: 100, height: 100), view: .zero))
        XCTAssertNil(ScreenGeometry.clampedFraction(at: .zero, image: .zero, view: CGSize(width: 100, height: 100)))
    }

    // MARK: - Keys

    private func stroke(
        _ characters: String,
        ignoring: String? = nil,
        code: UInt16 = 999,
        command: Bool = false,
        shift: Bool = false,
        option: Bool = false,
        control: Bool = false
    ) -> KeyStroke {
        KeyStroke(
            characters: characters,
            charactersIgnoringModifiers: ignoring ?? characters,
            keyCode: code,
            command: command,
            shift: shift,
            option: option,
            control: control
        )
    }

    func testAnOrdinaryKeyIsTypedAsText() {
        let action = KeyTranslator.action(for: stroke("a"))
        XCTAssertEqual(action?.type, .type)
        XCTAssertEqual(action?.text, "a")
        XCTAssertNil(action?.mods, "shift is already in the character; sending it again helps nobody")
    }

    func testAShiftedKeyTypesTheShiftedCharacter() {
        let action = KeyTranslator.action(for: stroke("A", ignoring: "A", shift: true))
        XCTAssertEqual(action?.type, .type)
        XCTAssertEqual(action?.text, "A")
    }

    func testAnAccentedCharacterIsTypedRatherThanNamed() {
        let action = KeyTranslator.action(for: stroke("é", ignoring: "e", option: true))
        XCTAssertEqual(action?.type, .type)
        XCTAssertEqual(action?.text, "é")
    }

    /// A shortcut has to arrive as a named key: nothing in the guest reads
    /// "\u{01}" as Select All.
    func testACommandShortcutIsAKeyWithModifiers() {
        let action = KeyTranslator.action(for: stroke("\u{01}", ignoring: "a", command: true))
        XCTAssertEqual(action?.type, .key)
        XCTAssertEqual(action?.key, "a")
        XCTAssertEqual(action?.mods, ["cmd"])
    }

    func testACommandShiftShortcutCarriesBothModifiers() {
        let action = KeyTranslator.action(for: stroke("S", ignoring: "S", command: true, shift: true))
        XCTAssertEqual(action?.key, "s")
        XCTAssertEqual(action?.mods, ["cmd", "shift"])
    }

    func testReturnAndTheArrowsAreNamedKeys() {
        for (code, name) in [(UInt16(36), "return"), (UInt16(48), "tab"), (UInt16(53), "escape"), (UInt16(126), "up")] {
            let action = KeyTranslator.action(for: stroke("", code: code))
            XCTAssertEqual(action?.type, .key, "key code \(code)")
            XCTAssertEqual(action?.key, name)
        }
    }

    func testANamedKeyKeepsItsModifiers() {
        let action = KeyTranslator.action(for: stroke("", code: 124, command: true, option: true))
        XCTAssertEqual(action?.key, "right")
        XCTAssertEqual(action?.mods, ["cmd", "alt"])
    }

    func testAKeyWithNothingToSayIsDropped() {
        XCTAssertNil(KeyTranslator.action(for: stroke("")))
        XCTAssertNil(KeyTranslator.action(for: stroke("", command: true)))
    }

    // MARK: - What goes over the wire

    func testARunOfMovesBecomesTheLastOne() {
        let batch = InputBatch.coalesced([
            InputAction(type: .move, x: 0.1, y: 0.1),
            InputAction(type: .move, x: 0.2, y: 0.2),
            InputAction(type: .move, x: 0.3, y: 0.3),
        ])
        XCTAssertEqual(batch.count, 1)
        XCTAssertEqual(batch.first?.x, 0.3)
    }

    func testMovesAroundAClickAreKept() {
        let batch = InputBatch.coalesced([
            InputAction(type: .move, x: 0.1, y: 0.1),
            InputAction(type: .down, x: 0.1, y: 0.1, button: "left", clicks: 1),
            InputAction(type: .move, x: 0.5, y: 0.5),
            InputAction(type: .up, x: 0.5, y: 0.5, button: "left", clicks: 1),
        ])
        XCTAssertEqual(batch.map(\.type), [.move, .down, .move, .up], "a drag is its own shape and must survive")
    }

    func testTypingIsJoinedIntoOneString() {
        let batch = InputBatch.coalesced([
            InputAction(type: .type, text: "h"),
            InputAction(type: .type, text: "i"),
            InputAction(type: .key, key: "return"),
            InputAction(type: .type, text: "!"),
        ])
        XCTAssertEqual(batch.count, 3)
        XCTAssertEqual(batch.first?.text, "hi")
        XCTAssertEqual(batch.last?.text, "!")
    }

    func testScrollsInTheSamePlaceAddUp() {
        let batch = InputBatch.coalesced([
            InputAction(type: .scroll, x: 0.5, y: 0.5, deltaX: 0, deltaY: -10),
            InputAction(type: .scroll, x: 0.5, y: 0.5, deltaX: 0, deltaY: -14),
        ])
        XCTAssertEqual(batch.count, 1)
        XCTAssertEqual(batch.first?.deltaY, -24)
    }

    func testScrollsInDifferentPlacesAreKeptApart() {
        let batch = InputBatch.coalesced([
            InputAction(type: .scroll, x: 0.2, y: 0.2, deltaY: -10),
            InputAction(type: .scroll, x: 0.8, y: 0.8, deltaY: -10),
        ])
        XCTAssertEqual(batch.count, 2)
    }

    func testAnEmptyBatchStaysEmpty() {
        XCTAssertTrue(InputBatch.coalesced([]).isEmpty)
    }

    // MARK: - The wire shape

    func testAnActionEncodesOnlyWhatItCarries() throws {
        let data = try JSONEncoder.daemon().encode(InputAction(type: .click, x: 0.25, y: 0.5, clicks: 2))
        let json = try XCTUnwrap(String(data: data, encoding: .utf8))
        XCTAssertTrue(json.contains("\"type\":\"click\""), json)
        XCTAssertFalse(json.contains("text"), "an empty field is not sent: \(json)")
        XCTAssertFalse(json.contains("deltaX"), json)
    }

    func testTheLeaseDecodesFromTheDaemonsShape() throws {
        let json = """
        {"holder":"human","since":"2026-09-20T10:00:00Z","expires":"2026-09-20T10:01:00Z","actions":7}
        """
        let lease = try JSONDecoder.daemon().decode(ControlLease.self, from: Data(json.utf8))
        XCTAssertTrue(lease.isHuman)
        XCTAssertEqual(lease.actions, 7)
        XCTAssertEqual(lease.expires.timeIntervalSince(lease.since), 60)
    }

    func testAMachineWithNoLeaseDecodes() throws {
        let json = """
        {"runId":"r1","name":"greenroom-r1","image":"i","status":"ready","createdAt":"2026-09-20T10:00:00Z","dir":"/d"}
        """
        let machine = try JSONDecoder.daemon().decode(Machine.self, from: Data(json.utf8))
        XCTAssertNil(machine.control)
    }

    func testAMachineWithALeaseDecodes() throws {
        let json = """
        {"runId":"r1","name":"greenroom-r1","image":"i","status":"ready","createdAt":"2026-09-20T10:00:00Z",
         "dir":"/d","control":{"holder":"human","since":"2026-09-20T10:00:00Z",
         "expires":"2026-09-20T10:01:00Z","actions":0}}
        """
        let machine = try JSONDecoder.daemon().decode(Machine.self, from: Data(json.utf8))
        XCTAssertEqual(machine.control?.holder, "human")
    }
}
