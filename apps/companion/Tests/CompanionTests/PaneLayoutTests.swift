import CoreGraphics
import XCTest

@testable import Companion

/// The adaptive layout of companion ADR 0004 decision 8 (in points since ADR 0008): the
/// width classes, `z` zoom, what `tab` cycles, and where each pane goes. Pure: no window.
final class PaneLayoutTests: XCTestCase {
    private let tokens = DesignData.shared.tokens.layout

    private func layout(_ widthClass: WidthClass, focus: FocusPane = .stage, stage: StagePane = .screen,
                        zoom: ZoomTarget? = nil, runOpen: Bool = true, sidebarShown: Bool = true,
                        conversationShown: Bool = true) -> PaneLayout {
        PaneLayout(widthClass: widthClass, focus: focus, stage: stage, zoom: zoom, runOpen: runOpen, hasRuns: true,
                   sidebarShown: sidebarShown, conversationShown: conversationShown)
    }

    // MARK: - Width classes

    func testTheWidthPicksTheClass() {
        XCTAssertEqual(WidthClass.of(width: 2560), .wide)
        XCTAssertEqual(WidthClass.of(width: 1600), .wide)
        XCTAssertEqual(WidthClass.of(width: tokens.wideMinWidth), .wide)
        XCTAssertEqual(WidthClass.of(width: tokens.wideMinWidth - 1), .medium)
        XCTAssertEqual(WidthClass.of(width: 1200), .medium)
        XCTAssertEqual(WidthClass.of(width: tokens.mediumMinWidth), .medium)
        XCTAssertEqual(WidthClass.of(width: tokens.mediumMinWidth - 1), .narrow)
        XCTAssertEqual(WidthClass.of(width: 820), .narrow)
        XCTAssertEqual(WidthClass.of(width: 0), .narrow)
    }

    /// The breakpoints are points, and the classes nest: a wide window has room for the
    /// runs column, the least stage and the least conversation; a medium one for the
    /// strip, the least stage and the least conversation.
    func testTheBreakpointsLeaveEachClassItsPanes() {
        XCTAssertGreaterThan(tokens.wideMinWidth, tokens.mediumMinWidth)
        XCTAssertGreaterThanOrEqual(tokens.wideMinWidth,
                                    tokens.runsMaxWidth + 1 + tokens.stageMinWidth + 1 + tokens.conversationMinWidth)
        XCTAssertGreaterThanOrEqual(tokens.mediumMinWidth,
                                    tokens.runsStripWidth + 1 + tokens.stageMinWidth + 1 + tokens.conversationMinWidth)
        // The window's own minimum is narrow: one pane at a time.
        XCTAssertEqual(WidthClass.of(width: RunLayout.windowMinimum.width), .narrow)
    }

    // MARK: - Zoom

    func testZTogglesTheFocusedPane() {
        var zoom = ZoomState()
        XCTAssertFalse(zoom.isZoomed)
        zoom.toggle(focus: .screen)
        XCTAssertEqual(zoom.target, .screen)
        zoom.toggle(focus: .screen)
        XCTAssertNil(zoom.target, "z again restores")
        zoom.toggle(focus: .conversation)
        XCTAssertEqual(zoom.target, .conversation)
        // z while zoomed restores, whatever has focus.
        zoom.toggle(focus: .steps)
        XCTAssertNil(zoom.target)
    }

    func testNothingToZoomStaysUnzoomed() {
        var zoom = ZoomState()
        zoom.toggle(focus: nil)
        XCTAssertNil(zoom.target)
    }

    func testEscRestoresFirstAndOnlyWhenZoomed() {
        var zoom = ZoomState()
        XCTAssertFalse(zoom.escape(), "esc with nothing zoomed goes on to back out")
        zoom.toggle(focus: .steps)
        XCTAssertTrue(zoom.escape())
        XCTAssertNil(zoom.target)
        XCTAssertFalse(zoom.escape())
    }

    /// As tmux: selecting another pane ends the zoom; the zoomed pane itself keeps it.
    func testFocusMovingElsewhereRestores() {
        var zoom = ZoomState()
        zoom.toggle(focus: .screen)
        zoom.focusMoved(to: .screen)
        XCTAssertEqual(zoom.target, .screen)
        zoom.focusMoved(to: .steps)
        XCTAssertNil(zoom.target)
        zoom.toggle(focus: .conversation)
        zoom.restore()
        XCTAssertNil(zoom.target)
    }

