import XCTest

@testable import Companion

/// The palette's matching, adapted from Ghostty's (companion ADR 0019).
final class PaletteMatchTests: XCTestCase {
    private func option(_ title: String, _ section: String = "This run") -> PaletteOption {
        PaletteOption(id: title, title: title, section: section, action: {})
    }

    func testASubstringMatchesIgnoringCase() {
        XCTAssertTrue(PaletteMatch.matches("Accept fail", "fail"))
        XCTAssertTrue(PaletteMatch.matches("Accept fail", "ACC"))
        XCTAssertFalse(PaletteMatch.matches("Accept fail", "reject"))
    }

    func testWordInitialsMatchInOrder() {
        XCTAssertTrue(PaletteMatch.matches("Message the verifier", "mtv"))
        XCTAssertFalse(PaletteMatch.matches("Message the verifier", "vtm"))
    }

    func testTitleMatchesRankAboveSectionMatchesAndKeepTheirOrder() {
        let options = [option("Open activity"), option("TipSplit: split the bill", "Go to run"), option("Play the recording"), option("Go back")]
        XCTAssertEqual(PaletteMatch.filter(options, query: "").map(\.title).count, 4)
        let found = PaletteMatch.filter(options, query: "go").map(\.title)
        XCTAssertEqual(found, ["Go back", "TipSplit: split the bill"])
        XCTAssertEqual(PaletteMatch.filter(options, query: "tipsplit").map(\.title), ["TipSplit: split the bill"])
        XCTAssertTrue(PaletteMatch.filter(options, query: "zzz").isEmpty)
    }
}
