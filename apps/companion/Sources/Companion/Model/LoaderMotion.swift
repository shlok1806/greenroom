import Foundation

/// The `load` motion (ADR 0006) as pure functions of time, so the view only draws.
///
/// Ported as behaviour from Beautiful UI's LoadingState, Drive variant (MIT, (c) 2026
/// Shane Levine, github.com/slev12397/beautiful-ui): a 3 x 3 grid whose cells pulse in a
/// chevron wavefront driving right, each column and each row away from the middle one
/// step later, on a cycle shorter than the sweep so two fronts are always in flight; a
/// label with a highlight band sweeping across it; and an elapsed timer. Timings come from
/// `tokens.json` `motion.loader`. Reduce Motion freezes the grid at rest and stops the
/// band; the timer still ticks, because it is information.
enum LoaderMotion {
    /// Where every cell rests between pulses (LoadingState's `pixel-on` 0% and 62%).
    static let rest = 0.15

    /// When cell (`row`, `column`) starts its pulse, after the loader appears: the middle
    /// row leads and the outer rows trail it, a chevron pointing right.
    static func delayMs(row: Int, column: Int, grid: Int, stepMs: Int) -> Int {
        (column + abs(row - grid / 2)) * stepMs
    }

    /// How far through its own pulse a cell is, 0 to 1; nil before its first pulse.
    static func phase(elapsedMs: Double, delayMs: Int, cycleMs: Int) -> Double? {
        guard cycleMs > 0 else { return nil }
        let local = elapsedMs - Double(delayMs)
        guard local >= 0 else { return nil }
        return local.truncatingRemainder(dividingBy: Double(cycleMs)) / Double(cycleMs)
    }

    /// The cell's level at a point in its pulse: at rest, up to full by 18%, held to 42%,
    /// back to rest by 62%, eased between (`pixel-on`'s keyframes).
    static func level(phase: Double?) -> Double {
        guard let phase else { return rest }
        switch phase {
        case ..<0.18: return rest + (1 - rest) * ease(phase / 0.18)
        case ..<0.42: return 1
        case ..<0.62: return 1 - (1 - rest) * ease((phase - 0.42) / 0.20)
        default: return rest
        }
    }

    /// One cell's level `elapsedMs` after the loader appeared. Frozen, every cell rests.
    static func level(row: Int, column: Int, elapsedMs: Double, tokens: DesignTokens.Motion.Loader,
                      frozen: Bool) -> Double {
        guard !frozen else { return rest }
        let delay = delayMs(row: row, column: column, grid: tokens.grid, stepMs: tokens.stepMs)
        return level(phase: phase(elapsedMs: elapsedMs, delayMs: delay, cycleMs: tokens.cycleMs))
    }

    /// CSS `ease-in-out` between keyframes, as a smoothstep.
    static func ease(_ x: Double) -> Double {
        let t = min(max(x, 0), 1)
        return t * t * (3 - 2 * t)
    }

    // MARK: - The label's shimmer

    /// One sweep of the highlight band across the label (`shimmer-text`, 1.4 s, linear).
    static let shimmerPeriod: TimeInterval = 1.4
    /// Half the band's width, as a share of the label.
    static let shimmerHalfWidth = 0.15

    /// The band's centre across the label, from just before its leading edge to just past
    /// its trailing one, so the sweep enters and leaves cleanly.
    static func shimmerCenter(elapsed: TimeInterval) -> Double {
        let progress = elapsed.truncatingRemainder(dividingBy: shimmerPeriod) / shimmerPeriod
        let travel = 1 + 4 * shimmerHalfWidth
        return -2 * shimmerHalfWidth + travel * max(progress, 0)
    }

    // MARK: - The elapsed timer

    /// Minutes and seconds, zero-padded so the width holds while it ticks ("00:07",
    /// "12:34"); hours lead once there are any ("1:02:03").
    static func elapsed(_ seconds: TimeInterval) -> String {
        let total = Int(max(0, seconds).rounded(.down))
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        let secs = total % 60
        if hours > 0 { return String(format: "%d:%02d:%02d", hours, minutes, secs) }
        return String(format: "%02d:%02d", minutes, secs)
    }

    /// The timer as VoiceOver reads it: "7 seconds", "1 minute 3 seconds".
    static func spokenElapsed(_ seconds: TimeInterval) -> String {
        let total = Int(max(0, seconds).rounded(.down))
        let parts = [(total / 3600, "hour"), ((total % 3600) / 60, "minute"), (total % 60, "second")]
            .filter { $0.0 > 0 }
            .map { "\($0.0) \($0.1)\($0.0 == 1 ? "" : "s")" }
        return parts.isEmpty ? "0 seconds" : parts.joined(separator: " ")
    }
}
