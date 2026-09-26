import Foundation

/// Everything the window says about one run's state, derived in one place from what the
/// store holds, so the sidebar, the header, the status line, the player and the verdict
/// card cannot contradict each other (companion ADR 0002). Pure; `RunFactsTests`.
struct RunFacts: Equatable, Sendable {
    /// Where the run is. Exactly one of these, whatever else is true.
    enum Phase: Equatable, Sendable {
        case booting
        /// The machine is up and something happened within `idleAfter`.
        case live
        /// The machine is up and nothing has happened since `lastActivity`.
        case idle
        /// The machine is gone.
        case ended(Ending)
        /// The machine failed to boot, or the run could not be read.
        case failed(String?)
    }

    enum Ending: Equatable, Sendable {
        /// Someone destroyed it on purpose: the coding agent or `byYou`.
        case destroyed(byYou: Bool)
        /// Its VM went away underneath the run.
        case lost(String?)
        /// No machine and no word on why (an old run).
        case finished
    }

    /// Whose move it is.
    enum Turn: Equatable, Sendable {
        case verifier
        case coder
        /// A question for the human, or a verdict only a human can close.
        case you(String)
        case nobody
    }

    var phase: Phase
    var turn: Turn
    var started: Date
    /// When the machine went away, if it has.
    var ended: Date?
    /// The newest step or message: what the person means by "last activity". Frames do not
    /// count; the recorder captures an idle machine too.
    var lastActivity: Date
    var lastStep: Step?
    var stepCount: Int
    /// Steps whose tool call failed or whose command exited non-zero, in order.
    var failures: [Int]
    var messageCount: Int
    var verdict: VerdictState?
    /// The machine exists and is ready: the only case the screen can be streamed or driven.
    var machineReady: Bool
    /// The daemon's verifier still reads this run's conversation. It stops only when the
    /// machine is destroyed (`verifier.Actors`); a failed or lost machine keeps it.
    var verifierListens: Bool

    static let idleAfter: TimeInterval = 5 * 60

    /// Start to end, or to `now` while alive: the one duration the window shows.
    func duration(now: Date) -> TimeInterval {
        max(0, (ended ?? now).timeIntervalSince(started))
    }

    func idle(now: Date) -> TimeInterval {
        max(0, now.timeIntervalSince(lastActivity))
    }

    var isAlive: Bool {
        switch phase {
        case .booting, .live, .idle: true
        case .ended, .failed: false
        }
    }

    /// A person has something to do here: a question, an open verdict, a contested one.
    var needsYou: Bool {
        if case .you = turn { return true }
        return false
    }

    // MARK: - Deriving

    static func derive(
        summary: RunSummary?,
        detail: RunDetail?,
        messages: [Message]?,
        steps: [Step]?,
        verdict: VerdictState?,
        now: Date
    ) -> RunFacts {
        let started = detail?.createdAt ?? summary?.createdAt ?? now
        let events = (messages ?? []).filter { $0.from == .system }
        let machine = detail?.machine
        // The detail is newer than the list whenever both are held.
        let status: RunStatus = detail?.status ?? summary?.status ?? .unknown("")
        let destroyedAt = detail?.destroyedAt ?? summary?.destroyedAt

        // Held records beat the list's copy, whose older daemons counted frames too.
        var last = started
        if steps != nil || messages != nil {
            for at in [steps?.last?.at, messages?.last?.at].compactMap({ $0 }) { last = max(last, at) }
        } else if let summarised = summary?.lastActivity {
            last = max(last, summarised)
        }

        let openVerdict = verdict.flatMap { $0.status == .none ? nil : $0 }

        let phase: Phase
        switch status {
        case .booting where machine != nil || detail == nil:
            phase = .booting
        case .ready where machine != nil || detail == nil:
            phase = now.timeIntervalSince(last) >= idleAfter ? .idle : .live
        case .failed:
            phase = .failed(machine?.error ?? events.last { $0.text.hasPrefix("machine failed") }?.text)
        default:
            phase = .ended(ending(events))
        }

        let alive: Bool
        switch phase {
        case .booting, .live, .idle: alive = true
        case .ended, .failed: alive = false
        }

        var destroyed = destroyedAt != nil
        if case .ended(.destroyed) = phase { destroyed = true }

        let failures = (steps ?? []).filter { $0.outcome.isFailure }.map(\.seq)
        return RunFacts(
            phase: phase,
            turn: turn(messages: messages ?? [], verdict: openVerdict, alive: alive),
            started: started,
            ended: alive ? nil : (destroyedAt ?? last),
            lastActivity: last,
            lastStep: steps?.last,
            stepCount: steps?.count ?? summary?.steps ?? 0,
            failures: failures,
            messageCount: messages?.count ?? summary?.messages ?? 0,
            verdict: openVerdict,
            machineReady: machine?.status == .ready,
            verifierListens: !destroyed
        )
    }

