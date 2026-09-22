import XCTest

@testable import Companion

/// The transcript is where agents write Markdown, and where the app decides
/// how much of it to honour. These are that line.
final class RichTextTests: XCTestCase {
    func testPlainTextIsOneBlock() {
        XCTAssertEqual(RichText.blocks("just a sentence"), [.prose("just a sentence")])
    }

    func testAHeadingIsCountedAndStripped() {
        XCTAssertEqual(RichText.blocks("## Project Overview"), [.heading("Project Overview", level: 2)])
        XCTAssertEqual(RichText.blocks("#### Deep"), [.heading("Deep", level: 4)])
    }

    /// Seven hashes is not a heading, and a hash on its own is not one either.
    func testWhatIsNotAHeading() {
        XCTAssertEqual(RichText.blocks("####### too deep"), [.prose("####### too deep")])
        XCTAssertEqual(RichText.blocks("#"), [.prose("#")])
    }

    func testProseAroundAHeadingKeepsItsParagraphs() {
        let blocks = RichText.blocks("intro line\n\n# Title\nbody one\nbody two")
        XCTAssertEqual(blocks, [
            .prose("intro line"),
            .heading("Title", level: 1),
            .prose("body one\nbody two"),
        ])
    }

    func testAFencedBlockKeepsItsLinesAndLosesItsFence() {
        let blocks = RichText.blocks("before\n```\nerror: nope\nsecond\n```\nafter")
        XCTAssertEqual(blocks, [
            .prose("before"),
            .code("error: nope\nsecond"),
            .prose("after"),
        ])
    }

    /// A fence the agent never closed must not swallow the rest as prose.
    func testAnUnclosedFenceStillEndsAsCode() {
        XCTAssertEqual(RichText.blocks("```\nhanging"), [.code("hanging")])
    }

    func testALanguageTagOnTheFenceIsNotContent() {
        XCTAssertEqual(RichText.blocks("```sh\nls -la\n```"), [.code("ls -la")])
    }

    /// A Markdown table's columns are spaces, so reflowing it destroys the one
    /// thing it was written for.
    func testATableIsKeptMonospaced() {
        let table = "| Tool | Required |\n|------|----------|\n| Go | 1.27.1 |"
        XCTAssertEqual(RichText.blocks(table), [.code(table)])
    }

    func testATableBetweenProseIsItsOwnBlock() {
        let blocks = RichText.blocks("head\n| a | b |\n| c | d |\ntail")
        XCTAssertEqual(blocks, [
            .prose("head"),
            .code("| a | b |\n| c | d |"),
            .prose("tail"),
        ])
    }

    func testAPipeThatIsNotATableIsProse() {
        XCTAssertEqual(RichText.blocks("ls | grep go"), [.prose("ls | grep go")])
    }

    func testEmptyTextIsNoBlocks() {
        XCTAssertTrue(RichText.blocks("").isEmpty)
        XCTAssertTrue(RichText.blocks("\n\n").isEmpty)
    }

    /// The input that made `Block`'s content-derived id collide. `Block` is
    /// no longer `Identifiable`, so a list can no longer be keyed on it and
    /// the compiler is what keeps this from coming back; these two cases
    /// record the shape of message that used to lose a row.
    func testAMessageThatSaysTheSameThingTwiceKeepsBothBlocks() {
        let blocks = RichText.blocks("done.\n\n# Result\ndone.")
        XCTAssertEqual(blocks, [
            .prose("done."),
            .heading("Result", level: 1),
            .prose("done."),
        ])
    }

    func testTwoIdenticalFencedBlocksAreBothKept() {
        let blocks = RichText.blocks("```\ngo test ./...\n```\nand again\n```\ngo test ./...\n```")
        XCTAssertEqual(blocks, [
            .code("go test ./..."),
            .prose("and again"),
            .code("go test ./..."),
        ])
    }

    /// A table whose last row runs straight into a fence used to come back
    /// glued to the front of the fenced code as one block.
    func testATableThatRunsIntoAFenceStaysItsOwnBlock() {
        let blocks = RichText.blocks("| Tool | Version |\n|---|---|\n```\ngo build ./...\n```")
        XCTAssertEqual(blocks, [
            .code("| Tool | Version |\n|---|---|"),
            .code("go build ./..."),
        ])
    }

    /// Nothing here may grow with the square of the input: a verifier can
    /// write a very long message and the transcript parses it on every draw.
    func testAVeryLongMessageIsSplitInReasonableTime() {
        let text = (0..<4000).map { "line \($0) with some **bold** words in it" }.joined(separator: "\n")
        let started = Date()
        let blocks = RichText.blocks(text)
        XCTAssertEqual(blocks.count, 1)
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }
}
