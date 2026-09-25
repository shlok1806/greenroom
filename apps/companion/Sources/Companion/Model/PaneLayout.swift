import CoreGraphics
import Foundation

// The window's adaptive layout (companion ADR 0004 decision 8, in points since ADR 0008):
// three width classes, `z` zoom, and where each pane goes. Pure: the views hand it the
// size they were given and place their panes where it says, so every rule has a test and
// no window is needed. Every pane stays in the view tree in every arrangement; a pane
// that is not shown is placed but hidden. That is what keeps the live screen, its lease
// and a half-typed draft alive across a resize or a zoom: nothing is rebuilt.

/// The part of the stage that has the keys: the screen, or the steps under it. In a
/// medium or narrow window the steps are a one-row track until they have the keys.
enum StagePane: String, CaseIterable, Identifiable, Sendable {
    case screen
    case steps

    var id: String { rawValue }

    var title: String {
        switch self {
        case .screen: "Screen"
        case .steps: "Steps"
        }
    }
}

/// How wide the window is, as the layout sees it (`tokens.json` `layout`).
enum WidthClass: String, CaseIterable, Sendable {
    /// Runs, then the screen with the steps under it, then the conversation.
    case wide
    /// The runs fold to a strip of marks; the steps to a one-row track.
    case medium
    /// One pane at a time; `tab` cycles.
    case narrow

    static func of(width: Double, _ layout: DesignTokens.Layout = DesignData.shared.tokens.layout) -> WidthClass {
        if width >= layout.wideMinWidth { return .wide }
        if width >= layout.mediumMinWidth { return .medium }
        return .narrow
    }
}

/// What `z` fills the window with: the focused pane, and in the stage, the focused part.
enum ZoomTarget: String, CaseIterable, Sendable {
    case runs, screen, steps, conversation

    init(pane: FocusPane, stage: StagePane) {
        switch pane {
        case .sidebar: self = .runs
        case .stage: self = stage == .screen ? .screen : .steps
        case .conversation: self = .conversation
        }
    }

    var pane: FocusPane {
        switch self {
        case .runs: .sidebar
        case .screen, .steps: .stage
        case .conversation: .conversation
        }
    }

    var title: String { rawValue }
}

/// `z` zooms the focused pane to the window and `z` again restores it (tmux's zoom). Esc
/// restores before it backs out of anything else, and focus moving to another pane
/// restores, as selecting another pane does in tmux. Nothing here touches the lease: a
/// zoomed-away screen stays in the tree.
struct ZoomState: Equatable, Sendable {
    private(set) var target: ZoomTarget?

    var isZoomed: Bool { target != nil }

    /// `z`: zoom `focus`, or restore when anything is zoomed.
    mutating func toggle(focus: ZoomTarget?) {
        target = target == nil ? focus : nil
    }

    /// `esc`: restores, and says whether it did (then esc does nothing else).
    mutating func escape() -> Bool {
        guard target != nil else { return false }
        target = nil
        return true
    }

    /// Focus moved (a click, `tab`, `g`): a zoom on another pane ends.
    mutating func focusMoved(to focus: ZoomTarget) {
        guard let target, target != focus else { return }
        self.target = nil
    }

    /// The zoomed pane went away (the run closed, the conversation was hidden).
    mutating func restore() {
        target = nil
    }
}

/// How the runs show.
enum RunsPresentation: Equatable, Sendable {
    /// A column beside the run, `tokens.layout.runsWidth` wide (wide windows).
    case column
    /// A strip of status marks (medium windows, or wide with the runs hidden); the full
    /// list opens over the run while the runs have the keys.
    case strip
    /// The whole window, as narrow's one pane (or zoomed).
    case pane
    /// Not shown: another pane is zoomed, or narrow shows another pane.
    case hidden
}

/// How the steps show under the screen.
enum StepsPresentation: Equatable, Sendable {
    /// Every step, a list (wide windows, or opened from the track).
    case list
    /// One row: a cell per step and the step at the playhead (medium and narrow).
    case track
}

