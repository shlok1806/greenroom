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
    private func store(_ state: F.State = .failed, posts: Posts = Posts(), mockup: Bool = false, compact: Bool = false,
                       passed: Bool = false, mockupState: F.State? = nil, wide: Bool = false) throws -> RunStore {
        let png = Self.greyPNG
        let client = StubURLProtocol.client { request in
            let path = request.url?.path ?? ""
            if request.httpMethod == "POST" {
                posts.add(path, String(data: request.httpBody ?? Self.read(request.httpBodyStream), encoding: .utf8) ?? "")
                return .json("{}", status: 202)
            }
            if path.contains("/artifacts/") || path.contains("/frames/") { return StubURLProtocol.Reply(body: png) }
            if path.hasSuffix("/messages") { return .json(#"{"messages": []}"#) }
            if path.hasSuffix("/frames") { return .json(Self.framesJSON) }
            if path.hasSuffix("/steps") || path.hasSuffix("/api/runs") { return .json("[]") }
            return .json("{}", status: 404)
        }
        let store = RunStore(client: client, controlClient: GrantingControlClient(), screenSource: NoScreen())
        let golden = try SummaryTests.golden()
        if wide {
            store.board = F.mockupWide(golden)
        } else if let mockupState {
            store.board = F.mockup(golden, state: mockupState)
        } else {
            store.board = passed ? F.mockupPassed(golden) : mockup ? F.mockup(golden, compact: compact) : F.board(golden, state: state)
        }
        store.reachable = true
        store.selectedRunId = F.tipSplit
        var detail = RunDetail(runId: F.tipSplit)
        detail.verdict = VerdictState(seq: 16, verdict: "fail", status: .proposed)
        store.details[F.tipSplit] = detail
        return store
    }

    /// Forty recorded frames, one a step, for the filmstrip.
    private nonisolated static let framesJSON: String = {
        let items = (1...40).map { i in
            #"{"at": "2026-09-23T04:4\#(i / 10):\#(String(format: "%02d", (i % 10) * 5))Z", "file": "f\#(i).jpg", "step": \#(i)}"#
        }
        return "[" + items.joined(separator: ",") + "]"
    }()

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

    private func host(_ shell: ShellModel, state: F.State, size: CGSize = CGSize(width: 1280, height: 800), redacted: Bool = true,
                      dark: Bool = false) async -> ParkedHost {
        let view = CompanionShell(shell: shell)
            .environment(\.frozenNow, F.start.addingTimeInterval(state.at))
            .environment(\.redactsGuestScreen, redacted)
        let host = ParkedHost(view, size: size, dark: dark)
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

    // MARK: - Against the Figma frames (docs/22 section 7)

    /// A Figma frame the window is held to: its layout file and PNG export under
    /// `docs/22-swiftui-clone-plan/figma/`, and the window that draws it.
    struct FigmaFrame {
        var name: String
        var size = CGSize(width: 1280, height: 800)
        var dark = false
        var compact = false
        var passed = false
        /// The state the frame draws TipSplit in, when not failed or passed.
        var state: StateFixtures.State?
        /// Parts the frame draws otherwise than the app, by decision (see the PR for why).
        var skip: [String] = []
        /// The palette open over the run (Figma 12), or the person driving (Figma 09).
        var palette = false
        var driving = false
        /// How many parts must match at least: a frame with little on it has few.
        var minParts = 50
        var wide = false
        /// The SSIM the chrome must reach. Frame 07a, with its runs list and run body masked
        /// since redesign 7, keeps only the header and the window's edges, where Inter and SF
        /// Pro setting the same words weigh more: 0.984 there (0.9955 before the mask).
        var minSSIM = 0.985
    }

    /// Redesign 7 moved the checks into the inspector column and put the transport bar under
    /// the picture (frame R7 03); the frames before it are held for the sidebar and the header
    /// only, and their run body and toolbar are masked.
    static let beforeR7 = ["Run/Body", "Run/Toolbar"]

    static let frames: [FigmaFrame] = [
        // Redesign 7: the player and the inspector. The scrub bar draws the run's own steps.
        FigmaFrame(name: "r7-failed-light", skip: ["Run/Body/Stage/Timeline/Track"]),
        FigmaFrame(name: "m03-failed-light", skip: beforeR7),
        FigmaFrame(name: "m03-failed-dark", dark: true, skip: beforeR7),
        FigmaFrame(name: "m03-failed-compact-light", size: CGSize(width: 1024, height: 680), compact: true, skip: beforeR7),
        FigmaFrame(name: "m04-passed-light", passed: true, skip: beforeR7),
        FigmaFrame(name: "m02-live-light", state: .live, skip: beforeR7),
        // The daemon puts a Mac that stopped answering under Needs you (root ADR 0036); the
        // frame leaves it under Running. Frames 07 also show four Done runs where 02 to 04 show five.
        FigmaFrame(name: "m07a-not-answering-light", state: .notAnswering, skip: ["Sidebar/Runs"] + beforeR7, minParts: 20, minSSIM: 0.98),
        FigmaFrame(name: "m07b-restarting-light", state: .restarting,
                   skip: ["Sidebar/Runs/Run row[7]", "Sidebar/Runs/Run row[8]", "Sidebar/Runs/More"] + beforeR7),
        // The frame's runs to go to are a few; the app lists every run, so the rows past the
        // first two of that section show other runs (words differ, places do not).
        // Redesign 7 lists the player's and the old window's actions too, so the rows past the
        // sixth are other commands than the frame's.
        FigmaFrame(name: "m12-palette-light", skip: beforeR7 + ["Palette/Frame[6]", "Palette/Frame[7]", "Palette/Frame[8]"],
                   palette: true, minParts: 40),
        FigmaFrame(name: "m02-live-dark", dark: true, state: .live, skip: beforeR7),
        FigmaFrame(name: "m01-home-wide-light", size: CGSize(width: 1600, height: 1000), skip: beforeR7, wide: true),
        // The live screen is the guest's: its picture and the ring drawn round it are not compared.
        FigmaFrame(name: "m09-take-control-light", state: .live, skip: ["Screen area/Live screen"], driving: true, minParts: 7),
    ]

    /// The failed run on the mockup's board, as `frame` draws it.
    private func mockupHost(_ frame: FigmaFrame, redacted: Bool) async throws -> ParkedHost {
        let shell = ShellModel(store: try store(.failed, mockup: true, compact: frame.compact, passed: frame.passed,
                                                mockupState: frame.state, wide: frame.wide))
        await shell.store.select(F.tipSplit)
        // The stub answers 404 to what it does not serve; that is not the frame's error.
        shell.store.clearError()
        if frame.driving { await shell.store.pilot(for: F.tipSplit).take() }
        shell.paletteOpen = frame.palette
        let host = await host(shell, state: frame.state ?? .failed, size: frame.size, redacted: redacted, dark: frame.dark)
        try await Task.sleep(for: .seconds(1))
        host.window.contentView?.layoutSubtreeIfNeeded()
        host.window.contentView?.displayIfNeeded()
        return host
    }

    private static func figma(_ file: String) -> URL {
        MotionTests.repo.appendingPathComponent("docs/22-swiftui-clone-plan/figma").appendingPathComponent(file)
    }

    /// Every part the window reports, against the layer of the same path in each frame's layout
    /// file. Boxes: origin and size within 0.5 pt. Text: the design draws Inter and the app SF
    /// Pro, so a text part's origin and height are held to 0.5 pt and its width is not compared.
    func testTheFailedRunIsLaidOutAsTheFigmaFrames() async throws {
        CloneParts.enabled = true
        defer { CloneParts.enabled = false }
        var report: [String] = []
        for frame in Self.frames {
            let host = try await mockupHost(frame, redacted: true)
            let parts = CloneParts.frames(in: host.window)
            // GREENROOM_CLONE_DUMP=<dir> writes what the window reports, one part a line.
            if let dump = ProcessInfo.processInfo.environment["GREENROOM_CLONE_DUMP"] {
                let lines = parts.sorted { $0.key < $1.key }.map { "\($0.key);\($0.value.minX);\($0.value.minY);\($0.value.width);\($0.value.height)" }
                try lines.joined(separator: "\n").write(to: URL(fileURLWithPath: dump).appendingPathComponent(frame.name + ".parts.txt"), atomically: true, encoding: .utf8)
            }
            let layout = try String(contentsOf: Self.figma(frame.name + ".layout.txt"), encoding: .utf8)
            let (compared, misses) = Self.layoutMisses(parts: parts, layout: layout, skip: frame.skip)
            report.append("\(frame.name) \(compared) parts, \(misses.count) off")
            misses.forEach { print("  off \(frame.name): " + $0) }
            XCTAssertGreaterThanOrEqual(compared, frame.minParts, "\(frame.name) parts found: \(parts.keys.sorted())")
            XCTAssertTrue(misses.isEmpty, "\(frame.name):\n" + misses.joined(separator: "\n"))
        }
        print("clone layout: " + report.joined(separator: "; "))
    }

    /// How each part may differ, and only this: a part sized by its words ("hugs") keeps its
    /// origin but not its width, since SF Pro and Inter set the same words a little apart; a part
    /// laid out from the trailing edge is held by its trailing edge; a text that follows another
    /// text on its line is held by its top and height only. The traffic lights are the system's.
    static func layoutMisses(parts: [String: CGRect], layout: String, skip: [String] = []) -> (compared: Int, misses: [String]) {
        func last(_ path: String) -> String { String(path.split(separator: "/").last ?? "") }
        func hugs(_ path: String) -> Bool {
            ["Button", "Toolbar button", "Outcome", "Now", "Run meta", "Status", "Shortcut", "Keycap", "Take control", "Live", "Tab"].contains { last(path).hasPrefix($0) }
        }
        func trailing(_ path: String) -> Bool {
            let name = last(path)
            return name == "Meta" || name == "Shortcut" || name == "Keycap" || name.hasPrefix("Button") || name.hasPrefix("Toolbar button") || name == "Icon button"
                || path.contains("/Button") || path.contains("/Toolbar button") || path.contains("Toolbar/Icon button")
        }
        func followsText(_ path: String) -> Bool { ["Tally", "Run meta"].contains(last(path)) || path == "Toolbar/Text[1]" }
        var misses: [String] = []
        var compared = 0
        for line in layout.split(separator: "\n") where !line.hasPrefix("#") {
            let f = line.split(separator: ";", omittingEmptySubsequences: false).map(String.init)
            guard f.count >= 5, let x = Double(f[1]), let y = Double(f[2]), let w = Double(f[3]), let h = Double(f[4]) else { continue }
            let path = f[0]
            if path.hasPrefix("Sidebar/Titlebar/Traffic lights") || skip.contains(where: { path.hasPrefix($0) }) { continue }
            guard let got = parts[path] else { continue }
            let text = f.count > 5
            var off: [String] = []
            if trailing(path), !path.contains("Icon/") || last(path) == "Icon button" {
                // Held by the trailing edge: the words before it set its start.
                if !text, abs(got.maxX - (x + w)) > 0.5, !path.hasSuffix("Button[0]"), !path.hasSuffix("Toolbar button[0]") {
                    off.append("maxX \(got.maxX) want \(x + w)")
                }
            } else if !followsText(path), !(path.split(separator: "/").dropLast().contains { hugs(String($0)) }) {
                if abs(got.minX - x) > 0.5 { off.append("x \(got.minX) want \(x)") }
            }
            if abs(got.minY - y) > 0.5 { off.append("y \(got.minY) want \(y)") }
            if !text, !hugs(path), abs(got.width - w) > 0.5 { off.append("w \(got.width) want \(w)") }
            if abs(got.height - h) > 0.5 { off.append("h \(got.height) want \(h)") }
            if !off.isEmpty { misses.append("\(path): " + off.joined(separator: ", ")) }
            compared += 1
        }
        return (compared, misses)
    }

    /// The failed run on the mockup's board against each frame's PNG export (the window at 48, 32
    /// with its shadow around it): SSIM over the chrome at least 0.985. Glyphs, icons and the
    /// guest pictures are masked out by the frame's layout file. GREENROOM_CLONE_RENDER names a
    /// directory to write the renders to.
    func testTheFailedRunMatchesTheFigmaFramesPixelForPixel() async throws {
        var report: [String] = []
        for frame in Self.frames {
            let host = try await mockupHost(frame, redacted: false)
            let image = try XCTUnwrap(host.image())
            if let out = ProcessInfo.processInfo.environment["GREENROOM_CLONE_RENDER"] {
                let url = URL(fileURLWithPath: out).appendingPathComponent(frame.name + ".png")
                try XCTUnwrap(NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])).write(to: url)
            }
            let png = try XCTUnwrap(NSBitmapImageRep(data: try Data(contentsOf: Self.figma(frame.name + ".png")))?.cgImage)
            let layout = try String(contentsOf: Self.figma(frame.name + ".layout.txt"), encoding: .utf8)
            let result = try XCTUnwrap(CloneSSIM.compare(render: image, figma: png, origin: CGPoint(x: 48, y: 32), layout: layout, masked: frame.skip))
            report.append(String(format: "%@ SSIM %.4f, %.2f%% off", frame.name, result.chrome, result.offShare * 100))
            XCTAssertGreaterThanOrEqual(result.chrome, frame.minSSIM, frame.name)
        }
        print("clone pixels: " + report.joined(separator: "; "))
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
        // Held for its undo first: nothing goes out until the window ends.
        try await waitUntil { shell.store.verdictUndo.pending != nil }
        XCTAssertFalse(posts.all.contains { $0.body.contains(#""kind":"accept""#) })
        await shell.store.sendHeldVerdictChoice(now: Date().addingTimeInterval(60))
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
        XCTAssertEqual(shell.inspectorTab, .message, "the reason is asked in the Message tab, beside the player")
        shell.composerDraft = "Each pays should include the tip."
        shell.sendComposer()
        try await waitUntil { shell.store.verdictUndo.pending != nil }
        await shell.store.sendHeldVerdictChoice(now: Date().addingTimeInterval(60))
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

    /// The palette offers a message only while the run's Mac is up, as the toolbar does.
    func testThePaletteOffersAMessageOnlyWhileTheMacIsUp() async throws {
        let live = ShellModel(store: try store(.live))
        await live.store.select(F.tipSplit)
        XCTAssertTrue(PaletteOptions.of(live).contains { $0.id == "message" })
        let done = ShellModel(store: try store(.done))
        await done.store.select(F.tipSplit)
        XCTAssertFalse(PaletteOptions.of(done).contains { $0.id == "message" })
    }

    func testAPassedRunsKeyFramesAreTheProofPicturesOnePerStepCaptioned() throws {
        let board = F.mockupPassed(try SummaryTests.golden())
        let checks = try XCTUnwrap(board.summary(F.tipSplit)).checks.items
        let frames = KeyFrames.items(checks)
        // Steps 3, 12 and 17: the two checks proven at 12 share a frame, captioned by the first.
        XCTAssertEqual(frames.map(\.step), [3, 12, 17])
        XCTAssertEqual(frames.map(\.caption), ["Step 3", "Saw $48.00", "Saw $50.00"])
        XCTAssertEqual(frames.map(\.checkID), ["window", "each", "each-25"])
        XCTAssertEqual(KeyFrames.rowWidth, 512, accuracy: 0.001)
        // A failed check is not a key frame; only the newest three are kept.
        var more = checks
        more.append(SummaryCheck(id: "late", text: "Late", state: .pass, saw: "1", picture: SummaryPicture(kind: "screenshot", file: "030.png", step: 30)))
        more.append(SummaryCheck(id: "bad", text: "Bad", state: .fail, saw: "2", picture: SummaryPicture(kind: "screenshot", file: "031.png", step: 31)))
        XCTAssertEqual(KeyFrames.items(more).map(\.step), [12, 17, 30])
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
