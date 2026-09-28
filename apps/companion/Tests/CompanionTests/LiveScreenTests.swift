import AVFoundation
import IOKit.pwr_mgt
import XCTest

@testable import Companion

@MainActor
final class LiveScreenTests: XCTestCase {
    private let colors: [SyntheticScreen.Color] = [
        .init(red: 30, green: 200, blue: 40),
        .init(red: 40, green: 60, blue: 220),
    ]

    private func messages() throws -> [ScreenMessage] {
        let encoded = try SyntheticScreen.encode(width: 1280, height: 960, colors: colors)
        var reader = ScreenFrameReader()
        return try reader.append(SyntheticScreen.wire(encoded, width: 1280, height: 960))
    }

    func testAStreamPlaysIntoTheLayerAtItsPixelSize() async throws {
        let awake = try await keepTheDisplayAwake()
        defer { IOPMAssertionRelease(awake) }
        let source = FakeScreenSource(try messages())
        let live = LiveScreen(runId: "r", source: source)
        let window = try hostedWindow(live)
        defer { window.close() }
        live.start()

        try await eventually { live.phase == .playing }
        XCTAssertEqual(live.pixelSize, CGSize(width: 1280, height: 960))
        let renderer = live.output.layer.sampleBufferRenderer
        let shown = try await eventually { renderer.displayedPixelBuffer() }
        XCTAssertNotEqual(renderer.status, .failed, String(describing: renderer.error))
        XCTAssertEqual(CVPixelBufferGetWidth(shown), 1280)
        XCTAssertEqual(CVPixelBufferGetHeight(shown), 960)
        live.stop()
    }

    /// The layer sits exactly on the rectangle `ScreenGeometry` maps clicks against.
    func testTheLayerCoversTheFittedRectangle() throws {
        let live = LiveScreen(runId: "r", source: FakeScreenSource([]))
        let view = LiveScreenHostView(displayLayer: live.output.layer)
        view.frame = CGRect(x: 0, y: 0, width: 801, height: 400)
        view.pixelSize = CGSize(width: 2048, height: 1536)
        view.layoutSubtreeIfNeeded()

        let fitted = ScreenGeometry.fitted(image: view.pixelSize, in: view.bounds.size)
        XCTAssertEqual(fitted, CGRect(x: 134, y: 0, width: 533 + 1.0 / 3, height: 400))
        XCTAssertEqual(view.convertFromLayer(live.output.layer.frame), fitted)
        XCTAssertEqual(live.output.layer.videoGravity, .resizeAspect)
    }

