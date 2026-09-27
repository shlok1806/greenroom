import XCTest

@testable import Companion

/// The Greenroom section in the registry (root ADR 0033, companion ADR 0005): a palette
/// entry and an app-menu item, and while it is open it owns the keyboard, esc closing it.
@MainActor
final class GreenroomKeysTests: XCTestCase {
    private func onScreen(_ change: (inout ActionState) -> Void = { _ in }) -> ActionState {
        var s = ActionState()
        s.runOpen = true
        s.runCount = 2
        s.pane = .stage
        s.available = [HandlerKey(id: .play, context: .screen)]
        change(&s)
        return s
    }

    func testThePaletteAndTheAppMenuOpenIt() {
        let item = PaletteModel.items(query: "updates", onScreen()).first
        XCTAssertEqual(item?.id, .greenroom)
        XCTAssertEqual(item?.enabled, true)
        XCTAssertEqual(ActionRegistry.menu(.app).map(\.id), [.greenroom])
        XCTAssertEqual(ActionRegistry.spec(.greenroom).menuTitle, "Builds and Updates...")
        XCTAssertFalse(PaletteModel.items(query: "", onScreen()).contains { $0.id == .closeGreenroom })
        // With no run open too: builds are not a run's.
        XCTAssertTrue(ActionRules.isEnabledAnywhere(.greenroom, ActionState()))
    }

    func testWhileOpenItOwnsTheKeyboardAndEscClosesIt() {
        let open = onScreen { $0.greenroomOpen = true }
        XCTAssertEqual(ActionRules.contexts(open), [.greenroom])
        XCTAssertEqual(KeyResolver.resolve(.escape, open), .perform(.closeGreenroom, .greenroom))
        XCTAssertEqual(KeyResolver.resolve(.space, open), .swallow, "the screen's play does not act under it")
        XCTAssertEqual(KeyResolver.resolve(.char("t"), open), .swallow, "take control does not act under it")
        XCTAssertEqual(KeyResolver.resolve(.cmd("q"), open), .pass, "the menu bar still quits")
        XCTAssertFalse(ActionRules.isEnabled(.closeGreenroom, in: .greenroom, onScreen()))
    }

    func testTheHintBarSaysHowToCloseIt() {
        let open = onScreen { $0.greenroomOpen = true }
        let bar = HintBar.content(open)
        XCTAssertEqual(bar.context, "greenroom")
        XCTAssertEqual(bar.hints.map(\.key), ["esc"])
        XCTAssertEqual(bar.hints.map(\.title), ["close"])
        XCTAssertNil(HintBar.trailing(open))
    }

    func testOpeningItClosesThePaletteAndHelpAndEscClosesIt() {
        let keyboard = KeyboardModel(store: RunStore(client: StubURLProtocol.client { _ in .json("[]") }))
        keyboard.perform(.palette)
        keyboard.helpOpen = true
        keyboard.perform(.greenroom)
        XCTAssertTrue(keyboard.greenroomOpen)
        XCTAssertFalse(keyboard.paletteOpen)
        XCTAssertFalse(keyboard.helpOpen)
        XCTAssertTrue(keyboard.handle(.escape, responder: .other))
        XCTAssertFalse(keyboard.greenroomOpen)
    }
}
