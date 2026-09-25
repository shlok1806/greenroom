import AppKit

// PROTOTYPE: one action registry feeds the hint bar, the `?` help, the Cmd-K palette and
// the key handler. A key that is not in here does nothing (decision 8).

enum ActionContext: String, Sendable, CaseIterable {
    case global, runs, screen, steps, transcript, verdict, question, composer, driving, palette, prototype

    var title: String {
        switch self {
        case .global: "everywhere"
        case .runs: "runs"
        case .screen: "screen"
        case .steps: "steps"
        case .transcript: "transcript"
        case .verdict: "verdict"
        case .question: "question"
        case .composer: "writing"
        case .driving: "driving"
        case .palette: "palette"
        case .prototype: "prototype scenes"
        }
    }
}

struct Action: Identifiable {
    var id: String
    var title: String
    /// Normalised key strings this action answers to (see `KeyName`).
    var keys: [String]
    /// What the hint bar and palette show, e.g. `j k`.
    var label: String
    var context: ActionContext
    /// Lower shows first in the hint bar; nil never shows there.
    var hint: Int?
    var enabled: @MainActor (PrototypeModel) -> Bool = { _ in true }
    var perform: @MainActor (PrototypeModel) -> Void
}

enum KeyName {
    /// Normalises an event to the strings the registry uses: `j`, `⏎`, `⇧⏎`, `⌘k`, `esc` ...
    static func of(_ e: NSEvent) -> String {
        let cmd = e.modifierFlags.contains(.command)
        let shift = e.modifierFlags.contains(.shift)
        let special: String? = switch e.keyCode {
        case 36, 76: "⏎"
        case 53: "esc"
        case 48: "tab"
        case 49: "space"
        case 126: "↑"
        case 125: "↓"
        case 123: "←"
        case 124: "→"
        case 51: "⌫"
        default: nil
        }
        if let special {
            return (cmd ? "⌘" : "") + (shift && special != "tab" ? "⇧" : "") + special
        }
        let ch = (cmd ? e.charactersIgnoringModifiers : e.characters)?.lowercased() ?? ""
        return (cmd ? "⌘" : "") + ch
    }
}

@MainActor
enum Registry {
    static let all: [Action] = navigation + global + review + composer + driving + scenes

    static let navigation: [Action] = [
        Action(id: "move.down", title: "Move down", keys: ["j", "↓"], label: "j k", context: .global, hint: 1,
               enabled: { !$0.driving }, perform: { $0.move(1) }),
        Action(id: "move.up", title: "Move up", keys: ["k", "↑"], label: "k", context: .global, hint: nil,
               enabled: { !$0.driving }, perform: { $0.move(-1) }),
        Action(id: "open", title: "Open", keys: ["⏎"], label: "⏎", context: .global, hint: 2,
               enabled: { !$0.driving }, perform: { $0.open() }),
        Action(id: "pane.next", title: "Next pane", keys: ["tab"], label: "tab", context: .global, hint: 3,
               perform: { $0.cyclePane(1) }),
        Action(id: "pane.prev", title: "Previous pane", keys: ["⇧tab"], label: "⇧tab", context: .global, hint: nil,
               perform: { $0.cyclePane(-1) }),
        Action(id: "go.steps", title: "Go to steps", keys: ["g s"], label: "g s", context: .global, hint: nil,
               perform: { $0.goto(.steps) }),
        Action(id: "go.transcript", title: "Go to transcript", keys: ["g t"], label: "g t", context: .global, hint: nil,
               perform: { $0.goto(.transcript) }),
        Action(id: "go.runs", title: "Go to runs", keys: ["g r"], label: "g r", context: .global, hint: nil,
               perform: { $0.goto(.runs) }),
        Action(id: "go.screen", title: "Go to screen", keys: ["g v"], label: "g v", context: .global, hint: nil,
               perform: { $0.goto(.screen) }),
        Action(id: "zoom", title: "Zoom pane", keys: ["z"], label: "z", context: .global, hint: 6,
               perform: { m in m.zoomed = m.zoomed == nil ? m.focus : nil }),
        Action(id: "back", title: "Back", keys: ["esc"], label: "esc", context: .global, hint: 9,
               enabled: { $0.canBack }, perform: { $0.back() }),
    ]