    func testTheZoomTargetIsTheFocusedPaneOrPart() {
        XCTAssertEqual(layout(.wide, focus: .stage, stage: .screen).zoomFocus, .screen)
        XCTAssertEqual(layout(.wide, focus: .stage, stage: .steps).zoomFocus, .steps)
        XCTAssertEqual(layout(.wide, focus: .conversation).zoomFocus, .conversation)
        XCTAssertEqual(layout(.wide, focus: .sidebar).zoomFocus, .runs)
        XCTAssertNil(layout(.wide, focus: .stage, runOpen: false).zoomFocus, "no run: nothing to zoom")
        XCTAssertNil(layout(.wide, focus: .conversation, conversationShown: false).zoomFocus)
    }

    func testAZoomShowsOnlyItsPane() {
        let screen = layout(.wide, zoom: .screen)
        XCTAssertEqual(screen.runs, .hidden)
        XCTAssertTrue(screen.screenShown)
        XCTAssertFalse(screen.stepsShown)
        XCTAssertFalse(screen.headerShown)
        XCTAssertFalse(screen.conversationShown(fits: true))

        let steps = layout(.medium, stage: .steps, zoom: .steps)
        XCTAssertFalse(steps.screenShown, "the screen is covered, not gone: it stays in the tree")
        XCTAssertTrue(steps.stepsShown)
        XCTAssertEqual(steps.steps, .list)

        let conversation = layout(.wide, focus: .conversation, zoom: .conversation)
        XCTAssertFalse(conversation.stageShown)
        XCTAssertTrue(conversation.conversationShown(fits: false))

        let runs = layout(.wide, focus: .sidebar, zoom: .runs)
        XCTAssertEqual(runs.runs, .pane)
        XCTAssertFalse(runs.detailShown)
    }

    // MARK: - Tab

    func testTabCyclesThePanesEachClassShows() {
        XCTAssertEqual(layout(.wide).cycle, [.sidebar, .stage, .conversation])
        XCTAssertEqual(layout(.wide, sidebarShown: false).cycle, [.stage, .conversation], "runs folded to their strip")
        XCTAssertEqual(layout(.medium).cycle, [.stage, .conversation], "the strip opens with g r or esc")
        XCTAssertEqual(layout(.narrow).cycle, [.sidebar, .stage, .conversation])
        XCTAssertEqual(layout(.narrow, conversationShown: false).cycle, [.sidebar, .stage])
        XCTAssertEqual(layout(.medium, runOpen: false).cycle, [.sidebar])
    }

    func testTabWrapsBothWays() {
        let narrow = layout(.narrow)
        XCTAssertEqual(narrow.cycled(from: .sidebar, by: 1), .stage)
        XCTAssertEqual(narrow.cycled(from: .conversation, by: 1), .sidebar)
        XCTAssertEqual(narrow.cycled(from: .sidebar, by: -1), .conversation)
        let medium = layout(.medium, focus: .sidebar)
        XCTAssertEqual(medium.cycled(from: .conversation, by: 1), .stage)
        // From the runs opened over a medium window, tab goes into the run.
        XCTAssertEqual(medium.cycled(from: .sidebar, by: 1), .stage)
    }

    // MARK: - What each class shows

    func testWideShowsRunsStageAndConversation() {
        let wide = layout(.wide)
        XCTAssertEqual(wide.runs, .column)
        XCTAssertTrue(wide.detailShown)
        XCTAssertTrue(wide.screenShown)
        XCTAssertTrue(wide.stepsShown)
        XCTAssertEqual(wide.steps, .list, "Screen and Steps are no longer tabs: the steps are under the screen")
        XCTAssertTrue(wide.conversationShown(fits: true))
        XCTAssertFalse(wide.runsOverlay)
    }

    func testMediumFoldsTheRunsAndTheSteps() {
        let medium = layout(.medium)
        XCTAssertEqual(medium.runs, .strip)
        XCTAssertEqual(medium.steps, .track)
        XCTAssertEqual(layout(.medium, stage: .steps).steps, .list, "the steps with the keys open")
        XCTAssertFalse(medium.runsOverlay)
        XCTAssertTrue(layout(.medium, focus: .sidebar).runsOverlay, "the runs with the keys open over the run")
        XCTAssertTrue(layout(.wide, focus: .sidebar, sidebarShown: false).runsOverlay)
    }

    func testNarrowShowsOnePane() {
        let runs = layout(.narrow, focus: .sidebar)
        XCTAssertEqual(runs.runs, .pane)
        XCTAssertFalse(runs.detailShown)

        let stage = layout(.narrow, focus: .stage)
        XCTAssertEqual(stage.runs, .hidden)
        XCTAssertTrue(stage.stageShown)
        XCTAssertFalse(stage.conversationShown(fits: true))
        XCTAssertEqual(stage.steps, .track)

        let conversation = layout(.narrow, focus: .conversation)
        XCTAssertFalse(conversation.stageShown)
        XCTAssertTrue(conversation.conversationShown(fits: false))

        // With no runs yet the welcome shows, not an empty list.
        var empty = layout(.narrow, focus: .sidebar, runOpen: false)
        empty.hasRuns = false
        XCTAssertTrue(empty.detailShown)
    }

