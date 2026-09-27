import Foundation

// How each card of the transcript reads (spec, Components and States; companion ADR
// 0009): a tool call, a question, the verdict in the pinned card and its one line in the
// history. Pure, so every rule has a test (`TranscriptCardsTests`); the views in
// `TranscriptViews.swift` and `VerdictCard.swift` only draw what these say.

/// One verifier tool call as a row (Beautiful UI's Tool Chips): a sentence a person would
/// say, a state glyph, at most two secondary facts, and the raw call one expand away.
struct ToolCallRow: Equatable, Sendable {
    enum State: Equatable, Sendable {
        /// Done: a muted check.
        case done
        /// The tool call failed or its command exited non-zero: a red cross and its words.
        case failed
    }

    /// One labelled part of the raw call: the tool, what it was given, what came back.
    struct Raw: Equatable, Sendable {
        var label: String
        var text: String
    }

    var phrase: String
    var state: State
    /// At most two, in this order: the step number, then how long it took.
    var facts: [String]
    /// The step the row seeks, when the call recorded one.
    var step: Int?
    var raw: [Raw]

    static let maxFacts = 2

    /// `steps` is the run's record; the row reads the step's own words when it is held,
    /// and the progress message's otherwise.
    static func of(_ message: Message, steps: [Step]) -> ToolCallRow {
        let record = message.step.flatMap { number in steps.first { $0.seq == number } }
        let phrase = refusedVerdict(message.text)
            ?? record.map { StepSummary.phrase(for: $0, in: steps) } ?? StepSummary.phrase(ofProgress: message.text)
        var facts: [String] = []
        if let number = message.step { facts.append("step \(number)") }
        if let record, record.durationMs > 0 { facts.append(Chrome.duration(record.durationMs)) }
        return ToolCallRow(
            phrase: phrase.isEmpty ? ToolCatalog.entry(for: record?.tool ?? "").title : phrase,
            state: record.map { $0.outcome.isFailure } ?? refused(message.text) ? .failed : .done,
            facts: Array(facts.prefix(maxFacts)),
            step: message.step,
            raw: raw(message, record: record)
        )
    }

    /// The call as the verifier made it: the tool's name, its input laid out as JSON, and
    /// the result it read (the progress message's text, which is what the model saw), or
    /// the error that ended it.
    static func raw(_ message: Message, record: Step?) -> [Raw] {
        let lines = message.text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        let head = lines.first ?? ""
        let parts = head.split(separator: " ", maxSplits: 1).map(String.init)
        let tool = record?.tool ?? ToolCatalog.tool(ofProgress: message.text) ?? ""
        var out: [Raw] = []
        if !tool.isEmpty { out.append(Raw(label: "Tool", text: tool)) }
        let input = record?.input.map(\.compact) ?? (ToolCatalog.tool(ofProgress: message.text) != nil && parts.count > 1 ? parts[1] : nil)
        if let input, !isEmptyJSON(input) {
            out.append(Raw(label: "Input", text: MarkdownText.prettyJSON(input) ?? input))
        }
        if let error = record?.error, !error.isEmpty {
            out.append(Raw(label: "Error", text: error))
        }
        // The progress message repeats the step number on its second line; the row says it.
        var rest = Array(lines.dropFirst())
        if let first = rest.first, first.hasPrefix("step "), Int(first.dropFirst(5)) != nil { rest.removeFirst() }
        let result = rest.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        if record == nil, refused(message.text) {
            out.append(Raw(label: "Error", text: String(result.dropFirst("error:".count)).trimmingCharacters(in: .whitespaces)))
        } else if !result.isEmpty {
            out.append(Raw(label: "Result", text: MarkdownText.prettyJSON(result) ?? result))
        } else if tool.isEmpty, !head.isEmpty {
            // Not a tool call's shape at all: its words are the whole of it.
            out.append(Raw(label: "Message", text: message.text))
        }
        return out
    }

    /// A call the daemon refused before it became a step: its result line starts
    /// "error:" and no step number was given ("machine_click needs an element id").
    /// A verdict the daemon's review refused (root ADR 0024, point 3) reads as that, with
    /// its first reason, never as the raw call: the refusal is the review working.
    static func refusedVerdict(_ progress: String) -> String? {
        guard refused(progress), ToolCatalog.tool(ofProgress: progress) == "report_verdict" else { return nil }
        let reason = progress.split(separator: "\n")
            .first { $0.hasPrefix("- ") }
            .map { $0.dropFirst(2).trimmingCharacters(in: .whitespaces) }
        return reason.map { "Verdict refused by greenroom: \($0)" } ?? "Verdict refused by greenroom"
    }

