import CoreGraphics
import Foundation

private let view = CGRect(x: 100, y: 200, width: 400, height: 300) // y 200 to 500

func testARowBelowTheViewScrollsDownToShowWithAMargin() {
    let row = CGRect(x: 110, y: 900, width: 380, height: 24) // ends at 924
    let d = intoViewDistance(frame: row, view: view)
    expectEqual(d.dy, 924 - (500 - 8), "down until its bottom is 8 points inside")
    expectEqual(d.dx, 0)
    expect(!insideView(row, view), "not in view yet")
    expect(insideView(row.offsetBy(dx: 0, dy: -d.dy), view), "in view after that distance")
}

func testARowAboveTheViewScrollsUp() {
    let row = CGRect(x: 110, y: 50, width: 380, height: 24)
    expectEqual(intoViewDistance(frame: row, view: view).dy, 50 - (200 + 8), "negative: up")
}

func testARowInViewNeedsNoScroll() {
    let row = CGRect(x: 110, y: 300, width: 380, height: 24)
    expectEqual(intoViewDistance(frame: row, view: view).dy, 0)
    expect(insideView(row, view))
    expect(insideView(CGRect(x: 110, y: 199.7, width: 380, height: 24), view), "resting half a point out")
}

func testAnElementTallerThanTheViewIsBroughtToItsStart() {
    let tall = CGRect(x: 110, y: 800, width: 380, height: 900)
    expectEqual(intoViewDistance(frame: tall, view: view).dy, 800 - 200)
    let filling = CGRect(x: 110, y: 100, width: 380, height: 900)
    expectEqual(intoViewDistance(frame: filling, view: view).dy, 0, "one that fills the view already shows")
    expect(insideView(filling, view))
}

func testIntoViewStopsAboveTheDock() {
    // A 1024x768 screen with a 24 point menu bar and a 70 point Dock: AppKit's visible frame is
    // y 70 to 744 from the bottom, 24 to 698 from the top.
    let screen = topLeftFrame(CGRect(x: 0, y: 70, width: 1024, height: 674), primaryHeight: 768)
    expectEqual(screen, CGRect(x: 0, y: 24, width: 1024, height: 674))
    // A scroll area from y 400 to 760: its bottom 62 points are under the Dock.
    let area = CGRect(x: 100, y: 400, width: 400, height: 360)
    guard let reach = reachableView(area, screenVisible: screen) else {
        expect(false, "part of the area is in the visible frame")
        return
    }
    expectEqual(reach, CGRect(x: 100, y: 400, width: 400, height: 298))
    // A row that would come to rest just inside the area's bottom is under the Dock; the reach
    // brings it above the Dock with the margin.
    let row = CGRect(x: 110, y: 900, width: 380, height: 24)
    expect(insideView(row.offsetBy(dx: 0, dy: -intoViewDistance(frame: row, view: area).dy), area), "inside the area")
    expect(!insideView(row.offsetBy(dx: 0, dy: -intoViewDistance(frame: row, view: area).dy), reach), "but under the Dock")
    let d = intoViewDistance(frame: row, view: reach)
    expectEqual(d.dy, 924 - (698 - 8), "down until its bottom is 8 points above the Dock")
    // An area wholly under the Dock has nothing to bring an element to.
    expect(reachableView(CGRect(x: 100, y: 700, width: 400, height: 60), screenVisible: screen) == nil, "under the Dock")
}

func testSidewaysDistancesToo() {
    let cell = CGRect(x: 700, y: 300, width: 50, height: 20)
    expectEqual(intoViewDistance(frame: cell, view: view).dx, 750 - (500 - 8))
}

func testWheelStepsAreShortWhereTheDistanceIsAndNeverJumpAView() {
    expectEqual(wheelStep(distance: 30, viewLength: 300), 30)
    expectEqual(wheelStep(distance: 5000, viewLength: 300), 240, "most of a view")
    expectEqual(wheelStep(distance: -5000, viewLength: 300), -240)
    expectEqual(wheelStep(distance: 5000, viewLength: 20), 40, "a small view still moves")
    expectEqual(wheelStep(distance: 0.3, viewLength: 300), 1, "at least a pixel")
    expectEqual(wheelStep(distance: 0, viewLength: 300), 0)
}

func testCGEventsWheelIsTheOtherWayRound() {
    expectEqual(wheelValue(240), -240, "down in Greenroom is a negative wheel")
    expectEqual(wheelValue(-120), 120)
    expectEqual(wheelValue(.infinity), 0)
}

