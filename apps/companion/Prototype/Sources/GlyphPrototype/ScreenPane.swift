// Connecting state and REC badge ported from Beautiful UI AgentScreen and LoadingState (MIT, (c) 2026 Shane Levine).
import SwiftUI

// PROTOTYPE: the screen pane. The well is the one element off the grid: a window cut into
// it that shows real pixels. Boot reveal and power-down turn those pixels into glyphs.

struct ScreenPane: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect
    let well: CGRect
    let joinedBelow: Bool

    var body: some View {
        let driving = model.driving
        ZStack(alignment: .topLeading) {
            PaneBox(rect: rect, title: title, right: chip, focused: model.focus == .screen || driving,
                    borderInk: driving ? .role(.driving) : nil) { EmptyView() }
            // the joined pane below owns the bottom edge
            if joinedBelow {
                model.palette.background.frame(width: G.w(rect.cols), height: G.cellH)
                    .offset(y: G.h(rect.rows - 1))
            }
            WellView(size: well.size)
                .frame(width: well.width, height: well.height)
                .clipped()
                .offset(x: well.minX, y: well.minY)
            if driving { giveBackSwitch }
        }
        .frame(width: G.w(rect.cols), height: G.h(rect.rows), alignment: .topLeading)
        .contentShape(Rectangle())
        .onTapGesture(coordinateSpace: .local) { p in
            if model.driving {
                let f = CGPoint(x: (p.x - well.minX) / well.width, y: (p.y - well.minY) / well.height)
                guard (0...1).contains(f.x), (0...1).contains(f.y) else { return }
                model.pointer = f
                if model.clickMarks { model.ripples.append(Ripple(point: f, start: model.now, driving: true)) }
            } else {
                model.focus = .screen
            }
        }
        .preference(key: CursorPrefs.self, value: spots)
    }

    private var spots: [String: CellSpot] {
        let (x, y) = rect.inner(0, 0)
        var out = ["focus.screen": CellSpot(x: x, y: y)]
        if model.driving {
            let px = G.w(rect.col) + well.minX + model.pointer.x * well.width
            let py = G.h(rect.row) + well.minY + model.pointer.y * well.height
            out["driving"] = CellSpot(x: px - G.cellW / 2, y: py - G.cellH / 2)
        }
        return out
    }

    private var title: GridLine {
        var t: GridLine = [Span("SCREEN", .chrome, .medium, ink: .dim)]
        if model.isScripted { t += [Span(" · TipSplit", ink: .dim)] }
        return t
    }

    /// One source chip: connecting, live, recording on a step, driving with REC, destroyed.
    private var chip: GridLine {
        if !model.isScripted { return [Span("recording", ink: .dim)] }
        if model.machineDestroyed { return [Span("■ destroyed", ink: .dim)] }
        if let b = model.bootStart {
            return model.now - b < Boot.terminalEnd ? [Span("◌ connecting", ink: .dim)] : [Span("● live", ink: .role(.live))]
        }
        if model.driving {
            let on = model.reduceMotion || Int(model.now * 1.6) % 2 == 0
            return [Span(on ? "●" : " ", .chrome, .bold, ink: .role(.failure)), Span(" REC ", .chrome, .bold, ink: .role(.failure)),
                    Span(FX.clock(model.now - model.drivingSince), ink: .fg), Span("  1024×768", ink: .dim)]
        }
        if !model.playing, model.focus == .steps, let s = model.selectedStep {
            return [Span("▮▮ ", ink: .dim), Span("step \(s)", ink: .fg), Span("  recording", ink: .dim)]
        }
        return [Span("● live", ink: .role(.live)), Span("  1024×768", ink: .dim)]
    }

    /// The switch that ends driving. Clicking it is the only way back (decision 9).
    private var giveBackSwitch: some View {
        let label: GridLine = [Span(" ◉ driving ", .chrome, .bold, ink: .cursorText, back: .role(.driving)),
                               Span(" give back ", .chrome, .bold, ink: .role(.driving), back: .bg)]
        return GridText(line: label)
            .contentShape(Rectangle())
            .onTapGesture { model.giveBack() }
            .offset(x: G.w(rect.cols - 2 - (chip.cellCount + 2) - label.cellCount - 1))
    }
}

