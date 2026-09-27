import Foundation

// The one action registry (companion ADR 0005). Every key the app answers to is an entry
// here: its title, its keys, the contexts it works in and where it shows. The hint bar,
// the `?` help, the Cmd-K palette, the menu bar and the key router all read this list; a
// key declared anywhere else is a bug (`ActionRegistryTests` scans the sources for one).
// Pure data and rules, so every rule has a test and no daemon or window is needed.

// MARK: - Keys

/// One key press as the registry names it. A typed character carries its shift in the
/// character (`?`, `G`, `N`); a Command chord names the unshifted key and keeps shift as
/// a modifier (`⇧⌘E`), as the menu bar spells it.
struct KeyChord: Hashable, Sendable {
    enum Key: Hashable, Sendable {
        case character(Character)
        case enter, escape, tab, space, up, down, left, right, delete
    }

    var key: Key
    var command = false
    var shift = false
    var option = false
    var control = false

    static func char(_ c: Character) -> KeyChord { KeyChord(key: .character(c)) }
    static func cmd(_ c: Character, shift: Bool = false) -> KeyChord {
        KeyChord(key: .character(c), command: true, shift: shift)
    }

    static let enter = KeyChord(key: .enter)
    static let escape = KeyChord(key: .escape)
    static let tab = KeyChord(key: .tab)
    static let space = KeyChord(key: .space)
    static let up = KeyChord(key: .up)
    static let down = KeyChord(key: .down)
    static let left = KeyChord(key: .left)
    static let right = KeyChord(key: .right)

    /// A plain key: no Command, Control or Option. The only kind a typing context
    /// swallows, and the only kind no destructive action may have.
    var isBare: Bool { !command && !control && !option }

    /// The key as AppKit reported it (`KeyStroke`, shared with the guest's input).
    init(stroke: KeyStroke) {
        let named: Key? = switch stroke.keyCode {
        case 36, 76: .enter
        case 53: .escape
        case 48: .tab
        case 49: .space
        case 126: .up
        case 125: .down
        case 123: .left
        case 124: .right
        case 51: .delete
        default: nil
        }
        if let named {
            self.init(key: named, command: stroke.command, shift: stroke.shift, option: stroke.option, control: stroke.control)
            return
        }
        if stroke.command || stroke.control {
            let base = stroke.charactersIgnoringModifiers.lowercased().first ?? " "
            self.init(key: .character(base), command: stroke.command, shift: stroke.shift,
                      option: stroke.option, control: stroke.control)
            return
        }
        // Shift is already in the typed character.
        let typed = stroke.characters.first ?? stroke.charactersIgnoringModifiers.first ?? " "
        self.init(key: .character(typed), option: stroke.option)
    }

    init(key: Key, command: Bool = false, shift: Bool = false, option: Bool = false, control: Bool = false) {
        self.key = key
        self.command = command
        self.shift = shift
        self.option = option
        self.control = control
    }

    /// How the hint bar, the help, the palette and the menu bar spell it: modifiers in
    /// the Mac's order (⌃⌥⇧⌘), then the key.
    var label: String {
        var out = ""
        if control { out += "⌃" }
        if option { out += "⌥" }
        if shift { out += "⇧" }
        if command { out += "⌘" }
        switch key {
        case .character(let c): out += command || control ? String(c).uppercased() : String(c)
        case .enter: out += "⏎"
        case .escape: out += "esc"
        case .tab: out += "tab"
        case .space: out += "space"
        case .up: out += "↑"
        case .down: out += "↓"
        case .left: out += "←"
        case .right: out += "→"
        case .delete: out += "⌫"
        }
        return out
    }
}

/// One way to call an action: a chord, or a sequence of them (`g` then `s`).
struct KeyBinding: Hashable, Sendable {
    var chords: [KeyChord]

    init(_ chords: KeyChord...) { self.chords = chords }

    var label: String { chords.map(\.label).joined(separator: " ") }
    var isBare: Bool { chords.allSatisfy(\.isBare) }
}

// MARK: - Contexts

