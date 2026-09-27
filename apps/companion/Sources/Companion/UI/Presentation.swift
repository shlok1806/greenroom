import Foundation

// How the native window words and marks a run (companion ADR 0019). Pure: every rule here is a
// function of the daemon's summary (root ADR 0036) and a clock, with a test each. The daemon
// decides the state, the group, the sentence and the actions; this file only lays them out:
// which glyph a state draws, how a count or a time reads, what a row's meta says.

/// The status glyph set (Figma Components, Status glyph): one shape per state.
enum GlyphKind: String, CaseIterable, Sendable {
    case passed, failed, checking, pending, paused, starting, stopped, warning

    /// Only Checking moves: it turns once every `Motion.ring`.
    var turns: Bool { self == .checking }
}

/// The one colour a glyph or a status word may carry. Colour only for a real state.
enum ToneColor: Sendable, Equatable {
    case pass, fail, accent, wait, secondary, tertiary

    var token: ColorToken {
        switch self {
        case .pass: .pass
        case .fail: .fail
        case .accent: .accent
        case .wait: .wait
        case .secondary: .textSecondary
        case .tertiary: .textTertiary
        }
    }
}

extension SummaryState {
    /// The glyph a run in this state draws.
    var glyph: GlyphKind {
        switch self {
        case .starting, .restarting: .starting
        case .checking: .checking
        case .paused: .paused
        case .notAnswering: .warning
        case .passed: .passed
        case .failed: .failed
        case .inconclusive, .stopped, .unknown: .stopped
        }
    }
}

extension SummaryTone {
    /// The colour the tone paints: an outcome only the coding agent accepted keeps its word
    /// and its shape but not its colour (`quiet`).
    var color: ToneColor {
        switch self {
        case .pass: .pass
        case .fail: .fail
        case .live: .accent
        case .wait: .wait
        case .quiet: .secondary
        }
    }
}

extension SummaryCheck.State {
    var glyph: GlyphKind {
        switch self {
        case .pass: .passed
        case .fail: .failed
        case .pending: .pending
        }
    }

    var color: ToneColor {
        switch self {
        case .pass: .pass
        case .fail: .fail
        case .pending: .tertiary
        }
    }
}

// MARK: - Times

/// Times as the window reads them. Tabular, short, no seconds past an hour of age.
enum Clock {
    /// Elapsed time, `m:ss`, or `h:mm:ss` from an hour: "4:18", "1:02:07".
    static func elapsed(_ seconds: Int) -> String {
        let s = max(0, seconds)
        let (h, m, r) = (s / 3600, (s % 3600) / 60, s % 60)
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, r) : String(format: "%d:%02d", m, r)
    }

    /// An age for a row's meta: "now", "8m", "2h", "1d", "3w".
    static func age(since date: Date, now: Date) -> String {
        let s = Int(now.timeIntervalSince(date))
        switch s {
        case ..<60: return "now"
        case ..<3600: return "\(s / 60)m"
        case ..<86400: return "\(s / 3600)h"
        case ..<(86400 * 14): return "\(s / 86400)d"
        default: return "\(s / (86400 * 7))w"
        }
    }

    /// An age in words, for the toolbar: "just now", "12 min ago", "2 h ago", "yesterday", "5 days ago".
    static func ago(_ date: Date, now: Date) -> String {
        let s = Int(now.timeIntervalSince(date))
        switch s {
        case ..<60: return "just now"
        case ..<3600: return "\(s / 60) min ago"
        case ..<86400: return "\(s / 3600) h ago"
        case ..<(86400 * 2): return "yesterday"
        default: return "\(s / 86400) days ago"
        }
    }
}

extension Summary {
    /// Seconds the run has lasted as of `now`: the daemon's count, carried on by the clock
    /// while the run is open.
    func elapsed(now: Date, receivedAt: Date? = nil) -> Int {
        guard endedAt == nil, state.isWorking || group != .done else { return elapsedSeconds }
        if startedAt > .epoch { return max(elapsedSeconds, Int(now.timeIntervalSince(startedAt))) }
        return elapsedSeconds
    }

    /// Seconds since the run entered its status.
    func inStatus(now: Date) -> Int { max(0, Int(now.timeIntervalSince(since))) }
}

// MARK: - The run row

/// A run's row in the sidebar: one glyph, a name of five words or fewer, one short meta.
struct RunRowModel: Equatable, Sendable, Identifiable {
    var id: String
    var name: String
    var glyph: GlyphKind
    var glyphColor: ToneColor
    var meta: String
    /// The meta's colour: a failed or passed tally on a run that needs you, else secondary.
    var metaColor: ToneColor
    /// What VoiceOver reads: "TipSplit: split the bill, Failed, 2 failed".
    var accessibilityLabel: String

