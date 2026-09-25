import XCTest

@testable import Companion

/// How a message's Markdown is cut into what the transcript draws (`MarkdownText`).
final class MarkdownTextTests: XCTestCase {
    private typealias Span = MarkdownText.Span

    func testPlainTextIsOneParagraph() {
        XCTAssertEqual(MarkdownText.blocks("just a sentence"), [.paragraph([Span("just a sentence")])])
    }

    func testNothingIsNoBlocks() {
        XCTAssertTrue(MarkdownText.blocks("").isEmpty)
        XCTAssertTrue(MarkdownText.blocks("\n\n").isEmpty)
    }

    func testHeadingsKeepTheirLevel() {
        XCTAssertEqual(MarkdownText.blocks("## Project Overview"), [.heading(level: 2, [Span("Project Overview")])])
        XCTAssertEqual(MarkdownText.blocks("#### Deep"), [.heading(level: 4, [Span("Deep")])])
    }

    /// Seven hashes is not a heading.
    func testTooManyHashesIsProse() {
        XCTAssertEqual(MarkdownText.blocks("####### too deep"), [.paragraph([Span("####### too deep")])])
    }

    /// Agents write screen text one line at a time with no blank lines between: each line
    /// stays its own line, as a comment on GitHub reads it.
    func testASingleLineBreakStays() {
        XCTAssertEqual(MarkdownText.blocks("Visible text:\n\"Bill 120\"\n\"Tip: $24.00\""),
                       [.paragraph([Span("Visible text:\n\"Bill 120\"\n\"Tip: $24.00\"")])])
    }

    func testABlankLineStartsAParagraph() {
        XCTAssertEqual(MarkdownText.blocks("one\n\ntwo"), [.paragraph([Span("one")]), .paragraph([Span("two")])])
    }

    func testEmphasisStrongAndCodeAreStyledRuns() {
        XCTAssertEqual(MarkdownText.blocks("a *b* **c** `d` ***e*** ~~f~~"), [.paragraph([
            Span("a "), Span("b", .emphasis), Span(" "), Span("c", .strong), Span(" "), Span("d", .code),
            Span(" "), Span("e", [.emphasis, .strong]), Span(" "), Span("f", .strikethrough),
        ])])
    }

    /// Smart punctuation is off: an agent's `--` and straight quotes stay as written.
    func testPunctuationIsAsWritten() {
        XCTAssertEqual(MarkdownText.blocks("run it -- \"now\""), [.paragraph([Span("run it -- \"now\"")])])
    }

    func testAFencedBlockIsCodeWithItsLanguage() {
        XCTAssertEqual(MarkdownText.blocks("before\n```sh\nls -la\n```\nafter"), [
            .paragraph([Span("before")]), .code(language: "sh", text: "ls -la"), .paragraph([Span("after")]),
        ])
    }

    func testAnUnclosedFenceRunsToTheEnd() {
        XCTAssertEqual(MarkdownText.blocks("```\nhanging"), [.code(language: nil, text: "hanging")])
    }

    /// JSON in a block with no language (or `json`) is laid out one key per line.
    func testJSONInABlockIsPrettyPrinted() {
        XCTAssertEqual(MarkdownText.blocks("```\n{\"b\":1,\"a\":[true]}\n```"),
                       [.code(language: "json", text: "{\n  \"a\": [\n    true\n  ],\n  \"b\": 1\n}")])
        // Another language's block is left as written, even when it parses.
        XCTAssertEqual(MarkdownText.blocks("```js\n{\"a\":1}\n```"), [.code(language: "js", text: "{\"a\":1}")])
    }

    func testPrettyJSONOnlyForAnObjectOrArray() {
        XCTAssertNil(MarkdownText.prettyJSON("42"))
        XCTAssertNil(MarkdownText.prettyJSON("{not json"))
        XCTAssertEqual(MarkdownText.prettyJSON("[1]"), "[\n  1\n]")
        XCTAssertEqual(MarkdownText.prettyJSON("{}"), "{}")
    }

    /// A decimal reads as written, not as its nearest binary double.
    func testPrettyJSONKeepsDecimalsShort() {
        XCTAssertEqual(MarkdownText.prettyJSON("{\"tip\":0.2,\"path\":\"/a/b\",\"q\":\"say \\\"hi\\\"\"}"),
                       "{\n  \"path\": \"/a/b\",\n  \"q\": \"say \\\"hi\\\"\",\n  \"tip\": 0.2\n}")
    }

