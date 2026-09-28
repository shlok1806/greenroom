import Foundation

/// The palette's ranking: cmdk's `command-score.ts`, ported line for line (dip/cmdk at
/// dd2250e, `cmdk/src/command-score.ts`, MIT; see ACKNOWLEDGEMENTS.md). A continuous match
/// scores 1; a match at the start of a word scores more than one inside a word; skipped
/// characters, a case mismatch and an incomplete match each cost a little. 0 means no match.
/// `CommandScoreTests` holds the port to the original's own scores.
enum CommandScore {
    private static let continueMatch = 1.0
    private static let spaceWordJump = 0.9
    private static let nonSpaceWordJump = 0.8
    private static let characterJump = 0.17
    private static let transposition = 0.1
    private static let penaltySkipped = 0.999
    private static let penaltyCaseMismatch = 0.9999
    private static let penaltyNotComplete = 0.99

    private static let gaps: Set<Character> = ["\\", "/", "_", "+", ".", "#", "\"", "@", "[", "(", "{", "&"]

    private static func isGap(_ c: Character?) -> Bool { c.map(gaps.contains) ?? false }
    private static func isSpace(_ c: Character?) -> Bool { c.map { $0.isWhitespace || $0 == "-" } ?? false }

    /// The score of `abbreviation` typed for `string`, whose `aliases` count as part of it.
    static func score(_ string: String, _ abbreviation: String, aliases: [String] = []) -> Double {
        let whole = aliases.isEmpty ? string : string + " " + aliases.joined(separator: " ")
        let s = Array(whole)
        let a = Array(abbreviation)
        var memo: [Int: Double] = [:]
        return inner(s, a, format(s), format(a), 0, 0, &memo)
    }

    /// Lower case, with every space character and hyphen a plain space, so they match each other.
    private static func format(_ text: [Character]) -> [Character] {
        text.map { c in isSpace(c) ? " " : (c.lowercased().first ?? c) }
    }

    private static func inner(_ string: [Character], _ abbreviation: [Character], _ lowerString: [Character],
                              _ lowerAbbreviation: [Character], _ stringIndex: Int, _ abbreviationIndex: Int,
                              _ memo: inout [Int: Double]) -> Double {
        if abbreviationIndex >= abbreviation.count {
            guard abbreviationIndex == abbreviation.count else { return 0 }
            return stringIndex == string.count ? continueMatch : penaltyNotComplete
        }
        let key = stringIndex * (abbreviation.count + 2) + abbreviationIndex
        if let held = memo[key] { return held }

        let wanted = lowerAbbreviation[abbreviationIndex]
        func indexOf(from: Int) -> Int? {
            guard from < lowerString.count else { return nil }
            return lowerString[from...].firstIndex(of: wanted)
        }
        // JavaScript's charAt: nothing outside the string, and nothing equals nothing.
        func at(_ chars: [Character], _ i: Int) -> Character? { i >= 0 && i < chars.count ? chars[i] : nil }

        var high = 0.0
        var index = indexOf(from: stringIndex)
        while let found = index {
            var score = inner(string, abbreviation, lowerString, lowerAbbreviation, found + 1, abbreviationIndex + 1, &memo)
            if score > high {
                if found == stringIndex {
                    score *= continueMatch
                } else if isGap(at(string, found - 1)) {
                    score *= nonSpaceWordJump
                    let breaks = slice(string, stringIndex, found - 1).count(where: gaps.contains)
                    if breaks > 0, stringIndex > 0 { score *= pow(penaltySkipped, Double(breaks)) }
                } else if isSpace(at(string, found - 1)) {
                    score *= spaceWordJump
                    let breaks = slice(string, stringIndex, found - 1).count(where: { isSpace($0) })
                    if breaks > 0, stringIndex > 0 { score *= pow(penaltySkipped, Double(breaks)) }
                } else {
                    score *= characterJump
                    if stringIndex > 0 { score *= pow(penaltySkipped, Double(found - stringIndex)) }
                }
                if at(string, found) != at(abbreviation, abbreviationIndex) { score *= penaltyCaseMismatch }
            }

            let next = at(lowerAbbreviation, abbreviationIndex + 1)
            let before = at(lowerString, found - 1)
            if (score < transposition && before == next) || (next == wanted && before != wanted) {
                let transposed = inner(string, abbreviation, lowerString, lowerAbbreviation, found + 1, abbreviationIndex + 2, &memo)
                if transposed * transposition > score { score = transposed * transposition }
            }
            if score > high { high = score }
            index = indexOf(from: found + 1)
        }
        memo[key] = high
        return high
    }

    /// JavaScript's `slice(start, end)` for indices that are never negative here.
    private static func slice(_ chars: [Character], _ start: Int, _ end: Int) -> ArraySlice<Character> {
        let e = min(max(end, 0), chars.count)
        let s = min(max(start, 0), chars.count)
        return s < e ? chars[s..<e] : []
    }
}
