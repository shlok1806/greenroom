import SwiftUI

// The keyboard's state and the only place a view gets a key (companion ADR 0005). Keys
// arrive through `KeyRouter` (a window's key events) and the menu bar, are resolved
// against `ActionRegistry` by `KeyResolver`, and are performed here: by the model itself
// for the window's own actions, or by the handler a view on screen offers. Every
// `.keyboardShortcut` and `.onKeyPress` in the app is in this file, built from a
// registry entry; `ActionRegistryTests` fails on one anywhere else.

/// Keyboard focus, the modes that own the keyboard, and the handlers the views on screen
/// offer. One per window.
@Observable
@MainActor
final class KeyboardModel {
    let store: RunStore

    /// The pane `j`, `k` and `⏎` act in. Moved by a click, `tab`, `g` and `esc`. In a
    /// narrow window it is also the one pane on screen.
    var pane: FocusPane = .sidebar {
        didSet { if pane != oldValue { zoom.focusMoved(to: ZoomTarget(pane: pane, stage: stage)) } }
    }
    /// Where the first responder is; `KeyRouter` keeps it current.
    var responder: KeyResponder = .other
    /// The part of the run's stage with the keys: the screen, or the steps under it.
    /// `RootView` keeps it across launches.
    var stage: StagePane = .screen {
        didSet { if stage != oldValue { zoom.focusMoved(to: ZoomTarget(pane: pane, stage: stage)) } }
    }
    /// Whether the run's conversation is shown, mirrored from `RootView`.
    var conversationShown = true {
        didSet {
            guard !conversationShown else { return }
            if zoom.target == .conversation { zoom.restore() }
            if pane == .conversation { pane = .stage }
        }
    }
    /// Wide windows: the runs as a column rather than a strip.
    var sidebarShown = true
    /// The window's width class, from its width (`RootView`).
    var widthClass: WidthClass = .wide
    /// What `z` has zoomed to the window.
    var zoom = ZoomState()

    var paletteOpen = false
    var paletteQuery = "" {
        didSet { if paletteQuery != oldValue { paletteIndex = 0 } }
    }
    var paletteIndex = 0
    var helpOpen = false
    /// The run whose machine "destroy?" is being asked about.
    var confirmingDestroy: String?
    var pendingPrefix: KeyChord?

    /// Bumped to put the cursor in the run search, with `searchText` as its query when set.
    private(set) var searchRequest = 0
    private(set) var searchText: String?
    /// The last search request a runs list on screen acted on: a list that appears after
    /// the request (the runs opened over a medium window) still takes it.
    @ObservationIgnored var searchHandled = 0

    /// What the views on screen offer. Observed, so the hint bar follows them.
    private(set) var available: Set<HandlerKey> = []
    @ObservationIgnored private var handlers: [HandlerKey: [(owner: UUID, perform: @MainActor () -> Void)]] = [:]
    /// The focused text fields that said what they are: what `esc` does besides leaving
    /// (a form's Cancel, clearing a search), and whether Return sends from it.
    @ObservationIgnored private var fields: [(owner: UUID, sends: Bool, leave: (@MainActor () -> Void)?)] = []
    /// Whether the focused field sends on Return (the composer, an answer) rather than
    /// being a search or a reason, so the hint bar offers `⏎ send` only where it works.
    private(set) var typingSends = true
    /// Ends editing in the window (`KeyRouter` sets it); nothing in a view with no window.
    @ObservationIgnored var endEditing: @MainActor () -> Void = {}
    /// The panes' frames in window coordinates, for a click to move focus.
    @ObservationIgnored var paneFrames: [FocusPane: CGRect] = [:]
    /// The stage's two parts, so a click on the steps gives them the keys.
    @ObservationIgnored var stageFrames: [StagePane: CGRect] = [:]

    init(store: RunStore) {
        self.store = store
    }

    // MARK: - State

    /// The window's arrangement now (`PaneLayout`).
    var layout: PaneLayout {
        PaneLayout(widthClass: widthClass, focus: pane, stage: stage, zoom: zoom.target,
                   runOpen: store.selectedRunId != nil, hasRuns: !store.runs.isEmpty,
                   sidebarShown: sidebarShown, conversationShown: conversationShown)
    }

