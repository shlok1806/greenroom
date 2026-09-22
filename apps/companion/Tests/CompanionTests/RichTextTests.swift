import XCTest

@testable import Companion

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

    /// Repeated text must yield repeated blocks (views key them by position).
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

    /// A table running straight into a fence stays its own block.
    func testATableThatRunsIntoAFenceStaysItsOwnBlock() {
        let blocks = RichText.blocks("| Tool | Version |\n|---|---|\n```\ngo build ./...\n```")
        XCTAssertEqual(blocks, [
            .code("| Tool | Version |\n|---|---|"),
            .code("go build ./..."),
        ])
    }

    /// Parsed on every draw, so it must stay linear.
    func testAVeryLongMessageIsSplitInReasonableTime() {
        let text = (0..<4000).map { "line \($0) with some **bold** words in it" }.joined(separator: "\n")
        let started = Date()
        let blocks = RichText.blocks(text)
        XCTAssertEqual(blocks.count, 1)
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }
}