    static func refused(_ progress: String) -> Bool {
        let lines = progress.split(separator: "\n", omittingEmptySubsequences: false)
        guard lines.count > 1, ToolCatalog.tool(ofProgress: progress) != nil else { return false }
        return lines[1].hasPrefix("error:")
    }

    private static func isEmptyJSON(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty || trimmed == "{}" || trimmed == "null"
    }
}

/// How a run of tool calls folds (spec, States: long transcript). A short run shows
/// whole; a longer one shows its last few with the rest behind one line.
enum ToolCallFold {
    /// Calls shown while folded.
    static let shown = 3

    /// The calls to draw, and how many are folded away above them.
    static func visible<T>(_ calls: [T], open: Bool) -> (calls: [T], hidden: Int) {
        // Folding one call away saves no room: its line takes the same row.
        guard !open, calls.count > shown + 1 else { return (calls, 0) }
        return (Array(calls.suffix(shown)), calls.count - shown)
    }

    static func summary(hidden: Int, open: Bool) -> String {
        open ? "Hide earlier tool calls" : "\(hidden) earlier tool calls"
    }
}

/// The verifier asking the person something, as a card (Beautiful UI's Approval Card):
/// the question in plain words, the answer field while it is open, and once someone has
/// answered, who did in place of the field.
struct QuestionCardState: Equatable, Sendable {
    /// Open questions take the attention role on their edge (spec, Colour: question cards).
    var open: Bool
    /// "Answered by you at 00:59", or nil while open.
    var result: String?

    static func of(_ question: Message, in messages: [Message], timeOfDay: (Date) -> String = Chrome.shortTime) -> QuestionCardState {
        guard let answer = messages.first(where: { $0.kind == .answer && $0.replyTo == question.seq }) else {
            return QuestionCardState(open: true, result: nil)
        }
        let who = answer.from == .human ? "you" : "the \(answer.from.displayName.lowercased())"
        return QuestionCardState(open: false, result: "Answered by \(who) at \(timeOfDay(answer.at)), below")
    }
}

/// How the pinned verdict card is framed (spec, Colour and States; ADR 0003): a proposed
/// verdict is outlined in dim with its outcome in the foreground, and only a verdict a
/// person closed takes its outcome's colour, on the outcome and the edge.
struct VerdictAppearance: Equatable, Sendable {
    enum Edge: Equatable, Sendable {
        /// The quiet hairline of any panel: closed, but not by a person's accept.
        case hairline
        /// The dim role: open for review, so it reads as waiting, not as settled.
        case dim
        /// The outcome's own role (pass or failure): a person accepted it.
        case outcome
    }

    var edge: Edge
    /// Whether the outcome word takes its colour; otherwise it is in the foreground.
    var outcomeInColour: Bool
    /// The outcome in capitals, with its glyph, for the card's mono headline: "✓ PASS".
    var outcome: String
    /// What replaced the actions once the choice was made, or nil while there are actions.
    var result: String?

    static func of(_ verdict: VerdictState, review: VerdictReview) -> VerdictAppearance {
        let word = "\(Chrome.outcomeGlyph(verdict.verdict)) \(Chrome.outcomeTitle(verdict.verdict).uppercased())"
        let personAccepted = verdict.status == .accepted && review.humanReviewed
        let edge: Edge = verdict.status.isOpen ? .dim : personAccepted && hasColour(verdict.verdict) ? .outcome : .hairline
        return VerdictAppearance(
            edge: edge,
            outcomeInColour: personAccepted,
            outcome: word,
            result: result(verdict, review: review)
        )
    }

    /// Only a pass or a fail has a colour; inconclusive stays in the foreground.
    private static func hasColour(_ verdict: String?) -> Bool {
        verdict == "pass" || verdict == "fail"
    }

    private static func when(_ review: VerdictReview) -> String {
        review.decision.isEmpty ? "" : " \(review.decision)"
    }

    /// The Approval Card's result row, for a verdict a person closed. An agent-accepted
    /// verdict still has an action (a re-check), so it has no result row.
    static func result(_ verdict: VerdictState, review: VerdictReview) -> String? {
        let outcome = Chrome.outcomeTitle(verdict.verdict).lowercased()
        switch verdict.status {
        case .accepted where review.humanReviewed:
            return "✓ You accepted this \(outcome) verdict\(when(review))."
        case .rejected:
            return "✗ You rejected this \(outcome) verdict\(when(review))."
        default:
            return nil
        }
    }
}

/// A verdict in the transcript's history, one line: the card holds the live one in full,
/// so it is never drawn twice (companion CLAUDE.md), and older ones read as superseded.
struct VerdictLine: Equatable, Sendable {
    var title: String
    /// "needs review, in full above", "superseded".
    var note: String
    /// Whether the line takes the outcome's colour: the latest verdict, closed by a person.
    var outcomeInColour: Bool
    var latest: Bool

