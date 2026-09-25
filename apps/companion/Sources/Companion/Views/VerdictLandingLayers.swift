import SwiftUI

// The verdict-lands moment's effect layers (ADR 0006 moment 3): the outcome decoding in
// over the card's real outcome text, and the card's border drawing itself. Each takes no
// clicks and is hidden from VoiceOver, which reads the real text under it. The math is
// `VerdictDecode` and `GlyphMotion.progress`; timings are `tokens.json` `motion`.

/// The outcome word scrambling and settling left to right over the real one: the same
/// face, size and tracking, on the card's own ground, so each settled cell sits exactly on
/// the character under it. Settled letters take the outcome's colour (pass emerald, fail
/// red), still-scrambling ones are dim. Gone once the word has settled.
struct VerdictDecodeLayer: View {
    let text: String
    let moment: VerdictMoment
    let settled: Color
    let scrambling: Color
    let ground: Color

    @Environment(\.momentFreeze) private var freeze

    private var tokens: DesignTokens.Motion.Decode { DesignData.shared.tokens.motion.decode }

    var body: some View {
        TimelineView(.animation(minimumInterval: Double(tokens.frameMs) / 1000, paused: freeze != nil)) { context in
            let elapsed = (freeze ?? context.date.timeIntervalSince(moment.start)) * 1000
            if elapsed < Double(tokens.maxMs) + Double(tokens.frameMs) {
                let cells = VerdictDecode.frame(text, elapsedMs: elapsed, maxMs: tokens.maxMs,
                                                frameMs: tokens.frameMs, seed: UInt64(moment.seq))
                cells.reduce(Text(verbatim: "")) { line, cell in
                    line + Text(verbatim: String(cell.character)).foregroundStyle(cell.settled ? settled : scrambling)
                }
                .font(Typeface.monoBold.font(size: TypeScale.title))
                .tracking(1.5)
                .fixedSize()
                .background(ground)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

/// The card's edge tracing itself around the card, from its top-left corner, in the edge's
/// own colour: at the end it is the card's border, so nothing jumps when the layer goes.
struct VerdictBorderDrawLayer: View {
    let moment: VerdictMoment
    let color: Color
    var radius: CGFloat = Radius.md

    @Environment(\.momentFreeze) private var freeze

    private var tokens: DesignTokens.Motion.Draw { DesignData.shared.tokens.motion.draw }

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 60, paused: freeze != nil)) { context in
            let elapsed = freeze ?? context.date.timeIntervalSince(moment.start)
            let progress = GlyphMotion.progress(elapsed: elapsed, ms: tokens.maxMs)
            let shape = CardOutline(radius: radius, inset: Space.hairline / 2)
            // Both ways round from the top-left corner, meeting at the bottom-right one.
            ZStack {
                shape.trim(from: 0, to: progress / 2)
                    .stroke(color, lineWidth: Space.hairline)
                shape.trim(from: 1 - progress / 2, to: 1)
                    .stroke(color, lineWidth: Space.hairline)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

/// A card's rounded outline as one path that starts halfway round its top-left corner and
/// runs clockwise, so trimming it from both ends draws it outward from that corner. The
/// panel's own border is a stroke inside its edge; `inset` puts this one on the same line.
struct CardOutline: Shape {
    var radius: CGFloat
    var inset: CGFloat

    func path(in rect: CGRect) -> Path {
        let box = rect.insetBy(dx: inset, dy: inset)
        let r = max(0, min(radius - inset, min(box.width, box.height) / 2))
        var path = Path()
        let corner = CGPoint(x: box.minX + r, y: box.minY + r)
        path.move(to: CGPoint(x: corner.x - r * cos(.pi / 4), y: corner.y - r * sin(.pi / 4)))
        path.addArc(center: corner, radius: r, startAngle: .degrees(225), endAngle: .degrees(270), clockwise: false)
        path.addLine(to: CGPoint(x: box.maxX - r, y: box.minY))
        path.addArc(center: CGPoint(x: box.maxX - r, y: box.minY + r), radius: r,
                    startAngle: .degrees(270), endAngle: .degrees(0), clockwise: false)
        path.addLine(to: CGPoint(x: box.maxX, y: box.maxY - r))
        path.addArc(center: CGPoint(x: box.maxX - r, y: box.maxY - r), radius: r,
                    startAngle: .degrees(0), endAngle: .degrees(90), clockwise: false)
        path.addLine(to: CGPoint(x: box.minX + r, y: box.maxY))
        path.addArc(center: CGPoint(x: box.minX + r, y: box.maxY - r), radius: r,
                    startAngle: .degrees(90), endAngle: .degrees(180), clockwise: false)
        path.addLine(to: CGPoint(x: box.minX, y: box.minY + r))
        path.addArc(center: corner, radius: r, startAngle: .degrees(180), endAngle: .degrees(225), clockwise: false)
        return path
    }
}
