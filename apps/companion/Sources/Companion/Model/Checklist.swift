import Foundation

// A verdict as a checklist (root ADR 0024, points 1, 2 and 6). The verifier declares its
// acceptance checks before it acts (a `progress` message carrying them), and its verdict
// answers each one. The checklist is the verdict's scope: a pass certifies every listed
// check was observed, nothing more, so the card lists what was not checked in words.

/// One acceptance check, as the daemon sends it on a verdict and on the progress message
/// that declares them. The declaring message sets only `id` and `criterion`.
struct AcceptanceCheck: Codable, Hashable, Sendable {
    enum Status: String, Codable, Hashable, Sendable, CaseIterable {
        case pass, fail, unchecked

        /// A status the app does not know claims nothing, so it reads as not checked.
        init(from decoder: any Decoder) throws {
            let raw = try? decoder.singleValueContainer().decode(String.self)
            self = raw.flatMap(Status.init(rawValue:)) ?? .unchecked
        }
    }

    var id: String
    var criterion: String
    var status: Status = .unchecked
    /// Step numbers of the observations that show the result.
    var evidence: [Int] = []
    /// Step numbers of the inputs the check depends on.
    var actions: [Int] = []
    /// What the evidence showed, in one sentence.
    var observed: String?
}

extension AcceptanceCheck {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(.id, or: "")
        criterion = try c.decode(.criterion, or: "")
        status = try c.decode(.status, or: .unchecked)
        evidence = Self.steps(try c.decodeIfPresent(JSONValue.self, forKey: .evidence))
        actions = Self.steps(try c.decodeIfPresent(JSONValue.self, forKey: .actions))
        let observed = try c.decodeIfPresent(String.self, forKey: .observed)?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.observed = observed?.isEmpty == false ? observed : nil
    }

    /// Step numbers, leniently: numbers, or "step 12" as the free evidence list writes them.
    /// Anything else is dropped rather than failing the whole message.
    private static func steps(_ value: JSONValue?) -> [Int] {
        guard case .array(let items)? = value else { return [] }
        return items.compactMap { item in
            switch item {
            case .int(let number): number
            case .double(let number) where number == number.rounded(): Int(number)
            case .string(let text): Evidence.parse(text).step ?? Int(text.trimmingCharacters(in: .whitespaces))
            default: nil
            }
        }
    }

    /// The status as a mark and a word: the glyph, its colour role, and what VoiceOver
    /// and the row say. Every state is a glyph and a word (companion CLAUDE.md).
    var mark: CheckMark { CheckMark.of(status) }
}

/// How a check's status is drawn: `✓` pass, `✗` fail, `○` not checked (in dim, so only a
/// result takes a colour).
struct CheckMark: Equatable, Sendable {
    var glyph: String
    var role: Role
    /// The accessible label and the row's own word.
    var word: String

    static func of(_ status: AcceptanceCheck.Status) -> CheckMark {
        switch status {
        case .pass: CheckMark(glyph: "✓", role: .pass, word: "Passed")
        case .fail: CheckMark(glyph: "✗", role: .failure, word: "Failed")
        case .unchecked: CheckMark(glyph: "○", role: .dim, word: "Not checked")
        }
    }
}

/// A verdict's checks as the card reads them: counts and one sentence saying what the
/// verdict covers ("Verified: 3 of 4 checks; not checked: ...").
struct Checklist: Equatable, Sendable {
    var checks: [AcceptanceCheck]

    /// A verdict's checks, each criterion filled in from the latest plan declared before it
    /// when the verdict names the check only by id (`report_verdict` answers by id).
    static func of(_ verdict: Message?, in messages: [Message]) -> Checklist {
        guard let verdict else { return Checklist(checks: []) }
        let plan = messages.last { $0.seq < verdict.seq && CheckPlan.isPlan($0) }
        let declared = Dictionary((plan?.checks ?? []).map { ($0.id, $0.criterion) }, uniquingKeysWith: { first, _ in first })
        return Checklist(checks: verdict.checks.map { check in
            var check = check
            if check.criterion.trimmingCharacters(in: .whitespaces).isEmpty { check.criterion = declared[check.id] ?? "" }
            return check
        })
    }