    /// Everything the rules read, now.
    func state(now: Date = Date()) -> ActionState {
        let runId = store.selectedRunId
        let facts = runId.map { store.facts($0, now: now) }
        let held = store.verdictUndo.pending.flatMap { store.verdictUndo.seconds(now: now) == nil ? nil : $0.payload }
        var s = ActionState()
        s.pane = pane
        s.stage = stage
        s.responder = responder
        s.typingSends = typingSends
        s.driving = runId.flatMap { store.existingPilot($0)?.active } ?? false
        s.paletteOpen = paletteOpen
        s.helpOpen = helpOpen
        s.confirmingDestroy = confirmingDestroy != nil
        s.pendingPrefix = pendingPrefix
        s.runOpen = runId != nil
        s.runCount = store.runs.count
        s.sidebarShown = sidebarShown
        s.conversationShown = conversationShown
        s.widthClass = widthClass
        s.zoomed = zoom.target
        s.verdictOpenForReview = (facts?.verdict?.status.isOpen ?? false) && runId.flatMap(store.heldVerdictChoice) == nil
        s.undoSeconds = store.verdictUndo.seconds(now: now)
        s.undoWord = held?.word
        s.machineReady = facts?.machineReady ?? false
        s.machineExists = runId.flatMap { store.details[$0]?.machine } != nil
        s.failureCount = facts?.failures.count ?? 0
        s.available = available
        return s
    }

    // MARK: - Keys

    /// One key press from the window. True when the app took it; false lets the focused
    /// view have it (a text field types, the guest's screen sends it).
    @discardableResult
    func handle(_ chord: KeyChord, responder: KeyResponder) -> Bool {
        if self.responder != responder { self.responder = responder }
        let outcome = KeyResolver.resolve(chord, state())
        if pendingPrefix != nil, outcome != .prefix(chord) { pendingPrefix = nil }
        switch outcome {
        case .perform(let id, let context):
            perform(id, in: context)
            return true
        case .prefix(let chord):
            pendingPrefix = chord
            return true
        case .swallow:
            return true
        case .pass:
            return false
        }
    }

    /// Runs an action: from a key in `context`, or from the palette or the menu bar
    /// (no context: wherever it is enabled).
    func perform(_ id: ActionID, in context: ActionContext? = nil) {
        let runId = store.selectedRunId
        switch id {
        case .palette:
            if paletteOpen { closePalette() } else { openPalette() }
        case .help:
            helpOpen.toggle()
        case .back:
            back()
        case .nextPane:
            cyclePane(1)
        case .previousPane:
            cyclePane(-1)
        case .search:
            search(nil)
        case .goRuns:
            pane = .sidebar
        case .refresh:
            Task { await store.resync() }
        case .toggleSidebar:
            if widthClass == .wide {
                sidebarShown.toggle()
                if !sidebarShown, pane == .sidebar, store.selectedRunId != nil { pane = .stage }
            } else if pane == .sidebar {
                if store.selectedRunId != nil { pane = .stage }
            } else {
                pane = .sidebar
            }
        case .zoom:
            zoom.toggle(focus: state().layout.zoomFocus)
        case .themeSystem, .themeDark, .themeLight, .themeDarkContrast, .themeLightContrast:
            if let theme = ActionRegistry.themes.first(where: { $0.0 == id })?.1 {
                UserDefaults.standard.set(theme.rawValue, forKey: ThemePreference.key)
            }
        case .open where context == .sidebar || (context == nil && pane == .sidebar):
            openFromSidebar()
        case .accept where verdictOutOfSight, .dispute where verdictOutOfSight:
            // The verdict card, where the choice is confirmed or its reason written, comes
            // forward first: out of a zoom, and in a narrow window to the conversation.
            if zoom.target != .conversation { zoom.restore() }
            if widthClass == .narrow, conversationShown { pane = .conversation }
            perform(id, in: context)
        case .accept:
            guard let runId else { return }
            // Asked "accept without opening the evidence?": a second `a` is the answer.
            if store.verdictDraft(runId).confirmingAccept {
                Task { await store.holdAccept(runId: runId) }
            } else {
                Task { await store.requestAccept(runId: runId) }
            }
        case .dispute:
            guard let runId else { return }
            store.updateVerdictDraft(runId) { $0.action = .reject }
        case .undo:
            store.undoVerdictChoice()
        case .destroy:
            confirmingDestroy = runId
        case .confirmDestroy:
            guard let target = confirmingDestroy else { return }
            confirmingDestroy = nil
            Task { await store.destroy(runId: target) }
        case .cancelDestroy:
            confirmingDestroy = nil
        case .giveBack:
            // By design a click, never a key (ADR 0005): nothing to do from here.
            break
        case .leave:
            leave()
        case .send, .newline:
            // The focused field's own keys (`sendOnReturn`).
            break
        case .paletteDown:
            paletteIndex = PaletteModel.move(paletteIndex, by: 1, count: paletteItems().count)
        case .paletteUp:
            paletteIndex = PaletteModel.move(paletteIndex, by: -1, count: paletteItems().count)
        case .paletteRun:
            runPaletteSelection()
        case .paletteClose:
            closePalette()
        default:
            guard let perform = handler(id, in: context) else { return }
            perform()
            switch id {
            case .goSteps, .goScreen: pane = .stage
            case .goTranscript, .compose: pane = .conversation
            default: break
            }
        }
    }

