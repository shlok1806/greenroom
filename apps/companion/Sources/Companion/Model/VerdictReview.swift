import Foundation

/// Who decided a verdict, when, and whether a person has looked at it: the headline of
/// the verdict card, so a reviewer never mistakes an agent agreeing with itself for a
/// human sign-off. Pure; `VerdictReviewTests`.
struct VerdictReview: Equatable, Sendable {
    /// "Proposed", "Accepted", "Contested", "Rejected".
    var state: String
    /// "awaiting review", "by you at 20:12", "by the coding agent at 20:12".
    var decision: String
    /// True once a person accepted or rejected it.
    var humanReviewed: Bool
    /// The line under the headline that says what this means for the reviewer.
    var note: String?
    /// Whether this verdict is the final word.
    var closed: Bool

    static func of(_ verdict: VerdictState, messages: [Message], verifierListens: Bool = true,
                   timeOfDay: (Date) -> String = Chrome.shortTime) -> VerdictReview {
        let closing = messages.last { ($0.kind == .accept || $0.kind == .dispute) && $0.replyTo == verdict.seq }
        let when = closing.map { " at \(timeOfDay($0.at))" } ?? ""
        let disputes = verdict.disputes == 1 ? "once" : "\(verdict.disputes) times"
        switch verdict.status {
        case .proposed:
            return VerdictReview(
                state: "Needs review",
                decision: "you or the coding agent can accept it",
                humanReviewed: false,
                note: verdict.disputes > 0 ? "The coding agent has disputed an earlier verdict \(disputes)." : nil,
                closed: false
            )
        case .accepted:
            let byYou = verdict.acceptedBy == .human
            return VerdictReview(
                state: byYou ? "You accepted" : "Unreviewed",
                decision: byYou ? String(when.dropFirst()) : "accepted by the \(Self.name(verdict.acceptedBy))\(when)",
                humanReviewed: byYou,
                note: byYou ? nil : "No person has reviewed this verdict.",
                closed: true
            )
        case .contested:
            return VerdictReview(
                state: "Contested",
                decision: "only a person can close it",
                humanReviewed: false,
                note: verdict.disputes > 0 ? "The coding agent disputed it \(disputes)." : "You rejected an earlier verdict.",
                closed: false
            )
        case .rejected:
            // The verifier stops with a destroyed machine; unless it answered the
            // rejection before that, nothing is looking again.
            let answered = closing.map { dispute in
                messages.contains { $0.from == .verifier && $0.kind != .progress && $0.seq > dispute.seq }
            } ?? false
            return VerdictReview(
                state: "You rejected",
                decision: when.isEmpty ? "" : String(when.dropFirst()),
                humanReviewed: true,
                note: verifierListens || answered
                    ? "The verifier was asked to look again."
                    : "The verifier stopped with the machine, so nothing will look again.",
                closed: true
            )
        case .none, .unknown:
            return VerdictReview(state: "No verdict", decision: "", humanReviewed: false, note: nil, closed: false)
        }
    }

    /// What each action does, in the daemon's own terms (session rules, ADR 0006).
    static func explanation(_ verdict: VerdictState, unreviewed: Bool, verifierListens: Bool, alive: Bool) -> String {
        let outcome = Chrome.outcomeTitle(verdict.verdict)
        if unreviewed {
            let rule = "The daemon does not let a person reopen a verdict the coding agent accepted. "
            if !verifierListens {
                return rule + "The verifier stopped when this run's machine was destroyed, so nothing can answer a re-check."
            }
            if !alive {
                return rule + "This run has ended, so the verifier answers a re-check from the record: "
                    + "the steps, pictures and conversation it already has."
            }
            return rule
                + "A re-check asks the verifier to look again with your reason; you can accept or reject what it proposes next."
        }
        let accept = "Accept closes it with this \(outcome.lowercased()) verdict. "
        if !verifierListens {
            return accept + "Reject closes it as rejected with your reason. The verifier stopped when this run's machine "
                + "was destroyed, so nothing will look again. The coding agent sees your decision in the conversation."
        }
        if verdict.status == .contested {
            return accept + "Reject closes it as rejected. "
                + "Either way the coding agent and the verifier read your decision in the conversation."
        }
        return accept + "Reject sends your reason to the verifier, which looks again; "
            + "after that only a person can close its verdicts. The coding agent sees both in the conversation."
    }

    private static func name(_ from: MessageFrom?) -> String {
        switch from {
        case .coder?: "coding agent"
        case .verifier?: "verifier"
        case .human?: "you"
        case .system?, .unknown?, nil: "daemon"
        }
    }
}

