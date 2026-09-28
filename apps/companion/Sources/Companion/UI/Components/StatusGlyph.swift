import SwiftUI

/// One status glyph (Figma Components, Status glyph, 3:35), drawn from the file's 16 pt vectors
/// at any size. Always paired with a word nearby; hidden from VoiceOver, which reads the word.
/// Checking turns once every `Motion.ring`, linear; under Reduce Motion it holds still.
struct StatusGlyph: View {
    var kind: GlyphKind
    var color: ToneColor
    var size: CGFloat = Metrics.glyph

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var turning = false

    var body: some View {
        GlyphShape(kind: kind, color: color.token.color)
            .rotationEffect(.degrees(turning ? 360 : 0))
            .animation(turning ? .linear(duration: Motion.ring).repeatForever(autoreverses: false) : nil, value: turning)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
            .onAppear { turning = kind.turns && !reduceMotion && !SnapshotMode.isActive }
            .onChange(of: kind.turns && !reduceMotion) { _, turns in turning = turns && !SnapshotMode.isActive }
    }
}

/// The glyph's drawing in a 16 x 16 box, scaled to its frame.
private struct GlyphShape: View {
    var kind: GlyphKind
    var color: Color

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
                var tick = Path()
                tick.move(to: CGPoint(x: 4.75, y: 8.25))
                tick.addLine(to: CGPoint(x: 7, y: 10.5))
                tick.addLine(to: CGPoint(x: 11.25, y: 6))
                context.stroke(tick, with: white, style: stroke)
            case .failed:
                context.fill(disc, with: .color(color))
                var cross = Path()
                cross.move(to: CGPoint(x: 5.5, y: 5.5))
                cross.addLine(to: CGPoint(x: 10.5, y: 10.5))
                cross.move(to: CGPoint(x: 10.5, y: 5.5))
                cross.addLine(to: CGPoint(x: 5.5, y: 10.5))
                context.stroke(cross, with: white, style: stroke)
            case .checking:
                let ring = Path(ellipseIn: CGRect(x: 1.5, y: 1.5, width: 13, height: 13))
                context.stroke(ring, with: .color(Palette.border), lineWidth: 2)
                var arc = Path()
                arc.addArc(center: CGPoint(x: 8, y: 8), radius: 6.5, startAngle: .degrees(-90), endAngle: .degrees(0), clockwise: false)
                context.stroke(arc, with: .color(color), style: StrokeStyle(lineWidth: 2, lineCap: .round))
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
                var triangle = Path()
                triangle.move(to: CGPoint(x: 6.7, y: 1.9))
                triangle.addQuadCurve(to: CGPoint(x: 9.3, y: 1.9), control: CGPoint(x: 8, y: 0.15))
                triangle.addLine(to: CGPoint(x: 15.3, y: 12.4))
                triangle.addQuadCurve(to: CGPoint(x: 14, y: 14.65), control: CGPoint(x: 16.2, y: 14.65))
                triangle.addLine(to: CGPoint(x: 2, y: 14.65))
                triangle.addQuadCurve(to: CGPoint(x: 0.7, y: 12.4), control: CGPoint(x: -0.2, y: 14.65))
                triangle.closeSubpath()
                context.fill(triangle, with: .color(color))
                context.fill(Path(roundedRect: CGRect(x: 7.2, y: 5, width: 1.6, height: 5), cornerRadius: 0.8), with: white)
                context.fill(Path(ellipseIn: CGRect(x: 7.05, y: 11.25, width: 1.9, height: 1.9)), with: white)
            }
        }
    }
}