func testARelativeScrollIsSplitIntoSteps() {
    expectEqual(relativeSteps(total: 100, viewLength: 300), [100])
    expectEqual(relativeSteps(total: 600, viewLength: 300), [240, 240, 120])
    expectEqual(relativeSteps(total: -500, viewLength: 300), [-240, -240, -20])
    expectEqual(relativeSteps(total: 0, viewLength: 300), [])
    expectEqual(relativeSteps(total: 1_000_000, viewLength: 300).count, maxScrollSteps, "bounded")
    expectEqual(relativeSteps(total: 1_000_000, viewLength: 300).reduce(0, +), 1_000_000, "the last step carries the rest")
    expectEqual(pageDistance(pages: 2, viewLength: 300), 540, "a page keeps a line of the last in sight")
    expectEqual(pageDistance(pages: -1, viewLength: 300), -270)
    expect(edgeStep(viewLength: 300, down: true) >= 1000, "to an end, in long steps")
    expect(edgeStep(viewLength: 300, down: false) < 0)
}

func testAScrollStopsWhenThePositionStopsMovingForTwoSteps() {
    var progress = ScrollProgress()
    expect(!progress.record(ScrollMarker(x: nil, y: 0.2, origin: CGPoint(x: 0, y: -100))), "the start")
    expect(!progress.record(ScrollMarker(x: nil, y: 0.4, origin: CGPoint(x: 0, y: -300))), "moved")
    expect(!progress.record(ScrollMarker(x: nil, y: 0.4, origin: CGPoint(x: 0, y: -300.2))), "still once")
    expect(progress.record(ScrollMarker(x: nil, y: 0.4, origin: CGPoint(x: 0, y: -300))), "still twice: stop")
    var lazy = ScrollProgress()
    _ = lazy.record(ScrollMarker(x: nil, y: 1, origin: CGPoint(x: 0, y: -900)))
    _ = lazy.record(ScrollMarker(x: nil, y: 1, origin: CGPoint(x: 0, y: -1200)))
    expectEqual(lazy.stillSteps, 0, "a lazy list at its bar's end still growing is moving")
}

func testTheEndIsWhereItCanMoveNoFurther() {
    var bottom = ScrollPosition()
    bottom.y = 1
    bottom.up = true
    expect(atEnd(bottom, down: true))
    expect(!atEnd(bottom, down: false))
    expect(atEnd(nil, down: true), "a view that does not scroll is at every end")
}

func testAWheelPointMustReachTheContainerItself() {
    expectEqual(wheelHit(roles: ["AXStaticText", "AXRow", "AXTable", "AXScrollArea", "AXWindow"], containerIndex: 3), .container)
    expectEqual(wheelHit(roles: ["AXTextArea", "AXScrollArea", "AXGroup", "AXScrollArea", "AXWindow"], containerIndex: 3), .nested(1),
                "a text view's own scroll area inside the list (#190)")
    expectEqual(wheelHit(roles: ["AXValueIndicator", "AXSlider", "AXGroup", "AXScrollArea"], containerIndex: 3), .swallowed(0),
                "a slider takes the wheel")
    expectEqual(wheelHit(roles: ["AXButton", "AXWindow"], containerIndex: nil), .elsewhere, "an inspector over the list")
    expectEqual(wheelHit(roles: ["AXScrollArea", "AXWindow"], containerIndex: 0), .container, "the container's own margin")
}

func testWheelPointsTryTheThreeGridThenTheFive() {
    let points = wheelPoints(in: CGRect(x: 0, y: 0, width: 300, height: 300))
    expectEqual(points.first, CGPoint(x: 150, y: 150))
    expectEqual(points.count, 9 + 24, "the 5x5 grid less its center, which the 3x3 has")
    expectEqual(Set(points.map { "\($0.x),\($0.y)" }).count, points.count, "no point twice")
}

func testTheLastResortSetsTheBarToTheFraction() {
    expectEqual(scrollBarValue(current: 0.25, distance: 500, contentLength: 2300, viewLength: 300), 0.5)
    expectEqual(scrollBarValue(current: 0.9, distance: 5000, contentLength: 2300, viewLength: 300), 1, "clamped")
    expect(scrollBarValue(current: 0, distance: 10, contentLength: 300, viewLength: 300) == nil, "content that fits")
}
