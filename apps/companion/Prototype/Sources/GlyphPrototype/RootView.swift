import SwiftUI

// PROTOTYPE: the window as one character grid: chrome row, panes, hint bar, overlays and
// the block cursor on top.

struct RootView: View {
    @Environment(PrototypeModel.self) private var model

    var body: some View {
        GeometryReader { geo in
            let cols = max(40, Int(geo.size.width / G.cellW))
            let rows = max(16, Int(geo.size.height / G.cellH))
            WindowGrid(cols: cols, rows: rows)
                .frame(width: geo.size.width, height: geo.size.height, alignment: .topLeading)
                .onAppear { model.layoutMode = model.resolveLayout(cols: cols) }
                .onChange(of: cols) { _, c in model.layoutMode = model.resolveLayout(cols: c) }
                .onChange(of: model.forcedLayout) { _, _ in model.layoutMode = model.resolveLayout(cols: cols) }
        }
        .background(model.palette.background)
        .ignoresSafeArea()
        .preferredColorScheme(model.theme.isDark ? .dark : .light)
    }
}

struct WindowGrid: View {
    @Environment(PrototypeModel.self) private var model
    let cols: Int
    let rows: Int

    var body: some View {
        let stripRows = model.showStrip ? 1 : 0
        let chromeRow = stripRows
        let help = model.helpOpen ? HelpPanel.lines(model, width: cols - 2) : []
        let helpRows = help.isEmpty ? 0 : help.count + 2
        let hintRow = rows - 1
        let bodyTop = chromeRow + 1
        let body = CellRect(col: 1, row: bodyTop, cols: cols - 2, rows: max(6, hintRow - bodyTop - helpRows))
        let mode = model.layoutMode
        let layout = PaneLayout.make(body: body, mode: mode, zoomed: model.zoomed, narrowPane: model.narrowPane)
        let spring: Animation? = model.reduceMotion ? nil : .spring(duration: 0.3, bounce: 0.12)

        ZStack(alignment: .topLeading) {
            panes(layout)
                .animation(spring, value: model.zoomed)
                .animation(spring, value: model.helpOpen)

            HouseLights(hole: CGRect(x: G.w(layout.screen.col), y: G.h(layout.screen.row),
                                     width: G.w(layout.screen.cols), height: G.h(layout.screen.rows)))
                .opacity(model.driving ? 1 : 0)
                .animation(model.reduceMotion ? nil : .easeInOut(duration: 0.35), value: model.driving)
                .allowsHitTesting(false)

            // Revision 21-22: a very faint olive wash on the top bar, matching the sidebar.
            model.palette.color(.chromeTint)
                .frame(width: G.w(cols), height: G.cellH)
                .offset(y: G.h(chromeRow))
                .allowsHitTesting(false)
            if model.showStrip {
                PrototypeStrip(cols: cols).offset(x: G.w(10))
            }
            ChromeRow(cols: cols, leftInset: model.showStrip ? 1 : 10)
                .offset(y: G.h(chromeRow))

            if model.helpOpen {
                HelpPanel(lines: help, cols: cols - 2)
                    .offset(x: G.w(1), y: G.h(hintRow - helpRows))
                    .transition(.opacity)
            }
            HintBar(cols: cols).offset(y: G.h(hintRow))

            if model.welcomeStart != nil {
                WelcomeOverlay(rect: body)
            }
            if model.paletteOpen {
                model.palette.background.opacity(0.55)
                    .frame(width: G.w(cols), height: G.h(rows))
                    .onTapGesture { model.paletteOpen = false }
                let pw = min(76, cols - 4)
                PaletteView(cols: pw, maxRows: min(22, rows - 8), originCol: (cols - pw) / 2, originRow: bodyTop + 3)
                    .offset(x: G.w((cols - pw) / 2), y: G.h(bodyTop + 3))
            }
        }
        .overlayPreferenceValue(CursorPrefs.self) { spots in
            let (key, spot) = cursorSpot(spots)
            BlockCursor(spot: spot, key: key)
        }
    }

    private func shown(_ p: Pane) -> Bool {
        if let z = model.zoomed { return p == z }
        if model.layoutMode == .narrow { return p == model.narrowPane }
        return true
    }