    // MARK: - Frames

    func testWideFramesGiveTheExtraWidthToTheStage() {
        let size = CGSize(width: 1600, height: 926)
        let window = layout(.wide).window(in: size, runsWidth: tokens.runsWidth)
        XCTAssertEqual(window.runs.width, tokens.runsWidth)
        XCTAssertEqual(window.detail.minX, tokens.runsWidth + 1)
        let run = layout(.wide).run(in: window.detail.size, conversationWidth: tokens.conversationWidth)
        XCTAssertEqual(run.conversation.width, tokens.conversationWidth, "the transcript keeps its measure")
        XCTAssertEqual(run.stage.width, window.detail.width - tokens.conversationWidth - 1)

        // A wider window: the conversation stays, the stage grows.
        let wider = layout(.wide).window(in: CGSize(width: 2400, height: 926), runsWidth: tokens.runsWidth)
        let widerRun = layout(.wide).run(in: wider.detail.size, conversationWidth: tokens.conversationWidth)
        XCTAssertEqual(widerRun.conversation.width, tokens.conversationWidth)
        XCTAssertEqual(widerRun.stage.width - run.stage.width, 800)
    }

    func testMediumFramesFoldTheRunsToTheStrip() {
        let window = layout(.medium).window(in: CGSize(width: 1200, height: 726), runsWidth: tokens.runsWidth)
        XCTAssertEqual(window.runs.width, tokens.runsStripWidth)
        XCTAssertEqual(window.detail.width, 1200 - tokens.runsStripWidth - 1)
    }

    func testANarrowPaneTakesTheWholeWindow() {
        let size = CGSize(width: 820, height: 526)
        let whole = CGRect(origin: .zero, size: size)
        let window = layout(.narrow, focus: .conversation).window(in: size, runsWidth: tokens.runsWidth)
        XCTAssertEqual(window.detail, whole)
        let run = layout(.narrow, focus: .conversation).run(in: size, conversationWidth: tokens.conversationWidth)
        XCTAssertEqual(run.conversation, whole)
        XCTAssertTrue(run.conversationFits)
    }

    func testAZoomedPaneTakesTheWholeWindow() {
        let size = CGSize(width: 1600, height: 926)
        let whole = CGRect(origin: .zero, size: size)
        XCTAssertEqual(layout(.wide, zoom: .screen).window(in: size, runsWidth: tokens.runsWidth).detail, whole)
        XCTAssertEqual(layout(.wide, zoom: .conversation).run(in: size, conversationWidth: 440).conversation, whole)
        XCTAssertEqual(layout(.wide, zoom: .screen).stage(in: size, screenNatural: 700).screen, whole)
        XCTAssertEqual(layout(.wide, stage: .steps, zoom: .steps).stage(in: size, screenNatural: 700).steps, whole)
    }

    /// Wide: the screen takes what it needs up to what the steps must keep; the steps take
    /// the rest, so there is no empty band between them.
    func testTheStepsListTakesWhatTheScreenLeaves() {
        let size = CGSize(width: 880, height: 830)
        let stage = layout(.wide).stage(in: size, screenNatural: 400)
        XCTAssertEqual(stage.screen.height, 400)
        XCTAssertEqual(stage.steps.minY, 400)
        XCTAssertEqual(stage.steps.height, 430)

        let tall = layout(.wide).stage(in: size, screenNatural: 2000)
        let least = max(tokens.stepsMinHeight, 830 * tokens.stepsShare)
        XCTAssertEqual(tall.steps.height, least, accuracy: 0.001)
        XCTAssertEqual(tall.screen.height + tall.steps.height, 830, accuracy: 0.001)
    }

    func testTheTrackSitsRightUnderTheScreen() {
        let size = CGSize(width: 700, height: 600)
        let stage = layout(.medium).stage(in: size, screenNatural: 400)
        XCTAssertEqual(stage.steps.height, tokens.stepsTrackHeight)
        XCTAssertEqual(stage.steps.minY, 400)
        let squeezed = layout(.medium).stage(in: size, screenNatural: 900)
        XCTAssertEqual(squeezed.steps.maxY, 600)
    }

    func testTheConversationGivesWayBeforeTheStageMinimum() {
        let run = layout(.wide).run(in: CGSize(width: 700, height: 600), conversationWidth: 440)
        XCTAssertFalse(run.conversationFits)
        XCTAssertEqual(run.stage.width, 700)
    }
}
