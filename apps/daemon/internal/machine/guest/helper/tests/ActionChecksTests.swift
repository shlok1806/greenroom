import CoreGraphics
import Foundation

func testActionTimeoutsDefaultAndAreCapped() {
    expectEqual(actionTimeoutMs(nil), 5000, "none given")
    expectEqual(actionTimeoutMs(0), 5000, "zero")
    expectEqual(actionTimeoutMs(1200), 1200)
    expectEqual(actionTimeoutMs(90_000), 30000, "capped")
}

func testTheBackoffIsPlaywrights() {
    expectEqual((0..<8).map(actionBackoff(attempt:)), [0, 20, 100, 100, 500, 500, 500, 500])
    // Waits add up: 0, 20, 120, 220, 720, 1220... and a try that would start past the timeout
    // is not made.
    var elapsed = 0
    var attempt = 0
    var starts: [Int] = []
    while let wait = nextActionWait(attempt: attempt, elapsedMs: elapsed, timeoutMs: 1300) {
        elapsed += wait
        starts.append(elapsed)
        attempt += 1
    }
    expectEqual(starts, [0, 20, 120, 220, 720, 1220])
    expectEqual(nextActionWait(attempt: 0, elapsedMs: 0, timeoutMs: 0), 0, "the first try always runs")
}

func testChecksRunInOrderForEachKindOfAction() {
    expectEqual(checkOrder(pointer: true, editable: false, frontmost: true),
                [.modal, .visible, .enabled, .stable, .frontmost, .hit], "a pointer press")
    expectEqual(checkOrder(pointer: true, editable: true, frontmost: true),
                [.modal, .visible, .enabled, .editable, .stable, .frontmost, .hit], "typing into a field it presses")
    expectEqual(checkOrder(pointer: false, editable: true, frontmost: true),
                [.modal, .enabled, .editable, .frontmost], "typing into a focused field")
    expectEqual(checkOrder(pointer: false, editable: true, frontmost: false),
                [.modal, .enabled, .editable], "setValue")
    expectEqual(checkOrder(pointer: false, editable: false, frontmost: false), [.modal, .enabled], "an AX press")
}

func testEveryCheckRefusesWithItsReason() {
    let reasons = Dictionary(uniqueKeysWithValues: ActionCheck.allCases.map { ($0, $0.reason) })
    expectEqual(reasons[.modal], "modal")
    expectEqual(reasons[.visible], "hidden")
    expectEqual(reasons[.enabled], "disabled")
    expectEqual(reasons[.editable], "not_editable")
    expectEqual(reasons[.stable], "unstable")
    expectEqual(reasons[.hit], "covered")
    expectEqual(reasons[.frontmost], "not_frontmost")
    // What the scroll area around it says decides between the two reasons of `visible`.
    let list = ClipContext(screen: CGRect(x: 0, y: 0, width: 1024, height: 768), window: CGRect(x: 100, y: 100, width: 600, height: 500),
                           scrollers: [ScrollClip(ref: "e20", rect: CGRect(x: 100, y: 150, width: 600, height: 300))])
    let below = visibility(of: CGRect(x: 120, y: 900, width: 200, height: 24), in: list)
    expectEqual(visibilityReason(offscreen: below.offscreen), "offscreen", "a row below the fold")
    let minimized = Visibility()
    expectEqual(visibilityReason(offscreen: minimized.offscreen), "hidden", "a window that shows nothing")
}

func testAFrameIsStableWithinHalfAPoint() {
    let frame = CGRect(x: 10, y: 20, width: 100, height: 30)
    expect(isStable(frame, frame.offsetBy(dx: 0.4, dy: -0.3)), "jitter")
    expect(!isStable(frame, frame.offsetBy(dx: 0, dy: 12)), "a sheet sliding in")
    expect(!isStable(frame, CGRect(x: 10, y: 20, width: 100, height: 31)), "growing")
    expect(!isStable(frame, nil), "gone between the reads")
    expect(!isStable(nil, nil), "never had a frame")
}