    @ViewBuilder private func panes(_ layout: PaneLayout) -> some View {
        let z = model.zoomed
        let narrow = model.layoutMode == .narrow
        ZStack(alignment: .topLeading) {
            if shown(.runs) {
                RunsPane(rect: layout.runs, compact: layout.mode == .medium && z == nil)
                    .offset(x: G.w(layout.runs.col), y: G.h(layout.runs.row))
            }
            if shown(.screen) {
                ScreenPane(rect: layout.screen, well: layout.well,
                           joinedBelow: shown(.steps) && !narrow && z == nil)
                    .offset(x: G.w(layout.screen.col), y: G.h(layout.screen.row))
            }
            if shown(.steps) {
                StepsPane(rect: layout.steps, compact: layout.mode == .medium && z == nil,
                          joined: shown(.screen) && !narrow && z == nil)
                    .offset(x: G.w(layout.steps.col), y: G.h(layout.steps.row))
            }
            if shown(.transcript) {
                TranscriptPane(rect: layout.transcript)
                    .offset(x: G.w(layout.transcript.col), y: G.h(layout.transcript.row))
            }
        }
    }

    /// Where the one cursor goes: palette, driving pointer, composer, the agent, then focus.
    private func cursorSpot(_ spots: [String: CellSpot]) -> (String, CellSpot?) {
        func pick(_ k: String) -> (String, CellSpot?)? { spots[k].map { (k, $0) } }
        if model.paletteOpen { return pick("palette") ?? ("", nil) }
        if model.welcomeStart != nil { return pick("welcome") ?? ("", nil) }
        if model.driving { return pick("driving") ?? ("", nil) }
        if model.composerActive, let s = pick("composer") { return s }
        if model.questionOpen, model.focus == .transcript, let q = pick("focus.question") { return q }
        if model.followAgent {
            if model.bootStart != nil, let s = pick("agent.transcript") { return s }
            switch model.activity {
            case .typing, .thinking: if let s = pick("agent.transcript") { return s }
            case .running: if let s = pick("agent.steps") ?? pick("agent.transcript") { return s }
            case .idle: break
            }
            if let s = pick("composer") { return s }
        }
        return pick("focus.\(model.focus.rawValue)") ?? ("", nil)
    }
}

// MARK: - The cursor

/// Text moves in steps, space moves in springs: the cursor steps along a line as text
/// streams, and glides (spring) when it changes line or target.
struct BlockCursor: View {
    @Environment(PrototypeModel.self) private var model
    let spot: CellSpot?
    let key: String

    var body: some View {
        if let spot {
            let palette = model.palette
            let on = !spot.blink || model.reduceMotion || Int(model.now / 0.53) % 2 == 0
            ZStack {
                palette.color(.brand)
                Text(String(spot.ch))
                    .font(FontCache.font(.neon, .regular))
                    .foregroundStyle(palette.color(.brandText))
            }
            .frame(width: G.cellW, height: G.cellH)
            .opacity(on ? 1 : 0)
            .position(x: spot.x + G.cellW / 2, y: spot.y + G.cellH / 2)
            .animation(model.reduceMotion ? nil : .spring(duration: model.driving ? 0.45 : 0.25, bounce: 0.18),
                       value: "\(key)|\(spot.y)|\(model.driving ? spot.x : 0)")
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
    }
}

/// Everything but the screen dims while driving.
struct HouseLights: View {
    @Environment(PrototypeModel.self) private var model
    let hole: CGRect

    var body: some View {
        Canvas { ctx, size in
            var p = Path(CGRect(origin: .zero, size: size))
            p.addRect(hole)
            ctx.fill(p, with: .color(model.palette.background.opacity(0.68)), style: FillStyle(eoFill: true))
        }
        .accessibilityHidden(true)
    }
}

// MARK: - Chrome row

struct ChromeRow: View {
    @Environment(PrototypeModel.self) private var model
    let cols: Int
    let leftInset: Int

    var body: some View {
        let run = Synthetic.runs.first { $0.id == model.selectedRun }
        var left: GridLine = [Span(String(repeating: " ", count: leftInset)),
                              Span("greenroom", .chrome, .bold, ink: .brand), Span("█", ink: .brand)]
        if let run, model.welcomeStart == nil {
            let state = model.runState(run)
            left += [Span("   "), Span(run.title, .chrome, .medium, ink: .fg), Span("  "),
                     Span(state.word, .chrome, .medium, ink: state.ink)]
        }
        let playing = model.playing
        var right: GridLine = []
        if model.isScripted {
            right += [Span(playing ? "▶ " : "▮▮ ", ink: playing ? .fg : .dim),
                      Span(FX.clock(model.playT) + " / " + FX.clock(Synthetic.length), ink: .dim), Span("   ")]
        }
        right += [Span("macos-15-xcode · 192.168.64.7", ink: .dim), Span(" ")]
        return GridText(line: spread(left, right, width: cols), width: cols)
    }
}

// MARK: - Prototype strip (not part of the design)

struct PrototypeStrip: View {
    @Environment(PrototypeModel.self) private var model
    let cols: Int

