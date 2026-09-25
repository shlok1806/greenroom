import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// ADR 0005's key-routing tests, on the real window: a bare key on a pane acts, a bare
/// key in the composer types, and a key while driving reaches the guest. Keys go through
/// `KeyboardModel.handle` with the responder `KeyRouter` reads off the window, which is
/// what the router does with every key event.
@MainActor
final class KeyRoutingTests: XCTestCase {
    private var windows: [NSWindow] = []

    override func tearDown() async throws {
        for window in windows {
            window.orderOut(nil)
            window.close()
        }
        windows = []
    }

    private func host(_ view: some View, size: CGSize = CGSize(width: 1280, height: 800)) -> NSWindow {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let window = FarWindow(
            contentRect: CGRect(origin: FarWindow.origin, size: size),
            styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        window.identifier = NSUserInterfaceItemIdentifier("main")
        window.contentViewController = NSHostingController(rootView: view)
        window.setContentSize(size)
        window.setFrameOrigin(FarWindow.origin)
        window.orderFrontRegardless()
        windows.append(window)
        return window
    }

    private nonisolated static let created = "2026-09-18T10:00:00Z"

    private func client(live: Bool = false) -> DaemonClient {
        let status = live ? "ready" : "finished"
        let machine = live
            ? #", "machine": {"runId": "run-1", "name": "gr-1", "image": "base", "status": "ready", "createdAt": "\#(Self.created)", "dir": "/r"}"#
            : ""
        return StubURLProtocol.client { request in
            switch request.url?.path(percentEncoded: true) ?? "" {
            case "/api/runs":
                return .json(#"[{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "\#(status)", "task": "Check the tip splitter", "steps": 3}, {"runId": "run-2", "createdAt": "\#(Self.created)", "status": "finished", "task": "Check the login", "steps": 1}]"#)
            case "/api/runs/run-1", "/api/runs/run-2":
                return .json(#"{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "\#(status)"\#(machine)}"#)
            case "/api/runs/run-1/messages", "/api/runs/run-2/messages":
                return .json(#"{"messages": [{"seq": 1, "at": "\#(Self.created)", "from": "coder", "kind": "task", "text": "Check the tip splitter"}]}"#)
            case "/api/runs/run-1/steps":
                return .json(#"[{"seq": 1, "at": "\#(Self.created)", "tool": "machine_exec", "input": {"command": "echo 1"}, "output": {"exitCode": 0}}, {"seq": 2, "at": "\#(Self.created)", "tool": "machine_exec", "input": {"command": "echo 2"}, "output": {"exitCode": 0}}]"#)
            default:
                return .json("[]")
            }
        }
    }

    private func textField(in view: NSView) -> NSView? {
        if view is NSTextView || view is NSTextField, view.acceptsFirstResponder { return view }
        for child in view.subviews {
            if let found = textField(in: child) { return found }
        }
        return nil
    }

    /// `j`, `⏎`, `g s` and `esc` on the panes, through the handlers the views offer.
    func testABareKeyOnAPaneActs() async throws {
        UserDefaults.standard.set("run-1", forKey: "selectedRunId")
        UserDefaults.standard.set(StagePane.screen.rawValue, forKey: "stagePane")
        let store = RunStore(client: client())
        await store.resync()
        store.selectedRunId = "run-1"
        let keyboard = KeyboardModel(store: store)
        let window = host(RootView(store: store, keyboard: keyboard))
        try await eventually { store.selectedRunId == "run-1" && keyboard.available.contains(HandlerKey(id: .moveDown, context: .sidebar)) }
        let responder = KeyRouter.responder(in: window)
        XCTAssertEqual(responder, .other)

        // j moves through the runs, as shown.
        XCTAssertTrue(keyboard.handle(.char("j"), responder: responder))
        XCTAssertEqual(store.selectedRunId, "run-2")
        XCTAssertTrue(keyboard.handle(.char("k"), responder: responder))
        XCTAssertEqual(store.selectedRunId, "run-1")

        // Return goes into the run; g then s shows its steps.
        XCTAssertTrue(keyboard.handle(.enter, responder: responder))
        XCTAssertEqual(keyboard.pane, .stage)
        try await eventually { keyboard.available.contains(HandlerKey(id: .goSteps, context: .run)) }
        XCTAssertTrue(keyboard.handle(.char("g"), responder: responder))
        XCTAssertEqual(keyboard.pendingPrefix, .char("g"))
        XCTAssertTrue(keyboard.handle(.char("s"), responder: responder))
        XCTAssertNil(keyboard.pendingPrefix)
        try await eventually { keyboard.stage == .steps }
        XCTAssertEqual(UserDefaults.standard.string(forKey: "stagePane"), StagePane.steps.rawValue)

        // In Steps, j is the steps' own: the run stays open.
        try await eventually { keyboard.available.contains(HandlerKey(id: .moveDown, context: .steps)) }
        XCTAssertTrue(keyboard.handle(.char("j"), responder: responder))
        XCTAssertEqual(store.selectedRunId, "run-1")

        // esc backs out to the runs; `?` opens the help and esc folds it first.
        XCTAssertTrue(keyboard.handle(.escape, responder: responder))
        XCTAssertEqual(keyboard.pane, .sidebar)
        XCTAssertTrue(keyboard.handle(.char("?"), responder: responder))
        XCTAssertTrue(keyboard.helpOpen)
        XCTAssertTrue(keyboard.handle(.escape, responder: responder))
        XCTAssertFalse(keyboard.helpOpen)
        UserDefaults.standard.set(StagePane.screen.rawValue, forKey: "stagePane")
    }

    /// With the composer focused a bare key is the field's: the router lets it through,
    /// and it types instead of acting.
    func testABareKeyInTheComposerTypes() async throws {
        UserDefaults.standard.set(true, forKey: "showsConversation")
        let store = RunStore(client: client())
        await store.resync()
        await store.select("run-1")
        let keyboard = KeyboardModel(store: store)
        let window = host(ConversationView(store: store, runId: "run-1").frame(width: 420, height: 700)
            .environment(\.keyboard, keyboard), size: CGSize(width: 420, height: 700))
        try await eventually { keyboard.available.contains(HandlerKey(id: .moveDown, context: .conversation)) }
        keyboard.pane = .conversation
        let field = try XCTUnwrap(textField(in: try XCTUnwrap(window.contentView)))
        XCTAssertTrue(window.makeFirstResponder(field))
        let responder = KeyRouter.responder(in: window)
        XCTAssertEqual(responder, .text)

        for key in ["j", "k", "g", "?", "t", "a"] {
            XCTAssertFalse(keyboard.handle(.char(Character(key)), responder: responder), "\(key) acted in the composer")
        }
        XCTAssertNil(keyboard.pendingPrefix)
        XCTAssertFalse(keyboard.helpOpen)
        XCTAssertEqual(HintBar.content(keyboard.state()).mode, .typing)

        // esc leaves the field; the next j is the transcript's again.
        XCTAssertTrue(keyboard.handle(.escape, responder: responder))
        XCTAssertTrue(keyboard.handle(.char("j"), responder: .other))
    }

    /// Driving: the router takes nothing, not even Cmd-Q or esc; the guest's surface gets
    /// every key, as before the registry (ADR 0009).
    func testAKeyWhileDrivingReachesTheGuest() async throws {
        let pilotClient = GrantingControlClient()
        let store = RunStore(client: client(live: true), controlClient: pilotClient)
        await store.resync()
        store.selectedRunId = "run-1"
        await store.select("run-1")
        await store.pilot(for: "run-1").take()
        XCTAssertTrue(store.pilot(for: "run-1").active)
        let keyboard = KeyboardModel(store: store)

        // The screen's input surface, holding the keyboard as it does while driving.
        let window = host(Color.clear, size: CGSize(width: 400, height: 300))
        let surface = InputSurfaceView(frame: CGRect(x: 0, y: 0, width: 400, height: 300))
        window.contentView?.addSubview(surface)
        surface.setActive(true)
        XCTAssertTrue(window.makeFirstResponder(surface))
        let responder = KeyRouter.responder(in: window)
        XCTAssertEqual(responder, .guest)

        let keys: [KeyChord] = [.char("t"), .char("j"), .char("?"), .escape, .enter, .space, .cmd("q"), .cmd("k"),
                                KeyChord(key: .delete, command: true), .char("g")]
        for key in keys {
            XCTAssertFalse(keyboard.handle(key, responder: responder), "\(key.label) was kept from the guest")
        }
        XCTAssertFalse(keyboard.paletteOpen)
        XCTAssertNil(keyboard.confirmingDestroy)
        XCTAssertEqual(HintBar.content(keyboard.state()).mode, .driving)
        await store.pilot(for: "run-1").release()
    }
}

/// Lends the screen to whoever asks.
private actor GrantingControlClient: ControlClient {
    func takeControl(runId: String) async throws -> ControlResponse {
        ControlResponse(
            control: ControlLease(holder: "human", since: Date(), expires: Date().addingTimeInterval(60), actions: 0),
            screen: GuestScreen(width: 1024, height: 768)
        )
    }

    func renewControl(runId: String) async throws -> ControlResponse {
        try await takeControl(runId: runId)
    }

    func releaseControl(runId: String) async throws {}

    func input(runId: String, actions: [InputAction]) async throws -> InputResult {
        InputResult(actions: actions.count, screen: GuestScreen(width: 1024, height: 768))
    }
}

/// Far off every display and never key, as `HostedViewTests`' windows are.
private final class FarWindow: NSWindow {
    static let origin = CGPoint(x: -32_000, y: -32_000)
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}