    /// The layer only displays frames inside an ordered window that is on a display.
    /// A window that intersects no screen has no display to refresh it: samples
    /// decode and `displayedPixelBuffer()` stays nil for good. Tests run in the
    /// developer's (or the self-hosted CI runner's) login session, so the window
    /// sits on the primary screen under the desktop picture, where nobody sees it,
    /// takes no clicks, and xctest never becomes a Dock app.
    private func hostedWindow(_ live: LiveScreen) throws -> NSWindow {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let screen = try XCTUnwrap(NSScreen.screens.first, "A display is online but AppKit lists no screen.")
        let origin = CGPoint(x: screen.frame.minX, y: screen.frame.minY)
        let window = NSWindow(contentRect: CGRect(origin: origin, size: CGSize(width: 640, height: 480)), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.level = NSWindow.Level(rawValue: Int(CGWindowLevelForKey(.desktopWindow)) - 1)
        window.collectionBehavior = [.canJoinAllSpaces, .stationary, .ignoresCycle]
        window.ignoresMouseEvents = true
        window.hasShadow = false
        let view = LiveScreenHostView(displayLayer: live.output.layer)
        view.pixelSize = CGSize(width: 1280, height: 960)
        window.contentView = view
        window.orderBack(nil)
        XCTAssertNotNil(window.screen, "The hosting window is on no display, so the layer can present nothing.")
        return window
    }

    /// The layer presents a frame only on a display's refresh, so while the display
    /// sleeps (a locked, idle Mac, like the unattended CI runner) the renderer decodes
    /// and `displayedPixelBuffer()` stays nil. Declaring user activity wakes it, as
    /// `caffeinate -u` does, even behind the lock screen. Release the returned assertion.
    private func keepTheDisplayAwake() async throws -> IOPMAssertionID {
        var displays: UInt32 = 0
        CGGetOnlineDisplayList(0, nil, &displays)
        if displays == 0 { throw XCTSkip("No display is online, so the layer presents nothing.") }
        var assertion = IOPMAssertionID(0)
        let declared = IOPMAssertionDeclareUserActivity("LiveScreenTests" as CFString, kIOPMUserActiveLocal, &assertion)
        XCTAssertEqual(declared, kIOReturnSuccess)
        try await eventually(within: .seconds(10)) { CGDisplayIsAsleep(CGMainDisplayID()) == 0 }
        return assertion
    }

    func testStoppingHangsUp() async throws {
        let source = FakeScreenSource(try messages())
        let live = LiveScreen(runId: "r", source: source)
        live.start()
        try await eventually { live.phase == .playing }

        live.stop()
        XCTAssertFalse(live.isRunning)
        XCTAssertEqual(live.phase, .connecting)
        try await eventually { source.hungUp }
    }

    func testAReportOnItsWayWhenStoppingChangesNothing() async throws {
        let all = try messages()
        let firstVideo = try XCTUnwrap(all.firstIndex { if case .video = $0 { true } else { false } })
        let source = ManualScreenSource()
        let live = LiveScreen(runId: "r", source: source)
        live.start()
        try await eventually { source.isOpen }
        source.send(all[..<firstVideo])
        try await eventually { live.pixelSize != nil }

        // The first frame's report waits for the main actor, which stops first.
        source.send(all[firstVideo...])
        usleep(300_000)
        live.stop()
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(live.phase, .connecting)
    }

    func testARefusalFallsBackWithTheDaemonsWords() async throws {
        let client = StubURLProtocol.client { _ in .json(#"{"error": "machine is not ready"}"#, status: 409) }
        let live = LiveScreen(runId: "r", source: client)
        live.start()
        try await eventually { live.phase == .failed("machine is not ready") }
        live.stop()
    }

    func testADroppedStreamReconnects() async throws {
        let source = FakeScreenSource(try messages(), endFirst: true)
        let live = LiveScreen(runId: "r", source: source)
        live.start()
        try await eventually { live.phase == .failed("The stream ended.") }
        // The first retry waits a second.
        try await eventually(within: .seconds(5)) { source.calls == 2 && live.phase == .playing }
        live.stop()
    }
}

/// Serves the same messages on every connection and then stays open, like a
/// still screen, unless told to end the first one.
final class FakeScreenSource: ScreenSource, @unchecked Sendable {
    private let messages: [ScreenMessage]
    private let endFirst: Bool
    private let lock = NSLock()
    private var opened = 0
    private var closed = 0

    init(_ messages: [ScreenMessage], endFirst: Bool = false) {
        self.messages = messages
        self.endFirst = endFirst
    }

    var calls: Int { lock.withLock { opened } }
    var hungUp: Bool { lock.withLock { closed == opened && opened > 0 } }

    func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> {
        let call = lock.withLock {
            opened += 1
            return opened
        }
        return AsyncThrowingStream { continuation in
            continuation.onTermination = { [self] _ in lock.withLock { closed += 1 } }
            for message in messages {
                continuation.yield(message)
            }
            if endFirst, call == 1 { continuation.finish() }
        }
    }
}

/// One connection that yields only what the test sends.
final class ManualScreenSource: ScreenSource, @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: AsyncThrowingStream<ScreenMessage, Error>.Continuation?

    var isOpen: Bool { lock.withLock { continuation != nil } }

    func send(_ messages: some Sequence<ScreenMessage>) {
        let continuation = lock.withLock { self.continuation }
        for message in messages {
            continuation?.yield(message)
        }
    }

    func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> {
        AsyncThrowingStream { continuation in
            lock.withLock { self.continuation = continuation }
        }
    }
}

@MainActor
func eventually(within limit: Duration = .seconds(5), _ condition: () -> Bool) async throws {
    _ = try await eventually(within: limit) { condition() ? true : nil }
}

@MainActor
func eventually<T>(within limit: Duration = .seconds(5), _ probe: () -> T?) async throws -> T {
    let deadline = ContinuousClock.now + limit
    while ContinuousClock.now < deadline {
        if let value = probe() { return value }
        try await Task.sleep(for: .milliseconds(20))
    }
    return try XCTUnwrap(probe(), "timed out after \(limit)")
}
