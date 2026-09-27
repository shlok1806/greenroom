import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// The real views in a real `NSWindow`, for rules only AppKit can show: the window's
/// title, where a list scrolled, what a key did to a text field. The window sits far off
/// every display, and xctest never becomes a Dock app (as in `LiveScreenTests`).
@MainActor
final class HostedViewTests: XCTestCase {
    private var windows: [NSWindow] = []

    override func tearDown() async throws {
        for window in windows {
            window.orderOut(nil)
            window.close()
        }
        windows = []
    }

    private func host(_ view: some View, size: CGSize = CGSize(width: 1200, height: 700)) -> NSWindow {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let window = OffDisplayWindow(
            contentRect: CGRect(origin: OffDisplayWindow.origin, size: size),
            styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        window.title = "Greenroom Companion"
        let host = NSHostingController(rootView: view)
        host.sceneBridgingOptions = [.toolbars, .title]
        window.contentViewController = host
        window.setContentSize(size)
        window.setFrameOrigin(OffDisplayWindow.origin)
        window.orderFrontRegardless()
        windows.append(window)
        return window
    }

    private nonisolated static let created = "2026-09-18T10:00:00Z"

    /// Answers every route for one run, `run-1`, with `steps` steps. Messages the app
    /// posts are handed to `posted`.
    private func client(steps: Int = 0, failing: Set<Int> = [], live: Bool = false, notes: Int = 0,
                        posted: (@Sendable (Data) -> Void)? = nil) -> DaemonClient {
        let status = live ? "ready" : "finished"
        let machine = live
            ? #", "machine": {"runId": "run-1", "name": "gr-1", "image": "base", "status": "ready", "createdAt": "\#(Self.created)", "dir": "/r"}"#
            : ""
        let stepList = (1...max(steps, 1)).prefix(steps).map { seq in
            failing.contains(seq)
                ? #"{"seq": \#(seq), "at": "\#(Self.created)", "tool": "machine_exec", "input": {"command": "false"}, "error": "exit 1"}"#
                : #"{"seq": \#(seq), "at": "\#(Self.created)", "tool": "machine_exec", "input": {"command": "echo \#(seq)"}, "output": {"exitCode": 0}}"#
        }.joined(separator: ",")
        // Each note long enough to wrap, so the transcript is many screens tall.
        let messageList = ([#"{"seq": 1, "at": "\#(Self.created)", "from": "coder", "kind": "task", "text": "Check the tip splitter"}"#]
            + (0..<notes).map { index in
                #"{"seq": \#(index + 2), "at": "\#(Self.created)", "from": "human", "kind": "note", "text": "Note \#(index + 1). Check the tip with 4 people as well, and the total after a 20% tip on a bill of 120."}"#
            }).joined(separator: ",")
        return StubURLProtocol.client { request in
            switch (request.httpMethod ?? "GET", request.url?.path(percentEncoded: true) ?? "") {
            case ("GET", "/api/runs"):
                return .json(#"[{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "\#(status)", "task": "Check the tip splitter", "steps": \#(steps)}]"#)
            case ("GET", "/api/runs/run-1"):
                return .json(#"{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "\#(status)"\#(machine)}"#)
            case ("GET", "/api/runs/run-1/messages"):
                return .json(#"{"messages": [\#(messageList)]}"#)
            case ("GET", "/api/runs/run-1/steps"):
                return .json("[\(stepList)]")
            case ("POST", "/api/runs/run-1/messages"):
                if let body = request.httpBody ?? request.httpBodyStream.map(Self.read) { posted?(body) }
                return .json("{}")
            default:
                return .json("[]")
            }
        }
    }

    private nonisolated static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }

    private func settle(_ seconds: Double = 0.6) async throws {
        try await Task.sleep(for: .seconds(seconds))
    }

    /// Waits up to `limit` for `condition`; the assertion after it says what went wrong.
    private func waitUntil(within limit: Duration = .seconds(4), _ condition: () -> Bool) async {
        try? await eventually(within: limit, condition)
    }

    // MARK: - #61 the window's title

    /// The one window is named by the open run (or the app), never by a pane in it.
    func testTheWindowIsNamedByTheOpenRunNotItsConversation() async throws {
        UserDefaults.standard.set(true, forKey: "showsConversation")
        let store = RunStore(client: client())
        await store.resync()
        store.selectedRunId = "run-1"
        let window = host(RootView(store: store))
        await waitUntil { window.title == "Check the tip splitter" }
        XCTAssertEqual(window.title, "Check the tip splitter")
    }

    /// Not a regression of #61 (the conversation is not shown then), but the other half
    /// of its rule.
    func testWithNoRunOpenTheWindowIsTheApp() async throws {
        UserDefaults.standard.set("none", forKey: "selectedRunId")
        let store = RunStore(client: client())
        await store.resync()
        let window = host(RootView(store: store))
        try await settle()
        XCTAssertNil(store.selectedRunId)
        XCTAssertEqual(window.title, "Greenroom Companion")
    }

    // MARK: - #59 scrolling the Steps list to a step

    private func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView, scroll.documentView != nil { return scroll }
        for child in view.subviews {
            if let found = scrollView(in: child) { return found }
        }
        return nil
    }

    /// Next Failure and every link to a step scroll the list to it, however far down.
    func testRevealingAStepScrollsTheListToIt() async throws {
        UserDefaults.standard.set(false, forKey: "stepsErrorsOnly")
        let store = RunStore(client: client(steps: 300, failing: [207]))
        await store.resync()
        store.selectedRunId = "run-1"
        await store.select("run-1")
        XCTAssertEqual(store.steps["run-1"]?.count, 300)
        let window = host(StepsView(store: store, runId: "run-1").frame(width: 900, height: 600),
                          size: CGSize(width: 900, height: 600))
        try await settle()
        let scroll = try XCTUnwrap(scrollView(in: try XCTUnwrap(window.contentView)))
        XCTAssertLessThan(scroll.documentVisibleRect.minY, 100, "starts at the top")

        store.requestSeek(runId: "run-1", step: 207, inSteps: true)
        let half = try XCTUnwrap(scroll.documentView).bounds.height * 0.5
        await waitUntil { scroll.documentVisibleRect.minY > half }
        let visible = scroll.documentVisibleRect
        let document = try XCTUnwrap(scroll.documentView).bounds
        // Step 207 of 300 sits well down the list; the view must have moved there.
        XCTAssertGreaterThan(visible.minY, document.height * 0.5, "the list stayed at \(visible) of \(document)")
    }

    /// #59, seen again on a live run: Steps opened with "Follow newest" on stayed at step 1
    /// of 51. Opening it on a live run shows the newest step.
    func testFollowingALiveRunOpensAtTheNewestStep() async throws {
        UserDefaults.standard.set(false, forKey: "stepsErrorsOnly")
        let store = RunStore(client: client(steps: 300, live: true))
        await store.resync()
        store.selectedRunId = "run-1"
        await store.select("run-1")
        XCTAssertTrue(store.facts("run-1").isAlive)
        let window = host(StepsView(store: store, runId: "run-1").frame(width: 900, height: 600),
                          size: CGSize(width: 900, height: 600))
        try await settle()
        let scroll = try XCTUnwrap(scrollView(in: try XCTUnwrap(window.contentView)))
        let document = try XCTUnwrap(scroll.documentView).bounds
        await waitUntil { scroll.documentVisibleRect.maxY > document.height * 0.9 }
        XCTAssertGreaterThan(scroll.documentVisibleRect.maxY, document.height * 0.9,
                             "the list stayed at \(scroll.documentVisibleRect) of \(document)")
    }

    // MARK: - #146 the transcript drew nothing after its rows changed

    /// The scroll views under `view`, deepest last.
    private func scrollViews(in view: NSView) -> [NSScrollView] {
        ((view as? NSScrollView).map { [$0] } ?? []) + view.subviews.flatMap { scrollViews(in: $0) }
    }

    /// How many sampled pixels of `view` differ from its first: 0 for a view that draws
    /// only its ground.
    private func ink(_ view: NSView) throws -> Int {
        let rep = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: rep)
        let ground = try XCTUnwrap(rep.colorAt(x: 1, y: 1)).usingColorSpace(.deviceRGB)
        var count = 0
        for y in stride(from: 0, to: rep.pixelsHigh, by: 3) {
            for x in stride(from: 0, to: rep.pixelsWide, by: 3) {
                guard let color = rep.colorAt(x: x, y: y)?.usingColorSpace(.deviceRGB), let ground else { continue }
                let difference = max(abs(color.redComponent - ground.redComponent),
                                     abs(color.greenComponent - ground.greenComponent),
                                     abs(color.blueComponent - ground.blueComponent))
                if difference > 0.12 { count += 1 }
            }
        }
        return count
    }

    /// #146: a lazy stack built only the rows it guessed were on screen, and while the
    /// window settled its width the bottom anchor chased each guess until the offset moved
    /// after the stack had built rows for it: the transcript drew no row at all (about 1 in
    /// 30 harness renders). The transcript is an eager stack, so every row is built wherever
    /// the scroll lands. The type check is the deterministic half; the drawing half is the
    /// case the harness caught, which a lazy stack failed only now and then.
    func testTheTranscriptDrawsItsRowsWhileTheWindowSettlesAndRowsChange() async throws {
        let store = RunStore(client: client(notes: 60))
        await store.resync()
        await store.select("run-1")
        XCTAssertEqual(store.messages["run-1"]?.count, 61)
        let view = ConversationView(store: store, runId: "run-1")
        XCTAssertFalse(String(reflecting: type(of: view.body)).contains("LazyVStack"),
                       "the transcript is a lazy stack again")

        let size = CGSize(width: 440, height: 560)
        let window = host(view, size: size)
        // The width settles in steps, as the real window's does when it folds its runs.
        for width in [1400.0, 1100, 560, 480, 450, size.width] {
            window.setContentSize(CGSize(width: width, height: size.height))
            try await settle(0.05)
        }
        try await settle()
        // Then the rows change, as a note taken back or "Tool calls" switched off does.
        store.messages["run-1"]?.removeLast()
        try await settle()

        // The transcript is the tallest scroll view (the composer's field may hold one too).
        let transcript = try XCTUnwrap(scrollViews(in: try XCTUnwrap(window.contentView))
            .max { $0.frame.height < $1.frame.height })
        XCTAssertGreaterThan(transcript.frame.height, 100)
        XCTAssertGreaterThan(try ink(transcript), 50, "the transcript drew only its ground")
    }

    // MARK: - #162 two rows looked selected after a run changed section

    /// Tall bands in a one-pixel column of `view` that differ from its ground: in the runs
    /// list, just inside a row's left edge, only a selected row's fill paints there.
    private func filledBands(_ view: NSView, x: CGFloat) throws -> Int {
        let rep = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: rep)
        let scale = CGFloat(rep.pixelsWide) / view.bounds.width
        let column = Int(x * scale)
        let ground = try XCTUnwrap(rep.colorAt(x: 1, y: rep.pixelsHigh / 2)?.usingColorSpace(.deviceRGB))
        var bands = 0, run = 0
        for y in 0..<rep.pixelsHigh {
            guard let color = rep.colorAt(x: column, y: y)?.usingColorSpace(.deviceRGB) else { continue }
            let difference = max(abs(color.redComponent - ground.redComponent),
                                 abs(color.greenComponent - ground.greenComponent),
                                 abs(color.blueComponent - ground.blueComponent))
            if difference > 0.15 {
                run += 1
            } else {
                if run > Int(24 * scale) { bands += 1 }
                run = 0
            }
        }
        return bands + (run > Int(24 * scale) ? 1 : 0)
    }

    /// #162, seen by the maintainer: run A was open while live (under Running), its
    /// machine went away (it moved to its day) and run B was opened. The lazy list drew A's
    /// first row, stale, under its day: filled as selected and "Live" beside B's. Exactly
    /// one row may look selected, and A reads as ended.
    func testOpeningAnotherRunAfterOneEndsLeavesOneRowSelected() async throws {
        let at = { (minutes: Double) in Date(timeIntervalSince1970: 1_000_000 - minutes * 60) }
        let store = RunStore()
        var a = RunSummary(runId: "20260926-231011-600cf88cfbdbcba8", createdAt: at(60), status: .ready,
                           verdict: VerdictState(seq: 9, verdict: "pass", status: .accepted, acceptedBy: .human),
                           lastActivity: Date(), task: "Please verify a Greenroom Companion change")
        let b = RunSummary(runId: "20260927-000233-acbc2b008dfc6a8f", createdAt: at(10), status: .ready,
                           lastActivity: Date(), task: "Check the transcript fix")
        let others = (1...4).map { index in
            RunSummary(runId: "20260926-10000\(index)-00000000000000\(index)0", createdAt: at(Double(200 + index)), destroyedAt: at(Double(190 + index)),
                       status: .finished, task: "Older run \(index)")
        }
        store.runs = [b, a] + others
        store.selectedRunId = a.runId
        let window = host(SidebarView(store: store).frame(width: 280, height: 700), size: CGSize(width: 280, height: 700))
        try await settle()
        let list = try XCTUnwrap(window.contentView)
        XCTAssertEqual(try filledBands(list, x: 11), 1, "A open: one selected row")

        // A's machine goes away, and the person opens B.
        a.status = .finished
        a.destroyedAt = Date()
        store.runs = [b, a] + others
        store.selectedRunId = b.runId
        try await settle()
        XCTAssertEqual(try filledBands(list, x: 11), 1, "two rows look selected")
    }

    // MARK: - #65 Shift-Return in the composer

    private func textView(in view: NSView) -> NSView? {
        if view is NSTextView || view is NSTextField, view.acceptsFirstResponder { return view }
        for child in view.subviews {
            if let found = textView(in: child) { return found }
        }
        return nil
    }

    private func key(_ window: NSWindow, return modifiers: NSEvent.ModifierFlags) throws {
        let event = try XCTUnwrap(NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: modifiers, timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: "\r", charactersIgnoringModifiers: "\r",
            isARepeat: false, keyCode: 36
        ))
        window.sendEvent(event)
    }