    /// Why a machine is gone, from the lifecycle lines the daemon wrote (`main.go`).
    static func ending(_ events: [Message]) -> Ending {
        for event in events.reversed() {
            let text = event.text
            if text.hasPrefix("human destroyed") { return .destroyed(byYou: true) }
            if text.hasPrefix("machine stopped") { return .lost(detail(of: text)) }
            if text.hasPrefix("machine destroyed") {
                // The daemon writes both lines for a human destroy; the specific one wins.
                if events.contains(where: { $0.text.hasPrefix("human destroyed") }) { return .destroyed(byYou: true) }
                return .destroyed(byYou: false)
            }
        }
        return .finished
    }

    private static func detail(of text: String) -> String? {
        guard let colon = text.firstIndex(of: ":") else { return nil }
        let rest = text[text.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        return rest.isEmpty ? nil : rest
    }

    /// The last word decides whose move it is (ADR 0006).
    static func turn(messages: [Message], verdict: VerdictState?, alive: Bool) -> Turn {
        if verdict?.status == .contested { return .you("Only you can close the verdict") }
        if let question = messages.last(where: { $0.kind == .question && $0.from == .verifier }),
           !messages.contains(where: { $0.kind == .answer && $0.replyTo == question.seq }),
           question.seq == messages.last(where: { $0.from == .verifier && $0.kind != .progress })?.seq {
            return .you("The verifier asked a question")
        }
        if verdict?.status == .proposed { return .you("The verdict needs review") }
        guard alive else { return .nobody }
        if RunStore.awaitingVerifier(messages) { return .verifier }
        guard let last = messages.last(where: { $0.from != .system && $0.kind != .progress }) else { return .nobody }
        return last.from == .verifier ? .coder : .nobody
    }
}

// MARK: - Words

extension RunFacts {
    /// The one-line state in the sidebar row: worded, never a bare glyph.
    /// The same words as the verdict card's headline (`VerdictReview.state`), so the list
    /// and the card never describe one verdict two ways.
    func rowStatus(now: Date) -> (text: String, tone: Tone) {
        if case .you(let why) = turn {
            // The outcome the verifier proposes, first: how carefully to review depends on
            // it (companion ADR 0012). The state is the same word the card's headline has.
            if let verdict, verdict.status == .proposed || verdict.status == .contested {
                let state = verdict.status == .proposed ? "needs review" : "contested"
                return ("\(Chrome.outcomeTitle(verdict.verdict)), \(state)", .attention)
            }
            return (why.contains("question") ? "Question for you" : "Needs you", .attention)
        }
        switch phase {
        case .booting: return ("Booting", .neutral)
        case .live: return ("Live", .live)
        case .idle: return ("Idle \(Chrome.span(idle(now: now)))", .attention)
        case .failed: return ("Failed to boot", .failure)
        case .ended(let ending):
            if let verdict {
                // An outcome nobody reviewed is not a pass yet: it keeps the outcome's
                // word but not its colour.
                let reviewed = verdict.status == .accepted && verdict.acceptedBy == .human
                let tone = verdict.status == .rejected ? .quiet : reviewed ? Tone.outcome(verdict.verdict) : .unsure
                return (Chrome.verdictLine(verdict), tone)
            }
            if case .lost = ending { return ("Machine lost", .failure) }
            return ("No verdict", .quiet)
        }
    }

    /// Colours each mean one thing (design spec, "Colour").
    enum Tone: Equatable, Sendable {
        /// `neutral` is work in progress (booting); `unsure` an inconclusive outcome.
        case pass, failure, attention, live, neutral, unsure, quiet

        static func outcome(_ verdict: String?) -> Tone {
            switch verdict {
            case "pass": .pass
            case "fail": .failure
            default: .unsure
            }
        }
    }
}

// MARK: - Steps

/// What a step came to. A tool error means the call itself failed, so its output is not
/// a result; a non-zero exit means the command ran and failed.
enum StepOutcome: Equatable, Sendable {
    case ok
    case exit(Int)
    case error(String)

    var isFailure: Bool { self != .ok }
}

extension Step {
    var outcome: StepOutcome {
        if let error, !error.isEmpty { return .error(error) }
        if case .int(let code)? = output?["exitCode"], code != 0 { return .exit(code) }
        return .ok
    }

    /// A command that can destroy data or change the machine for good.
    var isRisky: Bool {
        guard tool == "machine_exec", let command = input?["command"]?.stringValue else { return false }
        return StepRisk.isRisky(command)
    }
}

enum StepRisk {
    private static let patterns = [
        "rm -rf", "rm -fr", "rm -r ", "sudo ", "mkfs", "dd if=", "diskutil erase", "chmod -r 777",
        "git push --force", "git reset --hard", "killall", "shutdown", "reboot", "> /dev/",
    ]

    static func isRisky(_ command: String) -> Bool {
        let flat = " " + StepSummary.oneLine(command).lowercased() + " "
        return patterns.contains { flat.contains($0) }
    }
}