/// One arrangement of the window's panes, everything but the size: the width class, focus,
/// zoom and what the person asked for. Views compute one, then ask it where each pane goes
/// in the size they were given.
struct PaneLayout: Equatable, Sendable {
    var widthClass: WidthClass = .wide
    var focus: FocusPane = .sidebar
    var stage: StagePane = .screen
    var zoom: ZoomTarget?
    var runOpen = false
    /// Any runs on the list at all: a narrow window with none shows the welcome, not an
    /// empty list.
    var hasRuns = true
    /// Wide windows only: the runs as a column (else a strip). `⌃⌘S` toggles it.
    var sidebarShown = true
    /// The person wants the conversation beside the stage (View > Hide Conversation).
    var conversationShown = true

    // MARK: - What shows

    var runs: RunsPresentation {
        if let zoom { return zoom == .runs ? .pane : .hidden }
        switch widthClass {
        case .narrow: return narrowShowsRuns ? .pane : .hidden
        case .medium: return .strip
        case .wide: return sidebarShown ? .column : .strip
        }
    }

    /// The runs, opened over the run from their strip while they have the keys.
    var runsOverlay: Bool {
        runs == .strip && focus == .sidebar
    }

    private var narrowShowsRuns: Bool {
        focus == .sidebar && hasRuns
    }

    /// The open run (or the empty state where there is none).
    var detailShown: Bool {
        if let zoom { return zoom != .runs }
        if widthClass == .narrow { return !narrowShowsRuns }
        return true
    }

    /// The run's stage column: header, screen and steps.
    var stageShown: Bool {
        guard detailShown else { return false }
        if let zoom { return zoom == .screen || zoom == .steps }
        if widthClass == .narrow { return !(focus == .conversation && conversationShown) }
        return true
    }

    /// The conversation, when it has room (`conversationFits`, from the size).
    func conversationShown(fits: Bool) -> Bool {
        guard detailShown, conversationShown else { return false }
        if let zoom { return zoom == .conversation }
        if widthClass == .narrow { return focus == .conversation }
        return fits
    }

    var screenShown: Bool {
        guard stageShown else { return false }
        return zoom != .steps
    }

    var stepsShown: Bool {
        guard stageShown else { return false }
        return zoom != .screen
    }

    /// The run's header over the stage: gone while a stage part is zoomed.
    var headerShown: Bool {
        zoom == nil
    }

    var steps: StepsPresentation {
        if zoom == .steps || widthClass == .wide { return .list }
        return stage == .steps ? .list : .track
    }

    /// The zoom `z` would make now, or nil where there is nothing to zoom.
    var zoomFocus: ZoomTarget? {
        guard runOpen else { return nil }
        let target = ZoomTarget(pane: focus, stage: stage)
        if target == .conversation, !conversationShown { return nil }
        return target
    }

    // MARK: - Tab

    /// The panes `tab` moves through, in order. Wide: runs (while they are a column),
    /// stage, conversation. Medium: stage and conversation; the runs, folded to their
    /// strip, open with `g r` or esc. Narrow: runs, stage, conversation, one at a time.
    var cycle: [FocusPane] {
        var order: [FocusPane] = []
        let runsInCycle: Bool = switch widthClass {
        case .wide: sidebarShown
        case .medium: false
        case .narrow: true
        }
        if runsInCycle || !runOpen { order.append(.sidebar) }
        if runOpen {
            order.append(.stage)
            if conversationShown { order.append(.conversation) }
        }
        return order
    }

    /// The pane `delta` steps from `pane` in `cycle`, wrapping.
    func cycled(from pane: FocusPane, by delta: Int) -> FocusPane {
        let order = cycle
        guard !order.isEmpty else { return pane }
        guard let at = order.firstIndex(of: pane) else { return order[0] }
        return order[(at + delta + order.count) % order.count]
    }

    // MARK: - Where each pane goes

    /// The window under the top bar and over the hint bar: the runs, the line after them
    /// and the run. A hidden pane is placed on the whole area (under what shows), so it
    /// keeps a sensible size and nothing in it is rebuilt.
    struct WindowFrames: Equatable, Sendable {
        var runs: CGRect
        var divider: CGRect
        var detail: CGRect
    }