    func testListsKeepTheirOrderAndStart() {
        XCTAssertEqual(MarkdownText.blocks("- one\n- two"), [.list(ordered: false, start: 1, items: [
            .init(checked: nil, blocks: [.paragraph([Span("one")])]),
            .init(checked: nil, blocks: [.paragraph([Span("two")])]),
        ])])
        guard case .list(true, 3, let items)? = MarkdownText.blocks("3. three\n4. four").first else {
            return XCTFail("an ordered list from 3")
        }
        XCTAssertEqual(items.count, 2)
    }

    func testATaskListCarriesItsCheckboxes() {
        guard case .list(_, _, let items)? = MarkdownText.blocks("- [x] built\n- [ ] tested").first else {
            return XCTFail("a list")
        }
        XCTAssertEqual(items.map(\.checked), [true, false])
    }

    func testNestedListsAreItemsOfItems() {
        guard case .list(_, _, let items)? = MarkdownText.blocks("- outer\n  - inner").first,
              case .list(false, _, let inner)? = items.first?.blocks.last else {
            return XCTFail("a list inside a list")
        }
        XCTAssertEqual(inner.first?.blocks, [.paragraph([Span("inner")])])
    }

    func testAQuoteHoldsItsBlocks() {
        XCTAssertEqual(MarkdownText.blocks("> said so"), [.quote([.paragraph([Span("said so")])])])
    }

    func testATableIsAHeaderAndRows() {
        XCTAssertEqual(MarkdownText.blocks("| Tool | Version |\n|---|---|\n| go | 1.25 |"), [
            .table(header: [[Span("Tool")], [Span("Version")]], rows: [[[Span("go")], [Span("1.25")]]]),
        ])
    }

    func testARuleIsARule() {
        XCTAssertEqual(MarkdownText.blocks("above\n\n---\n\nbelow"), [
            .paragraph([Span("above")]), .rule, .paragraph([Span("below")]),
        ])
    }

    func testALinkKeepsItsDestination() {
        XCTAssertEqual(MarkdownText.blocks("see [the docs](https://example.com)"),
                       [.paragraph([Span("see "), Span("the docs", link: "https://example.com")])])
    }

    /// The app reads nothing but the daemon, so a picture in a message stands as its words.
    func testAnImageIsItsWords() {
        XCTAssertEqual(MarkdownText.blocks("![the tip screen](https://x/y.png)"), [.paragraph([Span("the tip screen")])])
    }

    // MARK: - Citations

    func testACitedStepInTheRecordIsAChip() {
        XCTAssertEqual(MarkdownText.blocks("The screenshot at step 16 shows it.", steps: [16]), [.paragraph([
            Span("The screenshot at "), Span("step 16", step: 16), Span(" shows it."),
        ])])
    }

    func testEveryNumberOfAListIsItsOwnChip() {
        XCTAssertEqual(MarkdownText.blocks("Steps 4, 5 and 6 agree.", steps: [4, 5, 6]), [.paragraph([
            Span("Steps "), Span("4", step: 4), Span(", "), Span("5", step: 5), Span(" and "), Span("6", step: 6),
            Span(" agree."),
        ])])
    }

    /// A step the run never recorded is not a chip: it would seek nothing.
    func testAStepNotInTheRecordIsWords() {
        XCTAssertEqual(MarkdownText.blocks("see step 99", steps: [1, 2]), [.paragraph([Span("see step 99")])])
        XCTAssertEqual(MarkdownText.blocks("see steps 1 and 99", steps: [1]), [.paragraph([Span("see steps 1 and 99")])])
    }

    /// Before the record is read nothing is cited.
    func testWithNoRecordNothingIsCited() {
        XCTAssertEqual(MarkdownText.blocks("see step 3"), [.paragraph([Span("see step 3")])])
    }

    func testCodeAndDecimalsAreNeverCitations() {
        XCTAssertEqual(MarkdownText.blocks("`step 3` and step 3.5", steps: [3]),
                       [.paragraph([Span("step 3", .code), Span(" and step 3.5")])])
    }

    func testACitationKeepsItsStyle() {
        XCTAssertEqual(MarkdownText.blocks("**at step 2**", steps: [2]),
                       [.paragraph([Span("at ", .strong), Span("step 2", .strong, step: 2)])])
    }

    func testCitedStepsAreInOrderOnceEach() {
        let blocks = MarkdownText.blocks("step 3, then step 1\n\n- again step 3\n\n> step 2", steps: [1, 2, 3])
        XCTAssertEqual(MarkdownText.citedSteps(blocks), [3, 1, 2])
    }

    // MARK: - Plain

    func testPlainTextOfBlocksReadsAloud() {
        let blocks = MarkdownText.blocks("# Result\n\nIt **works**.\n\n- one\n- two")
        XCTAssertEqual(MarkdownText.plain(blocks), "Result\nIt works.\n• one\n• two")
    }
}
