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
    /// What evidence the check needs (root ADR 0027): value, visual, timing. Empty on a
    /// verdict from before kinds, which reads as value.
    var kinds: [CheckKind] = []
    /// A timing check's window in seconds, from the end of its last action.
    var within: Double?
}

/// What evidence a check needs (root ADR 0027). An unknown kind is kept as its word.
enum CheckKind: Hashable, Sendable {
    case value, visual, timing
    case other(String)

    /// The kind as the wire writes it.
    var word: String {
        switch self {
        case .value: "value"
        case .visual: "visual"
        case .timing: "timing"
        case .other(let word): word
        }
    }

    init(_ raw: String) {
        switch raw.trimmingCharacters(in: .whitespaces).lowercased() {
        case "value": self = .value
        case "visual": self = .visual
        case "timing": self = .timing
        case let word: self = .other(word)
        }
    }
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
        // `kinds`, or the single `kind` an early ADR 0027 daemon wrote. Leniently: a
        // malformed kind drops the kinds, never the check.
        let raw = Self.words(try? c.decodeIfPresent(JSONValue.self, forKey: .kinds))
            + Self.words(try? c.decodeIfPresent(JSONValue.self, forKey: .kind))
        var seen = Set<CheckKind>()
        kinds = raw.filter { !$0.isEmpty }.map(CheckKind.init).filter { seen.insert($0).inserted }
        within = (try? c.decodeIfPresent(Double.self, forKey: .within)).flatMap { $0 }.flatMap { $0 > 0 ? $0 : nil }
    }

    private enum CodingKeys: String, CodingKey {
        case id, criterion, status, evidence, actions, observed, kinds, kind, within
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(criterion, forKey: .criterion)
        try c.encode(status, forKey: .status)
        try c.encode(evidence, forKey: .evidence)
        try c.encode(actions, forKey: .actions)
        try c.encodeIfPresent(observed, forKey: .observed)
        if !kinds.isEmpty { try c.encode(kinds.map(\.word), forKey: .kinds) }
        try c.encodeIfPresent(within, forKey: .within)
    }

    /// A number of seconds as a person writes it: 2, 2.5.
    static func number(_ value: Double) -> String {
        value == value.rounded() ? String(Int(value)) : String(format: "%.1f", value)
    }

    private static func words(_ value: JSONValue?) -> [String] {
        switch value {
        case .string(let word)?: [word]
        case .array(let items)?: items.compactMap(\.stringValue)
        default: []
        }
    }

    /// The check's kinds beyond a plain value, as a reviewer reads them: "visual",
    /// "timing, within 2 s". Nil for a value check, which needs no word.
    var kindTag: String? {
        let words = kinds.compactMap { kind -> String? in
            switch kind {
            case .value: nil
            case .visual: "visual"
            case .timing: within.map { "timing, within \(Self.number($0)) s" } ?? "timing"
            case .other(let word): word
            }
        }
        return words.isEmpty ? nil : words.joined(separator: " · ")
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

/// A verdict's checks as the card reads them: the tally beside the outcome and the rows,
/// failed first (companion ADR 0011).
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

    var passed: Int { checks.count(where: { $0.status == .pass }) }
    var failed: Int { checks.count(where: { $0.status == .fail }) }
    var unchecked: [AcceptanceCheck] { checks.filter { $0.status == .unchecked } }

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

// MARK: - The ledger (companion ADR 0011)

extension Checklist {
    /// One count of the tally beside the outcome: "2 failed", "1 not checked", "5 passed".
    struct TallyItem: Equatable, Sendable {
        var status: AcceptanceCheck.Status
        var count: Int
        var text: String
    }

    /// The counts a reviewer reads first, in the card's order (failed, not checked,
    /// passed), leaving out what is zero. Empty for a verdict without checks.
    var tally: [TallyItem] {
        [(AcceptanceCheck.Status.fail, "failed"), (.unchecked, "not checked"), (.pass, "passed")].compactMap { status, word in
            let count = checks.count(where: { $0.status == status })
            return count > 0 ? TallyItem(status: status, count: count, text: "\(count) \(word)") : nil
        }
    }

    /// The tally in words, for VoiceOver and the run list: "2 of 4 checks failed".
    var summary: String? {
        guard !checks.isEmpty else { return nil }
        let total = "\(checks.count) \(checks.count == 1 ? "check" : "checks")"
        return tally.map(\.text).joined(separator: ", ") + " of \(total)"
    }

    /// The verdict's scope in a run's row: what failed, else what was not checked, else
    /// that all passed, short enough to sit beside the time: "2/4 failed", "3/4 unchecked",
    /// "8/8 passed".
    var rowTally: String? {
        guard !checks.isEmpty else { return nil }
        let total = checks.count
        if failed > 0 { return "\(failed)/\(total) failed" }
        if !unchecked.isEmpty { return "\(unchecked.count)/\(total) unchecked" }
        return "\(total)/\(total) passed"
    }

    /// The check with this id, if the verdict answers it.
    func check(_ id: String?) -> AcceptanceCheck? {
        guard let id else { return nil }
        return checks.first { $0.id == id }
    }

    /// The check a step is evidence for, in the card's order: the one asked for when it
    /// cites the step, else the first that does. Nil when no check cites it.
    func check(citing step: Int, preferring id: String? = nil) -> AcceptanceCheck? {
        if let preferred = check(id), preferred.evidence.contains(step) { return preferred }
        return ordered.first { $0.evidence.contains(step) }
    }

    /// The check `delta` places after `id` in the card's order, wrapping; the first (or
    /// last) when none is selected.
    func check(after id: String?, by delta: Int) -> AcceptanceCheck? {
        let list = ordered
        guard !list.isEmpty else { return nil }
        guard let id, let at = list.firstIndex(where: { $0.id == id }) else {
            return delta >= 0 ? list.first : list.last
        }
        return list[(at + delta % list.count + list.count) % list.count]
    }
}

/// A cited step as a reviewer needs to know it: what kind of evidence it is (a picture a
/// person can look at, the app's own report of its UI, a command) and what of the text it
/// reports is not on screen (root ADR 0027). Pure; `ChecklistTests`.
struct EvidenceStep: Equatable, Sendable {
    enum Kind: Equatable, Sendable {
        case screenshot, uiRead, command, other
    }

    var step: Int
    var kind: Kind
    /// Whether the run's record holds the step.
    var held: Bool
    /// Text elements the read reports that a person cannot see there.
    var unseen: [UnseenText]

    /// "Screenshot 7", "UI read 6", "Command 4", "Step 9".
    var label: String {
        switch kind {
        case .screenshot: "Screenshot \(step)"
        case .uiRead: "UI read \(step)"
        case .command: "Command \(step)"
        case .other: "Step \(step)"
        }
    }

    static func of(_ step: Int, in steps: [Step]) -> EvidenceStep {
        guard let record = steps.first(where: { $0.seq == step }) else {
            return EvidenceStep(step: step, kind: .other, held: false, unseen: [])
        }
        let kind: Kind = switch record.tool {
        case "machine_screenshot": .screenshot
        case "machine_ui": .uiRead
        case "machine_exec", "machine_run", "run": .command
        default: .other
        }
        return EvidenceStep(step: step, kind: kind, held: true, unseen: kind == .uiRead ? UnseenText.all(in: record) : [])
    }
}

/// A text element a UI read reports but a person cannot see (root ADR 0027): its frame on
/// the screen holds no ink, it is off the screen, or another window covers it.
struct UnseenText: Equatable, Sendable {
    enum Why: String, Equatable, Sendable {
        case blank, offscreen, covered

        var words: String {
            switch self {
            case .blank: "not drawn"
            case .offscreen: "off screen"
            case .covered: "covered by another window"
            }
        }
    }

    var text: String
    var why: Why
    /// Where the element sits, in fractions of the screen (top-left origin), when the read
    /// gave its centre and size.
    var frame: CGRect?

    /// Every marked element of a `machine_ui` step that carries text.
    static func all(in step: Step) -> [UnseenText] {
        guard case .array(let elements)? = step.output?["elements"] else { return [] }
        return elements.compactMap { element in
            guard let mark = element["rendered"]?.stringValue, let why = Why(rawValue: mark) else { return nil }
            let text = [element["value"], element["title"], element["label"]]
                .compactMap { $0?.stringValue?.trimmingCharacters(in: .whitespacesAndNewlines) }
                .first { !$0.isEmpty }
            return text.map { UnseenText(text: $0, why: why, frame: frame(of: element)) }
        }
    }

    /// A `machine_ui` element's centre (`x`, `y`) and size (`w`, `h`) as a rectangle.
    private static func frame(of element: JSONValue) -> CGRect? {
        func number(_ key: String) -> Double? {
            switch element[key] {
            case .double(let value)?: value
            case .int(let value)?: Double(value)
            default: nil
            }
        }
        guard let x = number("x"), let y = number("y"), let w = number("w"), let h = number("h"), w > 0, h > 0 else {
            return nil
        }
        return CGRect(x: x - w / 2, y: y - h / 2, width: w, height: h)
    }

    /// Whether a check's words are about this text: the whole text, or its label before a
    /// colon ("Each pays" of "Each pays: $49.56"), appears in the criterion or what was
    /// observed. A read marks every unseen text on the screen; only these concern the check.
    func concerns(_ check: AcceptanceCheck) -> Bool {
        let words = (check.criterion + " " + (check.observed ?? "")).lowercased()
        let whole = text.lowercased()
        if whole.count >= 3, words.contains(whole) { return true }
        let label = whole.split(separator: ":", maxSplits: 1).first.map(String.init)?
            .trimmingCharacters(in: .whitespaces) ?? ""
        return label.count >= 3 && label != whole && words.contains(label)
    }
}

extension AcceptanceCheck {
    /// The unseen texts its UI reads report that this check is about, with where they sit:
    /// what the stage outlines over the picture at `step` (companion ADR 0014). Only a read
    /// cited as its evidence, taken at or before `step` with no input between them, so the
    /// screen has not been changed under the outline; the screenshot a reviewer checks is
    /// usually the step right after such a read.
    func unseenMarks(atStep step: Int, in steps: [Step]) -> [UnseenText] {
        guard evidence.contains(step) else { return [] }
        let inputs = Set(steps.filter { Self.inputTools.contains($0.tool) }.map(\.seq))
        var seen = Set<String>()
        return evidence.filter { $0 <= step }.sorted(by: >).flatMap { read -> [UnseenText] in
            guard !inputs.contains(where: { $0 > read && $0 <= step }) else { return [] }
            return EvidenceStep.of(read, in: steps).unseen.filter {
                $0.concerns(self) && $0.frame != nil && seen.insert($0.text).inserted
            }
        }
    }

    /// Tools that change what is on screen (root ADR 0024's inputs).
    static let inputTools: Set<String> = ["machine_click", "machine_type", "machine_key", "machine_scroll", "machine_input"]

    /// What a reviewer must see before trusting this check's evidence: each unseen text
    /// its UI reads report that the check is about, once. "UI read 6: \"Each pays:
    /// $49.56\" is not drawn".
    func unseenWarnings(in steps: [Step]) -> [String] {
        var seen = Set<String>()
        return evidence.flatMap { step -> [String] in
            let item = EvidenceStep.of(step, in: steps)
            return item.unseen.filter { $0.concerns(self) }.compactMap { unseen in
                guard seen.insert(unseen.text).inserted else { return nil }
                return "\(item.label): \"\(unseen.text)\" is \(unseen.why.words)"
            }
        }
    }
}
