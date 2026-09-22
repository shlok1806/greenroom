import Foundation
import Synchronization

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

extension DaemonError {
    init(_ error: URLError) {
        self = error.code == .cancelled ? .cancelled : .notReachable(error.localizedDescription)
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
        try await get([RunSummary].self, ["api", "runs"])
    }

    func run(_ runId: String) async throws -> RunDetail {
        try await get(RunDetail.self, runPath(runId))
    }

    func steps(_ runId: String) async throws -> [Step] {
        try await get([Step].self, runPath(runId, "steps"))
    }

    func messages(_ runId: String) async throws -> [Message] {
        let query = [URLQueryItem(name: "after", value: "0")]
        return try await get(MessagePage.self, runPath(runId, "messages"), query: query).messages
    }

    /// Oldest first (ADR 0008).
    func frames(_ runId: String) async throws -> [Frame] {
        try await get([Frame].self, runPath(runId, "frames"))
    }

    func artifact(runId: String, name: String) async throws -> Data {
        try await data(URLRequest(url: url(runPath(runId, "artifacts", name))))
    }

    /// One frame's JPEG bytes.
    func frame(runId: String, file: String) async throws -> Data {
        try await data(URLRequest(url: url(runPath(runId, "frames", file))))
    }

    /// An mp4 of the run's frames. Needs `ffmpeg` on the daemon host; without it
    /// the daemon's own error text is thrown.
    func recording(runId: String) async throws -> Data {
        try await data(URLRequest(url: url(runPath(runId, "recording.mp4"))))
    }

    // MARK: - Writes

    func send(runId: String, kind: MessageKind, text: String, replyTo: Int? = nil) async throws {
        struct Body: Encodable {
            var kind: String
            var text: String
            var replyTo: Int?
        }
        try await post(runPath(runId, "messages"), body: Body(kind: kind.text, text: text, replyTo: replyTo))
    }

    func screenshot(runId: String) async throws {
        try await post(runPath(runId, "screenshot"))
    }

    func destroy(runId: String) async throws {
        try await post(runPath(runId, "destroy"))
    }

    // MARK: - Driving the screen (ADR 0009)

    /// Takes the mouse and keyboard, or renews a lease this app already holds.
    func takeControl(runId: String) async throws -> ControlResponse {
        try decode(ControlResponse.self, from: await post(runPath(runId, "control")))
    }

    func releaseControl(runId: String) async throws {
        var request = URLRequest(url: try url(runPath(runId, "control")))
        request.httpMethod = "DELETE"
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        _ = try await data(request)
    }

    func input(runId: String, actions: [InputAction]) async throws -> InputResult {
        struct Body: Encodable { var actions: [InputAction] }
        return try decode(InputResult.self, from: await post(runPath(runId, "input"), body: Body(actions: actions)))
    }

    // MARK: - Events