    init(_ summary: Summary, now: Date) {
        id = summary.runId
        name = summary.name.isEmpty ? "Run \(summary.runId.suffix(6))" : summary.name
        glyph = summary.state.glyph
        glyphColor = summary.tone.color
        (meta, metaColor) = RunRowModel.meta(summary, now: now)
        accessibilityLabel = [name, summary.status, meta].filter { !$0.isEmpty }.joined(separator: ", ")
    }

    /// Needs you, with an outcome: the tally ("2 failed", "4 passed"). Open: the time it has run.
    /// Done: its age.
    static func meta(_ s: Summary, now: Date) -> (String, ToneColor) {
        if s.group == .needsYou, s.state.isOutcome, s.checks.total > 0 {
            if s.state == .failed, s.checks.failed > 0 { return ("\(s.checks.failed) failed", s.tone == .fail ? .fail : .secondary) }
            if s.state == .passed { return ("\(s.checks.passed) passed", s.tone == .pass ? .pass : .secondary) }
        }
        if s.group == .done {
            return (Clock.age(since: s.endedAt ?? s.since, now: now), .secondary)
        }
        return (Clock.elapsed(s.elapsed(now: now)), .secondary)
    }
}

/// The sidebar's list: group headings, rows, and "Show N more" under a long group.
enum SidebarItem: Hashable, Sendable, Identifiable {
    case heading(SummaryGroup)
    case run(String)
    case more(SummaryGroup, hidden: Int)
    case empty(SummaryGroup)

    var id: String {
        switch self {
        case .heading(let g): "h-\(g.rawValue)"
        case .run(let id): id
        case .more(let g, _): "m-\(g.rawValue)"
        case .empty(let g): "e-\(g.rawValue)"
        }
    }
}

enum SidebarLayout {
    /// How many Done runs show before "Show N more".
    static let doneShown = 5

    /// The rows in list order. Needs you and Running show every run; Done shows its newest
    /// `doneShown` unless expanded, plus the selected run wherever it is. An empty Needs you
    /// says so; an empty Running or Done group has no heading. A search shows every match.
    static func items(_ board: SummaryBoard, expanded: Set<SummaryGroup>, selected: String?, query: String = "") -> [SidebarItem] {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        var out: [SidebarItem] = []
        for group in SummaryGroup.allCases {
            let runs = (board.groups.first { $0.id == group }?.runs ?? [])
                .filter { needle.isEmpty || $0.name.lowercased().contains(needle) || $0.status.lowercased().contains(needle) }
            if runs.isEmpty {
                if group == .needsYou, needle.isEmpty, !board.runs.isEmpty {
                    out.append(.heading(group))
                    out.append(.empty(group))
                }
                continue
            }
            out.append(.heading(group))
            let limit = group == .done && needle.isEmpty && !expanded.contains(group) ? doneShown : Int.max
            var shown = 0
            for run in runs {
                if shown < limit || run.runId == selected {
                    out.append(.run(run.runId))
                    shown += 1
                }
            }
            if shown < runs.count { out.append(.more(group, hidden: runs.count - shown)) }
        }
        return out
    }

    /// The run `delta` rows after `current` among the runs the list shows, clamped; the first
    /// run when none is selected.
    static func step(from current: String?, by delta: Int, in items: [SidebarItem]) -> String? {
        let runs = items.compactMap { item -> String? in if case .run(let id) = item { id } else { nil } }
        guard !runs.isEmpty else { return nil }
        guard let current, let index = runs.firstIndex(of: current) else { return delta >= 0 ? runs.first : runs.last }
        return runs[min(max(0, index + delta), runs.count - 1)]
    }
}

extension SummaryBoard {
    /// The summary of one run.
    func summary(_ runId: String) -> Summary? {
        for group in groups { if let s = group.runs.first(where: { $0.runId == runId }) { return s } }
        return nil
    }

    /// The board with one run's new summary in its group, as the daemon orders a group: the
    /// run that entered its status last first, then the newer run. Every group stays present.
    func applying(_ summary: Summary, macs newMacs: SummaryMacs?) -> SummaryBoard {
        var byGroup: [SummaryGroup: [Summary]] = [:]
        for group in groups { byGroup[group.id, default: []] += group.runs.filter { $0.runId != summary.runId } }
        byGroup[summary.group, default: []].append(summary)
        let ordered = SummaryGroup.allCases.map { id in
            let runs = (byGroup[id] ?? []).sorted { a, b in
                a.since != b.since ? a.since > b.since : a.startedAt > b.startedAt
            }
            return Group(id: id, runs: runs)
        }
        var out = SummaryBoard(groups: ordered, macs: newMacs ?? macs)
        out.updatedAt = max(updatedAt ?? .epoch, summary.updatedAt)
        return out
    }
}