/// Where keyboard focus is, as the registry sees it. The router asks the live ones, most
/// specific first (`ActionRules.contexts`).
enum ActionContext: String, CaseIterable, Sendable {
    /// Anywhere outside a text field, with or without a run open.
    case global
    /// The runs list.
    case sidebar
    /// The run's Screen stage.
    case screen
    /// The run's Steps stage.
    case steps
    /// The conversation column's transcript.
    case conversation
    /// A run is open, whichever pane has focus.
    case run
    /// The run's verdict is open for review (proposed or contested).
    case verdict
    /// A text field: the composer, the run search, an answer, a dispute reason. Bare
    /// keys type.
    case composer
    /// The screen has the keyboard while the lease is held: every key goes to the guest.
    case driving
    /// The Cmd-K palette is open.
    case palette
    /// The inline "destroy the machine?" question is asked.
    case confirm

    /// The pane's name, as the hint bar leads with it.
    var title: String {
        switch self {
        case .global: "everywhere"
        case .sidebar: "runs"
        case .screen: "screen"
        case .steps: "steps"
        case .conversation: "conversation"
        case .run: "run"
        case .verdict: "verdict"
        case .composer: "typing"
        case .driving: "driving"
        case .palette: "commands"
        case .confirm: "destroy"
        }
    }
}

/// The groups of the `?` help, in the order they show.
enum ActionGroup: String, CaseIterable, Sendable {
    case move, go, run, screen, verdict, writing, window

    var title: String {
        switch self {
        case .move: "Move"
        case .go: "Go to"
        case .run: "Run"
        case .screen: "Screen"
        case .verdict: "Verdict"
        case .writing: "Writing"
        case .window: "Window"
        }
    }
}

/// Which menu of the menu bar an action sits in, if any.
enum MenuPlacement: Sendable {
    case view, run
}

// MARK: - Actions

enum ActionID: String, CaseIterable, Sendable {
    // Moving
    case moveDown, moveUp, open, back, nextPane, previousPane, search
    case goSteps, goScreen, goTranscript, goRuns, compose, latest
    // The window
    case palette, help, refresh, toggleSidebar, toggleConversation, zoom
    case themeSystem, themeDark, themeLight, themeDarkContrast, themeLightContrast
    // The run
    case takeControl, giveBack, capture, exportRecording, followLive
    case nextFailure, previousFailure, destroy, confirmDestroy, cancelDestroy, continueVerifier
    // The screen
    case play, previousFrame, nextFrame, speed, backToVerdict, clickMarks
    // The verdict
    case accept, dispute, undo, nextCheck, previousCheck
    // Writing
    case send, newline, leave
    // The palette
    case paletteDown, paletteUp, paletteRun, paletteClose
}

struct ActionSpec: Sendable {
    let id: ActionID
    let title: String
    /// The first is the one shown. Empty: no key (palette and menu only, or a click).
    let keys: [KeyBinding]
    let contexts: [ActionContext]
    let group: ActionGroup
    /// Order in the hint bar, lower first; nil never shows there. The verdict's choices
    /// lead (0), then the focused pane's keys (1 to 4), the run's (5 to 9), and the
    /// window's (10 and up).
    var hint: Int?
    /// The hint bar's word for it, per context when it differs (`⏎ open`, `⏎ expand`).
    var hintTitles: [ActionContext: String] = [:]
    var hintTitle: String?
    /// What the hint bar shows for the key, when it is not the first binding's label
    /// (`↑↓` for moving, `← →` for frames).
    var hintLabel: String?
    var menu: MenuPlacement?
    /// Its menu item's title, when it differs from `title`.
    var menuTitle: String?
    /// Destroys something that cannot be brought back. Never on a bare key (ADR 0005).
    var destructive = false
    /// Listed in the Cmd-K palette. Keys that only make sense inside a mode (the palette's
    /// own, the confirm, the text field's) are not.
    var inPalette = true
    /// Handled by the focused text field itself (built from this entry by the view), not
    /// by the router: Return sends, Shift-Return breaks the line.
    var handledByField = false

    var keyLabel: String { keys.first?.label ?? "" }

    func hintWord(in context: ActionContext) -> String {
        hintTitles[context] ?? hintTitle ?? title.lowercased()
    }
}

enum ActionRegistry {
    static let all: [ActionSpec] = moving + window + running + screen + verdict + writing + paletteKeys

    private static let panes: [ActionContext] = [.sidebar, .steps, .conversation]

