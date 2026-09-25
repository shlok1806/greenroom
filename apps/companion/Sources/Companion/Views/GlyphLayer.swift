import AppKit
import SwiftUI

// The signature moments' effect layers over the screen's picture (ADR 0006): the boot
// reveal, the power-down and the click marks. Each is a Canvas over the real picture,
// never the content itself: hidden from VoiceOver, taking no clicks, and gone under
// Reduce Motion (the marks hold still instead). Timings are `tokens.json` `motion`; the
// math is `GlyphMotion` and `ClickMarks`.

/// The mono cell the glyphs and the marks are drawn on: one Monaspace Neon character at
/// the grid's size (`tokens.json` `cell`).
enum GlyphCell {
    @MainActor static let size: CGSize = {
        let font = Typeface.monoRegular.nsFont(size: TypeScale.mono)
        let width = ("0" as NSString).size(withAttributes: [.font: font]).width
        return CGSize(width: max(1, width.rounded()), height: (TypeScale.mono * TypeScale.monoLineHeight).rounded())
    }()
}

/// A glyph rendering drawn once, off the main actor, as a picture: what the moment layer
/// reveals or dissolves cell by cell.
final class GlyphStill: @unchecked Sendable {
    let image: CGImage
    let columns: Int
    let rows: Int

    init(image: CGImage, columns: Int, rows: Int) {
        self.image = image
        self.columns = columns
        self.rows = rows
    }

    /// Where the picture for a moment comes from: the live screen's layer (read on its
    /// own queue) or a recording frame already decoded.
    enum Source: Sendable {
        case live(VideoOutput)
        case frame(Frame)

        /// An `NSImage` crosses to the sampling task only to be drawn there.
        struct Frame: @unchecked Sendable {
            let image: NSImage
        }
    }

    /// Samples the picture and draws its glyphs, entirely off the main actor. `nil` when
    /// there is no picture to read yet.
    static func make(from source: Source, columns: Int, rows: Int, cell: CGSize, scale: CGFloat, ground: RGB) async -> GlyphStill? {
        let picture: CGImage?
        switch source {
        case .live(let output):
            // The layer shows its first frame a moment after the stream says it plays.
            var found: CGImage?
            for _ in 0..<6 {
                found = await output.still()
                if found != nil || Task.isCancelled { break }
                try? await Task.sleep(for: .milliseconds(40))
            }
            picture = found
        case .frame(let frame):
            picture = await Task.detached(priority: .userInitiated) {
                frame.image.cgImage(forProposedRect: nil, context: nil, hints: nil)
            }.value
        }
        guard let picture else { return nil }
        return await Task.detached(priority: .userInitiated) { () -> GlyphStill? in
            guard let rendering = GlyphSampler.sample(picture, columns: columns, rows: rows),
                  let image = GlyphRaster.draw(rendering, cell: cell, scale: scale, ground: ground) else { return nil }
            return GlyphStill(image: image, columns: rendering.columns, rows: rendering.rows)
        }.value
    }
}

/// Draws a glyph rendering: per cell, the well's ground, a faint mosaic of the cell's
/// colour so no cell reads as a hole, and its braille dots in the cell's colour, lifted
/// so dark pictures still read.
enum GlyphRaster {
    static func draw(_ rendering: GlyphRendering, cell: CGSize, scale: CGFloat, ground: RGB) -> CGImage? {
        let width = Int((CGFloat(rendering.columns) * cell.width * scale).rounded())
        let height = Int((CGFloat(rendering.rows) * cell.height * scale).rounded())
        guard width > 0, height > 0, let context = CGContext(
            data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
            space: CGColorSpace(name: CGColorSpace.sRGB) ?? CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
        ) else { return nil }
        // Top-left origin, in points.
        context.translateBy(x: 0, y: CGFloat(height))
        context.scaleBy(x: scale, y: -scale)
        context.setFillColor(red: ground.red, green: ground.green, blue: ground.blue, alpha: 1)
        context.fill(CGRect(x: 0, y: 0, width: CGFloat(rendering.columns) * cell.width, height: CGFloat(rendering.rows) * cell.height))

        let dot = max(1.5, (cell.width / 4).rounded())
        let columnsX = [cell.width * 0.3, cell.width * 0.7]
        let top = cell.height * 0.14
        let step = cell.height * 0.72 / 3
        for row in 0..<rendering.rows {
            for column in 0..<rendering.columns {
                let index = row * rendering.columns + column
                let color = rendering.colors[index]
                let origin = CGPoint(x: CGFloat(column) * cell.width, y: CGFloat(row) * cell.height)
                context.setFillColor(red: color.red, green: color.green, blue: color.blue, alpha: 0.3)
                context.fill(CGRect(origin: origin, size: cell))
                let bits = rendering.dots[index]
                guard bits != 0 else { continue }
                let peak = max(0.05, max(color.red, max(color.green, color.blue)))
                let lift = max(1.2, 0.85 / peak)
                context.setFillColor(red: min(1, color.red * lift), green: min(1, color.green * lift),
                                     blue: min(1, color.blue * lift), alpha: 1)
                for dy in 0..<GlyphSampler.dotsDown {
                    for dx in 0..<GlyphSampler.dotsAcross where bits & (1 << GlyphSampler.dotBit[dy][dx]) != 0 {
                        let x = (origin.x + columnsX[dx] - dot / 2).rounded()
                        let y = (origin.y + top + step * CGFloat(dy) - dot / 2).rounded()
                        context.fill(CGRect(x: x, y: y, width: dot, height: dot))
                    }
                }
            }
        }
        return context.makeImage()
    }
}

