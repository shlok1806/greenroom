import Foundation

@testable import Companion

/// Answers a `DaemonClient`'s requests in-process. Each session gets its own
/// handler, keyed by a header, so tests never share replies.
final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    struct Reply: Sendable {
        var status = 200
        var body = Data()
        var headers: [String: String]?
        /// Delivers the body in pieces of this many bytes, as a network would.
        var chunkSize: Int?
        /// Never finishes the body, as an event stream stays open.
        var holdsOpen = false
        /// Fails the request the way a closed port does, with no answer at all.
        var failure: URLError.Code?

        static func json(_ text: String, status: Int = 200) -> Reply {
            Reply(status: status, body: Data(text.utf8))
        }

        /// An open event stream that has sent one ping.
        static let openStream = Reply(body: Data(": ping\n\n".utf8), headers: ["Content-Type": "text/event-stream"], holdsOpen: true)

        static let unreachable = Reply(failure: .cannotConnectToHost)
    }

    typealias Handler = @Sendable (URLRequest) -> Reply

    private static let header = "X-Stub-Session"
    private static let lock = NSLock()
    nonisolated(unsafe) private static var handlers: [String: Handler] = [:]

    /// A client whose every request is answered by `handler`.
    static func client(token: String? = nil, _ handler: @escaping Handler) -> DaemonClient {
        let id = UUID().uuidString
        lock.withLock { handlers[id] = handler }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        config.httpAdditionalHeaders = [header: id]
        return DaemonClient(baseURL: URL(string: "http://daemon.test")!, token: token, session: URLSession(configuration: config))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let id = request.value(forHTTPHeaderField: Self.header) ?? ""
        let handler = Self.lock.withLock { Self.handlers[id] }
        let reply = handler?(request) ?? Reply(status: 599)
        if let failure = reply.failure {
            client?.urlProtocol(self, didFailWithError: URLError(failure))
            return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: reply.headers)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        let size = max(reply.chunkSize ?? reply.body.count, 1)
        for start in stride(from: reply.body.startIndex, to: reply.body.endIndex, by: size) {
            client?.urlProtocol(self, didLoad: reply.body[start ..< min(start + size, reply.body.endIndex)])
        }
        if !reply.holdsOpen { client?.urlProtocolDidFinishLoading(self) }
    }

    override func stopLoading() {}
}