    static let global: [Action] = [
        Action(id: "palette", title: "Command palette", keys: ["⌘k"], label: "⌘K", context: .global, hint: 10,
               perform: { m in m.paletteOpen = true; m.paletteQuery = ""; m.paletteIndex = 0; m.helpOpen = false }),
        Action(id: "help", title: "All keys", keys: ["?"], label: "?", context: .global, hint: 11,
               perform: { $0.helpOpen.toggle() }),
        Action(id: "play", title: "Play run", keys: ["space"], label: "space", context: .global, hint: 4,
               enabled: { $0.isScripted }, perform: { $0.togglePlay() }),
        Action(id: "compose", title: "Write to the verifier", keys: ["/"], label: "/", context: .global, hint: 5,
               enabled: { !$0.driving && $0.isScripted }, perform: { m in m.composerActive = true; m.focus = .transcript }),
        Action(id: "control.take", title: "Take control", keys: ["t"], label: "t", context: .global, hint: 7,
               enabled: { !$0.driving && !$0.machineDestroyed && $0.isScripted }, perform: { $0.takeControl() }),
        Action(id: "capture", title: "Capture screenshot", keys: ["c"], label: "c", context: .screen, hint: 8,
               enabled: { !$0.machineDestroyed && $0.isScripted }, perform: { $0.capture() }),
        Action(id: "marks", title: "Click marks on/off", keys: ["m"], label: "m", context: .screen, hint: 9,
               perform: { m in m.clickMarks.toggle(); m.showToast("click marks \(m.clickMarks ? "on" : "off")") }),
        Action(id: "follow", title: "Follow agent on/off", keys: ["f"], label: "f", context: .global, hint: nil,
               perform: { m in m.followAgent.toggle(); m.showToast("cursor \(m.followAgent ? "follows the agent" : "stays with you")") }),
        Action(id: "destroy", title: "Destroy machine…", keys: ["⌘⌫"], label: "⌘⌫", context: .global, hint: nil,
               enabled: { !$0.machineDestroyed && $0.isScripted }, perform: { $0.confirmDestroy = true }),
    ]

    static let review: [Action] = [
        Action(id: "verdict.accept", title: "Accept verdict", keys: ["a"], label: "a", context: .verdict, hint: 0,
               enabled: { $0.verdict != nil && $0.review == .none }, perform: { $0.accept() }),
        Action(id: "verdict.dispute", title: "Dispute verdict", keys: ["d"], label: "d", context: .verdict, hint: 0,
               enabled: { $0.verdict != nil && $0.review == .none }, perform: { $0.dispute() }),
        Action(id: "undo", title: "Undo", keys: ["u"], label: "u", context: .verdict, hint: 0,
               enabled: { $0.undoUntil != nil }, perform: { $0.undo() }),
        Action(id: "question.answer", title: "Answer question", keys: ["1", "2", "3"], label: "1-3", context: .question, hint: 0,
               enabled: { $0.questionOpen }, perform: { $0.answerQuestion() }),
    ]

    static let composer: [Action] = [
        Action(id: "composer.send", title: "Send", keys: ["⏎", "⌘⏎"], label: "⏎", context: .composer, hint: 0,
               perform: { $0.sendComposer() }),
        Action(id: "composer.newline", title: "Newline", keys: ["⇧⏎"], label: "⇧⏎", context: .composer, hint: 1,
               perform: { $0.composer += "\n" }),
        Action(id: "composer.leave", title: "Leave", keys: ["esc"], label: "esc", context: .composer, hint: 2,
               perform: { $0.composerActive = false }),
    ]

    static let driving: [Action] = [
        Action(id: "control.give", title: "Give back control", keys: [], label: "click switch", context: .driving, hint: 0,
               enabled: { $0.driving }, perform: { $0.giveBack() }),
    ]

