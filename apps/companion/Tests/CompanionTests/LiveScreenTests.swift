import CoreVideo
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

    /// The app decodes the stream itself (root ADR 0045), so no window or awake display is
    /// needed to see what it would draw.
    func testAStreamDecodesAtItsPixelSizeAndStatesItsShape() async throws {
        let source = FakeScreenSource(try messages())
        var shapes: [ScreenShape] = []
        let live = LiveScreen(runId: "r", source: source) { shapes.append($0) }
        live.start()

        try await eventually { live.phase == .playing }
        XCTAssertEqual(live.pixelSize, CGSize(width: 1280, height: 960))
        let decoded = try await eventuallyAsync { await live.output.decodedSize() }
        XCTAssertEqual(decoded, CGSize(width: 1280, height: 960))
        // SyntheticScreen's HELLO is a 2x guest: 640x480 points.
        XCTAssertEqual(shapes, [ScreenShape(pixels: CGSize(width: 1280, height: 960), points: CGSize(width: 640, height: 480))])

        // The newest frame, as the glyph moments read it: the second colour.
        let still = try await eventuallyAsync { () -> CGImage? in
            guard let image = await live.output.still(), Self.isBlue(image) else { return nil }
            return image
        }
        XCTAssertEqual(still.width, 1280)
        live.stop()
        let gone = await live.output.still()
        XCTAssertNil(gone, "stopping removes the picture")
    }

    func testAnUndecodableFrameFailsTheConnection() async throws {
        let all = try messages()
        let garbled = all.map { message -> ScreenMessage in
            guard case .video(var sample) = message else { return message }
            sample.data = Data(repeating: 0xAB, count: sample.data.count)
            return .video(sample)
        }
        let firstVideo = try XCTUnwrap(garbled.firstIndex { if case .video = $0 { true } else { false } })
        let source = ManualScreenSource()
        let live = LiveScreen(runId: "r", source: source)
        live.start()
        try await eventually { source.isOpen }
        source.send(garbled[...firstVideo])
        try await eventually { live.phase == .playing }
        // The failure is noticed by the sample after it, as on a moving screen.
        try await Task.sleep(for: .milliseconds(300))
        source.send(garbled[(firstVideo + 1)...])
        try await eventually {
            if case .failed(let words) = live.phase { return words == "The picture could not be decoded." }
            return false
        }
        live.stop()
    }

    /// The layer sits on the rectangle `ScreenGeometry` maps clicks against, and asks for a
    /// drawable of exactly its size in display pixels.
    func testTheLayerCoversTheFittedRectangle() async throws {
        let live = LiveScreen(runId: "r", source: FakeScreenSource([]))
        let view = LiveScreenHostView(output: live.output)
        view.frame = CGRect(x: 0, y: 0, width: 801, height: 400)
        view.pixelSize = CGSize(width: 2048, height: 1536)
        view.layoutSubtreeIfNeeded()

        let fitted = ScreenGeometry.fitted(image: view.pixelSize, in: view.bounds.size)
        XCTAssertEqual(fitted, CGRect(x: 134, y: 0, width: 533 + 1.0 / 3, height: 400))
        XCTAssertEqual(view.convertFromLayer(live.output.layer.frame), fitted)
        XCTAssertEqual(live.output.layer.contentsScale, view.backingScale)
    }

    /// Hosted in a window, the layer's edges fall on whole display pixels, so the drawable maps
    /// one to one even when SwiftUI centres the picture on a half point.
    func testInAWindowTheLayerSitsOnWholePixels() throws {
        let live = LiveScreen(runId: "r", source: FakeScreenSource([]))
        let window = NSWindow(contentRect: CGRect(x: -10_000, y: -10_000, width: 1100, height: 800),
                              styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let container = NSView(frame: CGRect(x: 0, y: 0, width: 1100, height: 800))
        window.contentView = container
        let view = LiveScreenHostView(output: live.output)
        view.frame = CGRect(x: 38.5, y: 16.25, width: 1024, height: 768)
        container.addSubview(view)
        view.pixelSize = CGSize(width: 1024, height: 768)
        view.layoutSubtreeIfNeeded()

        let scale = window.backingScaleFactor
        let inWindow = view.convert(view.pictureRect, to: nil)
        for edge in [inWindow.minX, inWindow.minY, inWindow.maxX, inWindow.maxY] {
            XCTAssertEqual((edge * scale).rounded(), edge * scale, "an edge at \(edge) points is between display pixels")
        }
        XCTAssertEqual(view.pictureRect.size, CGSize(width: 1024, height: 768))
    }

    private static func isBlue(_ image: CGImage) -> Bool {
        guard let data = image.dataProvider?.data, let bytes = CFDataGetBytePtr(data) else { return false }
        let row = image.bytesPerRow, step = image.bitsPerPixel / 8
        let at = (image.height / 2) * row + (image.width / 2) * step
        // BGRA from the decoder: blue first.
        return bytes[at] > 150 && bytes[at + 1] < 120 && bytes[at + 2] < 120
    }

    private func eventuallyAsync<T>(within limit: Duration = .seconds(5), _ read: () async -> T?) async throws -> T {
        let deadline = ContinuousClock.now + limit
        while ContinuousClock.now < deadline {
            if let value = await read() { return value }
            try await Task.sleep(for: .milliseconds(20))
        }
        let last = await read()
        return try XCTUnwrap(last, "timed out after \(limit)")
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
