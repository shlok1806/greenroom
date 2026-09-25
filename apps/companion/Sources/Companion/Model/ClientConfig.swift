import Foundation
import os

/// Where the daemon is and the token it wants (root ADR 0021, decision 4). First match wins:
///
/// 1. `GREENROOM_URL` in the environment, with `GREENROOM_TOKEN` when set.
/// 2. `~/.greenroom/client.json`, `{"url": "...", "token": "..."}`, written by the installer.
/// 3. `http://127.0.0.1:7777` with no token.
///
/// A token only ever travels with the address it came with: `GREENROOM_URL` never picks up
/// the file's token, and `GREENROOM_TOKEN` alone changes nothing. A missing file is normal;
/// a file that cannot be read as that shape is logged and skipped, never fatal.
struct ClientConfig: Equatable, Sendable {
    var baseURL: URL
    var token: String?

    static let loopback = ClientConfig(baseURL: URL(string: "http://127.0.0.1:7777")!, token: nil)

    /// What this process resolved at first use.
    static let current = resolve()

    static func file(home: URL) -> URL {
        home.appending(components: ".greenroom", "client.json")
    }

    static func resolve(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        file: URL = ClientConfig.file(home: FileManager.default.homeDirectoryForCurrentUser)
    ) -> ClientConfig {
        if let raw = environment["GREENROOM_URL"] {
            if let url = daemonURL(raw) {
                return ClientConfig(baseURL: url, token: nonEmpty(environment["GREENROOM_TOKEN"]))
            }
            log.error("GREENROOM_URL is not an http or https address, ignoring it: \(raw, privacy: .public)")
        }
        if let config = read(file) { return config }
        return loopback
    }

    private static let log = Logger(subsystem: "com.greenroom.companion", category: "config")

    private static func read(_ file: URL) -> ClientConfig? {
        let data: Data
        do {
            data = try Data(contentsOf: file)
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            return nil
        } catch {
            log.error("cannot read \(file.path, privacy: .public), using the default daemon: \(error.localizedDescription, privacy: .public)")
            return nil
        }
        struct Shape: Decodable {
            var url: String
            var token: String?
        }
        guard let shape = try? JSONDecoder().decode(Shape.self, from: data), let url = daemonURL(shape.url) else {
            log.error("\(file.path, privacy: .public) is not {\"url\": \"http(s)://...\", \"token\": \"...\"}, using the default daemon")
            return nil
        }
        return ClientConfig(baseURL: url, token: nonEmpty(shape.token))
    }

    private static func daemonURL(_ raw: String) -> URL? {
        guard let url = URL(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              url.host() != nil else { return nil }
        return url
    }

    private static func nonEmpty(_ token: String?) -> String? {
        guard let trimmed = token?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else { return nil }
        return trimmed
    }
}
