import Foundation

enum DaemonError: Error, LocalizedError, Equatable {
    case notReachable(String)
    case status(code: Int, body: String)
    case badResponse(String)
    /// The app cancelled the request itself (e.g. a `.task(id:)` whose id moved
    /// on). Never shown as a failure.
    case cancelled

    var errorDescription: String? {
        switch self {
        case .cancelled:
            return nil
        case .notReachable(let detail):
            return "The daemon is not answering: \(detail)"
        case .status(let code, let body):
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? "The daemon answered \(code)" : "The daemon answered \(code): \(trimmed)"
        case .badResponse(let detail):
            return "The daemon sent something unexpected: \(detail)"
        }
    }
}

/// The only thing in the app that speaks HTTP: one method per route in ADR 0007.
final class DaemonClient: Sendable {
    let baseURL: URL
    private let session: URLSession

    /// `GREENROOM_URL` overrides the default address.
    static let defaultBaseURL: URL = {
        if let raw = ProcessInfo.processInfo.environment["GREENROOM_URL"], let url = URL(string: raw) {
            return url
        }
        return URL(string: "http://127.0.0.1:7777")!
    }()

    init(baseURL: URL = DaemonClient.defaultBaseURL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    // MARK: - Reads

    func runs() async throws -> [RunSummary] {
        try await get([RunSummary].self, "api/runs")
    }

    func run(_ runId: String) async throws -> RunDetail {
        try await get(RunDetail.self, "api/runs/\(escape(runId))")
    }

    func steps(_ runId: String) async throws -> [Step] {
        try await get([Step].self, "api/runs/\(escape(runId))/steps")
    }

    func messages(_ runId: String) async throws -> [Message] {
        let query = [URLQueryItem(name: "after", value: "0")]
        return try await get(MessagePage.self, "api/runs/\(escape(runId))/messages", query: query).messages
    }

    /// Oldest first (ADR 0008).
    func frames(_ runId: String) async throws -> [Frame] {
        try await get([Frame].self, "api/runs/\(escape(runId))/frames")
    }

    func artifact(runId: String, name: String) async throws -> Data {
        try await data(URLRequest(url: url("api/runs/\(escape(runId))/artifacts/\(escape(name))")))
    }

    /// One frame's JPEG bytes.
    func frame(runId: String, file: String) async throws -> Data {
        try await data(URLRequest(url: url("api/runs/\(escape(runId))/frames/\(escape(file))")))
    }

    /// An mp4 of the run's frames. Needs `ffmpeg` on the daemon host; without it
    /// the daemon's own error text is thrown.
    func recording(runId: String) async throws -> Data {
        try await data(URLRequest(url: url("api/runs/\(escape(runId))/recording.mp4")))
    }

    // MARK: - Writes

    func send(runId: String, kind: MessageKind, text: String, replyTo: Int? = nil) async throws {
        struct Body: Encodable {
            var kind: String
            var text: String
            var replyTo: Int?
        }
        try await post("api/runs/\(escape(runId))/messages", body: Body(kind: kind.text, text: text, replyTo: replyTo))
    }

    func screenshot(runId: String) async throws {
        try await post("api/runs/\(escape(runId))/screenshot")
    }

    func destroy(runId: String) async throws {
        try await post("api/runs/\(escape(runId))/destroy")
    }

    // MARK: - Driving the screen (ADR 0009)

    /// Takes the mouse and keyboard, or renews a lease this app already holds.
    func takeControl(runId: String) async throws -> ControlResponse {
        try decode(ControlResponse.self, from: await post("api/runs/\(escape(runId))/control"))
    }

    func releaseControl(runId: String) async throws {
        var request = URLRequest(url: try url("api/runs/\(escape(runId))/control"))
        request.httpMethod = "DELETE"
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        _ = try await data(request)
    }

    func input(runId: String, actions: [InputAction]) async throws -> InputResult {
        struct Body: Encodable { var actions: [InputAction] }
        return try decode(InputResult.self, from: await post("api/runs/\(escape(runId))/input", body: Body(actions: actions)))
    }

    // MARK: - Events

    /// One `ServerEvent` per SSE frame. Ends with the connection; reconnecting
    /// is the caller's business.
    func events() -> AsyncThrowingStream<ServerEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: try url("api/events"))
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    request.timeoutInterval = 3600
                    let (bytes, response) = try await session.bytes(for: request)
                    guard let http = response as? HTTPURLResponse else {
                        throw DaemonError.badResponse("no HTTP response")
                    }
                    guard http.statusCode == 200 else {
                        throw DaemonError.status(code: http.statusCode, body: "")
                    }
                    var parser = SSEParser()
                    var splitter = SSELineSplitter()
                    for try await byte in bytes {
                        guard let line = splitter.consume(byte) else { continue }
                        if let event = try parser.consume(line) {
                            continuation.yield(event)
                        }
                    }
                    if let line = splitter.flush(), let event = try parser.consume(line) {
                        continuation.yield(event)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: - Plumbing

    private struct Empty: Encodable {}

    private func escape(_ component: String) -> String {
        component.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? component
    }

    private func url(_ path: String, query: [URLQueryItem] = []) throws -> URL {
        var components = URLComponents(url: baseURL.appending(path: path), resolvingAgainstBaseURL: false)
        if !query.isEmpty { components?.queryItems = query }
        guard let built = components?.url else {
            throw DaemonError.badResponse("cannot build a URL for \(path)")
        }
        return built
    }

    /// Sends `request` and returns the body of a 2xx answer.
    private func data(_ request: URLRequest) async throws -> Data {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch let error as URLError {
            if error.code == .cancelled { throw DaemonError.cancelled }
            throw DaemonError.notReachable(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else {
            throw DaemonError.badResponse("no HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            // Every failing route answers {"error": "..."}.
            struct Failure: Decodable { var error: String }
            let message = (try? JSONDecoder().decode(Failure.self, from: data))?.error
                ?? String(data: data, encoding: .utf8)
                ?? ""
            throw DaemonError.status(code: http.statusCode, body: message)
        }
        return data
    }

    /// An empty body decodes as `{}`, so all-optional responses accept one.
    private func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do {
            return try JSONDecoder.daemon().decode(type, from: data.isEmpty ? Data("{}".utf8) : data)
        } catch {
            throw DaemonError.badResponse(String(describing: error))
        }
    }

    private func get<T: Decodable>(_ type: T.Type, _ path: String, query: [URLQueryItem] = []) async throws -> T {
        var request = URLRequest(url: try url(path, query: query))
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        return try decode(type, from: await data(request))
    }

    @discardableResult
    private func post(_ path: String, body: some Encodable = Empty()) async throws -> Data {
        var request = URLRequest(url: try url(path))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.httpBody = try JSONEncoder.daemon().encode(body)
        return try await data(request)
    }
}
