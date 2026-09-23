import AVFoundation
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
        let source = FakeScreenSource(try messages())
        let live = LiveScreen(runId: "r", source: source)
        let window = hostedWindow(live)
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

    private func hostedWindow(_ live: LiveScreen) -> NSWindow {
        let window = NSWindow(contentRect: CGRect(x: 0, y: 0, width: 640, height: 480), styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        let view = LiveScreenHostView(displayLayer: live.output.layer)
        view.pixelSize = CGSize(width: 1280, height: 960)
        window.contentView = view
        window.orderBack(nil)
        return window
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