struct WellView: View {
    @Environment(PrototypeModel.self) private var model
    let size: CGSize

    var body: some View {
        let palette = model.palette
        // Revision 19: the machine screen well is always dark, in both themes - it reads
        // like a monitor.
        let wellBG = palette.color(.screenWell)
        ZStack(alignment: .topLeading) {
            wellBG
            if !model.isScripted {
                Text("recording · \(Synthetic.runs.first { $0.id == model.selectedRun }?.title ?? "")")
                    .font(FontCache.font(.neon))
                    .foregroundStyle(Color(white: 0.6))
                    .frame(width: size.width, height: size.height)
            } else if let b = model.bootStart {
                bootLayer(model.now - b, bg: wellBG)
            } else if let p = model.powerDownStart {
                powerDown(model.now - p, bg: wellBG)
            } else {
                picture
                overlays
            }
            if let f = model.flashAt, model.now - f < 0.25 {
                Color.white.opacity(0.5 * (1 - (model.now - f) / 0.25)).allowsHitTesting(false)
            }
        }
        .frame(width: size.width, height: size.height)
        .overlay(Rectangle().strokeBorder(palette.color(.role(.border)).opacity(0.5), lineWidth: 1))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Machine screen")
    }

    @ViewBuilder private var picture: some View {
        if let img = model.screenImage {
            Image(decorative: img, scale: 2)
                .resizable()
                .interpolation(.high)
                .frame(width: size.width, height: size.height)
        }
    }

    // MARK: overlays: pointer, click marks

