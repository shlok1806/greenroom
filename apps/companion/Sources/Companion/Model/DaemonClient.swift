import Foundation

/// What went wrong talking to the daemon, in words a window can show.
enum DaemonError: Error, LocalizedError, Equatable {
    case notReachable(String)
    case status(code: Int, body: String)
    case badResponse(String)
    /// The request was cancelled by the app itself, not refused by the daemon.
    /// SwiftUI cancels a `.task(id:)` every time its id changes, so the Screen
    /// tab cancels the previous frame's download on every new frame. That is
    /// ordinary, and it must never reach the window as a failure.
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

/// The only thing in the app that speaks HTTP. Everything the companion can do
/// is a method here, and every method is a route in ADR 0007. The app never
/// touches tart, ssh or a run directory.
final class DaemonClient: Sendable {
    let baseURL: URL
    private let session: URLSession

    /// The daemon's address. `GREENROOM_URL` in the environment overrides
    /// the default, for a daemon on another port or, later, another host.
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
        try await get([RunSummary].self, path: "api/runs")
    }

    func run(_ runId: String) async throws -> RunDetail {
        try await get(RunDetail.self, path: "api/runs/\(escape(runId))")
    }

    func steps(_ runId: String) async throws -> [Step] {
        try await get([Step].self, path: "api/runs/\(escape(runId))/steps")
    }

    func messages(_ runId: String, after: Int = 0) async throws -> MessagePage {
        try await get(
            MessagePage.self,
            path: "api/runs/\(escape(runId))/messages",
            query: [URLQueryItem(name: "after", value: String(after))]
        )
    }

    func artifact(runId: String, name: String) async throws -> Data {
        let request = URLRequest(url: try url(path: "api/runs/\(escape(runId))/artifacts/\(escape(name))"))
        let (data, response) = try await perform(request)
        try check(response, data: data)
        return data
    }

    /// The run's recorded frames (ADR 0008), oldest first.
    func frames(_ runId: String) async throws -> [Frame] {
        try await get([Frame].self, path: "api/runs/\(escape(runId))/frames")
    }

    /// One frame's JPEG bytes.
    func frame(runId: String, file: String) async throws -> Data {
        let request = URLRequest(url: try url(path: "api/runs/\(escape(runId))/frames/\(escape(file))"))
        let (data, response) = try await perform(request)
        try check(response, data: data)
        return data
    }

    /// The run's recording as an mp4, built from its frames. Throws with the
    /// server's own text (`{"error": "..."}`, e.g. "ffmpeg not found") when
    /// none exists.
    func recording(runId: String) async throws -> Data {
        let request = URLRequest(url: try url(path: "api/runs/\(escape(runId))/recording.mp4"))
        let (data, response) = try await perform(request)
        try check(response, data: data)
        return data
    }

    // MARK: - Writes

    @discardableResult
    func send(runId: String, kind: MessageKind, text: String, replyTo: Int? = nil) async throws -> SentMessage {
        struct Body: Encodable {
            var kind: String
            var text: String
            var replyTo: Int?
        }
        return try await post(
            SentMessage.self,
            path: "api/runs/\(escape(runId))/messages",
            body: Body(kind: kind.text, text: text, replyTo: replyTo)
        )
    }

    @discardableResult
    func screenshot(runId: String) async throws -> ScreenshotResult {
        try await post(ScreenshotResult.self, path: "api/runs/\(escape(runId))/screenshot", body: Empty())
    }

    func destroy(runId: String) async throws {
        struct OK: Decodable { var ok: Bool? }
        _ = try await post(OK.self, path: "api/runs/\(escape(runId))/destroy", body: Empty())
    }

    // MARK: - Events

    /// The daemon's event stream, one `ServerEvent` per SSE frame. The stream
    /// ends when the connection does; reconnecting is the caller's business.
    func events(runId: String?) -> AsyncThrowingStream<ServerEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    let query = runId.map { [URLQueryItem(name: "runId", value: $0)] } ?? []
                    var request = URLRequest(url: try url(path: "api/events", query: query))
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
                    // Deliberately not `bytes.lines`: that sequence drops the
                    // blank line, and a blank line is exactly what ends an SSE
                    // frame. See SSELineSplitter.
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

    private func url(path: String, query: [URLQueryItem] = []) throws -> URL {
        guard var components = URLComponents(url: baseURL.appending(path: path), resolvingAgainstBaseURL: false) else {
            throw DaemonError.badResponse("cannot build a URL for \(path)")
        }
        if !query.isEmpty { components.queryItems = query }
        guard let built = components.url else {
            throw DaemonError.badResponse("cannot build a URL for \(path)")
        }
        return built
    }

    private func perform(_ request: URLRequest) async throws -> (Data, URLResponse) {
        do {
            return try await session.data(for: request)
        } catch let error as URLError {
            if error.code == .cancelled { throw DaemonError.cancelled }
            throw DaemonError.notReachable(error.localizedDescription)
        }
    }

