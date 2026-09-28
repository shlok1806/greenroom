// The decisions of `waitFor` and `expect` (daemon ADR 0006; docs/21 section 5.5): their timeouts
// and poll schedules, the text, count and flag comparisons, when a wait's state holds and what an
// expectation observed. Pure: the guest reads the observations (Waits.swift), the host tests run
// recorded ones.

import Foundation

/// A wait's timeout unless the request says, and the cap of both waits and expectations (under
/// the 45 s of a request, daemon ADR 0006 point 10).
let defaultWaitTimeoutMs = 10_000
let defaultExpectTimeoutMs = 2000
let maxWaitTimeoutMs = 40_000

func waitTimeoutMs(_ requested: Int?, default fallback: Int) -> Int {
    guard let requested, requested > 0 else { return fallback }
    return min(requested, maxWaitTimeoutMs)
}

/// How often a wait looks again when no notification wakes it: SwiftUI drops some.
let waitPollMs = 150

/// The waits between an expectation's polls: Playwright's 100, 250, 500 and 1000 ms, then every
/// second.
let expectPollsMs = [100, 250, 500, 1000]

/// The wait after poll `attempt` (0 is the first poll, made at once).
func expectPollWait(after attempt: Int) -> Int {
    attempt >= 0 && attempt < expectPollsMs.count ? expectPollsMs[attempt] : 1000
}

// MARK: - Comparisons

/// A text comparison: `equals` the whole text, `contains` a part of it, `matches` a regular
/// expression anywhere in it. Case counts in all three, as it does in what a person reads.
struct TextMatcher {
    enum Op: String {
        case equals, contains, matches
    }

    let op: Op
    let expected: String
    private let regex: NSRegularExpression?

    /// Throws a message a model can fix the call from: an unknown op, or a pattern that does not
    /// compile.
    init(op name: String?, expected: String) throws {
        let spelled = name ?? Op.equals.rawValue
        guard let op = Op(rawValue: spelled) else {
            throw PatternError(message: "op \(spelled) does not compare text; use equals, contains or matches")
        }
        self.op = op
        self.expected = expected
        if op == .matches {
            do {
                regex = try NSRegularExpression(pattern: expected)
            } catch {
                throw PatternError(message: "\"\(expected)\" is not a regular expression the guest can use (\(error.localizedDescription)); fix the pattern, or compare with equals or contains")
            }
        } else {
            regex = nil
        }
    }

    func matches(_ text: String) -> Bool {
        switch op {
        case .equals: return text == expected
        case .contains: return expected.isEmpty || text.contains(expected)
        case .matches:
            guard let regex else { return false }
            return regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil
        }
    }
}

/// A count comparison.
func countMatches(op: String, expected: Int, observed: Int) -> Bool? {
    switch op {
    case "equals": return observed == expected
    case "atLeast": return observed >= expected
    case "atMost": return observed <= expected
    default: return nil
    }
}

// MARK: - waitFor

enum WaitState: String, CaseIterable {
    case appears, disappears, enabled, disabled, focused, changes, value
}

/// What one poll of a wait saw of its target. For an element target the element's own facts; for
/// an app or a window, whether it is there and whether it is in front; `tree` is the signature
/// that `changes` of an app compares.
struct WaitObservation: Equatable {
    /// The target is there at all (the element, the window, the running app).
    var exists = false
    /// Something of it shows on screen.
    var shows = false
    var enabled: Bool?
    var focused: Bool?
    var name: String?
    /// The value, as a secure field says it: `secretText` of its length.
    var value: String?
    var tree: Signature?
}

/// Whether a wait's state holds, given this poll and the first one (for `changes`). `value` needs
/// its matcher; without one it never holds.
func waitSatisfied(_ state: WaitState, now: WaitObservation, first: WaitObservation, matcher: TextMatcher?) -> Bool {
    switch state {
    case .appears: return now.exists && now.shows
    case .disappears: return !now.exists || !now.shows
    case .enabled: return now.exists && now.enabled != false
    case .disabled: return now.exists && now.enabled == false
    case .focused: return now.exists && now.focused == true
    case .changes: return now != first
    case .value:
        guard now.exists, let value = now.value, let matcher else { return false }
        return matcher.matches(value)
    }
}

