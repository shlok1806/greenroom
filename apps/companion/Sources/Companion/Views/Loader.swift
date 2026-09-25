import SwiftUI

/// A pane waiting on long work (ADR 0006 `load`): the 3 x 3 wavefront, a shimmering label
/// and the time since the work began, in tabular mono digits. For a machine booting, the
/// live screen connecting, the verifier working, the first connection to the daemon.
///
/// Ported as behaviour from Beautiful UI's LoadingState, Drive variant (MIT, (c) 2026
/// Shane Levine, github.com/slev12397/beautiful-ui); the timings are `tokens.json`
/// `motion.loader` and the math is `LoaderMotion`. Reduce Motion freezes the grid at rest
/// and stills the label; the timer keeps ticking. The grid and the band are effects over
/// real text: VoiceOver reads the label and the time in words.
struct Loader: View {
    let label: String
    /// When the work began: the timer counts from here, not from when the view appeared.
    let since: Date
    /// Ink for words on another ground (the screen's well is dark in every theme).
    var ink: Color?
    var dim: Color?

    @State private var appeared = Date()
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.theme) private var theme

    private var tokens: DesignTokens.Motion.Loader { DesignData.shared.tokens.motion.loader }

    var body: some View {
        let ink = ink ?? theme.foreground
        let dim = dim ?? theme.dim
        TimelineView(schedule) { context in
            let now = context.date
            let running = now.timeIntervalSince(appeared)
            HStack(spacing: Space.s) {
                LoaderGrid(elapsedMs: running * 1000, frozen: reduceMotion, tokens: tokens, ink: ink)
                ShimmerLabel(text: label, center: reduceMotion ? nil : LoaderMotion.shimmerCenter(elapsed: running),
                             ink: ink, dim: dim)
                Text(LoaderMotion.elapsed(now.timeIntervalSince(since)))
                    .monoStyle(size: TypeScale.small)
                    .monospacedDigit()
                    .foregroundStyle(dim)
                    .fixedSize()
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(label)
            .accessibilityValue(LoaderMotion.spokenElapsed(now.timeIntervalSince(since)))
        }
        .accessibilityAddTraits(.updatesFrequently)
    }

    /// Frame-rate while it moves; only the timer's tick under Reduce Motion.
    private var schedule: LoaderSchedule {
        reduceMotion ? .tick(TimeInterval(tokens.elapsedTickMs) / 1000) : .moving
    }
}

/// `TimelineView` takes one schedule type; this is either of the two the loader needs.
private enum LoaderSchedule: TimelineSchedule {
    case moving
    case tick(TimeInterval)

    func entries(from startDate: Date, mode: TimelineScheduleMode) -> AnyIterator<Date> {
        switch self {
        case .moving:
            let entries = AnimationTimelineSchedule(minimumInterval: 1.0 / 30).entries(from: startDate, mode: mode)
            var iterator = entries.makeIterator()
            return AnyIterator { iterator.next() }
        case .tick(let interval):
            var iterator = PeriodicTimelineSchedule(from: startDate, by: interval).entries(from: startDate, mode: mode).makeIterator()
            return AnyIterator { iterator.next() }
        }
    }
}

/// The 3 x 3 grid: square cells that rest dim and light in a wave.
private struct LoaderGrid: View {
    let elapsedMs: Double
    let frozen: Bool
    let tokens: DesignTokens.Motion.Loader
    let ink: Color

    /// One cell and the gap after it, in points: LoadingState's 4 px cells and 1.5 px gaps.
    private static let cell: CGFloat = 4
    private static let gap: CGFloat = 1.5

    var body: some View {
        let grid = tokens.grid
        let side = CGFloat(grid) * Self.cell + CGFloat(max(grid - 1, 0)) * Self.gap
        Canvas { context, _ in
            for row in 0..<grid {
                for column in 0..<grid {
                    let level = LoaderMotion.level(row: row, column: column, elapsedMs: elapsedMs, tokens: tokens, frozen: frozen)
                    let rect = CGRect(x: CGFloat(column) * (Self.cell + Self.gap), y: CGFloat(row) * (Self.cell + Self.gap),
                                      width: Self.cell, height: Self.cell)
                    context.fill(Path(roundedRect: rect, cornerRadius: 1), with: .color(ink.opacity(level)))
                }
            }
        }
        .frame(width: side, height: side)
        .accessibilityHidden(true)
    }
}

/// The label, with a highlight band sweeping across it (`center` is the band's place
/// across the label, 0 to 1); still, in the ink, when `center` is nil.
private struct ShimmerLabel: View {
    let text: String
    let center: Double?
    let ink: Color
    let dim: Color

    var body: some View {
        Text(text)
            .monoStyle(.monoMedium, size: TypeScale.small)
            .lineLimit(1)
            .foregroundStyle(style)
    }

    private var style: AnyShapeStyle {
        guard let center else { return AnyShapeStyle(ink) }
        // The gradient spans twice the label, centred on the band, so the band's width
        // is a fixed share of the label wherever it is.
        let half = LoaderMotion.shimmerHalfWidth / 2
        let gradient = Gradient(stops: [
            .init(color: dim, location: 0),
            .init(color: dim, location: 0.5 - half),
            .init(color: ink, location: 0.5),
            .init(color: dim, location: 0.5 + half),
            .init(color: dim, location: 1),
        ])
        return AnyShapeStyle(LinearGradient(gradient: gradient, startPoint: UnitPoint(x: center - 1, y: 0.5),
                                            endPoint: UnitPoint(x: center + 1, y: 0.5)))
    }
}
