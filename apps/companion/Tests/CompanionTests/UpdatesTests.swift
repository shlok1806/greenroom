import Synchronization
import XCTest

@testable import Companion

/// The Greenroom section's controller and the one process the app runs (root ADR 0033): a
/// fake `update.sh` for the runner, a scripted one for `Updates`.
@MainActor
final class UpdatesTests: XCTestCase {
    // MARK: - The process runner, on a real script

    /// A checkout holding `scripts/update.sh` with `body` as its text.
    private func checkout(_ body: String) throws -> String {
        let root = FileManager.default.temporaryDirectory.appending(path: "updates-\(UUID().uuidString)")
        let scripts = root.appending(path: "scripts")
        try FileManager.default.createDirectory(at: scripts, withIntermediateDirectories: true)
        try ("#!/bin/bash\n" + body).write(to: scripts.appending(path: "update.sh"), atomically: true, encoding: .utf8)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return root.path
    }

    func testCheckReturnsTheScriptsOutput() async throws {
        let path = try checkout(#"""
        [ "$1" = --check ] || exit 9
        [ "$(pwd -P)" = "$(cd "$(dirname "$0")/.." && pwd -P)" ] || exit 8
        [ "$GIT_TERMINAL_PROMPT" = 0 ] || exit 7
        echo "main 9f8e7d6"; echo "ahead 1"; echo "commit 9f8e7d6 a subject" >&2
        """#)
        let output = try await ScriptRunner().check(checkout: path)
        XCTAssertEqual(UpdateCheck.parse(output), UpdateCheck(main: "9f8e7d6", ahead: 1, commits: [.init(sha: "9f8e7d6", subject: "a subject")], refusal: nil))
    }

    func testACheckThatFailsSaysItsLastLine() async throws {
        let path = try checkout("echo fetching; echo 'failed: fetch origin'; exit 1\n")
        do {
            _ = try await ScriptRunner().check(checkout: path)
            XCTFail("expected a failure")
        } catch let error as UpdateScriptError {
            XCTAssertEqual(error, .failed(1, "fetching\nfailed: fetch origin\n"))
            XCTAssertEqual(error.errorDescription, "failed: fetch origin")
        }
    }

    func testACheckoutWithoutTheScriptSaysSo() async throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "no-script-\(UUID().uuidString)").path
        do {
            _ = try await ScriptRunner().check(checkout: root)
            XCTFail("expected a failure")
        } catch let error as UpdateScriptError {
            XCTAssertEqual(error, .missing(root + "/scripts/update.sh"))
        }
    }

    func testAHungCheckIsStopped() async throws {
        let path = try checkout("sleep 30\n")
        let runner = ScriptRunner(checkTimeout: .milliseconds(300))
        let started = Date()
        do {
            _ = try await runner.check(checkout: path)
            XCTFail("expected a timeout")
        } catch let error as UpdateScriptError {
            XCTAssertEqual(error, .timedOut)
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 10)
    }

    /// Lines arrive as they are written, through the log file, then the exit status.
    func testAnUpdateStreamsItsLinesThroughTheLog() async throws {
        let path = try checkout("""
        [ -z "$1" ] || exit 9
        echo "step: fetch origin"
        sleep 0.4
        echo "step: install the daemon"
        echo "building" >&2
        printf 'no newline at the end'
        exit 3
        """)
        let log = FileManager.default.temporaryDirectory.appending(path: "update-\(UUID().uuidString)/update.log")
        addTeardownBlock { try? FileManager.default.removeItem(at: log.deletingLastPathComponent()) }
        var events: [UpdateEvent] = []
        var arrivals: [Date] = []
        for await event in ScriptRunner(pollInterval: .milliseconds(50)).update(checkout: path, log: log) {
            events.append(event)
            arrivals.append(Date())
        }
        XCTAssertEqual(events, [
            .line("step: fetch origin"), .line("step: install the daemon"), .line("building"),
            .line("no newline at the end"), .exit(3),
        ])
        // The first line came before the script finished, not all at the end.
        XCTAssertGreaterThan(arrivals[1].timeIntervalSince(arrivals[0]), 0.2)
        let saved = try String(contentsOf: log, encoding: .utf8)
        XCTAssertEqual(saved, "step: fetch origin\nstep: install the daemon\nbuilding\nno newline at the end")
    }

    func testAnUpdateThatCannotStartEndsAtOnce() async {
        var events: [UpdateEvent] = []
        let log = FileManager.default.temporaryDirectory.appending(path: "u-\(UUID().uuidString).log")
        for await event in ScriptRunner().update(checkout: "/nonexistent-\(UUID().uuidString)", log: log) {
            events.append(event)
        }
        XCTAssertEqual(events.last, .exit(127))
        guard case .line(let line) = events.first else { return XCTFail("\(events)") }
        XCTAssertTrue(line.hasPrefix("failed: start the update (there is no "), line)
    }

    // MARK: - Updates, on a scripted update.sh

    private let local = URL(string: "http://127.0.0.1:7777")!
    private let versionJSON = #"{"commit":"1a2b3c4","dirty":false,"builtAt":"2026-09-27T10:00:00Z","verifier":"nim","verifierModel":"m","visionModel":"v","checkout":"/src/greenroom","inputHelper":7,"imageRecipe":2}"#

    private func updates(version: StubURLProtocol.Reply? = nil, url: URL? = nil, script: FakeScript,
                         app: AppBuild = AppBuild(stamp: BuildStamp(commit: "1a2b3c4"), checkout: "/app/checkout")) -> Updates {
        let reply = version ?? .json(versionJSON)
        let client = StubURLProtocol.client(baseURL: url ?? local) { _ in reply }
        return Updates(client: client, app: app, script: script, log: URL(fileURLWithPath: "/dev/null"))
    }

    func testRefreshReadsTheDaemonThenChecksInItsCheckout() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 2\ncommit 9f8e7d6 b\ncommit 5e6f7a8 a\n"))
        let updates = updates(script: script)
        await updates.refresh()
        XCTAssertEqual(updates.daemon?.commit, "1a2b3c4")
        XCTAssertEqual(script.checkedIn, ["/src/greenroom"])
        guard case .checked(let check, _) = updates.check else { return XCTFail("\(updates.check)") }
        XCTAssertEqual(check.ahead, 2)
        XCTAssertEqual(updates.summary.headline, .updates(2))
        XCTAssertTrue(updates.summary.canUpdate)
    }

    /// A daemon from before `/api/version` says so; the app's own checkout still checks.
    func testAnOlderDaemonFallsBackToTheAppsCheckout() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 0\n"))
        let updates = updates(version: .json(#"{"error":"not found"}"#, status: 404), script: script)
        await updates.refresh()
        XCTAssertNil(updates.daemon)
        XCTAssertEqual(updates.daemonError, "This greenroom is older than build reporting. Update it to see its build.")
        XCTAssertEqual(script.checkedIn, ["/app/checkout"])
    }

    /// A daemon on another Mac: its checkout is not here, and updating here would rebuild a
    /// daemon nobody watches.
    func testARemoteDaemonIsNeverUpdatedFromHere() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 2\n"))
        let updates = updates(url: URL(string: "https://greenroom.example.com")!, script: script)
        await updates.refresh()
        XCTAssertEqual(updates.daemon?.commit, "1a2b3c4")
        XCTAssertNil(updates.checkout)
        XCTAssertTrue(script.checkedIn.isEmpty)
        XCTAssertFalse(updates.summary.canUpdate)
        await updates.update()
        XCTAssertNil(updates.run)
    }

    func testAFailedCheckSaysWhy() async {
        let script = FakeScript(check: .failure(UpdateScriptError.failed(1, "failed: fetch origin\n")))
        let updates = updates(script: script)
        await updates.refresh()
        XCTAssertEqual(updates.check, .failed("failed: fetch origin"))
    }

    /// ADR 0033: a verifier mid-turn is named and the person is asked; nothing runs until
    /// they choose to update anyway.
    func testAVerifierTurnInProgressAsksFirst() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 1\n"), update: ["step: fetch origin", "done: main is at 9f8e7d6"])
        let updates = updates(script: script)
        await updates.refresh()
        await updates.requestUpdate { ["TipSplit check"] }
        XCTAssertEqual(updates.confirming, ["TipSplit check"])
        XCTAssertNil(updates.run)
        XCTAssertTrue(script.updatedIn.isEmpty)
        updates.cancelUpdate()
        XCTAssertNil(updates.confirming)
        await updates.requestUpdate { ["TipSplit check"] }
        await updates.updateAnyway()
        XCTAssertNil(updates.confirming)
        XCTAssertEqual(script.updatedIn, ["/src/greenroom"])
        XCTAssertEqual(updates.run?.outcome, .done("main is at 9f8e7d6"))
    }

    func testWithNoVerifierWorkingAnUpdateRunsAndShowsTheFailingStep() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 1\n"), update: [
            "step: fetch origin", "step: install the daemon", "main.go:3: syntax error", "failed: install the daemon",
        ], status: 1)
        let updates = updates(script: script)
        await updates.refresh()
        await updates.requestUpdate { [] }
        XCTAssertEqual(updates.run?.outcome, .failed(step: "install the daemon", status: 1))
        XCTAssertEqual(updates.run?.failingOutput, ["main.go:3: syntax error"])
        // Read again after the run: the daemon and main.
        XCTAssertEqual(script.checkedIn.count, 2)
        updates.dismissRun()
        XCTAssertNil(updates.run)
    }

    func testNothingStartsWhenUpdateWouldRefuse() async {
        let script = FakeScript(check: .success("main 9f8e7d6\nahead 1\nrefused: the checkout at /src is not on main\n"))
        let updates = updates(script: script)
        await updates.refresh()
        await updates.requestUpdate { [] }
        XCTAssertNil(updates.run)
        XCTAssertTrue(script.updatedIn.isEmpty)
    }

    // MARK: - Which runs have a verifier turn in progress

    func testTheBusyRunsAreTheLiveOnesAwaitingTheVerifier() async {
        let at = Date()
        let client = StubURLProtocol.client { request in
            // The run the window does not hold is read for this.
            guard request.url?.path == "/api/runs/unheld/messages" else { return .json("{}", status: 404) }
            return .json(#"{"messages":[{"seq":1,"at":"2026-09-27T10:00:00Z","from":"coder","kind":"task","text":"Check the About window"}]}"#)
        }
        let store = RunStore(client: client)
        store.runs = [
            RunSummary(runId: "working", createdAt: at, status: .ready, task: "Check the split"),
            RunSummary(runId: "answered", createdAt: at, status: .ready, task: "Check the tip"),
            RunSummary(runId: "ended", createdAt: at, destroyedAt: at, status: .finished, task: "Check the total"),
            RunSummary(runId: "unheld", createdAt: at, status: .ready, task: "Check the About window"),
        ]
        store.messages["working"] = [Message(seq: 1, at: at, from: .coder, kind: .task, text: "Check the split")]
        store.messages["answered"] = [
            Message(seq: 1, at: at, from: .coder, kind: .task, text: "Check the tip"),
            Message(seq: 2, at: at, from: .verifier, kind: .reply, text: "Done."),
        ]
        store.messages["ended"] = [Message(seq: 1, at: at, from: .coder, kind: .task, text: "Check the total")]
        let busy = await store.runsWithVerifierTurn()
        XCTAssertEqual(busy, ["Check the split", "Check the About window"])
    }
}

/// `update.sh` as a test scripts it: what `--check` answers and what an update prints.
final class FakeScript: UpdateScript, @unchecked Sendable {
    private let checkResult: Result<String, Error>
    private let updateLines: [String]
    private let status: Int32
    private let calls = Mutex<(checks: [String], updates: [String])>(([], []))

    init(check: Result<String, Error>, update: [String] = [], status: Int32 = 0) {
        checkResult = check
        updateLines = update
        self.status = status
    }

    var checkedIn: [String] { calls.withLock { $0.checks } }
    var updatedIn: [String] { calls.withLock { $0.updates } }

    func check(checkout: String) async throws -> String {
        calls.withLock { $0.checks.append(checkout) }
        return try checkResult.get()
    }

    func update(checkout: String, log: URL) -> AsyncStream<UpdateEvent> {
        calls.withLock { $0.updates.append(checkout) }
        let lines = updateLines, status = status
        return AsyncStream { continuation in
            for line in lines { continuation.yield(.line(line)) }
            continuation.yield(.exit(status))
            continuation.finish()
        }
    }
}