    static let moving: [ActionSpec] = [
        ActionSpec(id: .moveDown, title: "Next item", keys: [KeyBinding(.char("j")), KeyBinding(.down)],
                   contexts: panes, group: .move, hint: 1,
                   hintTitles: [.sidebar: "runs", .steps: "steps", .conversation: "messages"], hintLabel: "↑↓",
                   inPalette: false),
        ActionSpec(id: .moveUp, title: "Previous item", keys: [KeyBinding(.char("k")), KeyBinding(.up)],
                   contexts: panes, group: .move, inPalette: false),
        ActionSpec(id: .open, title: "Open the focused item", keys: [KeyBinding(.enter)],
                   contexts: panes, group: .move, hint: 2,
                   hintTitles: [.sidebar: "open", .steps: "expand", .conversation: "show step"], inPalette: false),
        ActionSpec(id: .latest, title: "Jump to latest", keys: [KeyBinding(.char("G"))],
                   contexts: [.screen, .steps, .conversation], group: .move, hintTitle: "latest"),
        ActionSpec(id: .search, title: "Search runs", keys: [KeyBinding(.char("/"))],
                   contexts: [.global], group: .move, hint: 10, hintTitle: "search"),
        ActionSpec(id: .nextPane, title: "Next pane", keys: [KeyBinding(.tab)],
                   contexts: [.global], group: .move, hint: 20, hintTitle: "pane", inPalette: false),
        ActionSpec(id: .previousPane, title: "Previous pane", keys: [KeyBinding(KeyChord(key: .tab, shift: true))],
                   contexts: [.global], group: .move, inPalette: false),
        ActionSpec(id: .goScreen, title: "Go to the screen", keys: [KeyBinding(.char("g"), .char("v"))],
                   contexts: [.run], group: .go, menu: .view, menuTitle: "Screen"),
        ActionSpec(id: .goSteps, title: "Go to the steps", keys: [KeyBinding(.char("g"), .char("s"))],
                   contexts: [.run], group: .go, menu: .view, menuTitle: "Steps"),
        ActionSpec(id: .goTranscript, title: "Go to the transcript", keys: [KeyBinding(.char("g"), .char("t"))],
                   contexts: [.run], group: .go),
        ActionSpec(id: .goRuns, title: "Go to the runs", keys: [KeyBinding(.char("g"), .char("r"))],
                   contexts: [.global], group: .go),
        ActionSpec(id: .compose, title: "Write to the verifier", keys: [KeyBinding(.char("g"), .char("c"))],
                   contexts: [.run], group: .go),
        ActionSpec(id: .back, title: "Back", keys: [KeyBinding(.escape)],
                   contexts: [.global], group: .move, hint: 30, hintTitle: "back", inPalette: false),
    ]

    static let window: [ActionSpec] = [
        ActionSpec(id: .palette, title: "Command palette", keys: [KeyBinding(.cmd("k"))],
                   contexts: [.global, .composer], group: .window, menu: .view, menuTitle: "Command Palette...",
                   inPalette: false),
        ActionSpec(id: .help, title: "All keys", keys: [KeyBinding(.char("?"))],
                   contexts: [.global], group: .window, menu: .view, menuTitle: "All Keys"),
        ActionSpec(id: .refresh, title: "Refresh", keys: [KeyBinding(.cmd("r")), KeyBinding(.char("r"))],
                   contexts: [.global], group: .window, menu: .view),
        ActionSpec(id: .toggleSidebar, title: "Show or hide the runs", keys: [KeyBinding(KeyChord(key: .character("s"), command: true, control: true))],
                   contexts: [.global], group: .window, menu: .view),
        ActionSpec(id: .toggleConversation, title: "Show or hide the conversation", keys: [],
                   contexts: [.run], group: .window, menu: .view),
        // The pane with the keys fills the window; `z` again (or esc) puts it back.
        ActionSpec(id: .zoom, title: "Zoom pane", keys: [KeyBinding(.char("z"))],
                   contexts: [.sidebar, .screen, .steps, .conversation], group: .window, hint: 9,
                   hintTitle: "zoom", menu: .view, menuTitle: "Zoom Focused Pane"),
        ActionSpec(id: .themeSystem, title: "Theme: system", keys: [], contexts: [.global], group: .window),
        ActionSpec(id: .themeDark, title: "Theme: dark", keys: [], contexts: [.global], group: .window),
        ActionSpec(id: .themeLight, title: "Theme: light", keys: [], contexts: [.global], group: .window),
        ActionSpec(id: .themeDarkContrast, title: "Theme: dark, high contrast", keys: [], contexts: [.global], group: .window),
        ActionSpec(id: .themeLightContrast, title: "Theme: light, high contrast", keys: [], contexts: [.global], group: .window),
    ]

