import AppKit
import SwiftUI

// PROTOTYPE: the "machine screen" is a SwiftUI-drawn guest macOS desktop at 1024 x 768,
// rendered to a bitmap so the glyph reveal and power-down work on real pixels.

@MainActor
enum FakeDesktopRenderer {
    static func render(_ state: ScreenState, clock: String) -> CGImage? {
        let renderer = ImageRenderer(content: FakeDesktop(state: state, clock: clock))
        renderer.scale = 2
        renderer.proposedSize = ProposedViewSize(TipSplitLayout.screen)
        return renderer.cgImage
    }
}

struct FakeDesktop: View {
    let state: ScreenState
    let clock: String
    private typealias L = TipSplitLayout

    var body: some View {
        ZStack(alignment: .topLeading) {
            wallpaper
            if !state.terminal.isEmpty { terminal.offset(x: L.terminal.minX, y: L.terminal.minY) }
            if state.appOpen { tipSplit.offset(x: L.window.minX, y: L.window.minY) }
            menuBar
            dock
        }
        .frame(width: L.screen.width, height: L.screen.height, alignment: .topLeading)
        .environment(\.colorScheme, .light)
    }

    private var wallpaper: some View {
        ZStack {
            LinearGradient(colors: [Color(red: 0.16, green: 0.24, blue: 0.42), Color(red: 0.42, green: 0.36, blue: 0.58),
                                    Color(red: 0.86, green: 0.55, blue: 0.47)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
            RadialGradient(colors: [Color.white.opacity(0.22), .clear], center: UnitPoint(x: 0.78, y: 0.28), startRadius: 10, endRadius: 420)
            Ellipse().fill(Color(red: 0.10, green: 0.14, blue: 0.30).opacity(0.55))
                .frame(width: 1400, height: 420).offset(x: -120, y: 360).blur(radius: 30)
        }
        .frame(width: L.screen.width, height: L.screen.height)
    }

    private var menuBar: some View {
        HStack(spacing: 18) {
            Image(systemName: "apple.logo").font(.system(size: 13, weight: .semibold))
            Text(state.appOpen ? "TipSplit" : "Terminal").font(.system(size: 13, weight: .bold))
            ForEach(["File", "Edit", "View", "Window", "Help"], id: \.self) { Text($0).font(.system(size: 13)) }
            Spacer()
            Image(systemName: "wifi").font(.system(size: 12))
            Image(systemName: "battery.75percent").font(.system(size: 13))
            Text("Tue 23 Sep  \(clock)").font(.system(size: 13).monospacedDigit())
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 16)
        .frame(width: L.screen.width, height: 25)
        .background(.black.opacity(0.22))
    }

    private var dock: some View {
        HStack(spacing: 10) {
            ForEach(0..<6, id: \.self) { i in
                let colors: [Color] = [.blue, .gray, .black, .orange, .green, .purple]
                RoundedRectangle(cornerRadius: 11, style: .continuous)
                    .fill(LinearGradient(colors: [colors[i].opacity(0.95), colors[i].opacity(0.7)], startPoint: .top, endPoint: .bottom))
                    .frame(width: 46, height: 46)
                    .overlay {
                        if i == 2 { Text(">_").font(.system(size: 16, weight: .bold, design: .monospaced)).foregroundStyle(.green) }
                        if i == 3 { Text("%").font(.system(size: 22, weight: .heavy, design: .rounded)).foregroundStyle(.white) }
                    }
                    .overlay(alignment: .bottom) {
                        if i == 2 || (i == 3 && state.appOpen) { Circle().fill(.white.opacity(0.8)).frame(width: 4, height: 4).offset(y: 7) }
                    }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 18, style: .continuous).fill(.white.opacity(0.28)))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(.white.opacity(0.35), lineWidth: 1))
        .frame(width: L.screen.width)
        .offset(y: L.screen.height - 74)
    }

    private func trafficLights() -> some View {
        HStack(spacing: 8) {
            Circle().fill(Color(red: 1, green: 0.37, blue: 0.34)).frame(width: 12, height: 12)
            Circle().fill(Color(red: 1, green: 0.74, blue: 0.18)).frame(width: 12, height: 12)
            Circle().fill(Color(red: 0.16, green: 0.79, blue: 0.25)).frame(width: 12, height: 12)
        }
    }

    private var terminal: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                trafficLights()
                Spacer()
                Text("TipSplit — zsh — 80×24").font(.system(size: 12, weight: .semibold)).foregroundStyle(.white.opacity(0.7))
                Spacer()
                Color.clear.frame(width: 52)
            }
            .padding(.horizontal, 12)
            .frame(height: 28)
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(state.terminal.suffix(15).enumerated()), id: \.offset) { _, line in
                    Text(line).font(.system(size: 11, design: .monospaced)).foregroundStyle(Color(white: 0.9)).lineLimit(1)
                }
            }
            .padding(.horizontal, 10)
            .padding(.top, 4)
            Spacer(minLength: 0)
        }
        .frame(width: L.terminal.width, height: L.terminal.height, alignment: .topLeading)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color(white: 0.12).opacity(0.94)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(.white.opacity(0.14), lineWidth: 1))
        .shadow(color: .black.opacity(0.35), radius: 18, y: 10)
    }

    private func money(_ v: Double) -> String { String(format: "$%.2f", v) }

    private var tipSplit: some View {
        let w = L.window
        return ZStack(alignment: .topLeading) {
            RoundedRectangle(cornerRadius: 12, style: .continuous).fill(Color(white: 0.97))
            HStack {
                trafficLights()
                Spacer()
            }
            .padding(.horizontal, 14)
            .frame(width: w.width, height: 30)
            Text("TipSplit").font(.system(size: 13, weight: .semibold)).foregroundStyle(.black.opacity(0.8))
                .frame(width: w.width, height: 30)

            row("Bill", y: L.billField.minY - w.minY) {
                HStack {
                    Text("$").foregroundStyle(.secondary)
                    Text(state.bill.isEmpty ? "0.00" : state.bill).foregroundStyle(state.bill.isEmpty ? .secondary : .primary)
                    if state.focus == .bill { Rectangle().fill(.black).frame(width: 1, height: 16) }
                    Spacer()
                }
                .font(.system(size: 14).monospacedDigit())
                .padding(.horizontal, 8)
                .frame(width: L.billField.width, height: L.billField.height)
                .background(RoundedRectangle(cornerRadius: 6).fill(.white))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(state.focus == .bill ? Color.accentColor : Color(white: 0.8), lineWidth: state.focus == .bill ? 3 : 1))
            }
            row("Tip", y: L.segments.minY - w.minY) {
                HStack(spacing: 0) {
                    ForEach(L.tips, id: \.self) { tip in
                        Text("\(tip)%")
                            .font(.system(size: 13, weight: tip == state.tip ? .semibold : .regular))
                            .foregroundStyle(tip == state.tip ? .white : .primary)
                            .frame(width: L.segments.width / 4, height: L.segments.height)
                            .background(tip == state.tip ? RoundedRectangle(cornerRadius: 5).fill(Color.accentColor).padding(2) : nil)
                    }
                }
                .background(RoundedRectangle(cornerRadius: 7).fill(Color(white: 0.89)))
            }
            row("People", y: L.stepper.minY - w.minY) {
                HStack {
                    Text("\(state.people)").font(.system(size: 14).monospacedDigit())
                    Spacer()
                    HStack(spacing: 0) {
                        Text("−").frame(width: 30, height: 28)
                        Rectangle().fill(Color(white: 0.8)).frame(width: 1, height: 16)
                        Text("+").frame(width: 29, height: 28)
                    }
                    .font(.system(size: 15, weight: .medium))
                    .background(RoundedRectangle(cornerRadius: 7).fill(Color(white: 0.89)))
                }
                .frame(width: L.billField.width)
            }
            Rectangle().fill(Color(white: 0.86)).frame(width: w.width - 48, height: 1).offset(x: 24, y: 232)
            total("Tip", money(state.tipAmount), y: 252, big: false)
            total("Each pays", money(state.eachPays), y: 296, big: true)
        }
        .frame(width: w.width, height: w.height)
        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).stroke(.black.opacity(0.18), lineWidth: 1))
        .shadow(color: .black.opacity(0.35), radius: 24, y: 14)
    }

    private func row(_ label: String, y: CGFloat, @ViewBuilder content: () -> some View) -> some View {
        HStack(spacing: 0) {
            Text(label).font(.system(size: 14)).foregroundStyle(.black.opacity(0.8)).frame(width: 96, alignment: .leading)
            content()
        }
        .frame(height: 30)
        .offset(x: 24, y: y)
    }

    private func total(_ label: String, _ value: String, y: CGFloat, big: Bool) -> some View {
        HStack {
            Text(label).font(.system(size: big ? 16 : 14, weight: big ? .semibold : .regular))
            Spacer()
            Text(value).font(.system(size: big ? 28 : 16, weight: big ? .bold : .medium).monospacedDigit())
        }
        .foregroundStyle(.black.opacity(0.85))
        .frame(width: TipSplitLayout.window.width - 48, height: big ? 44 : 26)
        .offset(x: 24, y: y)
    }
}

