import Foundation

// What the hint bar, the `?` help and the Cmd-K palette show, worked out from the
// registry and the state alone (companion ADR 0005), so each has a test.

// MARK: - Hint bar

/// One key and what it does, as the hint bar and the help set it.
struct KeyHint: Equatable, Sendable {
    var id: ActionID?
    var key: String
    var title: String
    var enabled = true
    /// The context it was found in, so clicking it runs the same handler the key would.
    var context: ActionContext?
}

/// The hint bar's one row.
struct HintBarContent: Equatable, Sendable {
    enum Mode: Equatable, Sendable {
        /// The keys that work in the focused pane.
        case normal
        /// A text field has the keyboard: `⏎ send  ⇧⏎ newline  esc leave`.
        case typing
        /// Every key goes to the guest; only a click returns.
        case driving
        /// Driving, with the keyboard somewhere else in the window.
        case drivingElsewhere
        /// "Destroy the machine?" is asked.
        case confirm
        /// `g` was pressed: its second keys.
        case prefix
        /// The palette is open: its own keys.
        case palette
    }

    var mode: Mode
    /// The focused pane's name, leading the row.
    var context: String?
    var hints: [KeyHint]
    /// "accepting" or "disputing", and the seconds left to undo it.
    var undo: UndoHint?
}

struct UndoHint: Equatable, Sendable {
    var word: String
    var seconds: Int
}

enum HintBar {
    /// The row for `state`: only keys that work right now, most specific context first.
    static func content(_ s: ActionState) -> HintBarContent {
        let live = ActionRules.contexts(s)
        let undo = s.undoSeconds.map { UndoHint(word: s.undoWord ?? "sending", seconds: $0) }
        switch live {
        case [.driving]:
            return HintBarContent(mode: .driving, context: nil, hints: [], undo: nil)
        case [.confirm]:
            return HintBarContent(mode: .confirm, context: nil, hints: hints(in: [.confirm], s), undo: nil)
        case [.palette]:
            return HintBarContent(mode: .palette, context: ActionContext.palette.title, hints: hints(in: [.palette], s), undo: nil)
        case [.composer]:
            return HintBarContent(mode: .typing, context: ActionContext.composer.title, hints: hints(in: [.composer], s), undo: undo)
        default:
            break
        }
        if let prefix = s.pendingPrefix {
            let next = ActionRegistry.all.compactMap { spec -> KeyHint? in
                guard let binding = spec.keys.first(where: { $0.chords.count == 2 && $0.chords[0] == prefix }),
                      spec.contexts.contains(where: live.contains),
                      ActionRules.isEnabledAnywhere(spec.id, s) else { return nil }
                return KeyHint(id: spec.id, key: binding.chords[1].label, title: goWord(spec.id))
            }
            return HintBarContent(mode: .prefix, context: prefix.label, hints: next, undo: undo)
        }
        var out = hints(in: live, s)
        out.append(KeyHint(id: .help, key: ActionRegistry.label(.help), title: s.helpOpen ? "less" : "more"))
        let mode: HintBarContent.Mode = s.driving ? .drivingElsewhere : .normal
        var context = live.first?.title
        if let zoomed = s.zoomed, let name = context { context = zoomed.pane == s.pane ? "\(name) zoomed" : name }
        return HintBarContent(mode: mode, context: context, hints: out, undo: undo)
    }

    /// A pane as the hint bar names it: the runs, the stage by its focused part, the
    /// conversation.
    static func paneName(_ pane: FocusPane, stage: StagePane) -> String {
        switch pane {
        case .sidebar: ActionContext.sidebar.title
        case .stage: stage == .screen ? ActionContext.screen.title : ActionContext.steps.title
        case .conversation: ActionContext.conversation.title
        }
    }

    /// The hint's word where it depends on the state: `z` restores while zoomed, and in a
    /// narrow window `tab` names the pane it goes to (`tab → conversation`).
    private static func word(_ spec: ActionSpec, in context: ActionContext, _ s: ActionState) -> String {
        switch spec.id {
        case .zoom where s.zoomed != nil:
            return "restore"
        case .nextPane where s.widthClass == .narrow && s.zoomed == nil:
            let next = s.layout.cycled(from: s.pane, by: 1)
            return "→ " + paneName(next, stage: s.stage)
        default:
            return spec.hintWord(in: context)
        }
    }

    /// The trailing hint: the palette, everywhere it opens.
    static func trailing(_ s: ActionState) -> KeyHint? {
        guard !s.paletteOpen, !s.drivingFocused, !s.confirmingDestroy else { return nil }
        return KeyHint(id: .palette, key: ActionRegistry.label(.palette), title: "commands")
    }

    /// What works in the live contexts, most important first (`ActionSpec.hint`); a key or
    /// an action shows once, from the most specific context that has it.
    private static func hints(in live: [ActionContext], _ s: ActionState) -> [KeyHint] {
        var found: [(spec: ActionSpec, context: ActionContext, depth: Int)] = []
        for (depth, context) in live.enumerated() {
            for spec in ActionRegistry.all where spec.hint != nil && spec.contexts.contains(context) {
                guard !found.contains(where: { $0.spec.id == spec.id }),
                      ActionRules.isEnabled(spec.id, in: context, s) else { continue }
                found.append((spec, context, depth))
            }
        }
        found.sort { a, b in
            (a.spec.hint ?? 0, a.depth) < (b.spec.hint ?? 0, b.depth)
        }
        var out: [KeyHint] = []
        var shownKeys = Set<String>()
        for (spec, context, _) in found {
            let key = spec.hintLabel ?? spec.keyLabel
            guard shownKeys.insert(key).inserted else { continue }
            out.append(KeyHint(id: spec.id, key: key, title: word(spec, in: context, s), context: context))
        }
        return out
    }