    static let running: [ActionSpec] = [
        ActionSpec(id: .takeControl, title: "Take control", keys: [KeyBinding(.char("t"))],
                   contexts: [.run], group: .run, hint: 5, menu: .run, menuTitle: "Take Control"),
        ActionSpec(id: .giveBack, title: "Give back control", keys: [],
                   contexts: [.driving], group: .run, hint: 0, hintTitle: "return", hintLabel: "click switch",
                   inPalette: false),
        ActionSpec(id: .capture, title: "Capture a screenshot", keys: [KeyBinding(.char("c"))],
                   contexts: [.run], group: .run, hint: 6, hintTitle: "capture", menu: .run, menuTitle: "Capture Screenshot"),
        ActionSpec(id: .followLive, title: "Follow live", keys: [KeyBinding(.cmd("l"))],
                   contexts: [.run], group: .run, menu: .run, menuTitle: "Follow Live"),
        ActionSpec(id: .nextFailure, title: "Next error", keys: [KeyBinding(.char("n"))],
                   contexts: [.run], group: .run, hint: 7, hintTitle: "next error", menu: .run, menuTitle: "Next Error"),
        ActionSpec(id: .previousFailure, title: "Previous error", keys: [KeyBinding(.char("N"))],
                   contexts: [.run], group: .run, menu: .run, menuTitle: "Previous Error"),
        ActionSpec(id: .exportRecording, title: "Export the recording", keys: [KeyBinding(.char("e"))],
                   contexts: [.run], group: .run, menu: .run, menuTitle: "Export Recording..."),
        // The verifier stopped at a limit and waits: your note "Continue." starts its next
        // turn (companion ADR 0015).
        ActionSpec(id: .continueVerifier, title: "Continue the verifier", keys: [KeyBinding(.char("C"))],
                   contexts: [.run], group: .run, hint: 0, hintTitle: "continue", menu: .run, menuTitle: "Continue Verifier"),
        ActionSpec(id: .destroy, title: "Destroy the machine", keys: [KeyBinding(KeyChord(key: .delete, command: true))],
                   contexts: [.run], group: .run, menu: .run, menuTitle: "Destroy Machine...", destructive: true),
        ActionSpec(id: .confirmDestroy, title: "Destroy", keys: [KeyBinding(.enter)],
                   contexts: [.confirm], group: .run, hint: 0, hintTitle: "destroy", destructive: true, inPalette: false),
        ActionSpec(id: .cancelDestroy, title: "Keep the machine", keys: [KeyBinding(.escape)],
                   contexts: [.confirm], group: .run, hint: 1, hintTitle: "keep it", inPalette: false),
    ]

    static let screen: [ActionSpec] = [
        ActionSpec(id: .play, title: "Play or pause", keys: [KeyBinding(.space)],
                   contexts: [.screen], group: .screen, hint: 3, hintTitle: "play"),
        ActionSpec(id: .previousFrame, title: "Previous frame", keys: [KeyBinding(.left)],
                   contexts: [.screen], group: .screen),
        ActionSpec(id: .nextFrame, title: "Next frame", keys: [KeyBinding(.right)],
                   contexts: [.screen], group: .screen, hint: 4, hintTitle: "frame", hintLabel: "← →"),
        ActionSpec(id: .speed, title: "Play at 1× or 4×", keys: [KeyBinding(.char("f"))],
                   contexts: [.screen], group: .screen),
        ActionSpec(id: .backToVerdict, title: "Back to the verdict", keys: [],
                   contexts: [.screen, .steps], group: .screen),
        // Where the agent clicked or typed, over the picture (ADR 0006 decision 5).
        ActionSpec(id: .clickMarks, title: "Show or hide click marks", keys: [KeyBinding(.char("m"))],
                   contexts: [.screen], group: .screen, menu: .view, menuTitle: "Click Marks"),
    ]