// MARK: - expect

enum ExpectProperty: String, CaseIterable {
    case value, name, exists, visible, enabled, selected, count

    /// The ops a property takes, the first its default.
    var ops: [String] {
        switch self {
        case .value, .name: return ["equals", "contains", "matches"]
        case .count: return ["equals", "atLeast", "atMost"]
        default: return ["equals"]
        }
    }
}

/// What an expectation compares against: text, a flag or a count, as the request's `expected`
/// is a string, a bool or a number.
enum Expected: Equatable {
    case text(String)
    case flag(Bool)
    case count(Int)
}

/// What one poll of an expectation saw. `count` is how many elements matched a text target (1
/// or 0 for a ref).
struct ExpectObservation: Equatable {
    var exists = false
    /// It has a visible rect and nothing covers it.
    var visible = false
    var enabled: Bool?
    var selected: Bool?
    var name: String?
    var value: String?
    var count = 0
    var secret = false
}

/// Checks an expectation's arguments before any poll: the op fits the property, and `expected`
/// is of the property's kind (a missing flag is true). Throws a message naming what to pass.
func expectedFor(_ property: ExpectProperty, op: String, expected: Expected?) throws -> Expected {
    guard property.ops.contains(op) else {
        throw PatternError(message: "op \(op) does not apply to \(property.rawValue); use \(property.ops.joined(separator: ", "))")
    }
    switch (property, expected) {
    case (.value, .text?), (.name, .text?):
        return expected ?? .text("")
    case (.value, _), (.name, _):
        throw PatternError(message: "\(property.rawValue) compares text; pass expected as a string")
    case (.count, .count(let n)?) where n >= 0:
        return .count(n)
    case (.count, _):
        throw PatternError(message: "count compares a whole number of elements; pass expected as one, such as 3")
    case (_, nil):
        return .flag(true)
    case (_, .flag?):
        return expected ?? .flag(true)
    default:
        throw PatternError(message: "\(property.rawValue) is true or false; pass expected as a bool, or leave it out for true")
    }
}

/// Whether an observation passes. `op` is the count's comparison; text compares through
/// `matcher`. A secure field's value never passes: it is never read.
func expectPasses(_ property: ExpectProperty, op: String, expected: Expected, matcher: TextMatcher?, _ seen: ExpectObservation) -> Bool {
    switch (property, expected) {
    case (.value, .text):
        guard seen.exists, !seen.secret, let value = seen.value, let matcher else { return false }
        return matcher.matches(value)
    case (.name, .text):
        guard seen.exists, let name = seen.name, let matcher else { return false }
        return matcher.matches(name)
    case let (.exists, .flag(want)):
        return seen.exists == want
    case let (.visible, .flag(want)):
        return (seen.exists && seen.visible) == want
    case let (.enabled, .flag(want)):
        return seen.exists && (seen.enabled != false) == want
    case let (.selected, .flag(want)):
        return seen.exists && (seen.selected == true) == want
    case let (.count, .count(want)):
        return countMatches(op: op, expected: want, observed: seen.count) ?? false
    default:
        return false
    }
}

/// What an expectation observed, exactly as recorded: text, a flag, a count, or null when the
/// target was not there to read. A secure field's value is only its length.
func observedValue(_ property: ExpectProperty, _ seen: ExpectObservation) -> Any {
    switch property {
    case .value:
        guard seen.exists else { return NSNull() }
        if seen.secret { return secretText(seen.value?.count ?? 0) }
        return seen.value.map { $0 as Any } ?? NSNull()
    case .name:
        guard seen.exists else { return NSNull() }
        return seen.name.map { $0 as Any } ?? NSNull()
    case .exists: return seen.exists
    case .visible: return seen.exists && seen.visible
    case .enabled:
        guard seen.exists else { return NSNull() }
        return seen.enabled != false
    case .selected:
        guard seen.exists else { return NSNull() }
        return seen.selected == true
    case .count: return seen.count
    }
}
