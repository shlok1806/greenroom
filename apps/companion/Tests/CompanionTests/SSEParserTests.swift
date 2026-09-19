import XCTest

@testable import Companion

final class SSEParserTests: XCTestCase {
    /// Two frames, a multi-line data field, a heartbeat comment and an event
    /// kind the app does not know. Nothing here may throw or crash.
    func testParsesAStream() throws {
        let body = """
        : heartbeat

        event: run
        data: {"kind":"ready","runId":"run-1","machine":{"runId":"run-1","name":"gr-1","image":"base","ip":"192.168.64.9","status":"ready","bootSeconds":12.5,"createdAt":"2026-09-18T10:00:00Z","dir":"/runs/run-1","vncUrl":"vnc://x"}}

        event: message
        data: {"runId":"run-1","message":{"seq":4,"at":"2026-09-18T10:00:03.25Z",
        data: "from":"verifier","kind":"question","text":"which scheme?"}}

        : heartbeat

        event: step
        data: {"runId":"run-1","step":3}

        event: weather
        data: {"runId":"run-1"}

        """

        let events = try SSEParser.parse(body)
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

    /// A whole step record in the same field decodes too, so a daemon that
    /// starts sending one needs no change here.
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

    /// A recorded frame (ADR 0008): the daemon flattens the frame's fields
    /// into the event rather than nesting them under a `frame` key.
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
