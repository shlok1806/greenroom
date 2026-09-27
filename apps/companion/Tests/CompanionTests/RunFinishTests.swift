import XCTest

@testable import Companion

/// A finished run (root ADR 0034, companion ADR 0016): the wire decodes leniently, the store
/// picks the finish up from the event stream, and every indicator says Done with the outcome.
@MainActor
final class RunFinishTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_000_000)

    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try JSONDecoder.daemon().decode(type, from: Data(json.utf8))
    }

    private func finish(_ outcome: FinishOutcome, pr: String? = nil) -> RunFinish {
        RunFinish(outcome: outcome, summary: "Tip split rounds to the cent.", ref: RunRef(branch: "fix/tip", commit: nil, pr: pr),
                  at: now.addingTimeInterval(-30))
    }

    // MARK: - Decoding

    func testTheRunListDecodesAFinishAndOldRowsWithout() throws {
        let runs = try decode([RunSummary].self, """
        [
          {"runId": "a", "createdAt": "2026-09-27T09:00:00Z", "status": "finished", "steps": 3, "lastActivity": "2026-09-27T09:10:00Z",
           "messages": 9, "verdict": null,
           "finish": {"outcome": "verified", "summary": "Rounds to the cent.",
                      "ref": {"branch": "fix/tip", "commit": "0123456789abcdef", "pr": "https://github.com/o/r/pull/12"},
                      "at": "2026-09-27T10:00:00.25Z"}},
          {"runId": "b", "createdAt": "2026-09-27T09:00:00Z", "status": "finished", "steps": 1, "lastActivity": "2026-09-27T09:10:00Z",
           "messages": 2, "verdict": null},
          {"runId": "c", "createdAt": "2026-09-27T09:00:00Z", "status": "finished", "steps": 1, "lastActivity": "2026-09-27T09:10:00Z",
           "messages": 2, "verdict": null, "finish": null}
        ]
        """)
        let first = try XCTUnwrap(runs[0].finish)
        XCTAssertEqual(first.outcome, .verified)
        XCTAssertEqual(first.summary, "Rounds to the cent.")
        XCTAssertEqual(first.ref.branch, "fix/tip")
        XCTAssertEqual(first.ref.commit, "0123456789abcdef")
        XCTAssertEqual(first.ref.prURL?.absoluteString, "https://github.com/o/r/pull/12")
        XCTAssertNotNil(first.at)
        XCTAssertNil(runs[1].finish)
        XCTAssertNil(runs[2].finish)
    }

    /// An outcome this app does not know, a missing summary, a malformed ref or time: the
    /// finish still decodes, and so does the run around it.
    func testAFinishDecodesLeniently() throws {
        let detail = try decode(RunDetail.self, """
        {"runId": "r", "createdAt": "2026-09-27T09:00:00Z", "machine": null, "verdict": null,
         "finish": {"outcome": "shipped", "ref": ["not", "an", "object"], "at": "yesterday"}}
        """)
        let finish = try XCTUnwrap(detail.finish)
        XCTAssertEqual(finish.outcome, .unknown("shipped"))
        XCTAssertEqual(finish.summary, "")
        XCTAssertTrue(finish.ref.isEmpty)
        XCTAssertNil(finish.at)

        let bare = try decode(RunFinish.self, "{}")
        XCTAssertEqual(bare.outcome, .unknown(""))
        XCTAssertEqual(RunFacts.outcomeWord(bare.outcome), "Finished")
        XCTAssertEqual(RunFacts.outcomeWord(finish.outcome), "Shipped")
    }

    func testTheFinishEventMessageCarriesItsFinish() throws {
        let message = try decode(Message.self, """
        {"seq": 40, "at": "2026-09-27T10:00:00Z", "from": "system", "kind": "event",
         "text": "run finished: unverified. Rounds to the cent.",
         "finish": {"outcome": "unverified", "summary": "Rounds to the cent.", "ref": {"pr": "#12", "branch": "  "}}}
        """)
        XCTAssertEqual(message.finish?.outcome, .unverified)
        XCTAssertEqual(message.finish?.ref.pr, "#12")
        XCTAssertNil(message.finish?.ref.branch, "blank fields read as absent")
        XCTAssertNil(message.finish?.ref.prURL, "only an http(s) PR is a link")
    }

    func testOnlyAnHTTPPRIsALink() {
        XCTAssertNotNil(RunRef(pr: "https://github.com/o/r/pull/12").prURL)
        XCTAssertNotNil(RunRef(pr: "http://git.example.com/o/r/merge_requests/3").prURL)
        XCTAssertNil(RunRef(pr: "#12").prURL)
        XCTAssertNil(RunRef(pr: "javascript:alert(1)").prURL)
        XCTAssertNil(RunRef(pr: "file:///etc/passwd").prURL)
        XCTAssertNil(RunRef(pr: "https://").prURL)
    }

    // MARK: - Store

    func testAFinishOverTheEventStreamMarksAnOpenRunDoneAndAsksForTheRun() {
        let store = RunStore()
        store.runs = [RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .ready)]
        store.messages["r"] = []
        store.details["r"] = RunDetail(runId: "r", createdAt: now.addingTimeInterval(-600))
        let event = Message(seq: 1, at: now, from: .system, kind: .event, text: "run finished: verified. x", finish: finish(.verified))

        XCTAssertEqual(store.apply(.message(runId: "r", message: event)), .run("r"))
        XCTAssertEqual(store.run("r")?.finish?.outcome, .verified)
        XCTAssertEqual(store.details["r"]?.finish?.outcome, .verified)
        XCTAssertEqual(store.facts("r", now: now).finish?.outcome, .verified)
        XCTAssertEqual(store.facts("r", now: now).rowStatus(now: now).text, "Done, verified")
    }

    func testAFinishForARunThatIsNotOpenStillChangesItsRow() {
        let store = RunStore()
        store.runs = [RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .finished)]
        let event = Message(seq: 5, at: now, from: .system, kind: .event, text: "run finished: abandoned. x", finish: finish(.abandoned))

        XCTAssertEqual(store.apply(.message(runId: "r", message: event)), .run("r"))
        XCTAssertNil(store.messages["r"])
        XCTAssertEqual(store.facts("r", now: now).rowStatus(now: now).text, "Done, abandoned")
    }

    /// Only the daemon records a finish: the same field on another sender's message is ignored.
    func testOnlyASystemMessageFinishesARun() {
        let store = RunStore()
        store.runs = [RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), status: .ready)]
        store.messages["r"] = []
        let note = Message(seq: 1, at: now, from: .coder, kind: .note, text: "done", finish: finish(.verified))
        _ = store.apply(.message(runId: "r", message: note))
        XCTAssertNil(store.run("r")?.finish)
        XCTAssertNil(store.facts("r", now: now).finish)
    }

    // MARK: - Facts and words

    private func facts(finish: RunFinish?, verdict: VerdictState? = nil, messages: [Message] = [],
                       machine: Bool = false) -> RunFacts {
        let summary = RunSummary(runId: "r", createdAt: now.addingTimeInterval(-600), destroyedAt: machine ? nil : now,
                                 status: machine ? .ready : .finished, verdict: verdict, finish: finish)
        var detail = RunDetail(runId: "r", createdAt: now.addingTimeInterval(-600), destroyedAt: machine ? nil : now)
        if machine {
            detail.machine = Machine(runId: "r", name: "m", image: "i", ip: nil, status: .ready, error: nil, bootSeconds: nil,
                                     createdAt: now.addingTimeInterval(-600), dir: "", control: nil)
        }
        return RunFacts.derive(summary: summary, detail: detail, messages: messages, steps: [], verdict: verdict, now: now)
    }

    func testEachOutcomeReadsDoneWithItsWordAndOnlyVerifiedTakesThePassColour() {
        let pass = VerdictState(seq: 3, verdict: "pass", status: .accepted, acceptedBy: .coder)
        let verified = facts(finish: finish(.verified), verdict: pass).rowStatus(now: now)
        XCTAssertEqual(verified.text, "Done, verified")
        XCTAssertEqual(verified.tone, .pass, "a verified finish is an accepted pass, whoever accepted it")

        let unverified = facts(finish: finish(.unverified)).rowStatus(now: now)
        XCTAssertEqual(unverified.text, "Done, unverified")
        XCTAssertEqual(unverified.tone, .done)

        let abandoned = facts(finish: finish(.abandoned)).rowStatus(now: now)
        XCTAssertEqual(abandoned.text, "Done, abandoned")
        XCTAssertEqual(abandoned.tone, .done)

        let unknown = facts(finish: finish(.unknown("shipped"))).rowStatus(now: now)
        XCTAssertEqual(unknown.text, "Done, shipped")
        XCTAssertEqual(unknown.tone, .done)
    }

    func testDoneIsNeutralInEveryTheme() {
        for id in ThemeID.allCases {
            let theme = DesignData.shared.theme(id)
            XCTAssertEqual(theme.tone(.done), theme.foreground, id.rawValue)
            XCTAssertEqual(theme.tone(.pass), theme.color(.pass, on: .background), id.rawValue)
            XCTAssertNotEqual(theme.tone(.done), theme.tone(.pass), id.rawValue)
        }
    }

    /// A kept machine does not hide that the work is over.
    func testAFinishedRunWhoseMachineIsKeptIsStillDone() {
        let f = facts(finish: finish(.unverified), machine: true)
        XCTAssertTrue(f.isAlive)
        XCTAssertTrue(f.machineReady)
        XCTAssertEqual(f.phase, .idle, "no activity for 10 minutes, yet the row does not ask for attention")
        XCTAssertEqual(f.rowStatus(now: now).text, "Done, unverified")
        XCTAssertEqual(f.rowStatus(now: now).tone, .done)
    }

    /// Something waiting on the person still leads the row (companion ADR 0012, 0016).
    func testAVerdictWaitingOnYouOutranksDone() {
        let proposed = VerdictState(seq: 3, verdict: "fail", status: .proposed)
        let f = facts(finish: finish(.unverified), verdict: proposed)
        XCTAssertEqual(f.rowStatus(now: now).text, "Fail, needs review")
        XCTAssertTrue(f.needsYou)
    }

    func testAnUnfinishedRunKeepsTodaysStates() {
        let ended = facts(finish: nil, messages: [Message(seq: 1, at: now, from: .system, kind: .event, text: "machine destroyed")])
        XCTAssertNil(ended.finish)
        XCTAssertEqual(ended.rowStatus(now: now).text, "No verdict")
    }

    /// The transcript's finish is the newest word; the manifest's copies follow it.
    func testTheTranscriptsFinishWinsOverTheListsCopy() {
        let event = Message(seq: 9, at: now, from: .system, kind: .event, text: "run finished", finish: finish(.verified))
        let summary = RunSummary(runId: "r", createdAt: now, finish: finish(.unverified))
        XCTAssertEqual(RunFacts.finish(summary: summary, detail: nil, messages: [event])?.outcome, .verified)
        XCTAssertEqual(RunFacts.finish(summary: summary, detail: nil, messages: [])?.outcome, .unverified)
        var detail = RunDetail(runId: "r")
        detail.finish = finish(.abandoned)
        XCTAssertEqual(RunFacts.finish(summary: summary, detail: detail, messages: nil)?.outcome, .abandoned)
    }

    // MARK: - The ref line

    func testTheRefReadsBranchCommitAndPRInOrder() {
        let ref = RunRef(branch: "fix/tip", commit: "0123456789abcdef0123", pr: "https://github.com/o/r/pull/12")
        let parts = FinishNote.refParts(ref)
        XCTAssertEqual(parts.map(\.label), ["branch", "commit", "PR"])
        XCTAssertEqual(parts.map(\.value), ["fix/tip", "0123456789abcdef0123", "#12"])
        XCTAssertEqual(parts[2].url?.absoluteString, "https://github.com/o/r/pull/12")
        XCTAssertEqual(FinishNote.refParts(ref, short: true)[1].value, "0123456")

        let words = FinishNote.refParts(RunRef(commit: "HEAD of main", pr: "#12"), short: true)
        XCTAssertEqual(words.map(\.value), ["HEAD of main", "#12"])
        XCTAssertNil(words[1].url)

        let other = FinishNote.refParts(RunRef(pr: "https://git.example.com/o/r/merge_requests/3"))
        XCTAssertEqual(other.first?.value, "git.example.com/o/r/merge_requests/3")
    }

    func testTheHeaderHelpSaysWhoFinishedAndWhatTheOutcomeMeans() {
        XCTAssertTrue(RunStatusLine.doneHelp(finish(.verified)).contains("pass on this run was accepted"))
        XCTAssertTrue(RunStatusLine.doneHelp(finish(.unverified)).hasPrefix("The coding agent finished the run at "))
        XCTAssertFalse(RunStatusLine.doneHelp(finish(.abandoned)).contains("\u{2014}"))
    }
}
