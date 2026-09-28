import AppKit
import CoreGraphics

/// The pixel comparison of docs/22 section 7: SSIM on luminance (Gaussian window, sigma 1.5,
/// 11 taps) of the app's render against a Figma frame's PNG export, with the glyphs (the design
/// draws Inter, the app SF Pro) and the guest pictures masked out by the frame's layout file.
struct CloneSSIM {
    /// Luminance, row-major, 0...255.
    struct Luma {
        var width: Int
        var height: Int
        var values: [Double]

        subscript(x: Int, y: Int) -> Double { values[y * width + x] }
    }

    struct Result {
        /// Mean SSIM over the chrome: neither glyphs nor guest pictures.
        var chrome: Double
        /// The share of chrome pixels whose largest channel differs by more than 8/255.
        var offShare: Double
    }

    /// An image's RGB bytes, raw: no colour matching, as the export and the render are both
    /// sRGB bytes.
    static func pixels(_ image: CGImage) -> (width: Int, height: Int, rgb: [UInt8])? {
        let rep = NSBitmapImageRep(cgImage: image)
        guard rep.bitsPerSample == 8, !rep.isPlanar, rep.samplesPerPixel >= 3, let data = rep.bitmapData else { return nil }
        let (w, h, spp, row) = (rep.pixelsWide, rep.pixelsHigh, rep.samplesPerPixel, rep.bytesPerRow)
        let alphaFirst = rep.bitmapFormat.contains(.alphaFirst)
        let offset = spp == 4 && alphaFirst ? 1 : 0
        var rgb = [UInt8](repeating: 0, count: w * h * 3)
        for y in 0..<h {
            for x in 0..<w {
                let p = data + y * row + x * spp + offset
                let i = (y * w + x) * 3
                rgb[i] = p[0]; rgb[i + 1] = p[1]; rgb[i + 2] = p[2]
            }
        }
        return (w, h, rgb)
    }

    /// The 2x render halved to points, moved `shift` render pixels right and down first (the
    /// Figma export draws the window half a point off its whole-point origin).
    static func halve(_ rgb: [UInt8], width: Int, height: Int, shift: Int) -> [Double] {
        let (w, h) = (width / 2, height / 2)
        var out = [Double](repeating: 0, count: w * h * 3)
        func at(_ x: Int, _ y: Int, _ c: Int) -> Double {
            let sx = min(max(x - shift, 0), width - 1), sy = min(max(y - shift, 0), height - 1)
            return Double(rgb[(sy * width + sx) * 3 + c])
        }
        for y in 0..<h {
            for x in 0..<w {
                for c in 0..<3 {
                    out[(y * w + x) * 3 + c] = (at(2 * x, 2 * y, c) + at(2 * x + 1, 2 * y, c) + at(2 * x, 2 * y + 1, c) + at(2 * x + 1, 2 * y + 1, c)) / 4
                }
            }
        }
        return out
    }

    /// Compares `render` (2x) with the Figma PNG whose window sits at `origin`, masked by the
    /// layout file's text, glyph and icon frames (grown by a point) and its guest pictures.
    static func compare(render: CGImage, figma: CGImage, origin: CGPoint, layout: String) -> Result? {
        guard let r = pixels(render), let f = pixels(figma) else { return nil }
        let (w, h) = (r.width / 2, r.height / 2)
        guard Int(origin.x) + w <= f.width, Int(origin.y) + h <= f.height else { return nil }
        let a = halve(r.rgb, width: r.width, height: r.height, shift: 1)
        var b = [Double](repeating: 0, count: w * h * 3)
        for y in 0..<h {
            for x in 0..<w {
                for c in 0..<3 { b[(y * w + x) * 3 + c] = Double(f.rgb[((y + Int(origin.y)) * f.width + x + Int(origin.x)) * 3 + c]) }
            }
        }
        func luma(_ p: [Double]) -> Luma {
            Luma(width: w, height: h, values: (0..<(w * h)).map { 0.2126 * p[$0 * 3] + 0.7152 * p[$0 * 3 + 1] + 0.0722 * p[$0 * 3 + 2] })
        }
        let region = mask(layout: layout, width: w, height: h)
        let ssim = map(luma(a), luma(b))
        var (sum, count, off) = (0.0, 0, 0)
        for i in 0..<(w * h) where region[i] {
            sum += ssim[i]
            count += 1
            let d = (0..<3).map { abs(a[i * 3 + $0] - b[i * 3 + $0]) }.max() ?? 0
            if d > 8 { off += 1 }
        }
        guard count > 0 else { return nil }
        return Result(chrome: sum / Double(count), offShare: Double(off) / Double(count))
    }