    static let verdict: [ActionSpec] = [
        ActionSpec(id: .accept, title: "Accept the verdict", keys: [KeyBinding(.char("a"))],
                   contexts: [.verdict], group: .verdict, hint: 0, hintTitle: "accept", menu: .run, menuTitle: "Accept Verdict"),
        ActionSpec(id: .dispute, title: "Dispute the verdict", keys: [KeyBinding(.char("d"))],
                   contexts: [.verdict], group: .verdict, hint: 0, hintTitle: "dispute", menu: .run, menuTitle: "Dispute Verdict..."),
        ActionSpec(id: .undo, title: "Undo accept or dispute", keys: [KeyBinding(.char("u"))],
                   contexts: [.global], group: .verdict, menu: .run, menuTitle: "Undo Verdict Choice"),
        // Walks the verdict's checks and shows each one's evidence (companion ADR 0011).
        ActionSpec(id: .nextCheck, title: "Next check", keys: [KeyBinding(.char("]"))],
                   contexts: [.run], group: .verdict, hint: 1, hintTitle: "checks", hintLabel: "[ ]",
                   menu: .run, menuTitle: "Next Check"),
        ActionSpec(id: .previousCheck, title: "Previous check", keys: [KeyBinding(.char("["))],
                   contexts: [.run], group: .verdict, menu: .run, menuTitle: "Previous Check"),
    ]

    static let writing: [ActionSpec] = [
        ActionSpec(id: .send, title: "Send", keys: [KeyBinding(.enter), KeyBinding(KeyChord(key: .enter, command: true))],
                   contexts: [.composer], group: .writing, hint: 0, hintTitle: "send", inPalette: false, handledByField: true),
        ActionSpec(id: .newline, title: "New line",
                   keys: [KeyBinding(KeyChord(key: .enter, shift: true)), KeyBinding(KeyChord(key: .enter, option: true))],
                   contexts: [.composer], group: .writing, hint: 1, hintTitle: "newline", inPalette: false, handledByField: true),
        ActionSpec(id: .leave, title: "Leave the field", keys: [KeyBinding(.escape)],
                   contexts: [.composer], group: .writing, hint: 2, hintTitle: "leave", inPalette: false),
    ]

    static let paletteKeys: [ActionSpec] = [
        ActionSpec(id: .paletteDown, title: "Next command", keys: [KeyBinding(.down), KeyBinding(KeyChord(key: .character("n"), control: true))],
                   contexts: [.palette], group: .window, hint: 0, hintTitle: "move", hintLabel: "↑↓", inPalette: false),
        ActionSpec(id: .paletteUp, title: "Previous command", keys: [KeyBinding(.up), KeyBinding(KeyChord(key: .character("p"), control: true))],
                   contexts: [.palette], group: .window, inPalette: false),
        ActionSpec(id: .paletteRun, title: "Run the command", keys: [KeyBinding(.enter)],
                   contexts: [.palette], group: .window, hint: 1, hintTitle: "run", inPalette: false),
        ActionSpec(id: .paletteClose, title: "Close the palette", keys: [KeyBinding(.escape), KeyBinding(.cmd("k"))],
                   contexts: [.palette], group: .window, hint: 2, hintTitle: "close", inPalette: false),
    ]

    private static let byID: [ActionID: ActionSpec] = Dictionary(uniqueKeysWithValues: all.map { ($0.id, $0) })

    static func spec(_ id: ActionID) -> ActionSpec {
        guard let spec = byID[id] else { preconditionFailure("\(id) has no registry entry") }
        return spec
    }

    /// An action's key as the window spells it, for tooltips: "Capture the screen (c)".
    static func label(_ id: ActionID) -> String { spec(id).keyLabel }

    /// The entries in one menu of the menu bar, in registry order.
    static func menu(_ placement: MenuPlacement) -> [ActionSpec] {
        all.filter { $0.menu == placement }
    }

    static let themes: [(ActionID, ThemePreference)] = [
        (.themeSystem, .system), (.themeDark, .dark), (.themeLight, .light),
        (.themeDarkContrast, .darkHighContrast), (.themeLightContrast, .lightHighContrast),
    ]
}

// MARK: - The state the rules read

/// The run's panes, as keyboard focus moves between them (`tab`, a click, `g`).
enum FocusPane: String, CaseIterable, Sendable {
    case sidebar, stage, conversation
}

/// Where the first responder is, as far as keys are concerned.
enum KeyResponder: Sendable {
    /// A text field or text view: bare keys type.
    case text
    /// `InputSurfaceView` while the lease is held: every key belongs to the guest.
    case guest
    /// Anything else: the window, a pane, a button.
    case other
}

/// A handler a view offers for an action in one context, while it is on screen.
struct HandlerKey: Hashable, Sendable {
    let id: ActionID
    let context: ActionContext
}

