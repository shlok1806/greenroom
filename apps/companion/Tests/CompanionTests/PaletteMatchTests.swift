import XCTest

@testable import Companion

/// What the palette shows for a query (companion ADR 0019): cmdk's ranking over the option's
/// title and section. `MotionTests` holds the scores themselves to cmdk's.
final class PaletteMatchTests: XCTestCase {
    private func option(_ title: String, _ section: String = "This run") -> PaletteOption {
        PaletteOption(id: title, title: title, section: section, action: {})
    }

    func testAnEmptyQueryShowsEverythingInItsOwnOrder() {
        let options = [option("Open activity"), option("Accept fail"), option("TipSplit: split the bill", "Go to run")]
        XCTAssertEqual(PaletteMatch.filter(options, query: "").map(\.title), options.map(\.title))
    }

    func testTheBestMatchComesFirstAndWhatDoesNotMatchGoes() {
        let options = [option("Open activity"), option("Accept fail"), option("Reject"), option("Message the verifier"),
                       option("TipSplit: split the bill", "Go to run"), option("WordCount: case buttons", "Go to run")]
        XCTAssertEqual(PaletteMatch.filter(options, query: "acc").first?.title, "Accept fail")
        XCTAssertEqual(PaletteMatch.filter(options, query: "mtv").map(\.title), ["Message the verifier"])
        XCTAssertEqual(PaletteMatch.filter(options, query: "tips").first?.title, "TipSplit: split the bill")
        XCTAssertTrue(PaletteMatch.filter(options, query: "zzz").isEmpty)
    }

    func testTheSectionCountsAsKeywords() {
        let options = [option("Reject"), option("WordCount: case buttons", "Go to run"), option("TipSplit: split the bill", "Go to run")]
        XCTAssertEqual(PaletteMatch.filter(options, query: "go to").map(\.title), ["WordCount: case buttons", "TipSplit: split the bill"])
    }

    func testAStartOfAWordBeatsTheMiddleOfOne() {
        XCTAssertGreaterThan(CommandScore.score("Take control", "co"), CommandScore.score("Accept fail", "cc"))
        XCTAssertEqual(CommandScore.score("Reject", "Reject"), 1)
        XCTAssertEqual(CommandScore.score("Reject", "xyz"), 0)
    }
}