    /// `g` then: the pane's name alone.
    private static func goWord(_ id: ActionID) -> String {
        switch id {
        case .goSteps: "steps"
        case .goScreen: "screen"
        case .goTranscript: "transcript"
        case .goRuns: "runs"
        case .compose: "compose"
        default: ActionRegistry.spec(id).title.lowercased()
        }
    }
}

// MARK: - Help

/// The hint bar expanded in place: every key, by group, dim when it does not work here.
enum KeyHelp {
    struct Group: Equatable, Sendable {
        var group: ActionGroup
        var hints: [KeyHint]
    }

    static func groups(_ s: ActionState) -> [Group] {
        ActionGroup.allCases.compactMap { group in
            let hints = ActionRegistry.all
                .filter { $0.group == group && !$0.keys.isEmpty && $0.contexts != [.palette] && $0.contexts != [.confirm] }
                // One row for a pair a person reads as one: up with down, previous with next.
                .filter { ![.moveUp, .previousFrame].contains($0.id) }
                .map { spec in
                    KeyHint(id: spec.id, key: helpLabel(spec), title: helpTitle(spec),
                            enabled: ActionRules.isEnabledAnywhere(spec.id, s))
                }
            return hints.isEmpty ? nil : Group(group: group, hints: hints)
        }
    }

    private static func helpLabel(_ spec: ActionSpec) -> String {
        switch spec.id {
        case .moveDown: "j k"
        case .nextFrame: "← →"
        default: spec.keyLabel
        }
    }

    /// Short enough for a help column: the group heading already says the rest.
    private static func helpTitle(_ spec: ActionSpec) -> String {
        switch spec.id {
        case .moveDown: "move"
        case .open: "open"
        case .nextFrame: "frames"
        case .goScreen: "screen"
        case .goSteps: "steps"
        case .goTranscript: "transcript"
        case .goRuns: "runs"
        case .compose: "write to the verifier"
        case .capture: "capture screenshot"
        case .nextFailure: "next error"
        case .previousFailure: "previous error"
        case .nextCheck: "next check"
        case .previousCheck: "previous check"
        case .exportRecording: "export recording"
        case .destroy: "destroy machine"
        case .accept: "accept"
        case .dispute: "dispute"
        case .undo: "undo, 5 s"
        case .palette: "commands"
        case .toggleSidebar: "show or hide runs"
        case .zoom: "zoom or restore"
        case .leave: "leave the field"
        default: spec.title.lowercased()
        }
    }
}

// MARK: - The palette

/// Fuzzy matching for the palette: every query character in order, scored by how the
/// matches sit (runs of adjacent characters and word starts score most, shorter titles
/// win a tie). Nil: no match.
enum Fuzzy {
    static func score(_ query: String, in text: String) -> Int? {
        let needle = Array(query.lowercased().filter { !$0.isWhitespace })
        guard !needle.isEmpty else { return 0 }
        let haystack = Array(text.lowercased())
        var score = 0
        var from = 0
        var last = -2
        for character in needle {
            guard from < haystack.count, let found = haystack[from...].firstIndex(of: character) else { return nil }
            // A run of adjacent characters and a word's first letter count the same: "cap"
            // finds "Capture", "gs" finds "Go to the steps" before "things".
            if found == last + 1 || found == 0 || !haystack[found - 1].isLetter {
                score += 3
            } else {
                score += 1
            }
            last = found
            from = found + 1
        }
        // Shorter titles win a tie: the query covers more of them.
        return score * 100 - haystack.count
    }
}

struct PaletteItem: Equatable, Sendable {
    var id: ActionID
    var title: String
    var key: String
    var group: String
    var enabled: Bool
    /// Why it is disabled, shown dim beside it.
    var reason: String?
}

enum PaletteModel {
    /// Every palette action, enabled first, filtered and ranked by `query`.
    static func items(query: String, _ s: ActionState) -> [PaletteItem] {
        let pool = ActionRegistry.all.filter(\.inPalette)
        let scored: [(PaletteItem, Int, Int)] = pool.enumerated().compactMap { index, spec in
            guard let score = Fuzzy.score(query, in: spec.title) else { return nil }
            let enabled = ActionRules.isEnabledAnywhere(spec.id, s)
            let item = PaletteItem(id: spec.id, title: spec.title, key: spec.keyLabel, group: spec.group.title,
                                   enabled: enabled, reason: enabled ? nil : ActionRules.whyDisabled(spec.id, s))
            return (item, score, index)
        }
        return scored.sorted { a, b in
            if a.0.enabled != b.0.enabled { return a.0.enabled }
            if query.isEmpty {
                let ra = rank(ActionRegistry.spec(a.0.id).group), rb = rank(ActionRegistry.spec(b.0.id).group)
                return ra != rb ? ra < rb : a.2 < b.2
            }
            if a.1 != b.1 { return a.1 > b.1 }
            return a.2 < b.2
        }
        .map(\.0)
    }

    /// With nothing typed, what a person most likely came for comes first: the verdict and
    /// the run, then the screen, then moving about and the window.
    private static func rank(_ group: ActionGroup) -> Int {
        [ActionGroup.verdict, .run, .screen, .go, .move, .window, .writing].firstIndex(of: group) ?? 99
    }

    /// The selection after moving `delta` rows, kept on the list.
    static func move(_ index: Int, by delta: Int, count: Int) -> Int {
        guard count > 0 else { return 0 }
        return min(max(index + delta, 0), count - 1)
    }
}