    var body: some View {
        let items: [(String, () -> Void)] = [
            ("▶ play", { model.playRun(fromStart: true) }),
            ("boot", { model.startBoot() }),
            (model.driving ? "give back" : "take control", { model.driving ? model.giveBack() : model.takeControl() }),
            ("FAIL", { model.selectedRun = Synthetic.runID; model.landVerdict(.fail) }),
            ("PASS", { model.selectedRun = Synthetic.runID; model.landVerdict(.pass) }),
            ("marks \(model.clickMarks ? "on" : "off")", { model.clickMarks.toggle() }),
            (model.machineDestroyed ? "restore" : "power-down", { model.machineDestroyed ? model.restoreMachine() : model.powerDown() }),
            ("welcome", { model.welcomeStart = model.welcomeStart == nil ? model.now : nil }),
            ("theme \(model.theme.title)", { model.theme = ThemeID.allCases[(ThemeID.allCases.firstIndex(of: model.theme)! + 1) % 4] }),
            ("follow \(model.followAgent ? "on" : "off")", { model.followAgent.toggle() }),
            ("motion \(model.reduceMotion ? "reduced" : "full")", { model.simulateReduceMotion.toggle() }),
            ("layout \(model.forcedLayout?.rawValue ?? "fit")", {
                let order: [LayoutMode?] = [nil, .wide, .medium, .narrow]
                let i = order.firstIndex { $0 == model.forcedLayout } ?? 0
                model.forcedLayout = order[(i + 1) % order.count]
            }),
        ]
        let hatch: Ink = .alpha(.role(.needsYou), 0.16)
        HStack(spacing: 0) {
            GridText(line: [Span(" PROTOTYPE ", .tool, .bold, ink: .bg, back: .role(.needsYou)), Span(" ", back: hatch)])
            ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                GridText(line: [Span(" \(item.0) ", .tool, .regular, ink: .role(.needsYou), back: hatch), Span("╱", .tool, ink: .alpha(.role(.needsYou), 0.4), back: hatch)])
                    .contentShape(Rectangle())
                    .onTapGesture { item.1() }
            }
            Color.clear.frame(maxWidth: .infinity).frame(height: G.cellH)
                .background(model.palette.color(hatch))
            GridText(line: [Span(" × ", .tool, .bold, ink: .role(.needsYou), back: hatch)])
                .contentShape(Rectangle())
                .onTapGesture { model.showStrip = false }
        }
        .frame(width: G.w(cols - 11), height: G.cellH, alignment: .leading)
        .clipped()
    }
}

// MARK: - Hint bar

struct HintBar: View {
    @Environment(PrototypeModel.self) private var model
    let cols: Int

    var body: some View {
        GridText(line: line, width: cols)
    }