    /// The design spec's keyboard table: Return sends, Shift-Return is a new line. The
    /// message the daemon receives is both lines.
    func testShiftReturnStartsANewLineAndReturnSendsBoth() async throws {
        let sent = Box()
        let store = RunStore(client: client(posted: { sent.set($0) }))
        await store.resync()
        await store.select("run-1")
        let window = host(ConversationView(store: store, runId: "run-1").frame(width: 420, height: 700),
                          size: CGSize(width: 420, height: 700))
        try await settle()
        let field = try XCTUnwrap(textView(in: try XCTUnwrap(window.contentView)))
        XCTAssertTrue(window.makeFirstResponder(field))
        let editor = try XCTUnwrap(window.firstResponder as? NSTextView, "no field editor")

        // A person's keys arrive a run loop turn or more apart.
        editor.insertText("line one", replacementRange: editor.selectedRange())
        try await settle(0.2)
        try key(window, return: .shift)
        try await settle(0.2)
        editor.insertText("line two", replacementRange: editor.selectedRange())
        await waitUntil { editor.string == "line one\nline two" }
        XCTAssertEqual(editor.string, "line one\nline two")

        try key(window, return: [])
        try await eventually { sent.value != nil }
        let body = try JSONSerialization.jsonObject(with: try XCTUnwrap(sent.value)) as? [String: Any]
        XCTAssertEqual(body?["text"] as? String, "line one\nline two")
    }
}

/// Stays where it is put, far off every display; AppKit would otherwise pull a titled
/// window back on screen. Never key, like the snapshot harness's.
private final class OffDisplayWindow: NSWindow {
    static let origin = CGPoint(x: -30_000, y: -30_000)
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

private final class Box: @unchecked Sendable {
    private let lock = NSLock()
    private var data: Data?
    var value: Data? { lock.withLock { data } }
    func set(_ new: Data) { lock.withLock { data = new } }
}
