import XCTest

@testable import Companion

private func parse(_ text: String) throws -> [ServerEvent] {
    var parser = SSEParser()
    var events: [ServerEvent] = []
    for line in text.components(separatedBy: "\n") + [""] {
        if let event = try parser.consume(line) { events.append(event) }
    }
    return events
}

private func lines(of bytes: [UInt8]) -> [String] {
    var splitter = SSELineSplitter()
    var lines = bytes.compactMap { splitter.consume($0) }
    if let last = splitter.flush() { lines.append(last) }
    return lines
}

final class SSEParserTests: XCTestCase {
    func testParsesAStream() throws {
        let body = """
        : heartbeat

        event: run
        data: {"kind":"ready","runId":"run-1","machine":{"runId":"run-1","name":"gr-1","image":"base","ip":"192.168.64.9","status":"ready","bootSeconds":12.5,"createdAt":"2026-09-18T10:00:00Z","dir":"/runs/run-1"}}

        event: message
        data: {"runId":"run-1","message":{"seq":4,"at":"2026-09-18T10:00:03.25Z",
        data: "from":"verifier","kind":"question","text":"which scheme?"}}

        : heartbeat

        event: step
        data: {"runId":"run-1","step":3}

        event: weather
        data: {"runId":"run-1"}

        """

        let events = try parse(body)
        XCTAssertEqual(events.count, 3)

        guard case .run(let lifecycle) = events[0] else { return XCTFail("first event is not a run event") }
        XCTAssertEqual(lifecycle.kind, .ready)
        XCTAssertEqual(lifecycle.runId, "run-1")
        XCTAssertEqual(lifecycle.machine?.ip, "192.168.64.9")
        XCTAssertEqual(lifecycle.machine?.status, .ready)

        guard case .message(let runId, let message) = events[1] else { return XCTFail("second event is not a message") }
        XCTAssertEqual(runId, "run-1")
        XCTAssertEqual(message.seq, 4)
        XCTAssertEqual(message.kind, .question)
        XCTAssertEqual(message.from, .verifier)
        XCTAssertEqual(message.text, "which scheme?")

        // The daemon sends the step's number, not the record.
        guard case .step(let stepRunId, let seq, let step) = events[2] else { return XCTFail("third event is not a step") }
        XCTAssertEqual(stepRunId, "run-1")
        XCTAssertEqual(seq, 3)
        XCTAssertNil(step)
    }

    func testAStepEventCarryingTheWholeRecord() throws {
        var parser = SSEParser()
        XCTAssertNil(try parser.consume("event: step"))
        XCTAssertNil(try parser.consume(#"data: {"runId":"run-1","step":{"seq":7,"at":"2026-09-18T10:00:04Z","tool":"machine_screenshot","output":{"path":"/runs/run-1/artifacts/007-screenshot.png"},"durationMs":120}}"#))
        guard case .step(_, let seq, let step)? = try parser.consume("") else { return XCTFail("no step event") }
        XCTAssertEqual(seq, 7)
        XCTAssertEqual(step?.screenshotArtifact, "007-screenshot.png")
    }

    /// A field with no value, and `data` with no leading space, are both legal.
    func testToleratesOddFraming() throws {
        var parser = SSEParser()
        XCTAssertNil(try parser.consume("event:message"))
        XCTAssertNil(try parser.consume("id"))
        XCTAssertNil(try parser.consume(#"data:{"runId":"r","message":{"seq":1,"at":"2026-09-18T10:00:00Z","from":"human","kind":"note","text":"hi"}}"#))
        let event = try parser.consume("")
        guard case .message(_, let message)? = event else { return XCTFail("no message came out") }
        XCTAssertEqual(message.text, "hi")
        XCTAssertEqual(message.from, .human)
    }

    /// The daemon flattens the frame's fields into the envelope.
    func testAFrameEventDecodes() throws {
        var parser = SSEParser()
        XCTAssertNil(try parser.consume("event: frame"))
        XCTAssertNil(try parser.consume(#"data: {"runId":"run-1","at":"2026-09-18T10:00:02Z","file":"1758300002123.jpg","step":15}"#))
        guard case .frame(let runId, let frame)? = try parser.consume("") else { return XCTFail("no frame event") }
        XCTAssertEqual(runId, "run-1")
        XCTAssertEqual(frame.file, "1758300002123.jpg")
        XCTAssertEqual(frame.step, 15)
    }

    /// A malformed frame is an error, not a crash, and the parser carries on.
    func testBadJSONThrowsAndResets() throws {
        var parser = SSEParser()
        XCTAssertNil(try parser.consume("event: run"))
        XCTAssertNil(try parser.consume("data: {not json"))
        XCTAssertThrowsError(try parser.consume(""))

        XCTAssertNil(try parser.consume("event: run"))
        XCTAssertNil(try parser.consume(#"data: {"kind":"destroyed","runId":"run-2"}"#))
        guard case .run(let lifecycle)? = try parser.consume("") else { return XCTFail("no run event") }
        XCTAssertEqual(lifecycle.kind, .destroyed)
    }
}

/// `AsyncBytes.lines` drops blank lines, which terminate SSE frames.
final class SSELineSplitterTests: XCTestCase {
    func testBlankLinesSurvive() {
        let body = "event: message\ndata: {}\n\nevent: frame\ndata: {}\n\n"
        XCTAssertEqual(
            lines(of: Array(body.utf8)),
            ["event: message", "data: {}", "", "event: frame", "data: {}", ""]
        )
    }

    func testAByteStreamDispatchesEveryEvent() throws {
        let body = """
        : ping

        event: message
        data: {"runId":"run-1","message":{"seq":9,"at":"2026-09-18T10:00:00Z","from":"verifier","kind":"progress","text":"looking"}}

        event: message
        data: {"runId":"run-1","message":{"seq":10,"at":"2026-09-18T10:00:01Z","from":"verifier","kind":"reply","text":"it builds"}}


        """

        var splitter = SSELineSplitter()
        var parser = SSEParser()
        var events: [ServerEvent] = []
        for byte in Array(body.utf8) {
            guard let line = splitter.consume(byte) else { continue }
            if let event = try parser.consume(line) { events.append(event) }
        }

        XCTAssertEqual(events.count, 2)
        guard case .message(_, let progress) = events[0], case .message(_, let reply) = events[1] else {
            return XCTFail("the stream did not produce two messages")
        }
        XCTAssertEqual(progress.kind, .progress)
        XCTAssertEqual(reply.kind, .reply)
        XCTAssertEqual(reply.text, "it builds")
    }

    func testFlushReturnsATrailingPartialLine() {
        var splitter = SSELineSplitter()
        for byte in Array("a\nb".utf8) { _ = splitter.consume(byte) }
        XCTAssertEqual(splitter.flush(), "b")
        XCTAssertNil(splitter.flush())
    }

    func testCarriageReturnsAreLeftForTheParser() {
        XCTAssertEqual(lines(of: Array("event: run\r\n\r\n".utf8)), ["event: run\r", "\r"])
    }
}