    private func check(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else {
            throw DaemonError.badResponse("no HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            // Every failing route answers {"error": "..."}; show that rather
            // than the raw body.
            struct Failure: Decodable { var error: String }
            let message = (try? JSONDecoder().decode(Failure.self, from: data))?.error
                ?? String(data: data, encoding: .utf8)
                ?? ""
            throw DaemonError.status(code: http.statusCode, body: message)
        }
    }

    private func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do {
            return try JSONDecoder.daemon().decode(type, from: data)
        } catch {
            throw DaemonError.badResponse(String(describing: error))
        }
    }

    private func get<T: Decodable>(_ type: T.Type, path: String, query: [URLQueryItem] = []) async throws -> T {
        var request = URLRequest(url: try url(path: path, query: query))
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let (data, response) = try await perform(request)
        try check(response, data: data)
        return try decode(type, from: data)
    }

    private func post<T: Decodable, B: Encodable>(_ type: T.Type, path: String, body: B) async throws -> T {
        var request = URLRequest(url: try url(path: path))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.httpBody = try JSONEncoder.daemon().encode(body)
        let (data, response) = try await perform(request)
        try check(response, data: data)
        if data.isEmpty, let empty = try? JSONDecoder.daemon().decode(type, from: Data("{}".utf8)) {
            return empty
        }
        return try decode(type, from: data)
    }
}

// MARK: - SSE

/// Cuts a byte stream into lines, **keeping the empty ones**.
///
/// This exists because `URLSession.AsyncBytes.lines` does not: it skips blank
/// lines, and in server-sent events a blank line is not filler, it is the
/// terminator that ends a frame. Fed through `.lines`, the daemon's stream
/// arrives as an endless run of `event:`/`data:` lines that never dispatch, so
/// no event ever reaches the store. Splitting the bytes here is a value type
/// with no I/O in it, so the tests can feed it a fixed transcript.
struct SSELineSplitter {
    private var buffer: [UInt8] = []

    init() {}

    /// Feeds one byte. Returns a line when the byte closed one; a trailing
    /// `\r` is left on for `SSEParser.consume` to strip.
    mutating func consume(_ byte: UInt8) -> String? {
        guard byte == 0x0A else {
            buffer.append(byte)
            return nil
        }
        let line = String(decoding: buffer, as: UTF8.self)
        buffer.removeAll(keepingCapacity: true)
        return line
    }

    /// Whatever is left when the stream ends without a final newline.
    mutating func flush() -> String? {
        guard !buffer.isEmpty else { return nil }
        let line = String(decoding: buffer, as: UTF8.self)
        buffer.removeAll(keepingCapacity: true)
        return line
    }

    /// Splits a whole body. Used by the tests and by anything that already
    /// holds the bytes.
    static func lines(of bytes: [UInt8]) -> [String] {
        var splitter = SSELineSplitter()
        var lines: [String] = []
        for byte in bytes {
            if let line = splitter.consume(byte) { lines.append(line) }
        }
        if let line = splitter.flush() { lines.append(line) }
        return lines
    }
}

/// A line-at-a-time server-sent events parser. It is a value type with no I/O
/// in it so the tests can feed it a fixed transcript.
struct SSEParser {
    private var eventName: String?
    private var data: [String] = []

    init() {}

    /// Feeds one line. Returns an event when the line closed a frame.
    mutating func consume(_ rawLine: String) throws -> ServerEvent? {
        var line = rawLine
        if line.hasSuffix("\r") { line.removeLast() }

        if line.isEmpty { return try dispatch() }
        if line.hasPrefix(":") { return nil } // a heartbeat or another comment

        let field: String
        var value: String
        if let colon = line.firstIndex(of: ":") {
            field = String(line[line.startIndex..<colon])
            value = String(line[line.index(after: colon)...])
            if value.hasPrefix(" ") { value.removeFirst() }
        } else {
            field = line
            value = ""
        }

        switch field {
        case "event": eventName = value
        case "data": data.append(value)
        default: break // id and retry mean nothing here
        }
        return nil
    }

    private mutating func dispatch() throws -> ServerEvent? {
        defer {
            eventName = nil
            data = []
        }
        guard !data.isEmpty else { return nil }
        let payload = Data(data.joined(separator: "\n").utf8)
        let decoder = JSONDecoder.daemon()
        do {
            switch eventName {
            case "run":
                return .run(try decoder.decode(LifecycleEvent.self, from: payload))
            case "step":
                let event = try decoder.decode(StepEvent.self, from: payload)
                return .step(runId: event.runId, seq: event.seq, step: event.step)
            case "message":
                let event = try decoder.decode(MessageEvent.self, from: payload)
                return .message(runId: event.runId, message: event.message)
            case "frame":
                let event = try decoder.decode(FrameEvent.self, from: payload)
                return .frame(runId: event.runId, frame: event.frame)
            default:
                return nil // an event kind this app does not know yet
            }
        } catch {
            throw DaemonError.badResponse("bad \(eventName ?? "unnamed") event: \(error)")
        }
    }

    /// Parses a whole SSE body. Used by the tests and by anything that already
    /// holds the bytes.
    static func parse(_ text: String) throws -> [ServerEvent] {
        var parser = SSEParser()
        var events: [ServerEvent] = []
        for line in text.components(separatedBy: "\n") {
            if let event = try parser.consume(line) { events.append(event) }
        }
        if let event = try parser.consume("") { events.append(event) }
        return events
    }
}