/// One signature moment on the well: what it is, the picture's shape, its glyphs once
/// drawn, and when it began (set once the glyphs are ready, so the timing is honest).
struct ScreenMoment {
    let id = UUID()
    let kind: GlyphMotion.Kind
    let picture: CGSize
    var still: GlyphStill?
    var start: Date?
}

/// Signature moments frozen at a point, for design review: the snapshot harness renders
/// a moment mid-way. `nil` (always, in the app) lets them run.
private struct MomentFreezeKey: EnvironmentKey {
    static let defaultValue: TimeInterval? = nil
}

extension EnvironmentValues {
    /// Seconds into every signature moment to hold it at (the snapshot harness only).
    var momentFreeze: TimeInterval? {
        get { self[MomentFreezeKey.self] }
        set { self[MomentFreezeKey.self] = newValue }
    }
}

/// The boot reveal or the power-down over the picture: the glyph still clipped to the
/// cells that show glyphs now. Before the still is drawn a reveal veils the picture in the
/// well's ground, so the picture never shows before it resolves.
struct GlyphMomentLayer: View {
    let moment: ScreenMoment
    var ground: Color

    @Environment(\.momentFreeze) private var freeze

    private var tokens: DesignTokens.Motion { DesignData.shared.tokens.motion }

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 60, paused: moment.start == nil || freeze != nil)) { context in
            let elapsed = freeze ?? moment.start.map { context.date.timeIntervalSince($0) } ?? 0
            let phase = phase(elapsed: elapsed)
            Canvas { graphics, size in
                draw(in: &graphics, size: size, phase: phase)
            }
            .saturation(phase.saturation)
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    private struct Phase {
        /// Which cells show glyphs: those whose order is at or past `threshold` (a reveal)
        /// or below it (a power-down).
        var threshold: Double
        var saturation: Double
    }

    private func phase(elapsed: TimeInterval) -> Phase {
        switch moment.kind {
        case .reveal:
            return Phase(threshold: GlyphMotion.progress(elapsed: elapsed, ms: tokens.bootReveal.ms), saturation: 1)
        case .powerDown:
            switch GlyphMotion.powerDown(elapsed: elapsed, dissolveMs: tokens.powerDown.dissolveMs, holdMs: tokens.powerDown.holdMs) {
            case .dissolving(let progress): return Phase(threshold: progress, saturation: 1)
            // The still drains its colour as it holds, then the recording shows.
            case .still(let progress): return Phase(threshold: 1, saturation: 1 - 0.9 * progress)
            case .done: return Phase(threshold: 1, saturation: 0.1)
            }
        }
    }

    private func draw(in graphics: inout GraphicsContext, size: CGSize, phase: Phase) {
        let fitted = ScreenGeometry.fitted(image: moment.picture, in: size)
        guard fitted.width > 0 else { return }
        guard let still = moment.still else {
            if moment.kind == .reveal { graphics.fill(Path(fitted), with: .color(ground)) }
            return
        }
        let cellWidth = fitted.width / CGFloat(still.columns)
        let cellHeight = fitted.height / CGFloat(still.rows)
        var cells = Path()
        for row in 0..<still.rows {
            for column in 0..<still.columns {
                let order = GlyphMotion.order(column: column, row: row, columns: still.columns, rows: still.rows)
                let shows = moment.kind == .reveal
                    ? GlyphMotion.coveredDuringReveal(order: order, progress: phase.threshold)
                    : GlyphMotion.dissolved(order: order, progress: phase.threshold)
                guard shows else { continue }
                cells.addRect(CGRect(x: fitted.minX + CGFloat(column) * cellWidth, y: fitted.minY + CGFloat(row) * cellHeight,
                                     width: cellWidth, height: cellHeight))
            }
        }
        guard !cells.isEmpty else { return }
        graphics.clip(to: cells)
        graphics.draw(Image(decorative: still.image, scale: 1), in: fitted)
    }
}