// MARK: - Luminance sampling for the glyph renderings

struct LumaGrid: Sendable {
    var cols: Int
    var rows: Int
    /// Row-major luminance 0...1, `cols*2` x `rows*4` samples (braille resolution).
    var sub: [Float]
    /// Row-major average colour per cell.
    var colors: [SIMD3<Float>]

    func lum(_ x: Int, _ y: Int) -> Float { sub[y * cols * 2 + x] }
}

enum Luma {
    /// Samples an image down to `cols x rows` cells at 2 x 4 sub-samples per cell.
    static func sample(_ image: CGImage, cols: Int, rows: Int) -> LumaGrid? {
        let w = cols * 2, h = rows * 4
        guard w > 0, h > 0 else { return nil }
        var data = [UInt8](repeating: 0, count: w * h * 4)
        let ok = data.withUnsafeMutableBytes { buf -> Bool in
            guard let ctx = CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                      space: CGColorSpaceCreateDeviceRGB(),
                                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.interpolationQuality = .medium
            ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        var sub = [Float](repeating: 0, count: w * h)
        for i in 0..<(w * h) {
            let r = Float(data[i * 4]) / 255, g = Float(data[i * 4 + 1]) / 255, b = Float(data[i * 4 + 2]) / 255
            sub[i] = 0.2126 * r + 0.7152 * g + 0.0722 * b
        }
        // stretch contrast between the 4th and 96th percentile, so a mid-grey wallpaper
        // does not dither to a uniform half-tone
        let sorted = sub.sorted()
        let lo = sorted[sorted.count * 4 / 100], hi = sorted[sorted.count * 96 / 100]
        let span = max(0.05, hi - lo)
        for i in sub.indices { sub[i] = pow(min(1, max(0, (sub[i] - lo) / span)), 1.6) }
        var colors = [SIMD3<Float>](repeating: .zero, count: cols * rows)
        for cy in 0..<rows {
            for cx in 0..<cols {
                var acc = SIMD3<Float>.zero
                for sy in 0..<4 {
                    for sx in 0..<2 {
                        let i = ((cy * 4 + sy) * w + cx * 2 + sx) * 4
                        acc += SIMD3(Float(data[i]), Float(data[i + 1]), Float(data[i + 2])) / 255
                    }
                }
                colors[cy * cols + cx] = acc / 8
            }
        }
        return LumaGrid(cols: cols, rows: rows, sub: sub, colors: colors)
    }
}