    private var line: GridLine {
        if model.driving {
            let left: GridLine = [Span(" ◉ DRIVING ", .chrome, .bold, ink: .bg, back: .role(.driving)),
                                  Span("  all keys → machine · click switch to return", .chrome, .medium, ink: .role(.driving))]
            let keys = model.drivenKeys.isEmpty ? "" : "sent " + model.drivenKeys
            return spread(left, [Span(keys, .tool, ink: .dim), Span(" ")], width: cols)
        }
        if model.confirmDestroy {
            return spread([Span(" destroy the machine? ", .chrome, .bold, ink: .bg, back: .role(.failure)),
                           Span("  ⏎ destroy · any other key keeps it", ink: .role(.failure))], [], width: cols)
        }
        var left: GridLine = [Span(" ")]
        if model.review != .none, let until = model.undoUntil {
            left += [Span(model.review == .accepted ? "accepted" : "disputed", .chrome, .bold, ink: .role(model.review == .accepted ? .pass : .needsYou)),
                     Span(" · ", ink: .dim), Span("u", .chrome, .bold, ink: .fg), Span(" undo \(Int((until - model.now).rounded(.up)))s    ", ink: .dim)]
        }
        if model.layoutMode == .narrow && model.zoomed == nil {
            let order: [Pane] = [.runs, .screen, .steps, .transcript]
            let next = order[((order.firstIndex(of: model.narrowPane) ?? 0) + 1) % order.count]
            left += [Span(model.narrowPane.title, .chrome, .bold, ink: .fg), Span(" · ", ink: .dim),
                     Span("tab", .chrome, .bold, ink: .fg), Span(" → \(next.title)    ", ink: .dim)]
        }
        if model.pendingG {
            left += [Span("g", .chrome, .bold, ink: .fg), Span(" …  s steps  t transcript  r runs  v screen", ink: .dim)]
            return spread(left, [], width: cols)
        }
        var shown = Set<String>()
        for a in Registry.hints(model) {
            if model.helpOpen && a.id != "help" { continue }
            guard !shown.contains(a.label), a.id != "palette", a.id != "help" else { continue }
            if a.id == "pane.next" && model.layoutMode == .narrow { continue }
            shown.insert(a.label)
            let title = a.id == "move.down" ? "move" : (a.id == "open" ? openWord : a.title.lowercased())
            let add: GridLine = [Span(a.label, .chrome, .bold, ink: .fg), Span(" " + title + "   ", ink: .dim)]
            if left.cellCount + add.cellCount > cols - 26 { break }
            left += add
        }
        left += [Span("?", .chrome, .bold, ink: .fg), Span(model.helpOpen ? " less" : " more", ink: .dim)]
        var right: GridLine = []
        if let t = model.toast {
            right = [Span(t.text, ink: .fg), Span("   ")]
        }
        right += [Span("⌘K", .chrome, .bold, ink: .fg), Span(" commands ", ink: .dim)]
        return spread(left, right, width: cols)
    }

    private var openWord: String {
        switch model.focus {
        case .runs: "open"
        case .steps: "expand"
        case .transcript: model.questionOpen ? "answer" : "expand"
        case .screen: "zoom"
        }
    }
}

// MARK: - Help (the hint bar expanded in place)

struct HelpPanel: View {
    @Environment(PrototypeModel.self) private var model
    let lines: [GridLine]
    let cols: Int

    var body: some View {
        PaneBox(rect: CellRect(col: 0, row: 0, cols: cols, rows: lines.count + 2),
                title: [Span("ALL KEYS", .chrome, .medium, ink: .dim)], right: [Span("? closes", ink: .dim)], focused: true) {
            GridLines(lines: lines, width: cols - 2)
        }
        .background(model.palette.background)
    }

    @MainActor static func lines(_ m: PrototypeModel, width: Int) -> [GridLine] {
        let groups: [(String, [ActionContext])] = [
            ("move", [.global]), ("review", [.verdict, .question]), ("screen", [.screen]),
            ("writing", [.composer]), ("driving", [.driving]),
        ]
        let colW = max(24, (width - 4) / groups.count)
        var columns: [[GridLine]] = []
        for (title, ctxs) in groups {
            var col: [GridLine] = [[Span(title, .chrome, .bold, ink: .role(.heading))]]
            var seen = Set<String>()
            for a in Registry.all where ctxs.contains(a.context) && !a.label.isEmpty {
                if seen.contains(a.id) { continue }
                seen.insert(a.id)
                let on = a.enabled(m)
                col.append([Span(a.label.padding(toLength: max(7, a.label.count + 1), withPad: " ", startingAt: 0), .chrome, .bold, ink: on ? .fg : .dim),
                            Span(a.title.lowercased(), ink: on ? .alpha(.fg, 0.8) : .dim)].fitted(colW - 2))
            }
            columns.append(col)
        }
        let height = columns.map(\.count).max() ?? 0
        var out: [GridLine] = []
        for r in 0..<height {
            var line: GridLine = [Span(" ")]
            for c in columns {
                line += (r < c.count ? c[r] : []).fitted(colW)
            }
            out.append(line)
        }
        return out
    }
}

// MARK: - Welcome

struct WelcomeOverlay: View {
    @Environment(PrototypeModel.self) private var model
    let rect: CellRect

