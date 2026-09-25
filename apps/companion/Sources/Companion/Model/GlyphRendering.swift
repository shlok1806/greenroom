import CoreGraphics
import Foundation

/// A picture as glyphs, for the boot reveal and the power-down (ADR 0006 signature
/// moments): each cell of a coarse grid holds a braille pattern, 2 x 4 dots from an
/// ordered (Bayer) dither of the picture's luminance, drawn in the cell's average colour.
/// Sampled once per moment, off the main actor, from a picture already decoded. Pure:
/// the pixels are plain bytes, so `GlyphRenderingTests` needs no image.
struct GlyphRendering: Equatable, Sendable {
    let columns: Int
    let rows: Int
    /// Per cell, row-major: the lit braille dots, bit `i` for Unicode dot `i + 1`.
    let dots: [UInt8]
    /// Per cell, row-major: the average colour.
    let colors: [RGB]

    /// The cell's braille character (U+2800 plus its dots).
    func character(column: Int, row: Int) -> Character {
        Character(UnicodeScalar(0x2800 + UInt32(dots[row * columns + column]))!)
    }

    /// The whole rendering as text, a row per line: what a test reads.
    var text: String {
        (0..<rows).map { row in String((0..<columns).map { character(column: $0, row: row) }) }.joined(separator: "\n")
    }
}

enum GlyphSampler {
    /// Dots per cell, across and down (braille).
    static let dotsAcross = 2
    static let dotsDown = 4

    /// The 4 x 4 ordered-dither thresholds, 0 to 1.
    static let bayer: [Double] = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map { (Double($0) + 0.5) / 16 }

    /// Which Unicode dot (bit) sits at (`x`, `y`) of a cell's 2 x 4 lattice.
    static let dotBit: [[UInt8]] = [
        [0, 3], // top row: dots 1 and 4
        [1, 4],
        [2, 5],
        [6, 7], // bottom row: dots 7 and 8
    ]

    /// Samples `rgba` (8-bit RGBA, row-major, top row first, `width` x `height` pixels)
    /// down to `columns` x `rows` cells. Each dot averages the pixels under it; the
    /// luminance is stretched between its 4th and 96th percentiles when they differ
    /// enough, so a mid-grey desktop does not dither to one flat half-tone.
    static func sample(rgba: [UInt8], width: Int, height: Int, columns: Int, rows: Int) -> GlyphRendering? {
        guard columns > 0, rows > 0, width > 0, height > 0, rgba.count >= width * height * 4 else { return nil }
        let across = columns * dotsAcross
        let down = rows * dotsDown
        var luminance = [Double](repeating: 0, count: across * down)
        var dotColor = [(Double, Double, Double)](repeating: (0, 0, 0), count: across * down)
        for y in 0..<down {
            let top = y * height / down
            let bottom = max(top + 1, (y + 1) * height / down)
            for x in 0..<across {
                let left = x * width / across
                let right = max(left + 1, (x + 1) * width / across)
                var red = 0.0, green = 0.0, blue = 0.0
                for py in top..<min(bottom, height) {
                    for px in left..<min(right, width) {
                        let i = (py * width + px) * 4
                        red += Double(rgba[i])
                        green += Double(rgba[i + 1])
                        blue += Double(rgba[i + 2])
                    }
                }
                let count = Double((min(bottom, height) - top) * (min(right, width) - left)) * 255
                let color = (red / count, green / count, blue / count)
                dotColor[y * across + x] = color
                luminance[y * across + x] = 0.2126 * color.0 + 0.7152 * color.1 + 0.0722 * color.2
            }
        }
        let sorted = luminance.sorted()
        let low = sorted[sorted.count * 4 / 100]
        let high = sorted[min(sorted.count - 1, sorted.count * 96 / 100)]
        if high - low >= 0.05 {
            luminance = luminance.map { min(1, max(0, ($0 - low) / (high - low))) }
        }
        var dots = [UInt8](repeating: 0, count: columns * rows)
        var colors = [RGB](repeating: RGB(red: 0, green: 0, blue: 0), count: columns * rows)
        for row in 0..<rows {
            for column in 0..<columns {
                var bits: UInt8 = 0
                var red = 0.0, green = 0.0, blue = 0.0
                for dy in 0..<dotsDown {
                    for dx in 0..<dotsAcross {
                        let x = column * dotsAcross + dx
                        let y = row * dotsDown + dy
                        let index = y * across + x
                        if luminance[index] > bayer[(y % 4) * 4 + (x % 4)] { bits |= 1 << dotBit[dy][dx] }
                        red += dotColor[index].0
                        green += dotColor[index].1
                        blue += dotColor[index].2
                    }
                }
                let n = Double(dotsAcross * dotsDown)
                dots[row * columns + column] = bits
                colors[row * columns + column] = RGB(red: red / n, green: green / n, blue: blue / n)
            }
        }
        return GlyphRendering(columns: columns, rows: rows, dots: dots, colors: colors)
    }

