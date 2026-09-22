import Foundation

@testable import Companion

/// Answers a `DaemonClient`'s requests in-process. Each session gets its own
/// handler, keyed by a header, so tests never share replies.
final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    struct Reply: Sendable {
        var status = 200
        var body = Data()

        static func json(_ text: String, status: Int = 200) -> Reply {
            Reply(status: status, body: Data(text.utf8))
        }
    }

    typealias Handler = @Sendable (URLRequest) -> Reply

    private static let header = "X-Stub-Session"
    private static let lock = NSLock()
    nonisolated(unsafe) private static var handlers: [String: Handler] = [:]

    /// A client whose every request is answered by `handler`.
    static func client(_ handler: @escaping Handler) -> DaemonClient {
        let id = UUID().uuidString
        lock.withLock { handlers[id] = handler }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        config.httpAdditionalHeaders = [header: id]
        return DaemonClient(baseURL: URL(string: "http://daemon.test")!, session: URLSession(configuration: config))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let id = request.value(forHTTPHeaderField: Self.header) ?? ""
        let handler = Self.lock.withLock { Self.handlers[id] }
        let reply = handler?(request) ?? Reply(status: 599)
        let response = HTTPURLResponse(url: request.url!, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: reply.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
