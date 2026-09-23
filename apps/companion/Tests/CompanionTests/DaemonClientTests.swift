import XCTest

@testable import Companion

final class DaemonClientTests: XCTestCase {
    func testAnErrorBodyGivesTheDaemonsOwnMessage() async {
        let client = StubURLProtocol.client { _ in .json(#"{"error": "verdict is contested"}"#, status: 409) }
        do {
            try await client.send(runId: "r", kind: .accept, text: "ok")
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .status(code: 409, body: "verdict is contested"))
        }
    }

    func testAnErrorBodyThatIsNotJSONIsShownAsText() async {
        let client = StubURLProtocol.client { _ in .json("ffmpeg not found", status: 500) }
        do {
            _ = try await client.recording(runId: "r")
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .status(code: 500, body: "ffmpeg not found"))
        }
    }

    func testAnEmptyErrorBodyStillNamesTheStatus() async {
        let client = StubURLProtocol.client { _ in StubURLProtocol.Reply(status: 404) }
        do {
            _ = try await client.frames("r")
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .status(code: 404, body: ""))
            XCTAssertEqual(error.localizedDescription, "The daemon answered 404")
        }
    }

    func testAnEmptySuccessBodyDecodesAsAnEmptyObject() async throws {
        let client = StubURLProtocol.client { _ in StubURLProtocol.Reply() }
        let answer = try await client.takeControl(runId: "r")
        XCTAssertNil(answer.control)
        XCTAssertNil(answer.screen)
    }

    func testIdsAndNamesAreEscapedAsOneSegment() async throws {
        let seen = Recorder()
        let client = StubURLProtocol.client { request in
            seen.append(request.url?.absoluteString ?? "")
            return StubURLProtocol.Reply(body: Data([0xFF]))
        }
        _ = try await client.artifact(runId: "run 1", name: "a/b%.png")
        _ = try await client.frame(runId: "r?x", file: "#1.jpg")
        XCTAssertEqual(seen.values, [
            "http://daemon.test/api/runs/run%201/artifacts/a%2Fb%25.png",
            "http://daemon.test/api/runs/r%3Fx/frames/%231.jpg",
        ])
    }

    // MARK: - Live screen

    private static let screenHeaders = ["Content-Type": DaemonClient.screenStreamType]

    private static func screenWire() -> Data {
        let hello = #"{"version":"greenroom-input 3","screen":{"width":1024,"height":768},"pixels":{"width":2048,"height":1536}}"#
        let sample = VideoSample(keyframe: true, pts: 42, data: Data([0, 0, 0, 1, 0x65]))
        return SyntheticScreen.message(0x01, Data(hello.utf8))
            + SyntheticScreen.message(0x02, AVCConfigTests.guestRecord)
            + SyntheticScreen.message(0x03, SyntheticScreen.videoPayload(sample))
    }

    func testTheLiveScreenArrivesInPiecesAndComesOutWhole() async throws {
        let seen = Recorder()
        let wire = Self.screenWire()
        let client = StubURLProtocol.client { request in
            seen.append(request.url?.absoluteString ?? "")
            seen.append(request.value(forHTTPHeaderField: "Accept") ?? "")
            return StubURLProtocol.Reply(body: wire, headers: Self.screenHeaders, chunkSize: 7)
        }
        var messages: [ScreenMessage] = []
        for try await message in client.liveScreen(runId: "run 1") {
            messages.append(message)
        }
        var reader = ScreenFrameReader()
        XCTAssertEqual(messages, try reader.append(wire))
        XCTAssertEqual(messages.count, 3)
        XCTAssertEqual(seen.values, ["http://daemon.test/api/runs/run%201/screen/live", DaemonClient.screenStreamType])
    }

    func testALiveScreenRefusalGivesTheDaemonsOwnMessage() async {
        let client = StubURLProtocol.client { _ in .json(#"{"error": "machine is not ready"}"#, status: 409) }
        do {
            for try await _ in client.liveScreen(runId: "r") {}
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .status(code: 409, body: "machine is not ready"))
        }
    }

    func testALiveScreenAnswerOfAnotherTypeIsRefused() async {
        let client = StubURLProtocol.client { _ in
            StubURLProtocol.Reply(body: Data("<html>".utf8), headers: ["Content-Type": "text/html"])
        }
        do {
            for try await _ in client.liveScreen(runId: "r") {}
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .badResponse("text/html instead of \(DaemonClient.screenStreamType)"))
        }
    }

    func testALiveScreenCutMidMessageIsABadResponse() async {
        let wire = Self.screenWire().dropLast()
        let client = StubURLProtocol.client { _ in StubURLProtocol.Reply(body: wire, headers: Self.screenHeaders) }
        do {
            for try await _ in client.liveScreen(runId: "r") {}
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? DaemonError, .badResponse("the stream ended inside a message"))
        }
    }

    func testAnUnreachableDaemonIsNotReachableInTheEventStreamToo() async {
        let client = DaemonClient(baseURL: URL(string: "http://127.0.0.1:1")!)
        do {
            for try await _ in client.events() {}
            XCTFail("expected a failure")
        } catch {
            guard case DaemonError.notReachable = error else {
                return XCTFail("got \(error)")
            }
        }
    }
}

/// A thread-safe list for values seen inside a `@Sendable` stub.
final class Recorder: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String] = []

    func append(_ value: String) { lock.withLock { items.append(value) } }
    var values: [String] { lock.withLock { items } }
}