    static let scenes: [Action] = {
        var out: [Action] = [
            Action(id: "scene.play", title: "Play run from the start", keys: [], label: "", context: .prototype,
                   perform: { $0.playRun(fromStart: true) }),
            Action(id: "scene.boot", title: "Boot reveal", keys: [], label: "", context: .prototype,
                   perform: { $0.startBoot() }),
            Action(id: "scene.control", title: "Take control / give back", keys: [], label: "", context: .prototype,
                   perform: { m in m.driving ? m.giveBack() : m.takeControl() }),
            Action(id: "scene.fail", title: "Verdict lands: FAIL", keys: [], label: "", context: .prototype,
                   perform: { m in m.selectedRun = Synthetic.runID; m.landVerdict(.fail) }),
            Action(id: "scene.pass", title: "Verdict lands: PASS", keys: [], label: "", context: .prototype,
                   perform: { m in m.selectedRun = Synthetic.runID; m.landVerdict(.pass) }),
            Action(id: "scene.powerdown", title: "Power-down", keys: [], label: "", context: .prototype,
                   perform: { $0.powerDown() }),
            Action(id: "scene.restore", title: "Restore machine", keys: [], label: "", context: .prototype,
                   enabled: { $0.machineDestroyed }, perform: { $0.restoreMachine() }),
            Action(id: "scene.welcome", title: "Welcome (no daemon)", keys: [], label: "", context: .prototype,
                   perform: { m in m.welcomeStart = m.now }),
            Action(id: "scene.reduce", title: "Simulate reduce motion", keys: [], label: "", context: .prototype,
                   perform: { $0.simulateReduceMotion.toggle() }),
            Action(id: "scene.strip", title: "Show / hide prototype strip", keys: [], label: "", context: .prototype,
                   perform: { $0.showStrip.toggle() }),
        ]
        for theme in ThemeID.allCases {
            out.append(Action(id: "theme.\(theme.rawValue)", title: "Theme: \(theme.title)", keys: [], label: "", context: .prototype,
                              perform: { $0.theme = theme }))
        }
        for mode in [LayoutMode.wide, .medium, .narrow] {
            out.append(Action(id: "layout.\(mode.rawValue)", title: "Layout: \(mode.rawValue)", keys: [], label: "", context: .prototype,
                              perform: { $0.forcedLayout = mode }))
        }
        out.append(Action(id: "layout.auto", title: "Layout: fit window", keys: [], label: "", context: .prototype,
                          perform: { $0.forcedLayout = nil }))
        return out
    }()

    /// The contexts that are live right now, most specific first.
    static func contexts(_ m: PrototypeModel) -> [ActionContext] {
        if m.driving { return [.driving] }
        if m.composerActive { return [.composer] }
        var c: [ActionContext] = []
        if m.questionOpen && m.focus == .transcript { c.append(.question) }
        if m.verdict != nil { c.append(.verdict) }
        switch m.focus {
        case .screen: c.append(.screen)
        case .runs: c.append(.runs)
        case .steps: c.append(.steps)
        case .transcript: c.append(.transcript)
        }
        c.append(.global)
        return c
    }

    static func handle(_ key: String, _ m: PrototypeModel) -> Bool {
        let live = contexts(m)
        for ctx in live {
            if let a = all.first(where: { $0.context == ctx && $0.keys.contains(key) && $0.enabled(m) }) {
                a.perform(m)
                return true
            }
        }
        // Screen actions (capture, marks) also work from any pane of an open run.
        if !m.driving, !m.composerActive,
           let a = all.first(where: { $0.context == .screen && $0.keys.contains(key) && $0.enabled(m) }) {
            a.perform(m)
            return true
        }
        return false
    }

    /// Up to `limit` hints for the bar, context-specific first.
    static func hints(_ m: PrototypeModel) -> [Action] {
        let live = contexts(m)
        var out: [Action] = []
        for ctx in live {
            let here = all.filter { $0.context == ctx && $0.hint != nil && $0.enabled(m) }
                .sorted { ($0.hint ?? 0) < ($1.hint ?? 0) }
            out += here
        }
        if !m.driving, !m.composerActive, m.focus == .screen || m.focus == .transcript || m.focus == .steps {
            out += all.filter { $0.context == .screen && $0.hint != nil && $0.enabled(m) && !out.map(\.id).contains($0.id) }
        }
        return out
    }

