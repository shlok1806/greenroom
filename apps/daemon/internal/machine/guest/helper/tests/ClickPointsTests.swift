import CoreGraphics
import Foundation

// Calculator's Equals in the demo run (daemon ADR 0009): 48x48 points centred at (258, 717),
// with the Dock's top edge at y 690 on a 1024x768 screen.
private let equals = CGRect(x: 234, y: 693, width: 48, height: 48)

func testClickPointsStartAtTheCenterAndStayInside() {
    let points = clickPoints(in: equals)
    expectEqual(points.first, CGPoint(x: 258, y: 717), "the center comes first")
    expectEqual(points.count, 25, "a 5 by 5 grid, the center once")
    let inner = equals.insetBy(dx: 2, dy: 2)
    expect(points.allSatisfy { inner.contains($0) || $0.x == inner.maxX || $0.y == inner.maxY },
           "every point is inside the inset frame: \(points)")
    let distance = { (p: CGPoint) in hypot(p.x - 258, p.y - 717) }
    expect(zip(points, points.dropFirst()).allSatisfy { distance($0) <= distance($1) + 0.001 },
           "nearer points come first")
}

func testAnElementPartlyUnderTheDockIsClickedWhereItShows() {
    // A button whose lower half is under the Dock (top at y 700): the nearest point above it.
    let button = CGRect(x: 100, y: 680, width: 40, height: 40)
    let dockTop: CGFloat = 700
    let point = firstOwned(clickPoints(in: button)) { $0.y < dockTop }
    expect(point != nil, "a visible point was found")
    if let point {
        expect(point.y < dockTop, "the point is above the Dock: \(point)")
        expectEqual(point.x, button.midX, "straight above the center")
    }
}

func testAnElementWhollyCoveredHasNoPoint() {
    expectEqual(firstOwned(clickPoints(in: equals)) { $0.y < 690 }, nil)
}

func testADegenerateFrameGivesItsCenter() {
    expectEqual(clickPoints(in: CGRect(x: 10, y: 10, width: 0, height: 0)), [CGPoint(x: 10, y: 10)])
}