    /// The verdict card is not on screen: another pane is zoomed, or a narrow window
    /// shows another pane than the conversation it sits in.
    private var verdictOutOfSight: Bool {
        if let target = zoom.target, target != .conversation { return true }
        return widthClass == .narrow && conversationShown && pane != .conversation
    }

    func isEnabled(_ id: ActionID) -> Bool {
        ActionRules.isEnabledAnywhere(id, state())
    }

    // MARK: - Handlers

    /// A view on screen offers `ids` in `context`, performed by `perform`. The latest
    /// offer of an action wins; `withdraw` takes back everything one owner offered.
    func offer(_ owner: UUID, context: ActionContext, ids: Set<ActionID>, perform: @escaping @MainActor (ActionID) -> Void) {
        withdrawHandlers(owner)
        for id in ids {
            handlers[HandlerKey(id: id, context: context), default: []].append((owner, { perform(id) }))
        }
        publishAvailable()
    }

    func withdraw(_ owner: UUID) {
        withdrawHandlers(owner)
        typingField(owner, nil)
        publishAvailable()
    }

    private func withdrawHandlers(_ owner: UUID) {
        for key in handlers.keys {
            handlers[key]?.removeAll { $0.owner == owner }
            if handlers[key]?.isEmpty == true { handlers[key] = nil }
        }
    }

    private func publishAvailable() {
        let now = Set(handlers.keys)
        if now != available { available = now }
    }

    private func handler(_ id: ActionID, in context: ActionContext?) -> (@MainActor () -> Void)? {
        if let context, let found = handlers[HandlerKey(id: id, context: context)]?.last { return found.perform }
        return handlers.first { $0.key.id == id }?.value.last?.perform
    }

    /// A focused text field says whether Return sends from it and what `esc` does besides
    /// leaving it; nil when it loses focus.
    func typingField(_ owner: UUID, _ field: (sends: Bool, leave: (@MainActor () -> Void)?)?) {
        fields.removeAll { $0.owner == owner }
        if let field { fields.append((owner, field.sends, field.leave)) }
        let sends = fields.last?.sends ?? true
        if typingSends != sends { typingSends = sends }
    }

    // MARK: - Moving focus

    /// esc, one level out: the help, then a zoom, then evidence opened from the verdict,
    /// then the runs opened over a folded window, then out to the runs.
    private func back() {
        if helpOpen {
            helpOpen = false
            return
        }
        if zoom.escape() { return }
        if let backToVerdict = handler(.backToVerdict, in: nil) {
            backToVerdict()
            return
        }
        if state().runsOverlay {
            if store.selectedRunId != nil { pane = .stage }
            return
        }
        guard store.selectedRunId != nil, pane != .sidebar else { return }
        pane = .sidebar
    }

    private func leave() {
        let custom = fields.last?.leave
        endEditing()
        custom?()
    }

    private func cyclePane(_ delta: Int) {
        pane = state().layout.cycled(from: pane, by: delta)
    }

    /// A click lands in a pane: keys follow it. The runs opened over the run are tried
    /// first, since they lie on top of it.
    func focusPane(at point: CGPoint) {
        let order: [FocusPane] = [.sidebar, .conversation, .stage]
        guard let hit = order.first(where: { paneFrames[$0]?.contains(point) == true }) else { return }
        if hit == .stage, let part = StagePane.allCases.first(where: { stageFrames[$0]?.contains(point) == true }),
           part != stage {
            stage = part
        }
        if hit != pane { pane = hit }
    }

    /// A run was opened with a click in the runs: where the runs cover the run (a narrow
    /// window, or the runs opened over a folded one), the run comes forward.
    func openedRunByClick() {
        let layout = state().layout
        if layout.runs == .pane || layout.runsOverlay { pane = .stage }
    }