    var body: some View {
        let t = model.now - (model.welcomeStart ?? model.now)
        let word = "greenroom"
        let n = model.reduceMotion ? word.count : min(word.count, Int(max(0, t - 0.2) / 0.075))
        let after = model.reduceMotion ? 10 : t - 0.2 - Double(word.count) * 0.075
        let cmd = "greenroom serve"
        let cn = model.reduceMotion ? cmd.count : min(cmd.count, Int(max(0, after - 0.5) / 0.045))
        let w = 44
        let lines: [GridLine] = [
            [Span(String(word.prefix(n)), .chrome, .bold, ink: .brand)] + (n >= word.count && cn > 0 ? [Span("█", ink: .brand)] : []),
            [],
            [Span(after > 0.2 ? "No daemon is answering on 127.0.0.1:7777." : "", ink: .dim)],
            [Span(after > 0.35 ? "Start one, and your runs appear here:" : "", ink: .dim)],
            [],
            after > 0.45 ? [Span("  $ ", .tool, ink: .dim), Span(String(cmd.prefix(cn)), .tool, .medium, ink: .fg)] : [],
        ]
        let top = rect.row + max(2, rect.rows / 2 - 5)
        let left = rect.col + (rect.cols - w) / 2
        ZStack(alignment: .topLeading) {
            model.palette.background.frame(width: G.w(rect.cols), height: G.h(rect.rows))
                .offset(x: G.w(rect.col), y: G.h(rect.row))
            GridLines(lines: lines, width: w)
                .offset(x: G.w(left), y: G.h(top))
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("greenroom. No daemon is answering. Start one with greenroom serve.")
        }
        .preference(key: CursorPrefs.self, value: [
            "welcome": cn >= cmd.count
                ? CellSpot(x: G.w(left + 4 + cmd.count), y: G.h(top + 5), blink: true)
                : CellSpot(x: G.w(left + n), y: G.h(top), ch: " ", blink: after > 0),
        ])
    }
}

// MARK: - Cmd-K palette

struct PaletteView: View {
    @Environment(PrototypeModel.self) private var model
    let cols: Int
    let maxRows: Int
    let originCol: Int
    let originRow: Int

    var body: some View {
        let inner = cols - 2
        let items = Registry.paletteItems(model)
        let listRows = maxRows - 4
        let sel = min(model.paletteIndex, max(0, items.count - 1))
        let first = max(0, sel - listRows + 1)
        let lines = buildLines(items: items, inner: inner, sel: sel, first: first, listRows: listRows)
        PaneBox(rect: CellRect(col: 0, row: 0, cols: cols, rows: lines.count + 2),
                       title: [Span("⌘K", .chrome, .medium, ink: .dim)], right: [Span("\(items.count) actions · ⏎ run · esc close", ink: .dim)],
                       focused: true) {
            GridLines(lines: lines, width: inner)
        }
        .background(model.palette.background)
        .preference(key: CursorPrefs.self, value: [
            "palette": CellSpot(x: G.w(originCol + 1 + 3 + model.paletteQuery.count), y: G.h(originRow + 1), blink: true),
        ])
    }

    private func buildLines(items: [Action], inner: Int, sel: Int, first: Int, listRows: Int) -> [GridLine] {
        var lines: [GridLine] = [
            [Span(" › ", .chrome, .bold, ink: .fg), Span(model.paletteQuery, .chrome, .regular, ink: .fg)],
            [Span(String(repeating: "─", count: inner), ink: .role(.border))],
        ]
        if items.isEmpty {
            lines.append([])
            lines.append([Span(String(repeating: " ", count: max(0, (inner - 16) / 2))), Span("No results found", .chrome, .medium, ink: .fg)])
            lines.append([Span(String(repeating: " ", count: max(0, (inner - 31) / 2))), Span("Adjust your search to try again", ink: .dim)])
            lines.append([])
        } else {
            var lastCtx: ActionContext?
            for (i, a) in items.enumerated().dropFirst(first).prefix(listRows) {
                let on = i == sel
                let enabled = a.enabled(model)
                let ctx = a.context != lastCtx ? a.context.title : ""
                lastCtx = a.context
                var l = spread([Span(on ? " ▸ " : "   ", .chrome, .bold, ink: .fg),
                                Span(a.title, .chrome, on ? .medium : .regular, ink: enabled ? .fg : .dim)],
                               [Span(ctx, ink: .dim), Span("  "), Span(a.label.leftPad(6), .chrome, .bold, ink: enabled ? .fg : .dim), Span(" ")],
                               width: inner)
                if on { l = l.map { var s = $0; s.back = .selection; return s } }
                lines.append(l)
            }
        }
        return lines
    }
}
