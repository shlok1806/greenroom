// Loader, shimmer and elapsed timer ported from Beautiful UI LoadingState (MIT, (c) 2026 Shane Levine).
import Foundation

// PROTOTYPE: the motion vocabulary as pure functions of time. Views call these with the
// model's clock; effects never own content, the final text always exists first.

enum FX {
    static let spinnerFrames: [Character] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]

    // MARK: decode

    static let noise = Array("abcdefghkmnprstuvwxyz0123456789#%&*+=?/<>")

    /// Scramble -> settle: characters left of `progress` are final, the rest are noise
    /// that changes every frame. Spaces stay spaces so words hold their shape.
    static func decode(_ text: String, progress: Double, seed: Int) -> String {
        if progress >= 1 { return text }
        let chars = Array(text)
        let settled = Int(Double(chars.count) * max(0, progress))
        var out = ""
        for (i, ch) in chars.enumerated() {
            if i < settled || ch == " " {
                out.append(ch)
            } else {
                let h = abs((i &* 7919) &+ seed &* 104_729) % noise.count
                out.append(noise[h])
            }
        }
        return out
    }

    /// Frame-varying seed for decode noise.
    static func seed(_ now: Double) -> Int { Int(now * 30) }

    // MARK: shimmer

    /// A highlight band sweeping left to right across a label every 1.4 s, as in
    /// LoadingState's `shimmer-text`: ink-3 at the edges, full ink at the band.
    static func shimmer(_ text: String, now: Double, voice: Voice = .chrome, weight: Weight = .medium,
                        ink: Ink = .fg, frozen: Bool) -> GridLine {
        if frozen { return [Span(text, voice, weight, ink: .alpha(ink, 0.7))] }
        let chars = Array(text)
        let n = max(1, Double(chars.count))
        let phase = now.truncatingRemainder(dividingBy: 1.4) / 1.4
        let center = -0.3 + 1.6 * phase
        return chars.enumerated().map { i, ch in
            let u = (Double(i) + 0.5) / n
            let b = max(0, 1 - abs(u - center) / 0.3)
            return Span(String(ch), voice, weight, ink: .alpha(ink, 0.42 + 0.58 * b))
        }
    }

    // MARK: pixel-grid loader

    /// Drive pattern: a chevron wavefront moving right. Cell (r, c) starts
    /// `(c + |r - 1|) * 90` ms into a 650 ms cycle, so two fronts are always in flight.
    static func loaderOpacity(row: Int, col: Int, now: Double) -> Double {
        let delay = Double(col + abs(row - 1)) * 0.090
        let dur = 0.650
        let local = now - delay
        let p = (local.truncatingRemainder(dividingBy: dur) + dur).truncatingRemainder(dividingBy: dur) / dur
        // pixel-on: 0% 0.15, 18%-42% 1, 62%-100% 0.15
        switch p {
        case ..<0.18: return 0.15 + (1 - 0.15) * ease(p / 0.18)
        case ..<0.42: return 1
        case ..<0.62: return 1 - (1 - 0.15) * ease((p - 0.42) / 0.20)
        default: return 0.15
        }
    }

    static func ease(_ x: Double) -> Double { x * x * (3 - 2 * x) }

    /// The 3 x 3 loader as three grid lines of `▪ ▪ ▪` (a space between columns makes the
    /// 8 x 18 cells read as a square lattice). Reduced motion freezes it to its dim state.
    static func loader(now: Double, frozen: Bool, ink: Ink = .fg) -> [GridLine] {
        (0..<3).map { r in
            var line: GridLine = []
            for c in 0..<3 {
                let o = frozen ? 0.15 : loaderOpacity(row: r, col: c, now: now)
                let lit = o > 0.5
                line.append(Span(lit ? "▪" : "▫", .chrome, .regular, ink: .alpha(ink, lit ? o : 0.3)))
                if c < 2 { line.append(Span(" ")) }
            }
            return line
        }
    }

    /// Loader + shimmering label + elapsed timer in tabular mono: three lines, the label on
    /// the middle one. Reduced motion freezes grid and shimmer; the timer still ticks.
    static func working(_ label: String, elapsed: Double, now: Double, frozen: Bool,
                        ink: Ink = .fg, indent: Int = 0) -> [GridLine] {
        let grid = loader(now: now, frozen: frozen, ink: ink)
        let pad = [Span(String(repeating: " ", count: indent))]
        return [
            pad + grid[0],
            pad + grid[1] + [Span("  ")] + shimmer(label, now: now, ink: ink, frozen: frozen)
                + [Span("  "), Span(clock(elapsed), ink: .dim)],
            pad + grid[2],
        ]
    }

    static func clock(_ seconds: Double) -> String {
        let s = max(0, Int(seconds))
        return String(format: "%02d:%02d", s / 60, s % 60)
    }

    // MARK: tick

    static func spinner(_ now: Double, frozen: Bool) -> String {
        if frozen { return "⠶" }
        let f = FX.spinnerFrames
        return String(f[Int(now / 0.08) % f.count])
    }

    // MARK: draw

    /// 0 -> 1 over `duration`, instant under reduced motion.
    static func progress(since start: Double?, now: Double, duration: Double, reduce: Bool) -> Double {
        guard let start else { return 1 }
        if reduce { return 1 }
        return min(1, max(0, (now - start) / duration))
    }
}