/// Everything the rules need to know about "now", in one value: focus, modes, and what
/// the open run allows. Built by `KeyboardModel` from the store and the views.
struct ActionState: Equatable, Sendable {
    var pane: FocusPane = .sidebar
    var stage: StagePane = .screen
    var responder: KeyResponder = .other
    /// The focused field sends on Return (the composer, an answer), not a search or a reason.
    var typingSends = true
    /// The open run's lease is held.
    var driving = false
    var paletteOpen = false
    var helpOpen = false
    var confirmingDestroy = false
    /// `g` was pressed and waits for its second key.
    var pendingPrefix: KeyChord?
    var runOpen = false
    var runCount = 0
    var sidebarShown = true
    var conversationShown = true
    /// The window's width class and what is zoomed (`PaneLayout`).
    var widthClass: WidthClass = .wide
    var zoomed: ZoomTarget?
    /// The open run's verdict is proposed or contested, and no choice on it is waiting
    /// out its undo.
    var verdictOpenForReview = false
    /// An accept or dispute waits out its undo, with this many whole seconds left.
    var undoSeconds: Int?
    var undoWord: String?
    var machineReady = false
    var machineExists = false
    var failureCount = 0
    /// Handlers the views on screen offer right now.
    var available: Set<HandlerKey> = []

    var typing: Bool { responder == .text }

    /// The window's arrangement as these facts give it.
    var layout: PaneLayout {
        PaneLayout(widthClass: widthClass, focus: pane, stage: stage, zoom: zoomed, runOpen: runOpen,
                   hasRuns: runCount > 0, sidebarShown: sidebarShown, conversationShown: conversationShown)
    }

    /// The runs are open over the run from their strip.
    var runsOverlay: Bool { layout.runsOverlay }
    var drivingFocused: Bool { driving && responder == .guest }
    var undoPending: Bool { undoSeconds != nil }

    func offers(_ id: ActionID, in context: ActionContext) -> Bool {
        available.contains(HandlerKey(id: id, context: context))
    }

    func offersAnywhere(_ id: ActionID) -> Bool {
        available.contains { $0.id == id }
    }

    /// The context of the focused pane.
    var paneContext: ActionContext {
        switch pane {
        case .sidebar: .sidebar
        case .stage: stage == .screen ? .screen : .steps
        case .conversation: .conversation
        }
    }
}

// MARK: - Rules

enum ActionRules {
    /// The contexts live right now, most specific first. A mode owns the keyboard alone:
    /// the palette, the destroy question, driving and a text field each shut out the rest.
    static func contexts(_ s: ActionState) -> [ActionContext] {
        if s.paletteOpen { return [.palette] }
        if s.confirmingDestroy { return [.confirm] }
        if s.drivingFocused { return [.driving] }
        if s.typing { return [.composer] }
        var out: [ActionContext] = [s.runOpen ? s.paneContext : .sidebar]
        if s.runOpen, s.verdictOpenForReview { out.append(.verdict) }
        if s.runOpen { out.append(.run) }
        out.append(.global)
        return out
    }

    /// Whether `id` works in `context` now. Actions a view performs are enabled while
    /// that view offers them; the rest are decided here, from the state alone.
    static func isEnabled(_ id: ActionID, in context: ActionContext, _ s: ActionState) -> Bool {
        switch id {
        case .palette: return !s.drivingFocused
        case .help, .refresh, .toggleSidebar, .goRuns,
             .themeSystem, .themeDark, .themeLight, .themeDarkContrast, .themeLightContrast:
            return true
        case .nextPane, .previousPane: return s.runOpen
        case .zoom: return s.zoomed != nil || s.layout.zoomFocus != nil
        case .search: return s.runCount > 0
        case .back: return canGoBack(s)
        case .open:
            // The runs list opens its selection, or the run that most wants you.
            return context == .sidebar ? s.runCount > 0 : s.offers(.open, in: context)
        case .accept, .dispute: return s.runOpen && s.verdictOpenForReview && !s.undoPending
        case .undo: return s.undoPending
        case .destroy: return s.runOpen && s.machineExists
        case .confirmDestroy, .cancelDestroy: return s.confirmingDestroy
        case .giveBack: return s.driving
        case .send, .newline: return s.typing && s.typingSends
        case .leave: return s.typing
        case .paletteDown, .paletteUp, .paletteRun, .paletteClose: return s.paletteOpen
        default:
            return s.offers(id, in: context)
        }
    }

