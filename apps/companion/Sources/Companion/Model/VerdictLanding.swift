import Foundation

/// The verdict-lands signature moment (ADR 0006 moment 3, as amended by 0008): a verdict
/// that arrives while its run is open decodes its outcome in ("✓ PASS", "✗ FAIL"), the
/// card's border draws around it, and on a fail the run moves to the first failing step.
/// Pure: when a verdict counts as landed, what the moment does, and the decode's frames.
/// The layers are in `Views/VerdictCard.swift`; timings are `tokens.json` `motion`.
enum VerdictLanding {
    /// The verdict a run view had on screen: which run, and the verdict's message seq.
    struct OnScreen: Equatable, Sendable {
        let runId: String
        let seq: Int?
    }

    /// Whether the verdict now held for `runId` has just landed. Only one whose message
    /// came in through the event stream while the run's transcript was held
    /// (`arrivedLive`), with a seq past the one on screen for that same run. A run just
    /// opened (nothing on screen for it yet), the list catching up after the run opened,
    /// and a resync that finds a verdict the stream missed all read as already there.
    static func lands(onScreen: OnScreen?, runId: String, current: Int?, arrivedLive: Int?) -> Bool {
        guard let onScreen, onScreen.runId == runId else { return false }
        guard let current, current == arrivedLive else { return false }
        return onScreen.seq.map { current > $0 } ?? true
    }

    /// Where a fail moves the run to.
    enum Focus: Equatable, Sendable {
        /// The first step that errored, through the registry's next-error action.
        case firstFailure
        /// No step errored: the step the verdict cites first, as its evidence opens it.
        case cited(Int)
    }

    /// What the moment does once a verdict lands.
    struct Plan: Equatable, Sendable {
        /// The outcome scrambles and settles; the border draws. Off under Reduce Motion.
        var plays: Bool
        /// Where focus goes, or nil: not a fail, nothing to go to, or the person is typing
        /// or driving (the keys stay where they are).
        var focus: Focus?
        /// What VoiceOver hears, once: the final outcome, never the scramble.
        var announcement: String
    }

    static func plan(outcome: String?, failures: [Int], cited: Int?, reduceMotion: Bool, typing: Bool, driving: Bool) -> Plan {
        Plan(
            plays: GlyphMotion.plays(reduceMotion: reduceMotion),
            focus: focus(outcome: outcome, failures: failures, cited: cited, typing: typing, driving: driving),
            announcement: announcement(outcome: outcome, failedSteps: failures.count)
        )
    }

    static func focus(outcome: String?, failures: [Int], cited: Int?, typing: Bool, driving: Bool) -> Focus? {
        guard outcome == "fail", !typing, !driving else { return nil }
        if !failures.isEmpty { return .firstFailure }
        return cited.map(Focus.cited)
    }

    /// "Verdict: Fail, 2 steps failed"; "Verdict: Pass".
    static func announcement(outcome: String?, failedSteps: Int) -> String {
        let head = "Verdict: \(Chrome.outcomeTitle(outcome))"
        guard outcome == "fail", failedSteps > 0 else { return head }
        return "\(head), \(failedSteps) \(failedSteps == 1 ? "step" : "steps") failed"
    }

    /// How long the moment lasts in all: the longer of the decode and the draw, which
    /// start together.
    static func duration(_ tokens: DesignTokens.Motion) -> TimeInterval {
        Double(max(tokens.decode.maxMs, tokens.draw.maxMs)) / 1000
    }
}

/// One cell of a decoding word: the character it shows now, and whether it is the final one.
struct DecodeCell: Equatable, Sendable {
    let character: Character
    let settled: Bool
}

/// Characters scramble, then settle left to right (ADR 0006 `decode`). Only the ASCII
/// letters scramble: the outcome's mark (✓, ✗) and the spaces stand from the first frame,
/// so the word is never without its mark. Each scrambling glyph is one Monaspace Neon
/// draws at the same advance, so nothing shifts as the word settles. Deterministic: the
/// glyph a cell shows is a hash of the seed, the frame and the cell.
enum VerdictDecode {
    static let glyphs: [Character] = Array("#%&*+=<>?/$@ABCDEFGHJKLMNPRSTUVWXYZ0123456789")

    static func scrambles(_ character: Character) -> Bool {
        character.isASCII && character.isLetter
    }

    /// When the `index`th of `count` scrambling characters settles, in ms: evenly left to
    /// right, the last exactly at `maxMs`.
    static func settlesAt(index: Int, count: Int, maxMs: Int) -> Double {
        guard count > 0 else { return 0 }
        return Double(maxMs) * Double(index + 1) / Double(count)
    }

    /// The word `elapsedMs` into its decode.
    static func frame(_ text: String, elapsedMs: Double, maxMs: Int, frameMs: Int, seed: UInt64) -> [DecodeCell] {
        let characters = Array(text)
        let count = characters.filter(scrambles).count
        let tick = UInt64(max(0, elapsedMs) / Double(max(1, frameMs)))
        var nth = 0
        return characters.enumerated().map { position, character in
            guard scrambles(character) else { return DecodeCell(character: character, settled: true) }
            defer { nth += 1 }
            if elapsedMs >= settlesAt(index: nth, count: count, maxMs: maxMs) {
                return DecodeCell(character: character, settled: true)
            }
            var pick = Int(mix(seed ^ (tick &* 0x9E37_79B9_7F4A_7C15) ^ UInt64(position) << 48) % UInt64(glyphs.count))
            // A glyph that happens to be the final letter would read as settled early.
            if glyphs[pick] == character { pick = (pick + 1) % glyphs.count }
            return DecodeCell(character: glyphs[pick], settled: false)
        }
    }

    /// Every frame from the start to the settled word: one per `frameMs`, the last at `maxMs`.
    static func frames(_ text: String, maxMs: Int, frameMs: Int, seed: UInt64) -> [[DecodeCell]] {
        let step = max(1, frameMs)
        var out = stride(from: 0, to: maxMs, by: step).map {
            frame(text, elapsedMs: Double($0), maxMs: maxMs, frameMs: frameMs, seed: seed)
        }
        out.append(frame(text, elapsedMs: Double(maxMs), maxMs: maxMs, frameMs: frameMs, seed: seed))
        return out
    }

    /// SplitMix64's finaliser: a well-spread hash of one number.
    static func mix(_ value: UInt64) -> UInt64 {
        var z = value &+ 0x9E37_79B9_7F4A_7C15
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}