    static func of(_ message: Message, current: VerdictState?, review: VerdictReview?) -> VerdictLine {
        let latest = current?.seq == message.seq
        let outcome = Chrome.outcomeTitle(message.verdict)
        let glyph = Chrome.outcomeGlyph(message.verdict)
        guard latest, let current else {
            return VerdictLine(title: "\(glyph) Earlier verdict: \(outcome)", note: "superseded",
                               outcomeInColour: false, latest: false)
        }
        // The state in the card's own words ("needs review", "you accepted"), then where it is.
        let state = review.map { $0.state.prefix(1).lowercased() + $0.state.dropFirst() + ", " } ?? ""
        let personAccepted = current.status == .accepted && review?.humanReviewed == true
        return VerdictLine(title: "\(glyph) Verdict: \(outcome)", note: "\(state)in full above",
                           outcomeInColour: personAccepted, latest: true)
    }
}

/// A verifier turn that ended at its tool-call cap or its time budget before it finished
/// (issue #127, companion ADR 0015), as a card: which limit, what that means, and while it
/// waits, Continue, which posts `continueText` as your note and so starts a new turn.
struct LimitStop: Equatable, Sendable {
    enum State: Equatable, Sendable {
        /// Nothing has followed it: the verifier waits for a message to go on.
        case waiting
        /// A new turn began after it: who began it, and when ("You continued it at 07:31").
        case continued(String)
        /// Nothing followed it, and the verifier stopped with the machine.
        case over
    }

    var reason: StopReason
    /// "Stopped, out of time": the same words as the run's row.
    var title: String
    /// What the limit means, in a sentence.
    var meaning: String
    var state: State

    /// What Continue posts: a human note, which the verifier answers with a new turn.
    static let continueText = "Continue."

    var canContinue: Bool { state == .waiting }

    /// The card for a verifier reply that stopped at a limit; nil for any other message.
    static func of(_ message: Message, in messages: [Message], verifierListens: Bool,
                   timeOfDay: (Date) -> String = Chrome.shortTime) -> LimitStop? {
        guard let reason = reason(of: message) else { return nil }
        let state: State
        if let next = messages.first(where: { $0.seq > message.seq && beginsTurn($0) }) {
            state = .continued(continued(by: next, timeOfDay: timeOfDay))
        } else {
            state = verifierListens ? .waiting : .over
        }
        return LimitStop(reason: reason, title: "Stopped, \(short(reason))", meaning: meaning(reason), state: state)
    }

    /// The limit a message stopped at: only a verifier reply carries one.
    static func reason(of message: Message) -> StopReason? {
        guard message.from == .verifier, message.kind == .reply else { return nil }
        return message.stop
    }

    /// The stop the run waits on: the newest verifier word is a reply that stopped at a
    /// limit, and nothing that begins a turn came after it. Whether anything will still
    /// answer is the caller's (`RunFacts.verifierListens`).
    static func waiting(_ messages: [Message]) -> Message? {
        for message in messages.reversed() {
            if beginsTurn(message) { return nil }
            guard message.from == .verifier else { continue }
            return reason(of: message) == nil ? nil : message
        }
        return nil
    }

    /// "out of time", for the row and the card's title.
    static func short(_ reason: StopReason) -> String {
        switch reason {
        case .steps: "out of tool calls"
        case .time: "out of time"
        case .unknown: "at a limit"
        }
    }

    static func meaning(_ reason: StopReason) -> String {
        switch reason {
        case .steps: "The verifier used all its tool calls for this turn before it finished."
        case .time: "The verifier ran out of time for this turn before it finished."
        case .unknown(let raw): "The verifier's turn ended at its \(raw) limit before it finished."
        }
    }

    /// What a waiting card says under its meaning, and what one that nothing will answer says.
    static let waitingNote = "It waits for a message. Continue asks it to go on."
    static let overNote = "The verifier stopped with the machine. Nothing will continue it."

    /// A message that starts a verifier turn, or the verifier at work again (a turn a
    /// Give Back resumed has progress and no message before it).
    private static func beginsTurn(_ message: Message) -> Bool {
        switch message.from {
        case .human, .coder: RunStore.startsTurn(message)
        case .verifier: message.kind == .progress
        default: false
        }
    }

    private static func continued(by message: Message, timeOfDay: (Date) -> String) -> String {
        let at = timeOfDay(message.at)
        switch message.from {
        case .human: return "You continued it at \(at)"
        case .coder: return "The coding agent continued it at \(at)"
        default: return "The verifier went on at \(at)"
        }
    }
}
