import SwiftUI

/// One status glyph (Figma Components, Status glyph, 3:35), drawn from the file's 16 pt vectors
/// at any size. Always paired with a word nearby; hidden from VoiceOver, which reads the word.
/// Checking turns once every `Motion.ring`, linear, its angle worked out from the motion clock
/// (docs/22 C02), so any moment can be drawn; under Reduce Motion it holds still.
struct StatusGlyph: View {
    var kind: GlyphKind
    var color: ToneColor
    var size: CGFloat = Metrics.glyph
    /// Drawn on the primary button's fill: the ring in the button's text colour.
    var onPrimary = false

    /// A text recogniser reads a glyph as a letter; the harness's word count hides them.
    @Environment(\.redactsGuestScreen) private var counting

    var body: some View {
        Group {
            if kind.turns {
                Clocked { seconds in
                    GlyphShape(kind: kind, color: onPrimary ? Palette.onPrimary : color.token.color,
                               track: onPrimary ? Palette.onPrimary.opacity(0.3) : Palette.border)
                        .rotationEffect(.degrees(StatusGlyph.angle(at: seconds)))
                }
            } else {
                GlyphShape(kind: kind, color: color.token.color)
            }
        }
        .frame(width: size, height: size)
        .opacity(counting ? 0 : 1)
        .accessibilityHidden(true)
    }

    /// The checking ring's angle, in degrees, `seconds` into the motion clock.
    static func angle(at seconds: Double) -> Double {
        MotionClock.phase(seconds, period: Motion.ring) * 360
    }
}

/// The glyph's drawing in a 16 x 16 box, scaled to its frame.
private struct GlyphShape: View {
    var kind: GlyphKind
    var color: Color
    var track = Palette.border

    var body: some View {
        Canvas { context, size in
            let s = min(size.width, size.height) / 16
            context.scaleBy(x: s, y: s)
            let white = GraphicsContext.Shading.color(Palette.onStatus)
            let disc = Path(ellipseIn: CGRect(x: 0, y: 0, width: 16, height: 16))
            let stroke = StrokeStyle(lineWidth: 1.75, lineCap: .round, lineJoin: .round)
            switch kind {
            case .passed:
                context.fill(disc, with: .color(color))
                context.stroke(SVGPath.path("M4.75 8.25L7 10.5L11.25 6"), with: white, style: stroke)
            case .failed:
                context.fill(disc, with: .color(color))
                context.stroke(SVGPath.path("M5.5 5.5L10.5 10.5M10.5 5.5L5.5 10.5"), with: white, style: stroke)
            case .checking:
                let ring = Path(ellipseIn: CGRect(x: 1.5, y: 1.5, width: 13, height: 13))
                context.stroke(ring, with: .color(track), lineWidth: 2)
                context.stroke(SVGPath.path("M8 1.5C11.59 1.5 14.5 4.41 14.5 8"), with: .color(color),
                               style: StrokeStyle(lineWidth: 2, lineCap: .round))
            case .pending:
                let ring = Path(ellipseIn: CGRect(x: 1.25, y: 1.25, width: 13.5, height: 13.5))
                context.stroke(ring, with: .color(color), lineWidth: 1.5)
            case .starting:
                let ring = Path(ellipseIn: CGRect(x: 1.5, y: 1.5, width: 13, height: 13))
                context.stroke(ring, with: .color(color), style: StrokeStyle(lineWidth: 2, lineCap: .round, dash: [2.5, 2.6]))
            case .paused:
                context.fill(disc, with: .color(color))
                context.fill(Path(roundedRect: CGRect(x: 5.25, y: 4.75, width: 1.75, height: 6.5), cornerRadius: 0.6), with: white)
                context.fill(Path(roundedRect: CGRect(x: 9, y: 4.75, width: 1.75, height: 6.5), cornerRadius: 0.6), with: white)
            case .stopped:
                context.fill(disc, with: .color(color))
                context.fill(Path(roundedRect: CGRect(x: 5.25, y: 5.25, width: 5.5, height: 5.5), cornerRadius: 1), with: white)
            case .warning:
                context.fill(SVGPath.path("M6.7 1.9C7.28 0.9 8.72 0.9 9.3 1.9L15.3 12.4C15.88 13.4 15.16 14.65 14 14.65H2C0.84 14.65 0.12 13.4 0.7 12.4L6.7 1.9Z"),
                             with: .color(color))
                context.fill(Path(roundedRect: CGRect(x: 7.2, y: 5, width: 1.6, height: 5), cornerRadius: 0.8), with: white)
                context.fill(Path(ellipseIn: CGRect(x: 7.05, y: 11.25, width: 1.9, height: 1.9)), with: white)
            }
        }
    }
}
