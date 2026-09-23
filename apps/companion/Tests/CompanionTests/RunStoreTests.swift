import AppKit
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

    func testMessagesForARunThatIsNotOpenAreNotHeld() {
        let store = store()
        XCTAssertEqual(store.apply(.message(runId: "other", message: message(1))), .nothing)
        XCTAssertNil(store.messages["other"])
    }

    /// A verdict arriving for a run nobody has open must still reach its sidebar row.
    func testAVerdictForARunThatIsNotOpenAsksForTheRunList() {
        let store = store()
        store.runs.append(RunSummary(runId: "live", createdAt: Date(timeIntervalSince1970: 0), status: .ready, messages: 15))
        let verdict = Message(seq: 16, at: Date(timeIntervalSince1970: 16), from: .verifier, kind: .verdict, text: "no",
                              verdict: "fail")
        XCTAssertEqual(store.apply(.message(runId: "live", message: verdict)), .run("live"))
        XCTAssertNil(store.messages["live"])
        XCTAssertEqual(store.run("live")?.messages, 16)
    }

    /// A detail read before the verdict existed must not hide the list's newer one.
    func testANewerListedVerdictWinsOverAnEmptyOrOlderDetail() {
        let store = store()
        store.details["run-1"] = RunDetail(runId: "run-1")
        store.runs[0].verdict = VerdictState(seq: 16, verdict: "fail", status: .proposed)
        XCTAssertEqual(store.verdict("run-1")?.status, .proposed)
        store.details["run-1"] = RunDetail(runId: "run-1", verdict: VerdictState(seq: 9, verdict: "pass", status: .proposed))
        XCTAssertEqual(store.verdict("run-1")?.seq, 16)
    }

    /// A daemon without `RunSummary.task` still gets rows named by their task.
    func testRunsWithoutATaskLearnItFromTheirTranscript() async {
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs": .json(#"[{"runId": "20260919-170059-65804c", "createdAt": "2026-09-19T17:00:59Z", "messages": 2}]"#)
            default: .json(#"{"messages": [{"seq": 1, "from": "system", "kind": "event", "text": "machine is ready"}, {"seq": 2, "from": "coder", "kind": "task", "text": "Look around ~/work"}]}"#)
            }
        }
        let store = RunStore(client: client)
        await store.resync()
        for _ in 0..<50 where store.runs.first?.task == nil {
            try? await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertEqual(store.runs.first?.task, "Look around ~/work")
        // A later read of the list keeps what was learned.
        await store.resync()
        XCTAssertEqual(store.runs.first?.task, "Look around ~/work")
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

    /// Seen in the app on a destroyed run: a note sent there showed "Verifier is working"
    /// under a composer saying nothing will answer.
    func testNobodyIsWorkingOnARunWhoseVerifierStopped() {
        let note = message(1, kind: .note, from: .human)
        XCTAssertTrue(RunStore.verifierIsWorking([note], verifierListens: true))
        XCTAssertFalse(RunStore.verifierIsWorking([note], verifierListens: false))
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

    func testAFrameIsNotActivity() {
        let store = store()
        store.frames["run-1"] = []
        let before = store.runs[0].lastActivity
        let later = Frame(at: before.addingTimeInterval(3600), file: "late.jpg", step: 1)

        XCTAssertEqual(store.apply(.frame(runId: "run-1", frame: later)), .nothing)
        XCTAssertEqual(store.runs[0].frames, 1)
        XCTAssertEqual(store.runs[0].lastActivity, before, "an idle machine's frames made it look active")
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

    func testAClosedVerdictWinsATieOnSeq() {
        let store = store()
        store.details["run-1"] = RunDetail(
            runId: "run-1",
            verdict: VerdictState(seq: 5, verdict: "pass", status: .proposed)
        )
        store.runs[0].verdict = VerdictState(seq: 5, verdict: "pass", status: .accepted, acceptedBy: .human)
        XCTAssertEqual(store.verdict("run-1")?.status, .accepted)

        store.runs[0].verdict = VerdictState(seq: 5, verdict: "pass", status: .proposed)
        store.details["run-1"]?.verdict = VerdictState(seq: 5, verdict: "pass", status: .rejected)
        XCTAssertEqual(store.verdict("run-1")?.status, .rejected)
    }

    func testVerdictCardDraftsBelongToOneRunAndOneVerdict() {
        let verdict = VerdictState(seq: 7, verdict: "fail", status: .proposed)
        let same = VerdictCard.identity(runId: "run-1", verdict: verdict)
        XCTAssertEqual(same, VerdictCard.identity(runId: "run-1", verdict: VerdictState(seq: 7, verdict: "fail", status: .accepted)),
                       "accepting must not wipe the card mid-action")
        XCTAssertNotEqual(same, VerdictCard.identity(runId: "run-2", verdict: verdict), "a draft carried into the next run")
        XCTAssertNotEqual(same, VerdictCard.identity(runId: "run-1", verdict: VerdictState(seq: 9, verdict: "pass", status: .proposed)),
                          "a draft carried onto a new verdict")
    }

    func testAContestedVerdictWinsATieOverAProposedOne() {
        let store = store()
        store.details["run-1"] = RunDetail(runId: "run-1", verdict: VerdictState(seq: 5, verdict: "fail", status: .proposed))
        store.runs[0].verdict = VerdictState(seq: 5, verdict: "fail", status: .contested)
        XCTAssertEqual(store.verdict("run-1")?.status, .contested)

        store.runs[0].verdict = VerdictState(seq: 5, verdict: "fail", status: .proposed)
        store.details["run-1"]?.verdict = VerdictState(seq: 5, verdict: "fail", status: .contested)
        XCTAssertEqual(store.verdict("run-1")?.status, .contested)
    }

    /// Reproduces a restarted daemon with a shorter conversation: nothing held from before
    /// may still be shown, for the open run or any other.
    func testAReconnectReplacesEveryHeldTranscript() async {
        let messages = (1...16).map { #"{"seq": \#($0), "from": "coder", "kind": "note", "text": "m\#($0)"}"# }
        let page = #"{"messages": [\#(messages.joined(separator: ","))]}"#
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs":
                .json(#"[{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z", "messages": 16}, {"runId": "run-2", "createdAt": "2026-09-18T10:00:00Z", "messages": 16}]"#)
            case "/api/runs/run-1": .json(#"{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z"}"#)
            case "/api/runs/run-1/messages": .json(page)
            default: .json("[]")
            }
        }
        let store = RunStore(client: client)
        let stale = (1...18).map { message($0) }
        store.messages = ["run-1": stale, "run-2": stale]
        store.selectedRunId = "run-1"

        await store.resync()

        XCTAssertEqual(store.messages["run-1"]?.count, 16)
        XCTAssertNil(store.messages["run-2"], "a run that is not open kept the old daemon's transcript")
        XCTAssertEqual(store.facts("run-2").messageCount, 16)
    }

    func testAMessageThatDisagreesWithTheHeldOneRereadsTheTranscript() {
        let store = store()
        store.apply(.message(runId: "run-1", message: message(1)))
        store.apply(.message(runId: "run-1", message: message(2)))
        let other = Message(seq: 2, at: Date(timeIntervalSince1970: 2), from: .verifier, kind: .note, text: "not the held one")
        XCTAssertEqual(store.apply(.message(runId: "run-1", message: other)), .messages("run-1"))
    }

    /// The accept confirmation lives in the store, per run, so the card being rebuilt by
    /// the accept cannot strand it; accepting clears it.
    func testAcceptWithoutOpenedEvidenceAsksInTheStoreAndAcceptingClearsIt() async {
        let posts = Counter()
        let client = StubURLProtocol.client { request in
            if request.httpMethod == "POST" { posts.add(); return .json("{}") }
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs":
                return .json(#"[{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z", "verdict": {"seq": 5, "verdict": "fail", "evidence": ["step 2"], "status": "proposed"}}]"#)
            default:
                return .json(#"{"messages": []}"#)
            }
        }
        let store = RunStore(client: client)
        await store.resync()

        await store.requestAccept(runId: "run-1")
        XCTAssertTrue(store.verdictDraft("run-1").confirmingAccept)
        XCTAssertEqual(posts.value, 0, "accepted without asking")

        let accepted = await store.acceptVerdict(runId: "run-1")
        XCTAssertTrue(accepted)
        XCTAssertEqual(posts.value, 1)
        XCTAssertFalse(store.verdictDraft("run-1").confirmingAccept)
        XCTAssertNil(store.verdictDrafts["run-1"])
    }

    func testAcceptAfterOpeningEvidenceSendsAtOnce() async {
        let posts = Counter()
        let client = StubURLProtocol.client { request in
            if request.httpMethod == "POST" { posts.add(); return .json("{}") }
            return .json(#"{"messages": []}"#)
        }
        let store = RunStore(client: client)
        store.runs = [RunSummary(runId: "run-1", createdAt: Date(timeIntervalSince1970: 0), status: .ready,
                                 verdict: VerdictState(seq: 5, verdict: "pass", evidence: ["step 2"], status: .proposed))]
        store.updateVerdictDraft("run-1") { $0.openedEvidence = true }
        await store.requestAccept(runId: "run-1")
        XCTAssertEqual(posts.value, 1)
        XCTAssertFalse(store.verdictDraft("run-1").confirmingAccept)
    }

    func testADraftBelongsToOneVerdict() {
        let store = store()
        store.runs[0].verdict = VerdictState(seq: 5, verdict: "fail", status: .proposed)
        store.updateVerdictDraft("run-1") {
            $0.action = .reject
            $0.reason = "step 3 shows $48.00"
            $0.openedEvidence = true
        }
        XCTAssertEqual(store.verdictDraft("run-1").reason, "step 3 shows $48.00")

        store.runs[0].verdict = VerdictState(seq: 9, verdict: "pass", status: .proposed)
        XCTAssertEqual(store.verdictDraft("run-1"), VerdictDraft(verdictSeq: 9))
    }

    /// The coding agent accepting mid-draft must take the Reject form with it.
    func testADraftEndsWhenItsVerdictCloses() async {
        let posts = Counter()
        let client = StubURLProtocol.client { request in
            if request.httpMethod == "POST" { posts.add() }
            return .json("{}")
        }
        let store = RunStore(client: client)
        store.runs = [RunSummary(runId: "run-1", createdAt: Date(timeIntervalSince1970: 0), status: .ready,
                                 verdict: VerdictState(seq: 5, verdict: "fail", evidence: ["step 2"], status: .proposed))]
        store.updateVerdictDraft("run-1") {
            $0.action = .reject
            $0.reason = "step 3 shows $48.00"
            $0.confirmingAccept = true
        }

        store.runs[0].verdict = VerdictState(seq: 5, verdict: "fail", evidence: ["step 2"], status: .accepted, acceptedBy: .coder)
        XCTAssertEqual(store.verdictDraft("run-1"), VerdictDraft(verdictSeq: 5, verdictClosed: true))
        let sent = await store.sendVerdictAction(runId: "run-1")
        XCTAssertFalse(sent)
        XCTAssertEqual(posts.value, 0, "a dispute was posted to a closed verdict")

        // A re-check begun on the closed verdict stays while it stays closed.
        store.updateVerdictDraft("run-1") { $0.action = .recheck }
        XCTAssertEqual(store.verdictDraft("run-1").action, .recheck)
    }

    func testDraftsOutliveAResyncUntilTheirRunIsGone() async {
        let gone = Counter()
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs":
                let second = #", {"runId": "run-2", "createdAt": "2026-09-18T10:00:00Z", "verdict": {"seq": 5, "verdict": "fail", "status": "proposed"}}"#
                return .json(#"[{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z"}\#(gone.value == 0 ? second : "")]"#)
            case "/api/runs/run-1": return .json(#"{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z"}"#)
            case "/api/runs/run-1/messages": return .json(#"{"messages": []}"#)
            default: return .json("[]")
            }
        }
        let store = RunStore(client: client)
        await store.resync()
        store.updateVerdictDraft("run-2") {
            $0.action = .reject
            $0.reason = "half typed"
        }
        store.selectedRunId = "run-1"

        await store.resync()
        XCTAssertEqual(store.verdictDraft("run-2").reason, "half typed", "a refresh lost an unsent reason")

        gone.add()
        await store.resync()
        XCTAssertNil(store.verdictDrafts["run-2"])
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

    func testOneFailedPieceDoesNotLoseTheOthers() async {
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs/run-1": .json(#"{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z"}"#)
            case "/api/runs/run-1/messages": .json(#"{"messages": [{"seq": 1, "from": "coder", "kind": "task", "text": "go"}]}"#)
            case "/api/runs/run-1/steps": .json("[]")
            default: .json(#"{"error": "no frames"}"#, status: 404)
            }
        }
        let store = RunStore(client: client)
        await store.select("run-1")

        XCTAssertEqual(store.details["run-1"]?.runId, "run-1")
        XCTAssertEqual(store.messages["run-1"]?.map(\.text), ["go"])
        XCTAssertEqual(store.steps["run-1"], [])
        XCTAssertNil(store.frames["run-1"])
        XCTAssertEqual(store.lastError, "The daemon answered 404: no frames")
    }

    func testConnectionFollowsEveryReadOfTheRunList() async {
        let answering = StubURLProtocol.client { _ in .json(#"[{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z", "task": "Check the tip"}]"#) }
        let store = RunStore(client: answering)
        XCTAssertEqual(store.connection, .connecting)
        await store.resync()
        XCTAssertEqual(store.connection, .online)
        XCTAssertEqual(store.runs.first?.task, "Check the tip")

        let silent = RunStore(client: StubURLProtocol.client { _ in .unreachable })
        await silent.resync()
        XCTAssertEqual(silent.connection, .offline(hasData: false))
        silent.runs = [RunSummary(runId: "run-1", createdAt: Date())]
        XCTAssertEqual(silent.connection, .offline(hasData: true))
    }

    /// #54: an HTTP error status is the daemon answering, in its own words, not a daemon
    /// that is absent.
    func testAnHTTPErrorIsTheDaemonAnsweringInItsOwnWords() async {
        let refusing = RunStore(client: StubURLProtocol.client { _ in
            .json("forbidden: Host must be a loopback address", status: 403)
        })
        await refusing.resync()
        XCTAssertEqual(refusing.connection,
                       .refused("The daemon answered 403: forbidden: Host must be a loopback address", hasData: false))
        refusing.runs = [RunSummary(runId: "run-1", createdAt: Date())]
        XCTAssertEqual(refusing.connection,
                       .refused("The daemon answered 403: forbidden: Host must be a loopback address", hasData: true))

        let absent = RunStore(client: StubURLProtocol.client { _ in .unreachable })
        await absent.resync()
        XCTAssertEqual(absent.connection, .offline(hasData: false))
    }

    /// #53: one failed read of the list, then an event stream that stays open, must not
    /// leave the window saying the daemon is not answering.
    func testAFailedListReadIsRetriedWhileTheStreamStaysOpen() async throws {
        let listReads = Counter()
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/events":
                return .openStream
            case "/api/runs":
                listReads.add()
                if listReads.value == 1 {
                    return .json(#"{"error": "open /tmp/gr/runs: permission denied"}"#, status: 500)
                }
                return .json(#"[{"runId": "run-1", "createdAt": "2026-09-18T10:00:00Z"}]"#)
            default:
                return .json("{}")
            }
        }
        let store = RunStore(client: client)
        store.start()
        defer { store.stop() }
        try await eventually(within: .seconds(8)) { store.connection == .online }
        XCTAssertEqual(store.runs.map(\.runId), ["run-1"])
        XCTAssertNil(store.lastError)
    }

    /// #68: the daemon came back without the open run (the list lacks it and the run
    /// answers 404). Its old record must not stay on screen with live actions.
    func testTheOpenRunIsLetGoWhenTheDaemonComesBackWithoutIt() async {
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs": .json(#"[{"runId": "run-x", "createdAt": "2026-09-18T10:00:00Z"}]"#)
            default: .json(#"{"error": "no run run-y"}"#, status: 404)
            }
        }
        let store = RunStore(client: client)
        store.runs = [RunSummary(runId: "run-y", createdAt: Date(timeIntervalSince1970: 0), status: .ready, task: "Check the tip")]
        store.selectedRunId = "run-y"
        store.details["run-y"] = RunDetail(runId: "run-y", verdict: VerdictState(seq: 3, verdict: "pass", status: .proposed))
        store.messages["run-y"] = [message(1, kind: .task)]
        store.steps["run-y"] = []
        store.frames["run-y"] = []

        await store.resync()

        XCTAssertEqual(store.runs.map(\.runId), ["run-x"])
        XCTAssertNil(store.selectedRunId)
        XCTAssertNil(store.details["run-y"])
        XCTAssertNil(store.messages["run-y"])
        XCTAssertNil(store.steps["run-y"])
        XCTAssertNil(store.frames["run-y"])
        XCTAssertNil(store.verdict("run-y"))
        XCTAssertEqual(store.goneRun, "Check the tip")
        // The notice says what happened; the 404s behind it are not an error.
        XCTAssertNil(store.lastError)
        XCTAssertEqual(store.connection, .online)

        // Opening another run clears the notice.
        store.selectedRunId = "run-x"
        XCTAssertNil(store.goneRun)
    }

    /// A list that lacks the open run while the run itself still answers (a list read
    /// before the run existed) keeps it open.
    func testAnOpenRunTheDaemonStillHasStaysOpen() async {
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs": .json("[]")
            case "/api/runs/run-y": .json(#"{"runId": "run-y", "createdAt": "2026-09-18T10:00:00Z"}"#)
            case "/api/runs/run-y/messages": .json(#"{"messages": []}"#)
            default: .json("[]")
            }
        }
        let store = RunStore(client: client)
        store.selectedRunId = "run-y"
        await store.resync()
        XCTAssertEqual(store.selectedRunId, "run-y")
        XCTAssertEqual(store.details["run-y"]?.runId, "run-y")
        XCTAssertNil(store.goneRun)
    }

    /// A run switch must never carry a lease: each run has its own pilot, lent by the
    /// seam a test or the daemon provides.
    func testEachRunHasItsOwnPilot() {
        let store = RunStore()
        XCTAssertTrue(store.pilot(for: "run-1") === store.pilot(for: "run-1"))
        XCTAssertFalse(store.pilot(for: "run-1") === store.pilot(for: "run-2"))
    }

    func testAFullSelectClearsAnEarlierError() async {
        let client = StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) {
            case "/api/runs/run-1", "/api/runs/run-1/messages": .json("{}")
            default: .json("[]")
            }
        }
        let store = RunStore(client: client)
        store.lastError = "stale"
        await store.select("run-1")
        XCTAssertNil(store.lastError)
        XCTAssertEqual(store.frames["run-1"], [])
    }

    func testBackoffDoublesToTheCeilingAndResetsOnAConnection() {
        var backoff = Backoff()
        XCTAssertEqual((0..<6).map { _ in backoff.next() }, [1, 2, 4, 8, 10, 10])
        backoff.reset()
        XCTAssertEqual(backoff.next(), 1)
        XCTAssertEqual(backoff.next(), 2)
    }

    func testTheFrameCacheEvictsTheLeastRecentlyUsed() {
        let cache = FrameCache(capacity: 2)
        let image = NSImage(size: NSSize(width: 1, height: 1))
        cache.store(image, runId: "r", file: "a")
        cache.store(image, runId: "r", file: "b")
        // Reading `a` makes `b` the oldest.
        XCTAssertNotNil(cache.image(runId: "r", file: "a"))
        cache.store(image, runId: "r", file: "c")

        XCTAssertNotNil(cache.image(runId: "r", file: "a"))
        XCTAssertNil(cache.image(runId: "r", file: "b"))
        XCTAssertNotNil(cache.image(runId: "r", file: "c"))
        // Same file, other run: a different frame.
        XCTAssertNil(cache.image(runId: "other", file: "a"))
    }

    func testSearchMatchesWhatTheRowShows() {
        let created = Date(timeIntervalSince1970: 1_700_000_000)
        let run = RunSummary(runId: "20260921-050808-8ecfd5", createdAt: created, status: .finished)
        XCTAssertTrue(SidebarView.run(run, matches: ""))
        XCTAssertTrue(SidebarView.run(run, matches: "8ECFD5"))
        XCTAssertTrue(SidebarView.run(run, matches: "finish"))
        XCTAssertTrue(SidebarView.run(run, matches: Chrome.timeOfDay(created)))
        XCTAssertFalse(SidebarView.run(run, matches: "booting"))
    }
}

private final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0
    var value: Int { lock.withLock { count } }
    func add() { lock.withLock { count += 1 } }
}