/// What a skeptical reviewer should know before trusting a verdict. Each is checked
/// against the run's own record, never guessed from the prose.
enum VerdictCheck: Hashable, Sendable {
    case noEvidence
    /// The verdict cites a step the run never recorded.
    case missingStep(Int)
    /// The verdict cites a step whose tool call failed.
    case failedStep(Int)
    /// The verdict's outcome is not the one its own message states.
    case outcomeMismatch(state: String?, message: String?)
    /// Its own words read like the other outcome ("correctly", "matches" on a fail).
    case readsLike(String)

    var text: String {
        switch self {
        case .noEvidence: "No evidence cited"
        case .missingStep(let step): "Cites step \(step), which is not in the record"
        case .failedStep(let step): "Cites step \(step), which failed"
        case .outcomeMismatch(let state, let message):
            "The verdict reads \(Chrome.outcomeTitle(state)) but its message says \(Chrome.outcomeTitle(message))"
        case .readsLike(let other):
            "Its own summary reads like a \(other.lowercased()). Check before you accept"
        }
    }

    static func checks(_ verdict: VerdictState, messages: [Message], steps: [Step]?) -> [VerdictCheck] {
        var out: [VerdictCheck] = []
        let cited = (verdict.evidence ?? []).map(Evidence.parse)
        if cited.isEmpty { out.append(.noEvidence) }
        if let steps {
            for step in Set(cited.compactMap(\.step)).sorted() {
                guard let record = steps.first(where: { $0.seq == step }) else {
                    out.append(.missingStep(step))
                    continue
                }
                if record.outcome.isFailure { out.append(.failedStep(step)) }
            }
        }
        if let message = messages.first(where: { $0.seq == verdict.seq }), let stated = message.verdict,
           stated != verdict.verdict {
            out.append(.outcomeMismatch(state: verdict.verdict, message: stated))
        }
        let text = messages.first(where: { $0.seq == verdict.seq })?.text ?? verdict.summary ?? ""
        if let other = readsLike(text), other != verdict.verdict {
            out.append(.readsLike(other))
        }
        return out
    }

    private static let passWords: Set<String> = ["correct", "correctly", "matches", "match the expected", "as expected", "works", "passes"]
    private static let failWords: Set<String> = [
        "incorrect", "incorrectly", "instead of", "wrong", "does not", "doesn't", "fails", "failed", "broken",
        "missing", "not shown", "rather than",
    ]

    private static let leanPattern: NSRegularExpression? = {
        let phrases = passWords.union(failWords).sorted { $0.count != $1.count ? $0.count > $1.count : $0 < $1 }
        let alternatives = phrases.map(NSRegularExpression.escapedPattern(for:)).joined(separator: "|")
        return try? NSRegularExpression(pattern: "\\b(?:\(alternatives))\\b")
    }()

    /// "pass" or "fail" when the words lean clearly one way, else nil. A lean needs two
    /// more hits than the other side, so a mixed account stays unflagged. Whole words and
    /// phrases only, each counted once: "correctly" is one hit, "networks" is none.
    static func readsLike(_ text: String) -> String? {
        guard let regex = leanPattern else { return nil }
        let lower = text.lowercased().replacingOccurrences(of: "\u{2019}", with: "'")
        var pass = 0, fail = 0
        for match in regex.matches(in: lower, range: NSRange(lower.startIndex..., in: lower)) {
            guard let span = Range(match.range, in: lower) else { continue }
            let phrase = String(lower[span])
            if passWords.contains(phrase) { pass += 1 } else if failWords.contains(phrase) { fail += 1 }
        }
        if pass >= fail + 2 { return "pass" }
        if fail >= pass + 2 { return "fail" }
        return nil
    }

    /// Values the verdict claims to have seen: amounts, percentages and numbers with a
    /// decimal point, in order, once each. Shown beside the cited picture.
    static func claimedValues(_ text: String) -> [String] {
        var out: [String] = []
        let pattern = #"\$\d[\d,]*(\.\d+)?|\d+(\.\d+)?%|\b\d+\.\d+\b"#
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return [] }
        let range = NSRange(text.startIndex..., in: text)
        for match in regex.matches(in: text, range: range) {
            guard let span = Range(match.range, in: text) else { continue }
            let value = String(text[span])
            if !out.contains(value) { out.append(value) }
        }
        return out
    }
}

/// A dispute and what the verifier answered, so "disputed 2 times" can be audited.
struct DisputeRecord: Equatable, Sendable {
    var dispute: Message
    /// The verifier's first reply, question or verdict after it.
    var answer: Message?

    static func history(for verdict: VerdictState, in messages: [Message]) -> [DisputeRecord] {
        // Every dispute in the run, not only against this verdict: contested counts all.
        messages.enumerated().compactMap { index, message in
            guard message.kind == .dispute else { return nil }
            let answer = messages[(index + 1)...].first {
                $0.from == .verifier && $0.kind != .progress
            }
            return DisputeRecord(dispute: message, answer: answer)
        }
    }
}