    static func fuzzy(_ query: String, _ text: String) -> Int? {
        let q = query.lowercased().filter { $0 != " " }
        if q.isEmpty { return 0 }
        let t = Array(text.lowercased())
        var score = 0
        var ti = 0
        var last = -2
        for ch in q {
            guard let found = t[ti...].firstIndex(of: ch) else { return nil }
            score += found == last + 1 ? 3 : (found == 0 || t[found - 1] == " " ? 2 : 1)
            last = found
            ti = found + 1
        }
        return score * 100 - t.count
    }

    static func paletteItems(_ m: PrototypeModel) -> [Action] {
        let pool = all.filter { $0.context != .composer && $0.context != .palette && $0.context != .driving && $0.id != "palette" }
        let scored = pool.compactMap { a -> (Action, Int)? in
            guard let s = fuzzy(m.paletteQuery, a.title + " " + a.context.title) else { return nil }
            return (a, s)
        }
        if m.paletteQuery.isEmpty { return scored.map(\.0) }
        return scored.sorted { $0.1 > $1.1 }.map(\.0)
    }
}

// MARK: - Model verbs the registry calls

extension PrototypeModel {
    var canBack: Bool {
        paletteOpen || helpOpen || zoomed != nil || confirmDestroy || welcomeStart != nil || focus != .runs || !expandedSteps.isEmpty
    }

    func back() {
        if confirmDestroy { confirmDestroy = false; return }
        if welcomeStart != nil { welcomeStart = nil; return }
        if helpOpen { helpOpen = false; return }
        if zoomed != nil { zoomed = nil; return }
        switch focus {
        case .steps where !expandedSteps.isEmpty: expandedSteps = []
        case .runs: break
        default: focus = .runs
        }
    }

    func goto(_ pane: Pane) {
        focus = pane
        narrowPane = pane
        if zoomed != nil { zoomed = pane }
    }

    func cyclePane(_ d: Int) {
        let order: [Pane] = [.runs, .screen, .steps, .transcript]
        let i = order.firstIndex(of: focus) ?? 0
        goto(order[(i + d + order.count) % order.count])
    }

    func move(_ d: Int) {
        switch focus {
        case .runs:
            let ids = Synthetic.runs.map(\.id)
            let i = ids.firstIndex(of: runCursor) ?? 0
            runCursor = ids[max(0, min(ids.count - 1, i + d))]
        case .steps:
            let ids = visibleSteps.map(\.id)
            guard !ids.isEmpty else { return }
            let i = selectedStep.flatMap { ids.firstIndex(of: $0) } ?? ids.count
            selectedStep = ids[max(0, min(ids.count - 1, i + d))]
            stepsBack = 0
        case .transcript:
            if questionOpen { questionOption = max(0, min(2, questionOption + d)); return }
            let ids = visibleMessages.map(\.id)
            guard !ids.isEmpty else { return }
            let i = selectedMessage.flatMap { ids.firstIndex(of: $0) } ?? ids.count
            selectedMessage = ids[max(0, min(ids.count - 1, i + d))]
            transcriptBack = 0
        case .screen:
            break
        }
    }

    func open() {
        switch focus {
        case .runs:
            selectedRun = runCursor
            focus = .transcript
            selectedMessage = nil
            selectedStep = nil
            appearAt = now
        case .steps:
            guard let s = selectedStep else { return }
            if expandedSteps.contains(s) { expandedSteps.remove(s) } else { expandedSteps.insert(s) }
        case .transcript:
            if questionOpen { answerQuestion(); return }
            guard let id = selectedMessage else { return }
            if expandedMessages.contains(id) { expandedMessages.remove(id) } else { expandedMessages.insert(id) }
        case .screen:
            zoomed = zoomed == nil ? .screen : nil
        }
    }

    func answerQuestion() {
        guard questionOpen else { return }
        playT = Synthetic.answerAt
        runStateChangedAt[Synthetic.runID] = now
    }

    func runPalette(_ a: Action) {
        paletteOpen = false
        if a.enabled(self) { a.perform(self) }
    }
}
