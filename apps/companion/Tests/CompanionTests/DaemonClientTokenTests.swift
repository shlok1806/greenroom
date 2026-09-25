import XCTest

@testable import Companion

/// Root ADR 0021: every request carries the token, through the one request factory.
final class DaemonClientTokenTests: XCTestCase {
    private func stubbed(token: String?, seen: Recorder) -> DaemonClient {
        StubURLProtocol.client(token: token) { request in
            let path = request.url?.path() ?? ""
            seen.append("\(request.httpMethod ?? "GET") \(path) \(request.value(forHTTPHeaderField: "Authorization") ?? "none")")
            if path == "/api/events" { return .openStream }
            if path.hasSuffix("/screen/live") {
                return StubURLProtocol.Reply(headers: ["Content-Type": DaemonClient.screenStreamType])
            }
            if path == "/api/runs" { return .json("[]") }
            return StubURLProtocol.Reply()
        }
    }

    /// One of each kind: a decoded read, raw bytes, a POST, a DELETE, the event stream
    /// and the live screen's chunked task.
    private func exercise(_ client: DaemonClient) async throws {
        _ = try await client.runs()
        _ = try await client.artifact(runId: "r", name: "a.png")
        try await client.screenshot(runId: "r")
        try await client.releaseControl(runId: "r")
        for try await item in client.events() {
            if case .opened = item { break }
        }
        for try await _ in client.liveScreen(runId: "r") {}
    }

    func testEveryKindOfRequestCarriesTheToken() async throws {
        let seen = Recorder()
        try await exercise(stubbed(token: "s3cret", seen: seen))
        XCTAssertEqual(seen.values, [
            "GET /api/runs Bearer s3cret",
            "GET /api/runs/r/artifacts/a.png Bearer s3cret",
            "POST /api/runs/r/screenshot Bearer s3cret",
            "DELETE /api/runs/r/control Bearer s3cret",
            "GET /api/events Bearer s3cret",
            "GET /api/runs/r/screen/live Bearer s3cret",
        ])
    }

    func testNoTokenSendsNoAuthorization() async throws {
        let seen = Recorder()
        try await exercise(stubbed(token: nil, seen: seen))
        XCTAssertEqual(seen.values.count, 6)
        XCTAssertTrue(seen.values.allSatisfy { $0.hasSuffix(" none") }, "\(seen.values)")
    }

    /// A client named by hand never borrows the configured token.
    func testAClientNamedByHandHasNoToken() {
        XCTAssertNil(DaemonClient(baseURL: URL(string: "http://elsewhere.test")!).token)
        let config = ClientConfig(baseURL: URL(string: "https://gr.example.com")!, token: "t")
        let client = DaemonClient(config: config)
        XCTAssertEqual(client.baseURL, config.baseURL)
        XCTAssertEqual(client.token, "t")
    }

    /// The daemon's 401 reaches the refusal screen in words the advice recognises.
    func testAnUnauthorizedAnswerGetsItsAdvice() async {
        let client = StubURLProtocol.client { _ in .json("unauthorized\n", status: 401) }
        do {
            _ = try await client.runs()
            XCTFail("expected a failure")
        } catch {
            let words = (error as? LocalizedError)?.errorDescription ?? ""
            XCTAssertEqual(words, "greenroom answered 401: unauthorized")
            XCTAssertTrue(ConnectionState.advice(for: words)?.contains("~/.greenroom/client.json") ?? false)
        }
    }
}