    /// Beyond this many, the not-checked ones are counted in the scope, not named: the
    /// rows below name each one.
    static let namedUnchecked = 2

    var passed: Int { checks.count(where: { $0.status == .pass }) }
    var failed: Int { checks.count(where: { $0.status == .fail }) }
    var unchecked: [AcceptanceCheck] { checks.filter { $0.status == .unchecked } }

    /// The verdict's scope in one line, or nil when it has no checks (an older verdict,
    /// which the card shows as it always did).
    var scope: String? {
        guard !checks.isEmpty else { return nil }
        var parts = ["Verified: \(passed) of \(checks.count) \(checks.count == 1 ? "check" : "checks")"]
        if failed > 0 { parts.append("failed: \(failed)") }
        let open = unchecked
        if !open.isEmpty {
            let named = open.count <= Self.namedUnchecked
                ? open.map { Self.clip($0.criterion) }.joined(separator: "; ")
                : "\(open.count)"
            parts.append("not checked: \(named)")
        }
        return parts.joined(separator: "; ")
    }

    /// The checks as the card lists them: what failed, then what was not checked, then
    /// what passed, each in the order the verifier declared it. A reviewer reads what is
    /// wrong or outside the scope before what held.
    var ordered: [AcceptanceCheck] {
        let rank: [AcceptanceCheck.Status: Int] = [.fail: 0, .unchecked: 1, .pass: 2]
        return checks.enumerated()
            .sorted { (rank[$0.element.status] ?? 1, $0.offset) < (rank[$1.element.status] ?? 1, $1.offset) }
            .map(\.element)
    }

    /// The steps the checks cite, each once, in the card's order: a failed check's first,
    /// so the run opens where the fail shows.
    var citedSteps: [Int] {
        var seen = Set<Int>()
        return ordered.flatMap(\.evidence).filter { seen.insert($0).inserted }
    }

    /// Where a person starts reading a failing verdict: the first failed check's evidence.
    var firstFailedStep: Int? {
        checks.first { $0.status == .fail && !$0.evidence.isEmpty }?.evidence.first
    }

    /// A criterion short enough to name inside the scope line.
    static func clip(_ criterion: String, limit: Int = 60) -> String {
        let text = criterion.trimmingCharacters(in: .whitespacesAndNewlines)
        guard text.count > limit else { return text }
        let cut = text.prefix(limit)
        let word = cut.lastIndex(of: " ").map { cut[..<$0] } ?? cut
        return word.trimmingCharacters(in: .punctuationCharacters.union(.whitespaces)) + "..."
    }
}

/// The verifier's declared checks, as the transcript shows them: "Plan: 4 checks" and the
/// criteria, folded to the first few when there are many.
struct CheckPlan: Equatable, Sendable {
    var title: String
    var criteria: [String]
    /// Criteria past the fold, 0 when all show.
    var hidden: Int

    /// Criteria shown while folded.
    static let shown = 4

    /// The plan a message declares, or nil when it declares none: only a verifier progress
    /// message carrying checks does.
    static func of(_ message: Message, open: Bool) -> CheckPlan? {
        guard isPlan(message) else { return nil }
        let all = message.checks.map(\.criterion)
        // Folding one away saves no room: its line takes the fold's row.
        let folds = !open && all.count > shown + 1
        return CheckPlan(
            title: "Plan: \(all.count) \(all.count == 1 ? "check" : "checks")",
            criteria: folds ? Array(all.prefix(shown)) : all,
            hidden: folds ? all.count - shown : 0
        )
    }

    static func isPlan(_ message: Message) -> Bool {
        message.kind == .progress && message.from == .verifier && !message.checks.isEmpty
    }

    static func fold(hidden: Int, open: Bool) -> String {
        open ? "Show fewer" : "\(hidden) more"
    }
}

extension Message {
    /// Every step a verdict cites: its checks' evidence, then the free list's steps.
    var citedSteps: [Int] {
        var seen = Set<Int>()
        let free = (evidence ?? []).compactMap { Evidence.parse($0).step }
        return (Checklist(checks: checks).citedSteps + free).filter { seen.insert($0).inserted }
    }
}
