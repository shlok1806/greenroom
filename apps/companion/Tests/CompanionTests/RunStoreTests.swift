import XCTest

@testable import Companion

@MainActor
final class RunStoreTests: XCTestCase {
    private func store(withRun runId: String = "run-1") -> RunStore {
        let store = RunStore()
        store.runs = [RunSummary(runId: runId, createdAt: Date(timeIntervalSince1970: 0), status: .ready)]
        store.messages[runId] = []
        store.steps[runId] = []
        return store
    }

    private func message(_ seq: Int, kind: MessageKind = .note, from: MessageFrom = .coder) -> Message {
        Message(seq: seq, at: Date(timeIntervalSince1970: Double(seq)), from: from, kind: kind, text: "m\(seq)")
    }

    func testMessageIsAppendedOnce() {
        let store = store()
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: message(1))), .nothing)
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: message(2))), .nothing)
        XCTAssertEqual(store.messages["run-1"]?.map(\.seq), [1, 2])

        // The same message again, out of a reconnect, changes nothing.
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: message(2))), .nothing)
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: message(1))), .nothing)
        XCTAssertEqual(store.messages["run-1"]?.map(\.seq), [1, 2])
        XCTAssertEqual(store.runs[0].messages, 2)
    }

    func testMessagesForARunThatIsNotOpenAreIgnored() {
        let store = store()
        XCTAssertEqual(store.apply(.message(runId: "other", message: message(1))), .nothing)
        XCTAssertNil(store.messages["other"])
    }

    func testAVerdictAsksForTheRunAgain() {
        let store = store()
        let verdict = Message(
            seq: 5,
            at: Date(timeIntervalSince1970: 5),
            from: .verifier,
            kind: .verdict,
            text: "it builds",
            verdict: "pass",
            evidence: ["003-screenshot.png"]
        )
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: verdict)), .run("run-1"))
        XCTAssertEqual(store.messages["run-1"]?.last?.verdict, "pass")

        let accept = Message(seq: 6, at: Date(timeIntervalSince1970: 6), from: .human, kind: .accept, text: "", replyTo: 5)
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: accept)), .run("run-1"))
    }

    func testAStepAsksForTheStepsAgain() {
        let store = store()
        XCTAssertEqual(store.apply(.step(runId: "run-1", seq: 3, step: nil)), .steps("run-1"))
        XCTAssertEqual(store.runs[0].steps, 1)

        // A run whose steps are not loaded needs no fetch.
        XCTAssertEqual(store.apply(.step(runId: "other", seq: 4, step: nil)), .nothing)
        XCTAssertEqual(store.runs[0].steps, 1)
    }

    func testAStepAddsOneToTheCountEvenAcrossAGapInNumbering() {
        let store = store()
        let held = [1, 2, 3, 4, 6].map {
            Step(seq: $0, at: Date(timeIntervalSince1970: Double($0)), tool: "t", durationMs: 0)
        }
        store.steps["run-1"] = held
        store.runs[0].steps = held.count

        store.apply(.step(runId: "run-1", seq: 7, step: nil))
        XCTAssertEqual(store.runs[0].steps, 6)

        // A step already held, replayed by a reconnect, is not counted twice.
        store.apply(.step(runId: "run-1", seq: 6, step: nil))
        XCTAssertEqual(store.runs[0].steps, 6)
    }

    func testARunEventUpdatesTheMachineInPlace() throws {
        let store = store()
        store.details["run-1"] = RunDetail(runId: "run-1", image: "base", machineName: "gr-1")

        let machine = try JSONDecoder.daemon().decode(Machine.self, from: Data("""
        {"runId":"run-1","name":"gr-1","image":"base","ip":"192.168.64.9","status":"ready","createdAt":"2026-09-18T10:00:00Z","dir":"/runs/run-1"}
        """.utf8))

        let followup = store.apply(.run(LifecycleEvent(kind: .ready, runId: "run-1", machine: machine)))
        XCTAssertEqual(followup, .run("run-1"))
        XCTAssertEqual(store.details["run-1"]?.machine?.ip, "192.168.64.9")
        XCTAssertEqual(store.details["run-1"]?.status, .ready)
    }

    func testAwaitingVerifier() {
        func note(_ seq: Int, from: MessageFrom) -> Message { message(seq, kind: .note, from: from) }

        XCTAssertFalse(RunStore.awaitingVerifier([]))

        // A human note starts a turn, and the verifier owes an answer.
        XCTAssertTrue(RunStore.awaitingVerifier([note(1, from: .human)]))

        // Answered.
        XCTAssertFalse(RunStore.awaitingVerifier([
            note(1, from: .human),
            message(2, kind: .reply, from: .verifier),
        ]))

        // A coder note is context, not a question.
        XCTAssertFalse(RunStore.awaitingVerifier([note(1, from: .coder)]))

        // A task from either seat starts a turn.
        XCTAssertTrue(RunStore.awaitingVerifier([message(1, kind: .task, from: .coder)]))

        // Progress is the verifier working, not the answer that ends the turn.
        XCTAssertTrue(RunStore.awaitingVerifier([
            message(1, kind: .task, from: .coder),
            message(2, kind: .progress, from: .verifier),
        ]))

        // A question ends the turn: it is now the human's move.
        XCTAssertFalse(RunStore.awaitingVerifier([
            message(1, kind: .task, from: .coder),
            message(2, kind: .question, from: .verifier),
        ]))

        // A system event is not an answer.
        XCTAssertTrue(RunStore.awaitingVerifier([
            note(1, from: .human),
            message(2, kind: .event, from: .system),
        ]))
    }

    /// Raw SSE bytes through splitter, parser and merge, with no refetch.
    func testAVerifierTurnArrivesStraightFromTheStream() throws {
        let store = store()
        store.messages["run-1"] = [message(1, kind: .note, from: .human)]
        XCTAssertTrue(RunStore.awaitingVerifier(store.messages["run-1"] ?? []))

        let body = """
        : ping

        event: message
        data: {"runId":"run-1","message":{"seq":2,"at":"2026-09-18T10:00:01Z","from":"verifier","kind":"progress","text":"looking"}}

        event: message
        data: {"runId":"run-1","message":{"seq":3,"at":"2026-09-18T10:00:02Z","from":"verifier","kind":"reply","text":"it builds"}}


        """

        var splitter = SSELineSplitter()
        var parser = SSEParser()
        for byte in Array(body.utf8) {
            guard let line = splitter.consume(byte) else { continue }
            guard let event = try parser.consume(line) else { continue }
            store.apply(event)
        }

        XCTAssertEqual(store.messages["run-1"]?.map(\.seq), [1, 2, 3])
        XCTAssertEqual(store.messages["run-1"]?.last?.text, "it builds")
        XCTAssertFalse(RunStore.awaitingVerifier(store.messages["run-1"] ?? []))
    }

    private func frame(_ step: Int, file: String? = nil) -> Frame {
        Frame(at: Date(timeIntervalSince1970: Double(step)), file: file ?? "f\(step).jpg", step: step)
    }

    func testFrameIsAppendedAndDeduped() {
        let store = store()
        store.frames["run-1"] = []

        XCTAssertEqual(store.apply(.frame(runId: "run-1", frame: frame(1))), .nothing)
        XCTAssertEqual(store.apply(.frame(runId: "run-1", frame: frame(2))), .nothing)
        XCTAssertEqual(store.frames["run-1"]?.map(\.step), [1, 2])
        XCTAssertEqual(store.runs[0].frames, 2)

        // The same file again, out of a reconnect, changes nothing.
        XCTAssertEqual(store.apply(.frame(runId: "run-1", frame: frame(2, file: "f2.jpg"))), .nothing)
        XCTAssertEqual(store.frames["run-1"]?.count, 2)
        XCTAssertEqual(store.runs[0].frames, 2)
    }

    func testFramesForARunThatIsNotOpenAreIgnored() {
        let store = store()
        XCTAssertEqual(store.apply(.frame(runId: "other", frame: frame(1))), .nothing)
        XCTAssertNil(store.frames["other"])
    }

    func testResyncPlanFetchesEverythingForTheSelectedRunOnly() {
        XCTAssertEqual(RunStore.resyncPlan(selected: nil), [.runs])
        XCTAssertEqual(
            RunStore.resyncPlan(selected: "run-1"),
            [.runs, .detail("run-1"), .messages("run-1"), .steps("run-1"), .frames("run-1")]
        )
    }

    func testVerdictPrefersTheDetail() {
        let store = store()
        store.runs[0].verdict = VerdictState(seq: 1, verdict: "fail", status: .proposed)
        XCTAssertEqual(store.verdict("run-1")?.verdict, "fail")

        store.details["run-1"] = RunDetail(
            runId: "run-1",
            verdict: VerdictState(seq: 3, verdict: "pass", status: .accepted, acceptedBy: .human)
        )
        XCTAssertEqual(store.verdict("run-1")?.status, .accepted)
    }

    func testCancellationIsNotAFailure() {
        XCTAssertTrue(RunStore.isCancellation(CancellationError()))
        XCTAssertTrue(RunStore.isCancellation(DaemonError.cancelled))
        XCTAssertTrue(RunStore.isCancellation(URLError(.cancelled)))

        XCTAssertFalse(RunStore.isCancellation(URLError(.cannotConnectToHost)))
        XCTAssertFalse(RunStore.isCancellation(DaemonError.notReachable("connection refused")))
        XCTAssertFalse(RunStore.isCancellation(DaemonError.status(code: 404, body: "no ffmpeg")))
    }

    /// The request must outlive the Screen tab's creation, and a repeat click
    /// must be distinguishable by nonce.
    func testASeekRequestIsRaisedOnceAndKeepsItsPlace() {
        let store = store()
        XCTAssertNil(store.seekRequest)

        store.requestSeek(runId: "run-1", step: 4)
        let first = try? XCTUnwrap(store.seekRequest)
        XCTAssertEqual(first?.runId, "run-1")
        XCTAssertEqual(first?.step, 4)

        XCTAssertEqual(store.seekRequest, first)

        store.requestSeek(runId: "run-1", step: 9)
        XCTAssertEqual(store.seekRequest?.step, 9)
        XCTAssertNotEqual(store.seekRequest?.nonce, first?.nonce)
    }
}