    /// The window changed width class, or a run was restored at launch: runs folded to
    /// their strip must not open over the run on their own, and a run opened at launch
    /// in a narrow window shows the run, not the list.
    func settleFocus(runRestored: Bool = false) {
        guard pane == .sidebar, store.selectedRunId != nil else { return }
        let runs = layout.runs
        if runs == .strip || (runRestored && runs == .pane) { pane = .stage }
    }

    /// The screen must be on screen: taking control, a seek from the conversation in a
    /// narrow window. Gives the stage the keys, the screen within it, and ends a zoom on
    /// anything else.
    func showScreen() {
        stage = .screen
        pane = .stage
        if let target = zoom.target, target != .screen { zoom.restore() }
    }

    /// `⏎` in the runs: into the open run, or open the one that most wants you.
    private func openFromSidebar() {
        if store.selectedRunId != nil {
            pane = .stage
            return
        }
        let runs = store.runs
        let suggestion = runs.first { store.facts($0.runId).needsYou }
            ?? runs.first { store.facts($0.runId).isAlive } ?? runs.first
        store.selectedRunId = suggestion?.runId
    }

    private func search(_ text: String?) {
        pane = .sidebar
        searchText = text
        searchRequest += 1
    }

    // MARK: - The palette

    private func openPalette() {
        helpOpen = false
        paletteQuery = ""
        paletteIndex = 0
        paletteOpen = true
    }

    func closePalette() {
        paletteOpen = false
        paletteQuery = ""
    }

    func paletteItems() -> [PaletteItem] {
        PaletteModel.items(query: paletteQuery, state())
    }

    /// Runs the selected command. With nothing matching, the nearest action is to search
    /// the runs for the words (Beautiful UI's Search empty state).
    func runPaletteSelection() {
        let items = paletteItems()
        guard !items.isEmpty else {
            let query = paletteQuery.trimmingCharacters(in: .whitespaces)
            closePalette()
            if !query.isEmpty, store.runs.count > 0 { search(query) }
            return
        }
        let item = items[min(paletteIndex, items.count - 1)]
        guard item.enabled else { return }
        closePalette()
        perform(item.id)
    }

    func runPaletteItem(_ item: PaletteItem) {
        guard item.enabled else { return }
        closePalette()
        perform(item.id)
    }
}

// MARK: - The environment

/// Spelled out rather than `@Entry`: that macro's plugin ships only with Xcode. Nil where
/// a view is hosted alone (tests, the harness): it then offers nothing.
private struct KeyboardKey: EnvironmentKey {
    static var defaultValue: KeyboardModel? { nil }
}

extension EnvironmentValues {
    var keyboard: KeyboardModel? {
        get { self[KeyboardKey.self] }
        set { self[KeyboardKey.self] = newValue }
    }
}

// MARK: - Views offer actions

private struct ActionOffer: ViewModifier {
    let context: ActionContext
    let ids: Set<ActionID>
    let refresh: AnyHashable
    let perform: @MainActor (ActionID) -> Void

    @Environment(\.keyboard) private var keyboard
    @State private var owner = UUID()

    func body(content: Content) -> some View {
        content
            .onAppear { keyboard?.offer(owner, context: context, ids: ids, perform: perform) }
            .onChange(of: ids) { keyboard?.offer(owner, context: context, ids: ids, perform: perform) }
            .onChange(of: refresh) { keyboard?.offer(owner, context: context, ids: ids, perform: perform) }
            .onDisappear { keyboard?.withdraw(owner) }
    }
}

private struct TypingField: ViewModifier {
    let focused: Bool
    let sends: Bool
    let leave: (@MainActor () -> Void)?

    @Environment(\.keyboard) private var keyboard
    @State private var owner = UUID()

    func body(content: Content) -> some View {
        content
            .onChange(of: focused, initial: true) { keyboard?.typingField(owner, focused ? (sends, leave) : nil) }
            .onDisappear { keyboard?.typingField(owner, nil) }
    }
}

private struct PaneFrame: ViewModifier {
    let pane: FocusPane
    /// A pane hidden by the layout (under another, or zoomed away) takes no clicks.
    let active: Bool
    @Environment(\.keyboard) private var keyboard
    @State private var frame: CGRect = .zero

    func body(content: Content) -> some View {
        content
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: {
                frame = $0
                publish()
            }
            .onChange(of: active) { publish() }
            .onDisappear { keyboard?.paneFrames[pane] = nil }
    }