    /// `.opened` once the daemon answers 200, then one `.event` per SSE frame.
    /// Ends with the connection; reconnecting is the caller's business.
    func events() -> AsyncThrowingStream<EventStreamItem, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: try url(["api", "events"]))
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    request.timeoutInterval = 3600
                    let (bytes, response) = try await session.bytes(for: request)
                    guard let http = response as? HTTPURLResponse else {
                        throw DaemonError.badResponse("no HTTP response")
                    }
                    guard http.statusCode == 200 else {
                        throw DaemonError.status(code: http.statusCode, body: "")
                    }
                    continuation.yield(.opened)
                    var parser = SSEParser()
                    var splitter = SSELineSplitter()
                    for try await byte in bytes {
                        guard let line = splitter.consume(byte) else { continue }
                        if let event = try parser.consume(line) {
                            continuation.yield(.event(event))
                        }
                    }
                    if let line = splitter.flush(), let event = try parser.consume(line) {
                        continuation.yield(.event(event))
                    }
                    continuation.finish()
                } catch let error as URLError {
                    continuation.finish(throwing: DaemonError(error))
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: - Live screen (ADR 0011)

    static let screenStreamType = "application/x-greenroom-screen"

    /// The machine's screen as it happens: HELLO, then FORMAT and VIDEO. Ends
    /// with the connection; reconnecting is the caller's business.
    func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: try url(runPath(runId, "screen", "live")))
                    request.setValue(Self.screenStreamType, forHTTPHeaderField: "Accept")
                    // A still screen sends nothing; only the socket closing ends this.
                    request.timeoutInterval = 3600
                    var reader = ScreenFrameReader()
                    for try await chunk in chunks(request, contentType: Self.screenStreamType) {
                        for message in try reader.append(chunk) {
                            continuation.yield(message)
                        }
                    }
                    try reader.finish()
                    continuation.finish()
                } catch let error as ScreenStreamError {
                    continuation.finish(throwing: DaemonError.badResponse(error.localizedDescription))
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: - Plumbing

    private struct Empty: Encodable {}

    private func runPath(_ runId: String, _ rest: String...) -> [String] {
        ["api", "runs", runId] + rest
    }

    /// One segment per element, each percent-encoded whole, so an id or file
    /// name with a slash, space or percent sign stays one segment.
    private func url(_ segments: [String], query: [URLQueryItem] = []) throws -> URL {
        let path = segments.reduce(baseURL) { $0.appending(component: $1) }
        var components = URLComponents(url: path, resolvingAgainstBaseURL: false)
        if !query.isEmpty { components?.queryItems = query }
        guard let built = components?.url else {
            throw DaemonError.badResponse("cannot build a URL for \(segments.joined(separator: "/"))")
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
            throw DaemonError(error)
        }
        guard let http = response as? HTTPURLResponse else {
            throw DaemonError.badResponse("no HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw Self.failure(status: http.statusCode, body: data)
        }
        return data
    }

    /// Every failing route answers {"error": "..."}.
    fileprivate static func failure(status: Int, body: Data) -> DaemonError {
        struct Failure: Decodable { var error: String }
        let message = (try? JSONDecoder().decode(Failure.self, from: body))?.error
            ?? String(data: body, encoding: .utf8)
            ?? ""
        return .status(code: status, body: message)
    }

    /// The body of a 200 answer in whatever chunks the network delivers.
    /// `URLSession.AsyncBytes` hands out single bytes, too slow for video.
    private func chunks(_ request: URLRequest, contentType: String) -> AsyncThrowingStream<Data, Error> {
        AsyncThrowingStream { continuation in
            let task = session.dataTask(with: request)
            task.delegate = ChunkDelegate(continuation, contentType: contentType)
            continuation.onTermination = { _ in task.cancel() }
            task.resume()
        }
    }

    /// An empty body decodes as `{}`, so all-optional responses accept one.
    private func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do {
            return try JSONDecoder.daemon().decode(type, from: data.isEmpty ? Data("{}".utf8) : data)
        } catch {
            throw DaemonError.badResponse(String(describing: error))
        }
    }

    private func get<T: Decodable>(_ type: T.Type, _ path: [String], query: [URLQueryItem] = []) async throws -> T {
        var request = URLRequest(url: try url(path, query: query))
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        return try decode(type, from: await data(request))
    }

    @discardableResult
    private func post(_ path: [String], body: some Encodable = Empty()) async throws -> Data {
        var request = URLRequest(url: try url(path))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.httpBody = try JSONEncoder.daemon().encode(body)
        return try await data(request)
    }
}

/// Feeds `DaemonClient.chunks`. A failing answer's body is held back and
/// thrown as the daemon's error text once the request completes.
private final class ChunkDelegate: NSObject, URLSessionDataDelegate, Sendable {
    private let continuation: AsyncThrowingStream<Data, Error>.Continuation
    private let contentType: String
    private let failure = Mutex<(status: Int, body: Data)?>(nil)

    init(_ continuation: AsyncThrowingStream<Data, Error>.Continuation, contentType: String) {
        self.continuation = continuation
        self.contentType = contentType
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void
    ) {
        guard let http = response as? HTTPURLResponse else {
            continuation.finish(throwing: DaemonError.badResponse("no HTTP response"))
            return completionHandler(.cancel)
        }
        if http.statusCode != 200 {
            failure.withLock { $0 = (http.statusCode, Data()) }
        } else if http.mimeType != contentType {
            continuation.finish(throwing: DaemonError.badResponse("\(http.mimeType ?? "no content type") instead of \(contentType)"))
            return completionHandler(.cancel)
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        let failed = failure.withLock { state in
            state?.body.append(data)
            return state != nil
        }
        if !failed { continuation.yield(data) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: (any Error)?) {
        if let error {
            continuation.finish(throwing: (error as? URLError).map(DaemonError.init) ?? error)
        } else if let failed = failure.withLock({ $0 }) {
            continuation.finish(throwing: DaemonClient.failure(status: failed.status, body: failed.body))
        } else {
            continuation.finish()
        }
    }
}