extension Chrome {
    /// A closed verdict in a sidebar row: "Pass, you accepted", "Pass, agent only".
    /// "Pass, you accepted", "Pass, unreviewed", "Fail, you rejected".
    static func verdictLine(_ verdict: VerdictState) -> String {
        let outcome = outcomeTitle(verdict.verdict)
        switch verdict.status {
        case .accepted: return verdict.acceptedBy == .human ? "\(outcome), you accepted" : "\(outcome), unreviewed"
        case .rejected: return "\(outcome), you rejected"
        case .proposed: return "Needs review"
        case .contested: return "Contested"
        case .none, .unknown: return outcome
        }
    }

    /// A lifecycle line as the person reads it: they are "you", not "human".
    static func eventText(_ text: String) -> String {
        guard text.hasPrefix("human ") else { return text.prefix(1).uppercased() + text.dropFirst() }
        return "You " + text.dropFirst("human ".count)
    }
}

// MARK: - Titles

extension RunTitle {
    /// The task's first clause, without asides, short enough for one line.
    static func short(task: String?, runId: String, limit: Int = 72) -> String {
        var text = text(task: task, runId: runId)
        guard task.map({ !StepSummary.oneLine($0).isEmpty }) ?? false else { return text }
        text = neutral(withoutAsides(text))
        if let cut = firstBreak(in: text), text.distance(from: text.startIndex, to: cut) >= 12 {
            text = String(text[..<cut])
        }
        text = text.trimmingCharacters(in: CharacterSet(charactersIn: " ,;:.-"))
        guard text.count > limit else { return text }
        let clipped = text.prefix(limit)
        let word = clipped.lastIndex(of: " ").map { clipped[..<$0] } ?? clipped
        return word.trimmingCharacters(in: CharacterSet(charactersIn: " ,;:.-")) + "…"
    }

    /// Short titles made distinct: runs with the same one each say when they started,
    /// which does not change as more runs arrive.
    /// Minutes first; seconds only when two such runs started in the same minute.
    static func distinct(_ runs: [RunSummary]) -> [String: String] {
        func counted(_ label: (RunSummary) -> String) -> [String: Int] {
            runs.reduce(into: [:]) { $0[label($1), default: 0] += 1 }
        }
        let title: (RunSummary) -> String = { short(task: $0.task, runId: $0.runId) }
        let titles = counted(title)
        let minute: (RunSummary) -> String = { "\(title($0)), \(Chrome.shortTime($0.createdAt))" }
        let minutes = counted(minute)
        var out: [String: String] = [:]
        for run in runs {
            if titles[title(run), default: 0] < 2 {
                out[run.runId] = title(run)
            } else if minutes[minute(run), default: 0] < 2 {
                out[run.runId] = minute(run)
            } else {
                out[run.runId] = "\(title(run)), \(Chrome.timeOfDay(run.createdAt))"
            }
        }
        return out
    }

    /// A brief that opens by describing the screen ("X is running on screen", "X is on
    /// screen with ...") says nothing once the run is over; keep the subject and what
    /// follows, or the next sentence when nothing does.
    static func neutral(_ text: String) -> String {
        let states = [" is running on screen", " is on screen", " is running", " is open"]
        for state in states {
            guard let range = text.range(of: state) else { continue }
            let subject = text[..<range.lowerBound].trimmingCharacters(in: .whitespaces)
            guard !subject.isEmpty, subject.split(separator: " ").count <= 4 else { return text }
            let rest = text[range.upperBound...].trimmingCharacters(in: .whitespaces)
            if rest.hasPrefix(".") || rest.isEmpty {
                let next = rest.drop { $0 == "." || $0 == " " }
                return next.isEmpty ? subject : "\(subject): \(next.prefix(1).lowercased())\(next.dropFirst())"
            }
            return "\(subject) \(rest)"
        }
        return text
    }

    private static func withoutAsides(_ text: String) -> String {
        var out = ""
        var depth = 0
        for character in text {
            if character == "(" { depth += 1; continue }
            if character == ")", depth > 0 { depth -= 1; continue }
            if depth == 0 { out.append(character) }
        }
        return StepSummary.oneLine(out.replacingOccurrences(of: " ,", with: ","))
    }

    /// The end of the first sentence or clause introduced by a colon.
    private static func firstBreak(in text: String) -> String.Index? {
        var index = text.startIndex
        while index < text.endIndex {
            let next = text.index(after: index)
            if ".?!:".contains(text[index]), next == text.endIndex || text[next] == " " {
                return index
            }
            index = next
        }
        return nil
    }
}
