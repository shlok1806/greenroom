import Foundation

/// Cuts a message into headings, code and prose; SwiftUI's inline Markdown
/// renders inside prose. A full Markdown renderer would be a dependency.
enum RichText {
    /// Not `Identifiable`: text repeats within a message, so views must
    /// iterate blocks by position.
    enum Block: Equatable, Sendable {
        case heading(String, level: Int)
        /// A fenced block or a run of table rows, shown monospaced.
        case code(String)
        case prose(String)
    }

    static func blocks(_ text: String) -> [Block] {
        var out: [Block] = []
        var prose: [String] = []
        var code: [String] = []
        var fenced = false

        func flushProse() {
            let joined = prose.joined(separator: "\n").trimmingCharacters(in: .newlines)
            prose = []
            guard !joined.isEmpty else { return }
            out.append(.prose(joined))
        }

        func flushCode() {
            let joined = code.joined(separator: "\n").trimmingCharacters(in: .newlines)
            code = []
            guard !joined.isEmpty else { return }
            out.append(.code(joined))
        }

        for line in text.components(separatedBy: "\n") {
            if line.trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                // Also closes a table that runs straight into a fence.
                flushCode()
                if !fenced { flushProse() }
                fenced.toggle()
                continue
            }
            if fenced {
                code.append(line)
                continue
            }
            if isTableRow(line) {
                if code.isEmpty { flushProse() }
                code.append(line)
                continue
            }
            if !code.isEmpty { flushCode() }
            if let heading = heading(line) {
                flushProse()
                out.append(heading)
                continue
            }
            prose.append(line)
        }
        flushCode()
        flushProse()
        return out
    }

    private static func isTableRow(_ line: String) -> Bool {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard trimmed.hasPrefix("|"), trimmed.count > 1 else { return false }
        return trimmed.dropFirst().contains("|")
    }

    private static func heading(_ line: String) -> Block? {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard trimmed.hasPrefix("#") else { return nil }
        let hashes = trimmed.prefix { $0 == "#" }.count
        guard hashes <= 6 else { return nil }
        let rest = trimmed.dropFirst(hashes).trimmingCharacters(in: .whitespaces)
        guard !rest.isEmpty else { return nil }
        return .heading(rest, level: hashes)
    }
}
