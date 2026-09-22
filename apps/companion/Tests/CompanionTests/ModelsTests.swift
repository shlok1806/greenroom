import XCTest

@testable import Companion

final class ModelsTests: XCTestCase {
    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try JSONDecoder.daemon().decode(type, from: Data(json.utf8))
    }

    func testRunSummaryDecodes() throws {
        let runs = try decode([RunSummary].self, """
        [
          {
            "runId": "run-2026-09-18-abc",
            "createdAt": "2026-09-18T10:00:00.123456789Z",
            "image": "ventura-base",
            "status": "ready",
            "ip": "192.168.64.9",
            "vncUrl": "vnc://127.0.0.1:5900",
            "steps": 7,
            "verdict": {"seq": 12, "verdict": "pass", "summary": "the app builds", "evidence": ["003-screenshot.png"], "status": "proposed", "disputes": 0},
            "lastActivity": "2026-09-18T10:04:00Z",
            "messages": 12
          },
          {
            "runId": "run-old",
            "createdAt": "2026-09-17T09:00:00Z",
            "destroyedAt": "2026-09-17T09:30:00Z",
            "image": "ventura-base",
            "status": "finished",
            "steps": 2,
            "verdict": null,
            "lastActivity": "2026-09-17T09:30:00Z",
            "messages": 4
          }
        ]
        """)

        XCTAssertEqual(runs.count, 2)
        XCTAssertEqual(runs[0].status, .ready)
        XCTAssertEqual(runs[0].verdict?.status, .proposed)
        XCTAssertEqual(runs[0].verdict?.evidence, ["003-screenshot.png"])
        XCTAssertEqual(runs[0].messages, 12)
        XCTAssertNil(runs[1].verdict)
        XCTAssertEqual(runs[1].status, .finished)
        XCTAssertNotNil(runs[1].destroyedAt)
    }

    /// A daemon built before ADR 0008 leaves `frames` out entirely.
    func testRunSummaryToleratesAMissingFramesField() throws {
        let run = try decode(RunSummary.self, """
        {"runId":"run-1","createdAt":"2026-09-18T10:00:00Z","image":"base","status":"ready","steps":1,"verdict":null,"lastActivity":"2026-09-18T10:00:01Z","messages":1}
        """)
        XCTAssertNil(run.frames)

        let withFrames = try decode(RunSummary.self, """
        {"runId":"run-1","createdAt":"2026-09-18T10:00:00Z","image":"base","status":"ready","steps":1,"verdict":null,"lastActivity":"2026-09-18T10:00:01Z","messages":1,"frames":42}
        """)
        XCTAssertEqual(withFrames.frames, 42)
    }

    /// One frame of a run's recording (ADR 0008).
    func testFrameDecodes() throws {
        let frames = try decode([Frame].self, """
        [
          {"at": "2026-09-18T10:00:00Z", "file": "1758300000123.jpg", "step": 14, "bytes": 102400},
          {"at": "2026-09-18T10:00:02Z", "file": "1758300002123.jpg", "step": 15, "bytes": 98304}
        ]
        """)
        XCTAssertEqual(frames.count, 2)
        XCTAssertEqual(frames[0].file, "1758300000123.jpg")
        XCTAssertEqual(frames[0].step, 14)
        XCTAssertEqual(frames[0].bytes, 102400)
        XCTAssertEqual(frames[1].step, 15)
    }

    func testRunDetailDecodes() throws {
        let detail = try decode(RunDetail.self, """
        {
          "runId": "run-1",
          "image": "ventura-base",
          "machineName": "gr-run-1",
          "ip": "192.168.64.9",
          "createdAt": "2026-09-18T10:00:00Z",
          "steps": 3,
          "machine": {
            "runId": "run-1",
            "name": "gr-run-1",
            "image": "ventura-base",
            "ip": "192.168.64.9",
            "status": "ready",
            "bootSeconds": 14.2,
            "createdAt": "2026-09-18T10:00:00Z",
            "dir": "/Users/x/.greenroom/runs/run-1"
          },
          "verdict": {"status": "none", "disputes": 0}
        }
        """)

        XCTAssertEqual(detail.machineName, "gr-run-1")
        XCTAssertEqual(detail.machine?.bootSeconds, 14.2)
        XCTAssertEqual(detail.status, .ready)
        XCTAssertEqual(detail.address, "192.168.64.9")
        XCTAssertEqual(detail.verdict.status, .none)
    }

    func testDetailOfARunWhoseMachineIsGone() throws {
        let detail = try decode(RunDetail.self, """
        {
          "runId": "run-1",
          "image": "ventura-base",
          "machineName": "gr-run-1",
          "createdAt": "2026-09-18T10:00:00Z",
          "destroyedAt": "2026-09-18T10:20:00Z",
          "steps": 9,
          "machine": null,
          "verdict": {"seq": 8, "verdict": "fail", "summary": "no", "status": "accepted", "acceptedBy": "human", "disputes": 1}
        }
        """)

        XCTAssertNil(detail.machine)
        XCTAssertEqual(detail.status, .finished)
        XCTAssertEqual(detail.verdict.acceptedBy, .human)
        XCTAssertEqual(detail.verdict.disputes, 1)
    }

    /// The API writes `null` rather than leaving the key out for `verdict`,
    /// `machine` and `destroyedAt`.
    func testNullsDecodeAsAbsent() throws {
        let detail = try decode(RunDetail.self, """
        {"runId":"run-1","image":"base","machineName":"gr-1","createdAt":"2026-09-18T10:00:00Z","destroyedAt":null,"steps":0,"machine":null,"verdict":null}
        """)
        XCTAssertNil(detail.destroyedAt)
        XCTAssertNil(detail.machine)
        XCTAssertEqual(detail.verdict.status, .none)
    }

    /// A kind the app has never heard of must decode, not crash.
    func testMessagePageWithAnUnknownKind() throws {
        let page = try decode(MessagePage.self, """
        {
          "messages": [
            {"seq": 1, "at": "2026-09-18T10:00:00Z", "from": "coder", "kind": "task", "text": "check the login screen"},
            {"seq": 2, "at": "2026-09-18T10:00:01.5Z", "from": "verifier", "kind": "progress", "text": "machine_exec: xcodebuild", "step": 1},
            {"seq": 3, "at": "2026-09-18T10:00:02Z", "from": "verifier", "kind": "question", "text": "which scheme?"},
            {"seq": 4, "at": "2026-09-18T10:00:03Z", "from": "human", "kind": "answer", "text": "Release", "replyTo": 3},
            {"seq": 5, "at": "2026-09-18T10:00:04Z", "from": "someoneNew", "kind": "hologram", "text": "from the future"}
          ],
          "last": 5,
          "verdict": {"status": "none", "disputes": 0}
        }
        """)

        XCTAssertEqual(page.last, 5)
        XCTAssertEqual(page.messages[1].step, 1)
        XCTAssertEqual(page.messages[3].replyTo, 3)
        XCTAssertEqual(page.messages[4].kind, .unknown("hologram"))
        XCTAssertEqual(page.messages[4].from, .unknown("someoneNew"))
        XCTAssertEqual(page.messages[4].kind.text, "hologram")
    }

    /// A `reply` is the verifier answering a human with no verdict attached
    /// (ADR 0006, "A human is always answered").
    func testMessagePageWithAReply() throws {
        let page = try decode(MessagePage.self, """
        {
          "messages": [
            {"seq": 1, "at": "2026-09-18T10:00:00Z", "from": "human", "kind": "note", "text": "how is the boot going?"},
            {"seq": 2, "at": "2026-09-18T10:00:01Z", "from": "verifier", "kind": "reply", "text": "still booting, 20 s in", "replyTo": 1}
          ],
          "last": 2,
          "verdict": {"status": "none", "disputes": 0}
        }
        """)

        XCTAssertEqual(page.messages[1].kind, .reply)
        XCTAssertEqual(page.messages[1].kind.text, "reply")
        XCTAssertEqual(page.messages[1].from, .verifier)
        XCTAssertEqual(page.messages[1].replyTo, 1)
        XCTAssertNil(page.messages[1].verdict)
    }

    func testStepsDecodeWithArbitraryJSON() throws {
        let steps = try decode([Step].self, """
        [
          {"seq": 1, "at": "2026-09-18T10:00:00Z", "tool": "machine_exec", "input": {"command": "ls", "args": ["-l", "/"]}, "output": {"exitCode": 0, "stdout": "total 0"}, "durationMs": 340},
          {"seq": 2, "at": "2026-09-18T10:00:01Z", "tool": "machine_screenshot", "output": {"path": "/runs/run-1/artifacts/002-screenshot.png", "bytes": 98213}, "durationMs": 900},
          {"seq": 3, "at": "2026-09-18T10:00:02Z", "tool": "machine_exec", "error": "exit status 1", "durationMs": 20}
        ]
        """)

        XCTAssertEqual(steps[0].input?["command"]?.stringValue, "ls")
        XCTAssertEqual(steps[1].screenshotArtifact, "002-screenshot.png")
        XCTAssertNil(steps[0].screenshotArtifact)
        XCTAssertEqual(steps[2].error, "exit status 1")
        XCTAssertTrue(steps[0].output?.prettyPrinted.contains("\"exitCode\" : 0") ?? false)
    }

    func testLifecycleEventDecodes() throws {
        let event = try decode(LifecycleEvent.self, """
        {"kind": "destroyed", "runId": "run-1"}
        """)
        XCTAssertEqual(event.kind, .destroyed)
        XCTAssertNil(event.machine)

        let odd = try decode(LifecycleEvent.self, #"{"kind":"hibernated","runId":"run-1"}"#)
        XCTAssertEqual(odd.kind, .unknown("hibernated"))
    }

    func testDatesParseWithAndWithoutFractions() {
        XCTAssertNotNil(DaemonDate.parse("2026-09-18T10:00:00Z"))
        XCTAssertNotNil(DaemonDate.parse("2026-09-18T10:00:00.123Z"))
        XCTAssertNotNil(DaemonDate.parse("2026-09-18T10:00:00.123456789Z"))
        XCTAssertNil(DaemonDate.parse("not a time"))
    }

    func testVerdictStatusIsOpenOnlyWhileItCanBeClosed() {
        XCTAssertTrue(VerdictStatus.proposed.isOpen)
        XCTAssertTrue(VerdictStatus.contested.isOpen)
        XCTAssertFalse(VerdictStatus.accepted.isOpen)
        XCTAssertFalse(VerdictStatus.rejected.isOpen)
        XCTAssertFalse(VerdictStatus.none.isOpen)
    }
}