    /// Draws `image` down to one pixel per dot and samples that. Drawing decodes the
    /// picture, so this runs off the main actor (`Task.detached`), never on it.
    static func sample(_ image: CGImage, columns: Int, rows: Int) -> GlyphRendering? {
        let width = columns * dotsAcross
        let height = rows * dotsDown
        guard width > 0, height > 0 else { return nil }
        var bytes = [UInt8](repeating: 0, count: width * height * 4)
        let drawn = bytes.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(
                data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                space: CGColorSpace(name: CGColorSpace.sRGB) ?? CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
            ) else { return false }
            context.interpolationQuality = .medium
            // Drawn upright, the bitmap's first row in memory is the picture's top row.
            context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        guard drawn else { return nil }
        return sample(rgba: bytes, width: width, height: height, columns: columns, rows: rows)
    }

    /// How many cells of `cell` points fit the picture fitted into `view`: the grid the
    /// glyphs are sampled at, at least one each way.
    static func grid(picture: CGSize, view: CGSize, cell: CGSize) -> (columns: Int, rows: Int) {
        let fitted = ScreenGeometry.fitted(image: picture, in: view)
        guard cell.width > 0, cell.height > 0, fitted.width > 0, fitted.height > 0 else { return (1, 1) }
        return (max(1, Int((fitted.width / cell.width).rounded())), max(1, Int((fitted.height / cell.height).rounded())))
    }
}

/// The glyph moments over time, as pure functions (ADR 0006): the reveal resolves the
/// first picture out of its glyphs, the power-down dissolves the last one into them and
/// holds it as a still. Cells turn in a diagonal sweep broken up by noise, so the picture
/// resolves like a dither rather than a wipe. Timings are `tokens.json` `motion`.
enum GlyphMotion {
    /// Signature moments play only with motion on; under Reduce Motion the end state
    /// shows at once (the picture, or the recording).
    static func plays(reduceMotion: Bool) -> Bool { !reduceMotion }

    /// When a cell turns, 0 to 1 (never 1, so every cell has turned at progress 1).
    static func order(column: Int, row: Int, columns: Int, rows: Int) -> Double {
        let span = Double(max(1, columns + rows))
        let sweep = Double(column + row) / span
        let s = sin(Double(column) * 12.9898 + Double(row) * 78.233) * 43_758.5453
        let noise = s - s.rounded(.down)
        return min(0.999_999, 0.55 * noise + 0.45 * sweep)
    }

    /// 0 to 1 over `ms`, linear, clamped.
    static func progress(elapsed: TimeInterval, ms: Int) -> Double {
        guard ms > 0 else { return 1 }
        return min(1, max(0, elapsed * 1000 / Double(ms)))
    }

    /// The reveal: a cell still shows its glyph until the sweep reaches it.
    static func coveredDuringReveal(order: Double, progress: Double) -> Bool { order >= progress }

    /// The power-down: a cell has turned to its glyph once the sweep has passed it.
    static func dissolved(order: Double, progress: Double) -> Bool { order < progress }

    /// Where a power-down is, `elapsed` seconds in.
    enum PowerDownPhase: Equatable, Sendable {
        /// The picture turning into glyphs, 0 to 1.
        case dissolving(Double)
        /// The glyphs held still, draining their colour, 0 to 1.
        case still(Double)
        /// Over: the recording shows.
        case done
    }

    static func powerDown(elapsed: TimeInterval, dissolveMs: Int, holdMs: Int) -> PowerDownPhase {
        let dissolve = Double(max(0, dissolveMs)) / 1000
        let hold = Double(max(0, holdMs)) / 1000
        if elapsed < dissolve { return .dissolving(progress(elapsed: elapsed, ms: dissolveMs)) }
        if elapsed < dissolve + hold { return .still(progress(elapsed: elapsed - dissolve, ms: holdMs)) }
        return .done
    }

    /// How long a moment lasts in all, for the end of its effect layer.
    static func duration(of kind: Kind, tokens: DesignTokens.Motion) -> TimeInterval {
        switch kind {
        case .reveal: Double(tokens.bootReveal.ms) / 1000
        case .powerDown: Double(tokens.powerDown.dissolveMs + tokens.powerDown.holdMs) / 1000
        }
    }

    enum Kind: Equatable, Sendable {
        case reveal
        case powerDown
    }
}