    @ViewBuilder private var overlays: some View {
        let ripples = model.ripples
        let now = model.now
        let palette = model.palette
        let pointer = model.pointer
        let driving = model.driving
        let staticMark: CGPoint? = {
            guard model.clickMarks, !model.playing, model.focus == .steps, let id = model.selectedStep,
                  let s = Synthetic.steps.first(where: { $0.id == id }) else { return nil }
            return s.click
        }()
        Canvas { ctx, size in
            let cw = G.cellW, ch = G.cellH
            func cellAt(_ p: CGPoint) -> (Int, Int) { (Int(p.x * size.width / cw), Int(p.y * size.height / ch)) }
            // cell-shaped ripples: rings of cells growing out from the hit cell
            for r in ripples {
                let age = (now - r.start) / 0.8
                guard age >= 0, age < 1 else { continue }
                let color = palette.color(.role(r.driving ? .driving : .live))
                let (cx, cy) = cellAt(r.point)
                let radius = Int(age * 3.2)
                for dy in -radius...radius {
                    for dx in -radius...radius where max(abs(dx), abs(dy)) == radius {
                        let rect = CGRect(x: CGFloat(cx + dx) * cw, y: CGFloat(cy + dy) * ch, width: cw, height: ch).insetBy(dx: 0.5, dy: 0.5)
                        ctx.stroke(Path(rect), with: .color(color.opacity((1 - age) * 0.9)), lineWidth: 1)
                    }
                }
                let hit = CGRect(x: CGFloat(cx) * cw, y: CGFloat(cy) * ch, width: cw, height: ch)
                ctx.fill(Path(hit), with: .color(color.opacity(0.85 * (1 - age))))
            }
            if let p = staticMark {
                let (cx, cy) = cellAt(p)
                let color = palette.color(.role(.live))
                let box = CGRect(x: CGFloat(cx - 1) * cw, y: CGFloat(cy - 1) * ch, width: cw * 3, height: ch * 3).insetBy(dx: 0.5, dy: 0.5)
                var path = Path()
                let l: CGFloat = 5
                for (x, y, sx, sy) in [(box.minX, box.minY, 1.0, 1.0), (box.maxX, box.minY, -1.0, 1.0), (box.minX, box.maxY, 1.0, -1.0), (box.maxX, box.maxY, -1.0, -1.0)] {
                    path.move(to: CGPoint(x: x + sx * l, y: y)); path.addLine(to: CGPoint(x: x, y: y)); path.addLine(to: CGPoint(x: x, y: y + sy * l))
                }
                ctx.stroke(path, with: .color(color), lineWidth: 1.5)
                ctx.fill(Path(CGRect(x: CGFloat(cx) * cw, y: CGFloat(cy) * ch, width: cw, height: ch)), with: .color(color.opacity(0.35)))
            }
            // the guest's pointer; while driving the block cursor is the pointer instead
            if !driving {
                let p = CGPoint(x: pointer.x * size.width, y: pointer.y * size.height)
                var arrow = Path()
                arrow.move(to: p)
                arrow.addLine(to: CGPoint(x: p.x, y: p.y + 15))
                arrow.addLine(to: CGPoint(x: p.x + 4, y: p.y + 11.5))
                arrow.addLine(to: CGPoint(x: p.x + 7, y: p.y + 17.5))
                arrow.addLine(to: CGPoint(x: p.x + 9, y: p.y + 16.5))
                arrow.addLine(to: CGPoint(x: p.x + 6.2, y: p.y + 10.6))
                arrow.addLine(to: CGPoint(x: p.x + 11, y: p.y + 10.6))
                arrow.closeSubpath()
                ctx.fill(arrow, with: .color(.black))
                ctx.stroke(arrow, with: .color(.white), lineWidth: 1)
            }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    // MARK: boot

    @ViewBuilder private func bootLayer(_ t: Double, bg: Color) -> some View {
        if t < Boot.connecting {
            connecting(t)
        } else if t < Boot.terminalEnd {
            terminal(t)
        } else {
            let p = (t - Boot.terminalEnd)
            ZStack(alignment: .topLeading) {
                if p >= Boot.glyphsEnd - Boot.terminalEnd { picture }
                GlyphField(size: size, mode: .reveal(appear: min(1, p / (Boot.glyphsEnd - Boot.terminalEnd)),
                                                     resolve: max(0, (t - Boot.glyphsEnd) / 0.6)), bg: bg)
            }
        }
    }

    /// Agent-screen connecting state: the loader on black with a shimmering label and timer.
    private func connecting(_ t: Double) -> some View {
        let lines = FX.working("Connecting to tipsplit-vm", elapsed: t, now: model.now, frozen: model.reduceMotion, ink: .slot(15))
        return GridLines(lines: lines)
            .frame(width: size.width, height: size.height)
    }

    private func terminal(_ t: Double) -> some View {
        var lines: [GridLine] = [[Span("greenroom", .tool, .bold, ink: .slot(15)), Span(" · run tipsplit · macos-15-xcode", .tool, ink: .slot(8))], []]
        for l in Boot.lines where t >= l.at {
            let done = t >= l.resultAt
            let status: Span = done ? Span("✓", .tool, .bold, ink: .role(.pass)) : Span(FX.spinner(model.now, frozen: model.reduceMotion), ink: .role(.live))
            let typedCount = Int((t - l.at) * 90)
            let detail = String(l.detail.prefix(typedCount))
            lines.append([Span(" "), status, Span(" "), Span(l.label.padding(toLength: 7, withPad: " ", startingAt: 0), .tool, .bold, ink: .slot(15)),
                          Span(detail.padding(toLength: 38, withPad: " ", startingAt: 0), .tool, ink: .slot(7)),
                          Span(done ? l.result.leftPad(7) : "", .tool, ink: l.label == "ready" ? .role(.pass) : .slot(8))])
        }
        if let last = Boot.lines.last, t >= last.resultAt + 0.1 {
            lines.append([])
            lines.append([Span(" "), Span("first frame", .tool, ink: .slot(8)), Span(" ▸", .tool, ink: .slot(8))])
        }
        return GridLines(lines: lines)
            .padding(.leading, G.cellW)
            .padding(.top, G.cellH)
    }

    // MARK: power-down

    @ViewBuilder private func powerDown(_ t: Double, bg: Color) -> some View {
        let dissolve = min(1, t / 0.6)
        let fade = min(1, max(0, (t - 0.6) / 0.6))
        ZStack {
            if dissolve < 1 { picture }
            GlyphField(size: size, mode: .powerDown(dissolve: dissolve, mono: fade), bg: bg)
            if fade >= 1 {
                GridLines(lines: [
                    [Span(" ■ machine destroyed ", .chrome, .bold, ink: .slot(15), back: .slot(0))],
                    [Span(" last frame · \(Synthetic.clock(model.playT)) ", .chrome, ink: .slot(8), back: .slot(0))],
                ])
            }
        }
    }
}

/// The screen as glyphs: each cell samples the picture's luminance at 2 x 4 and draws the
/// braille pattern an ordered dither gives, in the cell's own colour.
struct GlyphField: View {
    @Environment(PrototypeModel.self) private var model

    enum Mode {
        /// `appear` 0...1 fades glyphs in; `resolve` 0...1 flips cells to pixels.
        case reveal(appear: Double, resolve: Double)
        /// `dissolve` 0...1 turns pixels to glyphs; `mono` 0...1 drains their colour.
        case powerDown(dissolve: Double, mono: Double)
    }

    let size: CGSize
    let mode: Mode
    let bg: Color

    static let bayer: [Float] = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map { ($0 + 0.5) / 16 }

    var body: some View {
        let cols = Int(size.width / G.cellW)
        let rows = Int(size.height / G.cellH)
        let grid = model.luma(cols: cols, rows: rows)
        let fg = model.palette.color(.slot(15))
        let mode = mode
        let bg = bg
        Canvas { ctx, size in
            guard let grid else { return }
            let cw = size.width / CGFloat(cols), ch = size.height / CGFloat(rows)
            for cy in 0..<rows {
                for cx in 0..<cols {
                    // order: a diagonal sweep broken up by hash noise
                    let s = sin(Double(cx) * 12.9898 + Double(cy) * 78.233) * 43_758.5453
                    let h = s - s.rounded(.down)
                    let order = 0.55 * h + 0.45 * Double(cx + cy) / Double(cols + rows)
                    var showGlyph = true
                    var colorMix = 0.0
                    var alpha = 1.0
                    switch mode {
                    case .reveal(let appear, let resolve):
                        if order > appear * 1.02 { alpha = 0 }
                        if order < resolve { showGlyph = false }
                    case .powerDown(let dissolve, let mono):
                        if order > dissolve { showGlyph = false }
                        colorMix = mono
                    }
                    guard showGlyph else { continue }
                    let rect = CGRect(x: CGFloat(cx) * cw, y: CGFloat(cy) * ch, width: cw, height: ch)
                    let c = grid.colors[cy * cols + cx]
                    ctx.fill(Path(rect), with: .color(bg))
                    guard alpha > 0 else { continue }
                    // a dark mosaic of the cell's colour under the dots, so no cell reads as a hole
                    let under = Double(0.2 * (1 - colorMix))
                    if under > 0 {
                        ctx.fill(Path(rect), with: .color(Color(.sRGB, red: Double(c.x), green: Double(c.y), blue: Double(c.z), opacity: under)))
                    }
                    var bits: UInt8 = 0
                    let map: [(Int, Int)] = [(0, 0), (0, 1), (0, 2), (1, 0), (1, 1), (1, 2), (0, 3), (1, 3)]
                    for (i, (sx, sy)) in map.enumerated() {
                        let x = cx * 2 + sx, y = cy * 4 + sy
                        let l = grid.lum(x, y)
                        let th = GlyphField.bayer[(y % 4) * 4 + (x % 4)]
                        if l * 1.15 > th { bits |= 1 << i }
                    }
                    let peak = max(0.05, max(c.x, max(c.y, c.z)))
                    let boost: Float = max(1.2, 0.85 / peak)
                    let col = Color(.sRGB, red: Double(min(1, c.x * boost)), green: Double(min(1, c.y * boost)),
                                    blue: Double(min(1, c.z * boost)), opacity: 1)
                    var path = Path()
                    SpriteDraw.braillePath(bits, in: rect, into: &path)
                    if colorMix > 0 {
                        ctx.fill(path, with: .color(fg.opacity(0.55 * colorMix)))
                        if colorMix < 1 { ctx.fill(path, with: .color(col.opacity(1 - colorMix))) }
                    } else {
                        ctx.fill(path, with: .color(col.opacity(alpha)))
                    }
                }
            }
        }
        .frame(width: size.width, height: size.height)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

extension PrototypeModel {
    /// Luminance of the current picture at cell resolution, cached per picture and size.
    func luma(cols: Int, rows: Int) -> LumaGrid? {
        let key = "\(screenImageVersion)x\(cols)x\(rows)"
        if lumaKey == key { return lumaValue }
        guard let img = screenImage else { return nil }
        lumaValue = Luma.sample(img, cols: cols, rows: rows)
        lumaKey = key
        return lumaValue
    }
}
