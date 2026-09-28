// What `find` matches (daemon ADR 0006): its text as a substring whatever the case, or as a
// regular expression when written /.../, and its role with or without AX's prefix.

import Foundation

enum TextPattern {
    /// The text, to be found anywhere in a field, whatever the case.
    case substring(String)
    case regex(NSRegularExpression)
}

struct PatternError: Error, Equatable {
    let message: String
}

/// Reads `find`'s text. `/pattern/` is a regular expression, case sensitive as written;
/// `/pattern/i` ignores case. Anything else, a lone slash included, is plain text.
func textPattern(_ text: String) throws -> TextPattern {
    guard text.hasPrefix("/"), text.count >= 3 else { return .substring(text) }
    var body = text.dropFirst()
    var options: NSRegularExpression.Options = []
    if body.hasSuffix("/i") {
        body = body.dropLast(2)
        options.insert(.caseInsensitive)
    } else if body.hasSuffix("/") {
        body = body.dropLast()
    } else {
        return .substring(text)
    }
    if body.isEmpty {
        throw PatternError(message: "the /regex/ is empty; put a pattern between the slashes, or pass plain text")
    }
    do {
        return .regex(try NSRegularExpression(pattern: String(body), options: options))
    } catch {
        throw PatternError(message: "/\(body)/ is not a regular expression the guest can use (\(error.localizedDescription)); fix the pattern, or pass plain text")
    }
}

func matches(_ pattern: TextPattern, _ field: String) -> Bool {
    if field.isEmpty { return false }
    switch pattern {
    case let .substring(text):
        return field.range(of: text, options: [.caseInsensitive]) != nil
    case let .regex(regex):
        return regex.firstMatch(in: field, range: NSRange(field.startIndex..., in: field)) != nil
    }
}

/// Whether an element's role is the one asked for: `Button`, `AXButton` and `button` all name
/// AXButton. No role asked for matches every element.
func roleMatches(wanted: String?, role: String) -> Bool {
    guard let wanted = meaningful(wanted)?.trimmingCharacters(in: .whitespaces) else { return true }
    return wireRole(wanted).caseInsensitiveCompare(wireRole(role)) == .orderedSame
}