    /// True where the chrome is compared.
    static func mask(layout: String, width w: Int, height h: Int) -> [Bool] {
        var region = [Bool](repeating: true, count: w * h)
        func clear(_ x0: Int, _ y0: Int, _ x1: Int, _ y1: Int) {
            for y in max(0, y0)..<min(h, max(0, y1)) {
                for x in max(0, x0)..<min(w, max(0, x1)) { region[y * w + x] = false }
            }
        }
        let lines = layout.split(separator: "\n").filter { !$0.hasPrefix("#") && !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        let paths = lines.map { String($0.split(separator: ";", omittingEmptySubsequences: false)[0]) }
        for line in lines {
            let f = line.split(separator: ";", omittingEmptySubsequences: false).map(String.init)
            guard f.count >= 5, let x = Double(f[1]), let y = Double(f[2]), let fw = Double(f[3]), let fh = Double(f[4]) else { continue }
            let path = f[0], name = String(path.split(separator: "/").last ?? "")
            // A row the layout lists without its parts: its words are masked with it.
            let bareRow = name.hasPrefix("Run row") && !paths.contains { $0.hasPrefix(path + "/") }
            if bareRow || f.count > 5 || path.hasSuffix("/Glyph") || path.contains("/Icon/") || path.hasSuffix("GlyphBox") {
                clear(Int(x) - 1, Int(y) - 1, Int(x + fw) + 2, Int(y + fh) + 2)
            }
            if path.hasSuffix("/Screen") || path.hasSuffix("/Filmstrip") || path.hasSuffix("Traffic lights") {
                clear(Int(x), Int(y), Int(x + fw), Int(y + fh))
            }
        }
        return region
    }

    /// SSIM at every pixel.
    static func map(_ a: Luma, _ b: Luma) -> [Double] {
        let n = a.width * a.height
        let c1: Double = (0.01 * 255) * (0.01 * 255), c2: Double = (0.03 * 255) * (0.03 * 255)
        func product(_ x: [Double], _ y: [Double]) -> [Double] { zip(x, y).map { $0 * $1 } }
        let ma = gauss(a.values, a.width, a.height), mb = gauss(b.values, a.width, a.height)
        let aa = gauss(product(a.values, a.values), a.width, a.height)
        let bb = gauss(product(b.values, b.values), a.width, a.height)
        let ab = gauss(product(a.values, b.values), a.width, a.height)
        return (0..<n).map { i in
            let (va, vb, cov) = (aa[i] - ma[i] * ma[i], bb[i] - mb[i] * mb[i], ab[i] - ma[i] * mb[i])
            return ((2 * ma[i] * mb[i] + c1) * (2 * cov + c2)) / ((ma[i] * ma[i] + mb[i] * mb[i] + c1) * (va + vb + c2))
        }
    }

    /// A separable Gaussian blur, zero outside the image.
    static func gauss(_ v: [Double], _ w: Int, _ h: Int) -> [Double] {
        var k: [Double] = (-5...5).map { (t: Int) -> Double in
            let u = Double(t) / 1.5
            return exp(-0.5 * u * u)
        }
        let s = k.reduce(0, +)
        k = k.map { $0 / s }
        var rows = [Double](repeating: 0, count: v.count)
        for y in 0..<h {
            for x in 0..<w {
                var acc = 0.0
                for t in -5...5 where x + t >= 0 && x + t < w { acc += k[t + 5] * v[y * w + x + t] }
                rows[y * w + x] = acc
            }
        }
        var out = [Double](repeating: 0, count: v.count)
        for y in 0..<h {
            for x in 0..<w {
                var acc = 0.0
                for t in -5...5 where y + t >= 0 && y + t < h { acc += k[t + 5] * rows[(y + t) * w + x] }
                out[y * w + x] = acc
            }
        }
        return out
    }
}