func testPressPointsAreTheCenterThenTheGrid() {
    let points = pressPoints(in: CGRect(x: 0, y: 0, width: 60, height: 30))
    expectEqual(points.count, 9)
    expectEqual(points.first, CGPoint(x: 30, y: 15), "the center first")
    // The edge midpoints before the corners.
    expectEqual(Array(points[1...4]), [CGPoint(x: 30, y: 5), CGPoint(x: 10, y: 15), CGPoint(x: 50, y: 15), CGPoint(x: 30, y: 25)])
    expectEqual(Array(points[5...]), [CGPoint(x: 10, y: 5), CGPoint(x: 50, y: 5), CGPoint(x: 10, y: 25), CGPoint(x: 50, y: 25)])
    let rect = CGRect(x: 0, y: 0, width: 60, height: 30)
    expect(points.allSatisfy { rect.contains($0) }, "every point is inside")
}

func testASliverIsNotTriedNineTimes() {
    let points = pressPoints(in: CGRect(x: 100, y: 100, width: 1, height: 1))
    expectEqual(points.count, 1, "a one-point rect has one point")
    expect(pressPoints(in: CGRect(x: 0, y: 0, width: 0, height: 10)).isEmpty, "nothing shows")
    let thin = pressPoints(in: CGRect(x: 0, y: 0, width: 300, height: 1))
    expectEqual(thin.count, 3, "a hairline is tried along its length")
}

func testAHitReachesTheElementItsInsideAndForLabelsWhatHoldsThem() {
    expect(hitReaches(.same, role: "AXButton"), "itself")
    expect(hitReaches(.descendant, role: "AXButton"), "its label")
    expect(!hitReaches(.ancestor, role: "AXButton"), "a button's parent is not the button")
    expect(hitReaches(.ancestor, role: "AXStaticText"), "a label inside a button presses the button")
    expect(hitReaches(.ancestor, role: "AXImage"), "an icon inside a cell")
    expect(!hitReaches(.other, role: "AXButton"), "something over it")
    // A recorded case: the navlab covered button, hit at its center by the overlay's group.
    let target = ["button", "group", "window", "app"]
    let hit = ["overlay-text", "overlay", "window", "app"]
    let relation = hitRelation(target: target, hit: hit)
    expectEqual(relation, .other)
    expectEqual(covererIndex(hit: hit, isListed: { $0 == "overlay" }), 1, "named by the overlay the reader can see")
}

func testAModalBlocksWhatIsNotInsideIt() {
    let modals = ["sheet"]
    expectEqual(blockingModal(chain: ["field", "group", "window", "app"], modals: modals), "sheet", "a field of the window under the sheet")
    expect(blockingModal(chain: ["button", "sheet", "window", "app"], modals: modals) == nil, "the sheet's own button")
    expect(blockingModal(chain: ["field", "window", "app"], modals: [String]()) == nil, "no modal")
    expectEqual(blockingModal(chain: ["item", "menu2"], modals: ["menu1", "menu2"]), nil, "inside one of two open menus")
}

func testTheCheckLogChargesWaitsToTheCheckThatHeldUp() {
    var log = CheckLog()
    log.mark(.attached)
    log.mark(.modal)
    log.mark(.visible)
    log.mark(.enabled)
    log.charge(.enabled, ms: 120)
    log.charge(.enabled, ms: 500)
    log.note(.enabled, ["was": "disabled"])
    log.mark(.stable)
    log.mark(.hit)
    log.note(.hit, ["of": 2])
    let wire = log.wire
    expectEqual(wire.map { $0["check"] as? String ?? "" }, ["attached", "modal", "visible", "enabled", "stable", "hit"])
    expectEqual(wire[3]["ms"] as? Int, 620, "the disabled wait")
    expectEqual(wire[0]["ms"] as? Int, 0, "attached passed at once")
    expect(wire[0]["detail"] == nil, "no detail when there is nothing to say")
    expectEqual((wire[5]["detail"] as? [String: Any])?["of"] as? Int, 2)
    expect(JSONSerialization.isValidJSONObject(["checks": wire]), "the log is JSON")
}