    /// Enabled in any context it lists: what the palette and the menu bar ask.
    static func isEnabledAnywhere(_ id: ActionID, _ s: ActionState) -> Bool {
        let spec = ActionRegistry.spec(id)
        if spec.contexts.contains(.sidebar) { return isEnabled(id, in: s.runOpen ? s.paneContext : .sidebar, s) }
        return spec.contexts.contains { isEnabled(id, in: $0, s) }
    }

    static func canGoBack(_ s: ActionState) -> Bool {
        s.helpOpen || s.pendingPrefix != nil || s.zoomed != nil || s.offersAnywhere(.backToVerdict) || s.runsOverlay
            || (s.runOpen && s.pane != .sidebar)
    }

    /// Why a disabled action cannot run, for the palette's dim rows.
    static func whyDisabled(_ id: ActionID, _ s: ActionState) -> String {
        let spec = ActionRegistry.spec(id)
        if spec.contexts.contains(.run), !s.runOpen { return "Open a run first" }
        switch id {
        case .accept, .dispute:
            if s.undoPending { return "Waiting to send the last choice" }
            return "No verdict is open for review"
        case .undo: return "Nothing to undo"
        case .takeControl: return s.driving ? "You already have control" : "The machine is not ready"
        case .capture, .followLive: return "The machine is not ready"
        case .destroy: return "This run has no machine"
        case .nextFailure, .previousFailure: return "No step errored"
        case .nextCheck, .previousCheck: return "The verdict has no checks"
        case .continueVerifier: return "The verifier is not stopped at a limit"
        case .exportRecording: return "No recording yet"
        case .play, .previousFrame, .nextFrame, .speed: return "Show a recording first"
        case .backToVerdict: return "No evidence is open"
        case .clickMarks: return "Show the screen first"
        case .search: return "No runs yet"
        case .nextPane, .previousPane: return "Open a run first"
        case .zoom: return "Open a run first"
        case .back: return "Already at the runs"
        default: return "Not available here"
        }
    }
}

// MARK: - Resolving a key

/// What a key press does, given the state.
enum KeyOutcome: Equatable, Sendable {
    /// Run this action in this context.
    case perform(ActionID, ActionContext)
    /// The first key of a sequence: wait for the second.
    case prefix(KeyChord)
    /// Taken by the app, doing nothing (a disabled action, a sequence that went nowhere).
    case swallow
    /// Not the app's: the focused view (a text field, the guest's screen, a menu) gets it.
    case pass
}

enum KeyResolver {
    static func resolve(_ chord: KeyChord, _ s: ActionState) -> KeyOutcome {
        let live = ActionRules.contexts(s)
        // Driving is a hard mode: every key, Cmd-Q included, goes to the guest (ADR 0005, 0009).
        if live == [.driving] { return .pass }
        // The destroy question: Return destroys, any other key keeps the machine.
        if live == [.confirm] {
            return chord == .enter ? .perform(.confirmDestroy, .confirm) : .perform(.cancelDestroy, .confirm)
        }
        if let prefix = s.pendingPrefix {
            let sequence = [prefix, chord]
            if let hit = match(sequence, in: live, s) { return hit }
            // A sequence that goes nowhere ends; its second key does nothing else.
            return .swallow
        }
        let sequence = [chord]
        if let hit = match(sequence, in: live, s) { return hit }
        // In a mode that types (a text field, the palette's query), everything else is the
        // field's: bare keys type.
        if live == [.composer] || live == [.palette] { return .pass }
        if startsSequence(chord, in: live) { return .prefix(chord) }
        return .pass
    }

    /// The first live context with an entry bound to `sequence`: performed when enabled,
    /// swallowed when every entry bound to it is disabled.
    private static func match(_ sequence: [KeyChord], in live: [ActionContext], _ s: ActionState) -> KeyOutcome? {
        var found = false
        for context in live {
            for spec in ActionRegistry.all where spec.contexts.contains(context) && !spec.handledByField {
                guard spec.keys.contains(where: { $0.chords == sequence }) else { continue }
                found = true
                if ActionRules.isEnabled(spec.id, in: context, s) { return .perform(spec.id, context) }
            }
        }
        return found ? .swallow : nil
    }

    private static func startsSequence(_ chord: KeyChord, in live: [ActionContext]) -> Bool {
        ActionRegistry.all.contains { spec in
            spec.contexts.contains(where: live.contains)
                && spec.keys.contains { $0.chords.count > 1 && $0.chords.first == chord }
        }
    }
}