// MARK: - The run header

/// The header of a run: the status (glyph, word, tally or time), the line under it, and the
/// actions. Answers "is it working", "did it pass" and "what do I do now" (docs/20 section 7).
struct HeaderModel: Equatable, Sendable {
    var glyph: GlyphKind
    var glyphColor: ToneColor
    var status: String
    /// Beside the word: "2 of 4 checks", "4 checks, 4:18", "for 1:02", "0:24".
    var tally: String
    /// Under the word: "clicking 25% in TipSplit" after "Now", or the daemon's sentence.
    var line: String
    /// Whether `line` is the Now line (drawn with the live dot and the word "Now").
    var isNow: Bool
    var primary: SummaryAction?
    var secondary: [SummaryAction]

    init(_ s: Summary, now: Date) {
        glyph = s.state.glyph
        glyphColor = s.tone.color
        status = s.status.isEmpty ? "Unknown" : s.status
        tally = HeaderModel.tally(s, now: now)
        if let words = s.now, s.state == .checking {
            line = HeaderModel.lowerFirst(words)
            isNow = true
        } else {
            line = s.detail ?? s.now ?? ""
            isNow = false
        }
        primary = s.primaryAction
        secondary = s.secondaryActions
    }

    static func tally(_ s: Summary, now: Date) -> String {
        let c = s.checks
        let checks: String? = {
            guard c.total > 0 else { return nil }
            let noun = c.total == 1 ? "check" : "checks"
            switch s.state {
            case .failed: return "\(c.failed) of \(c.total) \(noun)"
            case .passed: return "\(c.passed) of \(c.total) \(noun)"
            case .inconclusive: return "\(c.pending) of \(c.total) \(noun) not checked"
            default:
                let answered = c.passed + c.failed
                return answered > 0 ? "\(answered) of \(c.total) \(noun)" : "\(c.total) \(noun)"
            }
        }()
        switch s.state {
        case .notAnswering: return "for \(Clock.elapsed(s.inStatus(now: now)))"
        case .restarting: return Clock.elapsed(s.inStatus(now: now))
        case .starting: return Clock.elapsed(s.elapsed(now: now))
        case .checking, .paused:
            let time = Clock.elapsed(s.elapsed(now: now))
            return checks.map { "\($0), \(time)" } ?? time
        default: return checks ?? ""
        }
    }

    /// "Clicking 25% in TipSplit" reads "Now clicking 25% in TipSplit".
    static func lowerFirst(_ s: String) -> String {
        guard let first = s.first else { return s }
        let second = s.dropFirst().first
        // Keep a name's capital: "TipSplit", "UI".
        if let second, second.isUppercase { return s }
        return first.lowercased() + s.dropFirst()
    }
}

// MARK: - The evidence caption

/// The sentence under the picture.
enum EvidenceCaption: Equatable, Sendable {
    /// "Expected $50.00, saw $10.00"
    case disagreement(expected: String, saw: String)
    /// "Saw $50.00, as expected"
    case agreement(saw: String)
    /// The check's own sentence, when it names no values.
    case sentence(String)
    /// "Last step Clicked 25%"
    case lastStep(String)
    case none

    static func of(_ check: SummaryCheck?) -> EvidenceCaption {
        guard let check else { return .none }
        switch check.state {
        case .fail:
            if let expected = check.expected, let saw = check.saw { return .disagreement(expected: expected, saw: saw) }
        case .pass:
            if let saw = check.saw { return .agreement(saw: saw) }
        case .pending:
            break
        }
        return check.observed.map(EvidenceCaption.sentence) ?? .none
    }

    /// The caption as plain words, for VoiceOver and the word count.
    var words: String {
        switch self {
        case .disagreement(let e, let s): "Expected \(e), saw \(s)"
        case .agreement(let s): "Saw \(s), as expected"
        case .sentence(let s): s
        case .lastStep(let s): "Last step \(s)"
        case .none: ""
        }
    }
}

/// The check a run opens on: the first failed (the daemon lists them first), else the first
/// with a picture, else the first.
enum CheckSelection {
    static func initial(_ checks: [SummaryCheck]) -> String? {
        (checks.first { $0.state == .fail } ?? checks.first { $0.picture != nil } ?? checks.first)?.id
    }

    /// The check `delta` after `id`, wrapping; the first (or last) when none is selected.
    static func step(from id: String?, by delta: Int, in checks: [SummaryCheck]) -> String? {
        guard !checks.isEmpty else { return nil }
        guard let id, let index = checks.firstIndex(where: { $0.id == id }) else {
            return delta >= 0 ? checks.first?.id : checks.last?.id
        }
        let next = ((index + delta) % checks.count + checks.count) % checks.count
        return checks[next].id
    }
}