    func window(in size: CGSize, runsWidth: Double, tokens: DesignTokens.Layout = DesignData.shared.tokens.layout) -> WindowFrames {
        let whole = CGRect(origin: .zero, size: size)
        let column: Double
        switch runs {
        case .column: column = min(max(runsWidth, tokens.runsMinWidth), tokens.runsMaxWidth)
        case .strip: column = tokens.runsStripWidth
        case .pane, .hidden:
            return WindowFrames(runs: whole, divider: CGRect(x: 0, y: 0, width: 0, height: size.height), detail: whole)
        }
        let left = min(column, size.width)
        return WindowFrames(
            runs: CGRect(x: 0, y: 0, width: left, height: size.height),
            divider: CGRect(x: left, y: 0, width: 1, height: size.height),
            detail: CGRect(x: left + 1, y: 0, width: max(size.width - left - 1, 0), height: size.height)
        )
    }

    /// The run: the stage column, the line before the conversation and the conversation.
    struct RunFrames: Equatable, Sendable {
        var stage: CGRect
        var divider: CGRect
        var conversation: CGRect
        /// Whether the conversation had room beside the stage; when it did not, the
        /// verdict card goes above the stage.
        var conversationFits: Bool
    }

    /// The conversation's width beside the stage: the person's `chosen` width, kept to
    /// the token range and given back to the stage's minimum first. Nil when it does not
    /// fit beside the stage at all.
    static func conversationWidth(_ chosen: Double, in available: Double,
                                  tokens: DesignTokens.Layout = DesignData.shared.tokens.layout) -> Double? {
        let clamped = min(max(chosen, tokens.conversationMinWidth), tokens.conversationMaxWidth)
        guard available > 0 else { return clamped }
        let room = available - tokens.stageMinWidth - 1
        guard room >= tokens.conversationMinWidth else { return nil }
        return min(clamped, room)
    }

    func run(in size: CGSize, conversationWidth chosen: Double,
             tokens: DesignTokens.Layout = DesignData.shared.tokens.layout) -> RunFrames {
        let whole = CGRect(origin: .zero, size: size)
        let hiddenLine = CGRect(x: size.width, y: 0, width: 0, height: size.height)
        let fitted = Self.conversationWidth(chosen, in: size.width, tokens: tokens)
        let fits = widthClass == .narrow || zoom == .conversation || fitted != nil
        let beside = conversationShown(fits: fits) && stageShown
        guard beside, let width = fitted else {
            return RunFrames(stage: whole, divider: hiddenLine, conversation: whole, conversationFits: fits)
        }
        let stageWidth = max(size.width - width - 1, 0)
        return RunFrames(
            stage: CGRect(x: 0, y: 0, width: stageWidth, height: size.height),
            divider: CGRect(x: stageWidth, y: 0, width: 1, height: size.height),
            conversation: CGRect(x: stageWidth + 1, y: 0, width: width, height: size.height),
            conversationFits: true
        )
    }

    /// The stage under the run's header: the screen, then the steps.
    struct StageFrames: Equatable, Sendable {
        var screen: CGRect
        var steps: CGRect
    }

    /// `screenNatural` is the height the screen wants at this width (its picture fitted
    /// to the width, plus its player). The screen never takes more; the extra goes to the
    /// steps, so there is no empty band between them.
    func stage(in size: CGSize, screenNatural: Double,
               tokens: DesignTokens.Layout = DesignData.shared.tokens.layout) -> StageFrames {
        let whole = CGRect(origin: .zero, size: size)
        if !screenShown { return StageFrames(screen: whole, steps: whole) }
        if !stepsShown { return StageFrames(screen: whole, steps: whole) }
        let height = Double(size.height)
        let natural = max(screenNatural, 0)
        let screenHeight: Double
        let stepsHeight: Double
        switch steps {
        case .track:
            // The track sits right under the picture's player; what is left stays below it.
            stepsHeight = min(tokens.stepsTrackHeight, height)
            screenHeight = min(natural, height - stepsHeight)
        case .list:
            let share = widthClass == .wide ? tokens.stepsShare : tokens.stepsExpandedShare
            let least = min(max(tokens.stepsMinHeight, height * share), height)
            // The screen takes what it needs up to what the steps leave; the steps the rest.
            screenHeight = min(natural, height - least)
            stepsHeight = height - screenHeight
        }
        return StageFrames(
            screen: CGRect(x: 0, y: 0, width: size.width, height: screenHeight),
            steps: CGRect(x: 0, y: screenHeight, width: size.width, height: stepsHeight)
        )
    }
}
