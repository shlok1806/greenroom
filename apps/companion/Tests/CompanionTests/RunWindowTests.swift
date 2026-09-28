import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The run window (companion ADR 0019): its pure rules, its keys, its actions, its word budget
/// per state (read back off the rendered window with Apple's text recogniser) and what
/// VoiceOver reads, in order.
@MainActor
final class RunWindowTests: XCTestCase {
    private typealias F = StateFixtures
    private var hosts: [ParkedHost] = []

    override func tearDown() async throws {
        hosts.forEach { $0.close() }
        hosts = []
    }

    // MARK: - A store with no daemon

    /// Posted requests, for the action tests.
    private final class Posts: @unchecked Sendable {
        private let lock = NSLock()
        private var items: [(path: String, body: String)] = []
        func add(_ path: String, _ body: String) { lock.withLock { items.append((path, body)) } }
        var all: [(path: String, body: String)] { lock.withLock { items } }
    }

    /// A store over the golden board with TipSplit in `state`: every picture a grey PNG, every
    /// list empty, every post recorded.
    private func store(_ state: F.State = .failed, posts: Posts = Posts()) throws -> RunStore {
        let png = Self.greyPNG
        let client = StubURLProtocol.client { request in
            let path = request.url?.path ?? ""
            if request.httpMethod == "POST" {
                posts.add(path, String(data: request.httpBody ?? Self.read(request.httpBodyStream), encoding: .utf8) ?? "")
                return .json("{}", status: 202)
            }
            if path.contains("/artifacts/") || path.contains("/frames/") { return StubURLProtocol.Reply(body: png) }
            if path.hasSuffix("/messages") { return .json(#"{"messages": []}"#) }
            if path.hasSuffix("/steps") || path.hasSuffix("/frames") || path.hasSuffix("/api/runs") { return .json("[]") }
            return .json("{}", status: 404)
        }
        let store = RunStore(client: client, screenSource: NoScreen())
        store.board = F.board(try SummaryTests.golden(), state: state)
        store.reachable = true
        store.selectedRunId = F.tipSplit
        var detail = RunDetail(runId: F.tipSplit)
        detail.verdict = VerdictState(seq: 16, verdict: "fail", status: .proposed)
        store.details[F.tipSplit] = detail
        return store
    }

    /// URLSession hands a stub the body as a stream.
    private nonisolated static func read(_ stream: InputStream?) -> Data {
        guard let stream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }

    /// A live screen that never sends a picture.
    private struct NoScreen: ScreenSource {
        func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> { AsyncThrowingStream { _ in } }
    }

    private static let greyPNG: Data = {
        let image = NSImage(size: NSSize(width: 64, height: 48))
        image.lockFocus()
        NSColor.gray.setFill()
        NSRect(x: 0, y: 0, width: 64, height: 48).fill()
        image.unlockFocus()
        let rep = NSBitmapImageRep(data: image.tiffRepresentation!)!
        return rep.representation(using: .png, properties: [:])!
    }()

    private func host(_ shell: ShellModel, state: F.State, size: CGSize = CGSize(width: 1280, height: 800)) async -> ParkedHost {
        let view = CompanionShell(shell: shell)
            .environment(\.frozenNow, F.start.addingTimeInterval(state.at))
            .environment(\.redactsGuestScreen, true)
        let host = ParkedHost(view, size: size)
        hosts.append(host)
        await host.settle(1.0)
        return host
    }

    // MARK: - The word budget, measured

    func testEveryStateStaysWithinItsWordBudgetAndAnswersTheFiveQuestions() async throws {
        var report: [String] = []
        for state in F.State.allCases {
            let shell = ShellModel(store: try store(state))
            let host = await host(shell, state: state)
            let image = try XCTUnwrap(host.image())
            let reading = try VisibleWords.read(image, excluding: [CGRect(x: 0, y: 0, width: 248 * 2, height: CGFloat(image.height))])
            report.append("\(state.rawValue): \(reading.words) of \(state.budget.budget)")
            XCTAssertLessThanOrEqual(reading.words, state.budget.budget, "\(state): \(reading.lines.joined(separator: " | "))")
            let text = reading.lines.joined(separator: " ")
            // Is it working, did it pass: the status word, top left.
            let status = try XCTUnwrap(shell.summary?.status)
            XCTAssertTrue(text.contains(status), "\(state): no status word \(status) in \(text)")
            // What do I do now: the one primary action, labeled.
            if let primary = shell.summary?.primaryAction {
                XCTAssertTrue(text.contains(primary.label), "\(state): no primary \(primary.label)")
            }
            if state == .failed {
                // Why: the first failure selected, the claim beside its proof, without a click.
                XCTAssertTrue(text.contains("2 of 4 checks"), text)
                XCTAssertTrue(text.contains("Expected $50.00, saw $10.00"), text)
                XCTAssertTrue(text.contains("saw $10.00"), text)
            }
            if state == .live {
                // What is it doing: the Now line.
                XCTAssertTrue(text.contains("clicking 25% in TipSplit"), text)
            }
            host.close()
        }
        print("word budget: " + report.joined(separator: ", "))
    }

    // MARK: - VoiceOver

    /// What VoiceOver reads for each piece. (SwiftUI builds its accessibility tree only for an
    /// assistive client, which a parked test window never has; the order the tree reads in is
    /// checked in the running app through Greenroom's UI reader, companion ADR 0019.)
    func testEveryPieceSaysItsStateInWords() throws {
        let board = F.board(try SummaryTests.golden(), state: .failed)
        let failed = try XCTUnwrap(board.summary(F.tipSplit))
        XCTAssertEqual(RunRowModel(failed, now: F.start).accessibilityLabel, "TipSplit: split the bill, Failed, 2 failed")
        let check = try XCTUnwrap(failed.checks.items.first)
        XCTAssertEqual(CheckRowView.label(check, checking: false, meta: "saw $10.00"),
                       "Each pays becomes $50.00 at 25%, failed, saw $10.00")
        XCTAssertEqual(CheckRowView.label(SummaryCheck(id: "p", text: "Tip is $24.00", state: .pending), checking: true, meta: "checking"),
                       "Tip is $24.00, checking, checking")
        XCTAssertEqual(Keys.hint(for: SummaryAction(id: SummaryAction.reject, label: "Reject")), "Reject (⌘⌫)")
    }

    // MARK: - Keys

    private func key(_ characters: String, code: UInt16, flags: NSEvent.ModifierFlags = []) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil,
                         characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
    }

    func testTheKeysMoveBetweenChecksAndRunsAndOpenThePanels() throws {
        let shell = ShellModel(store: try store(.failed))
        let keys = Keys(shell: shell)
        let first = shell.selectedCheckID
        XCTAssertEqual(shell.selectedCheck?.saw, "$10.00", "the first failure is selected by itself")
        XCTAssertTrue(keys.handle(key("j", code: 38), responder: nil))
        XCTAssertNotEqual(shell.selectedCheckID, first)
        XCTAssertTrue(keys.handle(key("k", code: 40), responder: nil))
        XCTAssertEqual(shell.selectedCheckID, first)

        XCTAssertTrue(keys.handle(key("a", code: 0), responder: nil))
        XCTAssertTrue(shell.activityOpen)
        XCTAssertTrue(keys.handle(key("m", code: 46), responder: nil))
        XCTAssertEqual(shell.composer, .message)
        XCTAssertTrue(keys.handle(key("\u{1b}", code: 53), responder: nil), "esc closes the composer")
        XCTAssertNil(shell.composer)
        XCTAssertTrue(keys.handle(key("e", code: 14), responder: nil))
        XCTAssertTrue(shell.evidenceOpen)
        XCTAssertTrue(keys.handle(key("\u{1b}", code: 53), responder: nil))
        XCTAssertFalse(shell.evidenceOpen)

        XCTAssertTrue(keys.handle(key("k", code: 40, flags: .command), responder: nil))
        XCTAssertTrue(shell.paletteOpen)
        XCTAssertFalse(keys.handle(key("j", code: 38), responder: nil), "the palette's field has the keys")
        XCTAssertTrue(keys.handle(key("\u{1b}", code: 53), responder: nil))
        XCTAssertFalse(shell.paletteOpen)

        let run = shell.runId
        XCTAssertTrue(keys.handle(key("\u{F701}", code: 125), responder: nil))
        XCTAssertNotEqual(shell.runId, run, "down moves to the next run")
    }

    func testWhileTypingOnlyEscAndCommandKAreTheWindows() throws {
        let shell = ShellModel(store: try store(.failed))
        let keys = Keys(shell: shell)
        let field = NSTextView()
        XCTAssertFalse(keys.handle(key("j", code: 38), responder: field))
        XCTAssertFalse(keys.handle(key("a", code: 0), responder: field))
        XCTAssertFalse(shell.activityOpen)
        XCTAssertTrue(keys.handle(key("k", code: 40, flags: .command), responder: field))
        XCTAssertTrue(shell.paletteOpen)
    }

    // MARK: - Actions

    func testAcceptPostsTheAcceptAndRestartPostsTheReboot() async throws {
        let posts = Posts()
        let shell = ShellModel(store: try store(.failed, posts: posts))
        shell.perform(try XCTUnwrap(shell.summary?.primaryAction))
        try await waitUntil { posts.all.contains { $0.path.hasSuffix("/messages") && $0.body.contains(#""kind":"accept""#) } }

        let stuck = ShellModel(store: try store(.notAnswering, posts: posts))
        stuck.perform(try XCTUnwrap(stuck.summary?.primaryAction))
        try await waitUntil { posts.all.contains { $0.path.hasSuffix("/reboot") } }
    }

    func testRejectAsksWhyAndSendsADisputeWithTheReason() async throws {
        let posts = Posts()
        let shell = ShellModel(store: try store(.failed, posts: posts))
        shell.perform(SummaryAction(id: SummaryAction.reject, label: "Reject"))
        XCTAssertEqual(shell.composer, .reject)
        XCTAssertTrue(shell.activityOpen)
        shell.composerDraft = "Each pays should include the tip."
        shell.sendComposer()
        try await waitUntil { posts.all.contains { $0.body.contains(#""kind":"dispute""#) && $0.body.contains("include the tip") } }
    }

    func testKeepWaitingDemotesTheRestartAndDrivingOffersTheWayBack() throws {
        let shell = ShellModel(store: try store(.notAnswering))
        let summary = try XCTUnwrap(shell.summary)
        shell.perform(try XCTUnwrap(summary.secondaryActions.first))
        let after = shell.actions(for: summary)
        XCTAssertNil(after.primary)
        XCTAssertEqual(after.secondary.map(\.id), [SummaryAction.restart])
    }

    private func waitUntil(_ condition: @escaping () -> Bool, timeout: Double = 5) async throws {
        let end = Date().addingTimeInterval(timeout)
        while !condition() {
            if Date() > end { return XCTFail("timed out") }
            try await Task.sleep(for: .milliseconds(20))
        }
    }

    // MARK: - Pure rules

    func testBootStepsFoldThePhasesIntoFourPlainSteps() {
        let at = Date()
        func phase(_ name: String, running: Bool = false) -> BootPhase {
            BootPhase(phase: BootPhaseName(text: name), at: at, seconds: running ? nil : 3)
        }
        let rows = BootRow.rows([phase("clone"), phase("start"), phase("agent"), phase("ip", running: true)])
        XCTAssertEqual(rows.map(\.glyph), [.passed, .passed, .checking, .pending])
        XCTAssertEqual(rows.map(\.text).first, "Copied a fresh Mac")
        XCTAssertEqual(rows[1].meta, "0:06")
        XCTAssertEqual(rows[2].meta, "now")
        XCTAssertEqual(BootRow.rows([]).map(\.glyph), [.pending, .pending, .pending, .pending])
        XCTAssertEqual(BootRow.rows([], ready: true).map(\.glyph), [.passed, .passed, .passed, .passed])
        for row in rows { XCTAssertFalse(row.text.contains("192."), "no addresses") }
    }

    func testTheFilmstripKeepsTheProofFramesAndEndsOnTheNewest() {
        let base = Date(timeIntervalSince1970: 1000)
        let frames = (1...40).map { Frame(at: base.addingTimeInterval(Double($0)), file: "f\($0).jpg", step: $0) }
        let checks = [
            SummaryCheck(id: "a", text: "a", state: .fail, step: 12),
            SummaryCheck(id: "b", text: "b", state: .pass, step: 30),
        ]
        let items = Filmstrip.items(frames, checks: checks, count: 8)
        XCTAssertEqual(items.count, 8)
        XCTAssertEqual(items.last?.file, "f40.jpg")
        XCTAssertEqual(items.first { $0.file == "f12.jpg" }?.mark, .failed)
        XCTAssertTrue(items.contains { $0.file == "f30.jpg" })
        XCTAssertEqual(items.map(\.file), items.sorted { $0.step < $1.step }.map(\.file), "oldest first")
        XCTAssertTrue(Filmstrip.items([], checks: checks).isEmpty)
    }

    func testTheStageShowsTheLiveScreenOrTheSelectedChecksProof() throws {
        let board = F.board(try SummaryTests.golden(), state: .failed)
        let failed = try XCTUnwrap(board.summary(F.tipSplit))
        let check = try XCTUnwrap(failed.checks.items.first)
        guard case .picture(let picture, let mark, let color, let dimmed) = StageContent.of(failed, check: check, pickedFrame: nil, liveWanted: false, framesHeld: []) else {
            return XCTFail("a failed check shows its picture")
        }
        XCTAssertEqual(picture.file, "017-screenshot.png")
        XCTAssertNotNil(mark)
        XCTAssertEqual(color, .fail)
        XCTAssertFalse(dimmed)
        XCTAssertEqual(StageContent.of(failed, check: check, pickedFrame: nil, liveWanted: true, framesHeld: []), .live)
        if case .picture(let p, _, _, _) = StageContent.of(failed, check: check, pickedFrame: "f.jpg", liveWanted: true, framesHeld: []) {
            XCTAssertEqual(p.file, "f.jpg", "a picked frame wins")
        } else { XCTFail() }

        var stuck = failed
        StateFixtures.apply(.notAnswering, to: &stuck)
        if case .picture(_, _, _, let dimmed) = StageContent.of(stuck, check: nil, pickedFrame: nil, liveWanted: false, framesHeld: []) {
            XCTAssertTrue(dimmed, "a stuck screen shows its last picture dimmed")
        } else { XCTFail() }
        var starting = failed
        StateFixtures.apply(.starting, to: &starting)
        if case .waiting = StageContent.of(starting, check: nil, pickedFrame: nil, liveWanted: false, framesHeld: []) {} else { XCTFail() }
    }

    func testActivityGroupsTheVerifiersStepsUnderTheCheckTheyProve() {
        let at = Date(timeIntervalSince1970: 1000)
        let messages = [
            Message(seq: 1, at: at, from: .coder, kind: .task, text: "Check TipSplit"),
            Message(seq: 2, at: at.addingTimeInterval(1), from: .verifier, kind: .progress, text: "Opening TipSplit. Then I look."),
            Message(seq: 3, at: at.addingTimeInterval(10), from: .verifier, kind: .progress, text: "Reading Each pays at 25%"),
        ]
        let steps = [
            Step(seq: 1, at: at.addingTimeInterval(2), tool: "machine_click", input: nil, output: nil, error: nil, durationMs: 400),
            Step(seq: 2, at: at.addingTimeInterval(11), tool: "machine_screenshot", input: nil, output: nil, error: nil, durationMs: 900),
        ]
        let checks = [SummaryCheck(id: "each", text: "Each pays becomes $50.00 at 25%", state: .fail, step: 2)]
        let sections = ActivityLayout.sections(messages: messages, steps: steps, checks: checks, working: false)
        XCTAssertEqual(sections.map(\.title), ["Setup", "Each pays becomes $50.00 at 25%"])
        XCTAssertEqual(sections[0].rows.first?.title, "Opening TipSplit")
        XCTAssertEqual(sections[1].rows.first?.glyph, .failed)
        XCTAssertTrue(sections[1].rows.first?.opensItself == true, "the failing row opens itself")
        XCTAssertEqual(sections[1].rows.first?.chips.first?.label, "Took a screenshot")
        let working = ActivityLayout.sections(messages: messages, steps: steps, checks: [], working: true)
        XCTAssertEqual(working.last?.rows.last?.glyph, .checking)
        XCTAssertEqual(ActivityLayout.title("machine_ui {}"), "Read the frontmost app", "a tool echo reads in words")
    }
}
