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