/// Where the agent clicked or typed (ADR 0006 decision 5): a transient ripple of cells
/// for each fresh step, and a still mark on the step a paused recording shows. Placed
/// through `ScreenGeometry`, on the same letterbox as the picture and `InputSurface`.
struct ClickMarkLayer: View {
    struct Ripple: Identifiable, Equatable {
        let id = UUID()
        let fraction: CGPoint
        let start: Date
    }

    let picture: CGSize
    let ripples: [Ripple]
    /// A paused recording's step with a click: held still.
    let still: CGPoint?

    @Environment(\.theme) private var theme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var tokens: DesignTokens.Motion.ClickMark { DesignData.shared.tokens.motion.clickMark }

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 60, paused: ripples.isEmpty || reduceMotion)) { context in
            Canvas { graphics, size in
                draw(in: &graphics, size: size, now: context.date)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    private func draw(in graphics: inout GraphicsContext, size: CGSize, now: Date) {
        let fitted = ScreenGeometry.fitted(image: picture, in: size)
        guard fitted.width > 0 else { return }
        graphics.clip(to: Path(fitted))
        let ink = theme.color(.live)
        // A dark rim under the ink, so the mark reads on a light picture and a dark one.
        let halo = Color.black.opacity(0.35)
        let cell = GlyphCell.size
        if let still, let center = ClickMarks.cell(at: still, image: picture, view: size, cell: cell) {
            drawStill(center, ink: ink, halo: halo, in: &graphics)
        }
        for ripple in ripples {
            guard let frame = ClickMarks.ripple(age: now.timeIntervalSince(ripple.start), ms: tokens.ms, rings: tokens.rings,
                                                reduceMotion: reduceMotion),
                  let center = ClickMarks.cell(at: ripple.fraction, image: picture, view: size, cell: cell) else { continue }
            if frame.ring > 0 {
                var ring = Path()
                for box in ClickMarks.ring(frame.ring, around: center) { ring.addRect(box.insetBy(dx: 0.5, dy: 0.5)) }
                graphics.stroke(ring, with: .color(halo.opacity(frame.opacity)), lineWidth: 3)
                graphics.stroke(ring, with: .color(ink.opacity(frame.opacity)), lineWidth: 1.5)
            }
            graphics.stroke(Path(center.insetBy(dx: -0.5, dy: -0.5)), with: .color(halo.opacity(frame.opacity)), lineWidth: 2)
            graphics.fill(Path(center), with: .color(ink.opacity(0.85 * frame.opacity)))
        }
    }

    /// Corner brackets one cell's width out from the cell clicked, the cell itself tinted:
    /// close enough to name the control, clear of its neighbours.
    private func drawStill(_ center: CGRect, ink: Color, halo: Color, in graphics: inout GraphicsContext) {
        let box = center.insetBy(dx: -center.width, dy: -center.width).insetBy(dx: 0.5, dy: 0.5)
        let arm = center.width
        var brackets = Path()
        for (x, y, sx, sy) in [(box.minX, box.minY, 1.0, 1.0), (box.maxX, box.minY, -1.0, 1.0),
                               (box.minX, box.maxY, 1.0, -1.0), (box.maxX, box.maxY, -1.0, -1.0)] {
            brackets.move(to: CGPoint(x: x + sx * arm, y: y))
            brackets.addLine(to: CGPoint(x: x, y: y))
            brackets.addLine(to: CGPoint(x: x, y: y + sy * arm))
        }
        graphics.stroke(brackets, with: .color(halo), style: StrokeStyle(lineWidth: 3, lineCap: .square))
        graphics.stroke(brackets, with: .color(ink), style: StrokeStyle(lineWidth: 1.5, lineCap: .square))
        graphics.fill(Path(center), with: .color(ink.opacity(0.35)))
        graphics.stroke(Path(center.insetBy(dx: 0.5, dy: 0.5)), with: .color(ink), lineWidth: 1)
    }
}
