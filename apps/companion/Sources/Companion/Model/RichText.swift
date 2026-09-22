import Foundation

/// The little of Markdown that a transcript has to honour.
///
/// The verifier and the coding agent both write Markdown: headings, fenced
/// code, tables, `**bold**`. Shown raw it reads as a debug dump, and a full
/// Markdown renderer is a dependency this app will not take (see CLAUDE.md).
/// The middle is this: cut a message into headings, fenced code and prose, and
/// let SwiftUI's own inline Markdown handle what is inside a prose line.
///
/// Splitting is pure, so the rules are tested rather than eyeballed.
enum RichText {
    /// Deliberately not `Identifiable`. A block's only candidate for an id is
    /// its own text, and a message that says the same thing twice ("done." on
    /// either side of a heading, the same fenced command run again) then hands
    /// a list two rows with one id, which makes SwiftUI draw the first one's
    /// content for both. Blocks are positional: a view iterates them by
    /// position and nothing else.
    enum Block: Equatable, Sendable {
        /// A `#`-prefixed line, with the hashes counted and removed.
        case heading(String, level: Int)
        /// The inside of a fenced block, or a run of lines that all look like
        /// a table. Shown monospaced and never reflowed.
        case code(String)
        /// Everything else, kept as written so paragraph breaks survive.
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
                if fenced {
                    flushCode()
                } else {
                    // A table that runs straight into a fence is its own
                    // block. Without this its rows stayed pending and came
                    // back out glued to the front of the fenced code.
                    flushCode()
                    flushProse()
                }
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

    /// A Markdown table is the one block this cannot reflow without destroying
    /// it: its columns are spaces. It goes through as code so the pipes line
    /// up, which is the whole point of writing one.
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