    private func publish() {
        keyboard?.paneFrames[pane] = active ? frame : nil
    }
}

private struct StagePartFrame: ViewModifier {
    let part: StagePane
    let active: Bool
    @Environment(\.keyboard) private var keyboard
    @State private var frame: CGRect = .zero

    func body(content: Content) -> some View {
        content
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: {
                frame = $0
                publish()
            }
            .onChange(of: active) { publish() }
            .onDisappear { keyboard?.stageFrames[part] = nil }
    }

    private func publish() {
        keyboard?.stageFrames[part] = active ? frame : nil
    }
}

extension View {
    /// Offers `ids` in `context` while this view is on screen, each performed by `perform`.
    /// An action is enabled only while it is offered, so offer what applies now; the offer
    /// is renewed when `ids` or `refresh` change (pass what the closure reads that is not
    /// state, such as a run id).
    func offersActions(_ context: ActionContext, _ ids: Set<ActionID>, refresh: some Hashable = 0,
                       perform: @escaping @MainActor (ActionID) -> Void) -> some View {
        modifier(ActionOffer(context: context, ids: ids, refresh: AnyHashable(refresh), perform: perform))
    }

    /// Says, while this text field is focused, whether Return sends from it (the hint bar
    /// offers `⏎ send` only then) and what `esc` does besides leaving it.
    func typingField(focused: Bool, sends: Bool, onLeave leave: (@MainActor () -> Void)? = nil) -> some View {
        modifier(TypingField(focused: focused, sends: sends, leave: leave))
    }

    /// Marks this view as a pane: a click in it moves keyboard focus there, while it is
    /// `active` (shown).
    func keyboardPane(_ pane: FocusPane, active: Bool = true) -> some View {
        modifier(PaneFrame(pane: pane, active: active))
    }

    /// Marks this view as one part of the stage: a click in it gives that part the keys.
    func stagePart(_ part: StagePane, active: Bool = true) -> some View {
        modifier(StagePartFrame(part: part, active: active))
    }
}

// MARK: - Key equivalents from the registry

extension KeyChord {
    var keyEquivalent: KeyEquivalent {
        switch key {
        case .character(let c): KeyEquivalent(c)
        case .enter: .return
        case .escape: .escape
        case .tab: .tab
        case .space: .space
        case .up: .upArrow
        case .down: .downArrow
        case .left: .leftArrow
        case .right: .rightArrow
        case .delete: .delete
        }
    }

    var eventModifiers: EventModifiers {
        var out: EventModifiers = []
        if command { out.insert(.command) }
        if shift { out.insert(.shift) }
        if option { out.insert(.option) }
        if control { out.insert(.control) }
        return out
    }

    /// A Return press as SwiftUI reports it to a text field.
    init?(press: KeyPress) {
        guard press.key == .return else { return nil }
        self.init(key: .enter, command: press.modifiers.contains(.command), shift: press.modifiers.contains(.shift),
                  option: press.modifiers.contains(.option), control: press.modifiers.contains(.control))
    }
}

extension View {
    /// The menu item's key equivalent, from its registry entry: only a Command or Control
    /// chord, since a bare key in the menu bar would fire while a person types.
    @ViewBuilder
    func keyboardShortcut(for id: ActionID) -> some View {
        if let binding = ActionRegistry.spec(id).keys.first, binding.chords.count == 1,
           let chord = binding.chords.first, !chord.isBare {
            keyboardShortcut(chord.keyEquivalent, modifiers: chord.eventModifiers)
        } else {
            self
        }
    }

    /// A text field's Return keys, from the registry's `send` and `newline` entries:
    /// Return and Cmd-Return send; Shift-Return and Option-Return start a new line through
    /// `newline`, when the field has one. Caught here because a vertical `TextField` would
    /// insert the newline itself, and left to itself treats Shift-Return as Return: it
    /// ends editing and selects the whole draft, so the next key replaces it. A blank
    /// draft swallows Return without a newline.
    func sendOnReturn(enabled: Bool, _ action: @escaping () -> Void, newline: (() -> Void)? = nil) -> some View {
        onKeyPress(phases: .down) { press in
            guard let chord = KeyChord(press: press) else { return .ignored }
            let binding = KeyBinding(chord)
            if ActionRegistry.spec(.newline).keys.contains(binding) {
                guard let newline else { return .ignored }
                newline()
                return .handled
            }
            guard ActionRegistry.spec(.send).keys.contains(binding) else { return .ignored }
            if enabled { action() }
            return .handled
        }
    }
}
