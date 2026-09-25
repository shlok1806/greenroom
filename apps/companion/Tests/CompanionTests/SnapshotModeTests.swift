import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// A snapshot (`GREENROOM_SNAPSHOT`) is a camera, never a seat: it never writes to the
/// daemon, no real event reaches its window, and a run it cannot find fails it rather
/// than showing another one.
@MainActor
final class SnapshotModeTests: XCTestCase {
    private var windows: [NSWindow] = []

    override func tearDown() async throws {
        for window in windows {
            window.orderOut(nil)
            window.close()
        }
        windows = []
    }

    private nonisolated static let created = "2026-09-18T10:00:00Z"

    /// Two runs, each with a verdict open for review; every request is recorded.
    private func client(readOnly: Bool, recorder: Recorder) -> DaemonClient {
        StubURLProtocol.client(readOnly: readOnly) { request in
            let path = request.url?.path(percentEncoded: true) ?? ""
            recorder.append("\(request.httpMethod ?? "GET") \(path)")
            switch path {
            case "/api/runs":
                return .json(#"[{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "finished", "task": "Check the tip splitter", "steps": 1}, {"runId": "run-2", "createdAt": "\#(Self.created)", "status": "finished", "task": "Buy the groceries", "steps": 1}]"#)
            case "/api/runs/run-1", "/api/runs/run-2":
                return .json(#"{"runId": "run-1", "createdAt": "\#(Self.created)", "status": "finished", "verdict": {"seq": 2, "verdict": "fail", "summary": "The total is wrong.", "status": "proposed", "disputes": 0}}"#)
            case "/api/runs/run-1/messages", "/api/runs/run-2/messages":
                return .json(#"{"messages": [{"seq": 1, "at": "\#(Self.created)", "from": "coder", "kind": "task", "text": "Check it"}]}"#)
            default:
                return request.httpMethod == "GET" ? .json("[]") : .json("{}")
            }
        }
    }

    // MARK: - Writes

    /// Every write route is refused inside the client: nothing reaches the network.
    func testAReadOnlyClientRefusesEveryWriteBeforeItLeaves() async throws {
        let recorder = Recorder()
        let client = client(readOnly: true, recorder: recorder)
        let click = InputAction(type: .click, x: 0.5, y: 0.5, button: "left", clicks: 1)
        let writes: [(String, () async throws -> Void)] = [
            ("POST /api/runs/run-1/messages", { try await client.send(runId: "run-1", kind: .accept, text: "accepted", replyTo: 2) }),
            ("POST /api/runs/run-1/screenshot", { try await client.screenshot(runId: "run-1") }),
            ("POST /api/runs/run-1/destroy", { try await client.destroy(runId: "run-1") }),
            ("POST /api/runs/run-1/control", { _ = try await client.takeControl(runId: "run-1") }),
            ("POST /api/runs/run-1/control", { _ = try await client.renewControl(runId: "run-1") }),
            ("DELETE /api/runs/run-1/control", { try await client.releaseControl(runId: "run-1") }),
            ("POST /api/runs/run-1/input", { _ = try await client.input(runId: "run-1", actions: [click]) }),
        ]
        for (route, write) in writes {
            do {
                try await write()
                XCTFail("\(route) was sent")
            } catch {
                XCTAssertEqual(error as? DaemonError, .readOnly(route))
            }
        }
        XCTAssertEqual(recorder.values, [])
        // Reads still go.
        let runs = try await client.runs()
        XCTAssertEqual(runs.map(\.runId), ["run-1", "run-2"])
        XCTAssertEqual(recorder.values, ["GET /api/runs"])
    }

    /// The app's client is read-only exactly in a snapshot, and a test process is not one.
    func testOnlyASnapshotIsReadOnly() {
        XCTAssertFalse(SnapshotMode.isActive)
        XCTAssertFalse(DaemonClient().readOnly)
        XCTAssertTrue(AppDefaults.shared === UserDefaults.standard)
    }

    /// An accept held in its undo and sent when the window ends reaches nothing from a
    /// snapshot's store; the same store with a writing client does send it.
    func testAHeldAcceptInASnapshotIsNeverSent() async throws {
        for readOnly in [true, false] {
            let recorder = Recorder()
            let store = RunStore(client: client(readOnly: readOnly, recorder: recorder))
            await store.resync()
            store.selectedRunId = "run-1"
            await store.resync()
            XCTAssertTrue(store.verdict("run-1")?.status.isOpen ?? false, "the stub's verdict is open")
            await store.holdAccept(runId: "run-1")
            XCTAssertNotNil(store.heldVerdictChoice("run-1"))
            await store.sendHeldVerdictChoice(now: Date().addingTimeInterval(60))
            store.undoVerdictChoice()
            let writes = recorder.values.filter { !$0.hasPrefix("GET") }
            XCTAssertEqual(writes, readOnly ? [] : ["POST /api/runs/run-1/messages"], "readOnly: \(readOnly)")
        }
    }

    // MARK: - Keys

    private func host(_ view: some View) -> NSWindow {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let window = NeverKeyWindow(
            contentRect: CGRect(origin: NeverKeyWindow.origin, size: CGSize(width: 1280, height: 800)),
            styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        window.identifier = NSUserInterfaceItemIdentifier("main")
        window.contentViewController = NSHostingController(rootView: view)
        window.setFrameOrigin(NeverKeyWindow.origin)
        window.orderFrontRegardless()
        windows.append(window)
        return window
    }

    private func key(_ characters: String, in window: NSWindow) -> NSEvent {
        NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: characters,
            charactersIgnoringModifiers: characters, isARepeat: false, keyCode: 50
        )!
    }

    /// In a snapshot a person's key or click never reaches the window, even one aimed
    /// straight at it; only the snapshot's own keys, handed to `inject`, drive it.
    func testASnapshotRouterIgnoresRealEventsAndTakesOnlyItsOwn() async throws {
        let store = RunStore(client: client(readOnly: true, recorder: Recorder()))
        await store.resync()
        store.selectedRunId = "run-1"
        let keyboard = KeyboardModel(store: store)
        let window = host(RootView(store: store, keyboard: keyboard))
        try await eventually { KeyRouter.responder(in: window) == .other }

        let snapshot = KeyRouter(keyboard: keyboard, snapshot: true)
        let help = key("?", in: window)
        XCTAssertFalse(snapshot.passes(help))
        XCTAssertFalse(keyboard.helpOpen)
        let next = key("j", in: window)
        XCTAssertFalse(snapshot.passes(next))
        XCTAssertEqual(store.selectedRunId, "run-1")
        let click = NSEvent.mouseEvent(
            with: .leftMouseDown, location: CGPoint(x: 100, y: 100), modifierFlags: [],
            timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil,
            eventNumber: 1, clickCount: 1, pressure: 1
        )!
        XCTAssertFalse(snapshot.passes(click))
        XCTAssertTrue(KeyRouter.personEvents.contains(.keyDown))
        XCTAssertTrue(KeyRouter.personEvents.contains(.leftMouseDown))
        XCTAssertTrue(KeyRouter.personEvents.contains(.scrollWheel))

        snapshot.inject(help, into: window)
        XCTAssertTrue(keyboard.helpOpen)

        // Outside a snapshot the same key is the person's, and acts.
        keyboard.helpOpen = false
        let app = KeyRouter(keyboard: keyboard, snapshot: false)
        XCTAssertFalse(app.passes(help))
        XCTAssertTrue(keyboard.helpOpen)
    }

    // MARK: - The requested run

    #if DEBUG
    /// A run the daemon does not list fails the snapshot and leaves the selection alone;
    /// a listed one is opened, whatever was open before.
    func testARequestedRunThatIsMissingFailsTheSnapshot() async throws {
        let store = RunStore(client: client(readOnly: true, recorder: Recorder()))
        XCTAssertEqual(SnapshotHook.open("run-1", in: store)?.description.contains("did not answer"), true)

        await store.resync()
        store.selectedRunId = "run-2"
        let missing = SnapshotHook.open("run-9", in: store)
        XCTAssertEqual(missing?.description, "run run-9 is not in greenroom's list of 2 runs; no other run is shown in its place")
        XCTAssertEqual(store.selectedRunId, "run-2")

        XCTAssertNil(SnapshotHook.open("run-1", in: store))
        XCTAssertEqual(store.selectedRunId, "run-1")
        XCTAssertNil(SnapshotHook.open(nil, in: store))
    }

    /// A window the hook seals can never be key or main.
    func testASealedWindowCanNeverBeKey() {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let window = SealableWindow(
            contentRect: CGRect(origin: NeverKeyWindow.origin, size: CGSize(width: 400, height: 300)),
            styleMask: [.titled, .resizable], backing: .buffered, defer: false
        )
        window.isReleasedWhenClosed = false
        windows.append(window)
        XCTAssertTrue(window.canBecomeKey)
        SnapshotHook.seal(window)
        XCTAssertFalse(window.canBecomeKey)
        XCTAssertFalse(window.canBecomeMain)
        XCTAssertNoThrow(try SnapshotHook.assertNotAPerson(window))
    }
    #endif
}

/// Far off every display and never key, as `HostedViewTests`' windows are.
private final class NeverKeyWindow: NSWindow {
    static let origin = CGPoint(x: -32_000, y: -32_000)
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

/// A window class of its own, so sealing it (which answers for the whole class) touches
/// no other test's windows.
private final class SealableWindow: NSWindow {
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
}
