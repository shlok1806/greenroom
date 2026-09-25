import Foundation

/// The steps read as a thinking trace: each row in plain words, with one state glyph.
///
/// Ported as behaviour from Beautiful UI's ThinkingState, Steps variant (MIT, (c) 2026
/// Shane Levine, github.com/slev12397/beautiful-ui): while the agent works, the newest row
/// ticks; finished rows settle to a muted check; the trace stays expandable. Ours adds the
/// failure, which ThinkingState has no word for: `✗` and the word in the failure role.
///
/// The daemon records a step when its tool call ends, so no recorded step is ever still
/// running. What runs is the verifier's next call: while the verifier has the turn on a
/// live machine, the trace ends in a running row that ticks (`working`), and the one-row
/// track ends in a running cell. Pure; `StepTraceTests`.
enum StepTrace {
    enum State: Equatable, Sendable {
        /// The tool call ended and did what it was asked: a muted check.
        case done
        /// The call failed, or its command exited non-zero: `✗` and the word.
        case failed(word: String)
        /// The verifier is at work on its next call: the tick.
        case running
    }

    /// How a recorded step ended.
    static func state(of step: Step) -> State {
        switch step.outcome {
        case .ok: .done
        case .exit(let code): .failed(word: "exit \(code)")
        case .error: .failed(word: "error")
        }
    }

    /// The glyph a settled state draws; the running state draws the spinner instead.
    static func glyph(_ state: State) -> String? {
        switch state {
        case .done: "✓"
        case .failed: "✗"
        case .running: nil
        }
    }

    /// The same state for VoiceOver, in words.
    static func spoken(_ state: State) -> String {
        switch state {
        case .done: "done"
        case .failed(let word): "errored, \(word)"
        case .running: "working"
        }
    }

    /// One recorded step's row: its number, the state, what it did as a sentence and at
    /// most two facts (the failure's word, how long it took).
    struct Row: Equatable, Sendable {
        var seq: Int
        var state: State
        var words: String
        var duration: String
        /// The step the screen is showing.
        var atPlayhead: Bool
        var risky: Bool

        /// The words take the failure role only when the step failed.
        var failed: Bool {
            if case .failed = state { return true }
            return false
        }
    }

    static func row(for step: Step, in steps: [Step], playhead: Int?) -> Row {
        Row(seq: step.seq, state: state(of: step), words: StepSummary.phrase(for: step, in: steps),
            duration: Chrome.duration(step.durationMs), atPlayhead: step.seq == playhead, risky: step.isRisky)
    }

    /// Who is at work while the trace ticks, and since when.
    struct Working: Equatable, Sendable {
        var words: String
        var since: Date
    }

    /// The verifier has the turn on a live machine: its next call is running. It started
    /// with the newest step or message, whichever is later. Nothing runs otherwise: not
    /// while booting (the screen says so), idle, ended, or waiting on the coding agent or
    /// on you.
    static func working(_ facts: RunFacts) -> Working? {
        guard facts.phase == .live, facts.turn == .verifier else { return nil }
        return Working(words: "The verifier is working", since: facts.lastActivity)
    }

    /// One cell of the one-row track: the same states as the rows.
    struct Cell: Equatable, Sendable {
        /// Nil for the running cell, which has no step yet.
        var seq: Int?
        var state: State
        /// Drawn full height in the foreground: the step at the playhead (or hovered).
        var current: Bool
    }

    static func cells(_ steps: [Step], current: Int?, working: Bool) -> [Cell] {
        var out = steps.map { Cell(seq: $0.seq, state: state(of: $0), current: $0.seq == current) }
        if working { out.append(Cell(seq: nil, state: .running, current: false)) }
        return out
    }

    /// "26 steps", "26 steps, 2 errored": the trace's header once it has settled.
    static func summary(count: Int, failures: Int) -> String {
        let steps = Chrome.plural(count, "step")
        return failures > 0 ? "\(steps), \(failures) errored" : steps
    }
}
