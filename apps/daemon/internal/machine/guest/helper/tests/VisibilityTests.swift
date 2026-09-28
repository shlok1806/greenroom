import CoreGraphics
import Foundation

// A 1024x768 screen, a window at (100, 100) 600x500, a conversation scroll area e20 in it at
// (120, 160) 400x300, and a code block scroll area e30 inside that at (140, 200) 300x120.
private let screen = CGRect(x: 0, y: 0, width: 1024, height: 768)
private let window = CGRect(x: 100, y: 100, width: 600, height: 500)
private let conversation = ScrollClip(ref: "e20", rect: CGRect(x: 120, y: 160, width: 400, height: 300))
private let code = ScrollClip(ref: "e30", rect: CGRect(x: 140, y: 200, width: 300, height: 120))

private let inWindow = ClipContext(screen: screen, window: window)
private let inConversation = inWindow.inside(conversation)
private let inCode = inConversation.inside(code)

func testAFrameInViewIsWhollyVisible() {
    let frame = CGRect(x: 200, y: 200, width: 80, height: 24)
    expectEqual(visibility(of: frame, in: inWindow), Visibility(vis: frame))
    expectEqual(visibility(of: frame, in: inConversation), Visibility(vis: frame, scroller: "e20"))
}

func testAFrameIsClippedThroughNestedScrollAreas() {
    // A line of code wider than its block: the block cuts it on the right.
    let line = CGRect(x: 150, y: 210, width: 500, height: 20)
    expectEqual(visibility(of: line, in: inCode),
                Visibility(vis: CGRect(x: 150, y: 210, width: 290, height: 20), scroller: "e30", clipped: "e30"))

    // The block itself, scrolled so the conversation cuts its top: what is inside it is cut by
    // the conversation, not by the block.
    let high = ScrollClip(ref: "e30", rect: CGRect(x: 140, y: 100, width: 300, height: 120))
    let top = CGRect(x: 150, y: 150, width: 100, height: 20)
    expectEqual(visibility(of: top, in: inConversation.inside(high)),
                Visibility(vis: CGRect(x: 150, y: 160, width: 100, height: 10), scroller: "e30", clipped: "e20"))

    // Cut by both: the innermost names it, and the visible rect is what both leave.
    let both = CGRect(x: 400, y: 150, width: 100, height: 30)
    expectEqual(visibility(of: both, in: inConversation.inside(high)),
                Visibility(vis: CGRect(x: 400, y: 160, width: 40, height: 20), scroller: "e30", clipped: "e30"))
}

func testAnElementOutOfItsScrollAreaIsOffscreenAndSaysWhichWay() {
    let ways: [(CGRect, String)] = [
        (CGRect(x: 130, y: 60, width: 100, height: 40), "above"),
        (CGRect(x: 130, y: 900, width: 100, height: 40), "below"),
        (CGRect(x: 10, y: 200, width: 100, height: 40), "left"),
        (CGRect(x: 600, y: 200, width: 100, height: 40), "right"),
        // Out both ways: the vertical scroll is named.
        (CGRect(x: 600, y: 900, width: 100, height: 40), "below"),
        // Touching the edge shows nothing of it.
        (CGRect(x: 130, y: 460, width: 100, height: 40), "below"),
        // Half a point of it shows: that is nothing either.
        (CGRect(x: 130, y: 120.5, width: 100, height: 40), "above"),
    ]
    for (frame, way) in ways {
        expectEqual(visibility(of: frame, in: inConversation), Visibility(offscreen: way, scroller: "e20"), "\(frame)")
    }
}

func testOffscreenNamesTheScrollAreaThatHidesIt() {
    // In the code block's view, but the block is scrolled out of the conversation's.
    let low = ScrollClip(ref: "e30", rect: CGRect(x: 140, y: 600, width: 300, height: 120))
    let line = CGRect(x: 150, y: 610, width: 100, height: 20)
    expectEqual(visibility(of: line, in: inConversation.inside(low)), Visibility(offscreen: "below", scroller: "e20"))

    // Out of the code block's own view: the block is the one to scroll.
    let far = CGRect(x: 150, y: 400, width: 100, height: 20)
    expectEqual(visibility(of: far, in: inCode), Visibility(offscreen: "below", scroller: "e30"))
}

func testAnElementOutsideItsWindowIsHiddenUnlessAScrollAreaHoldsIt() {
    let outside = CGRect(x: 800, y: 200, width: 80, height: 24)
    expectEqual(visibility(of: outside, in: inWindow), Visibility(listed: false))

    // A scroll area laid out past the window's edge, and an element in the part past it.
    let wide = ScrollClip(ref: "e40", rect: CGRect(x: 500, y: 200, width: 400, height: 100))
    expectEqual(visibility(of: outside, in: inWindow.inside(wide)), Visibility(scroller: "e40", clipped: "window"))
}

func testAnElementWithNoAreaIsNotListed() {
    expectEqual(visibility(of: nil, in: inWindow), Visibility(listed: false))
    expectEqual(visibility(of: CGRect(x: 200, y: 200, width: 0, height: 24), in: inWindow), Visibility(listed: false))
    expectEqual(visibility(of: CGRect(x: 200, y: 200, width: 80, height: 0.5), in: inWindow), Visibility(listed: false))
    expectEqual(visibility(of: CGRect(x: CGFloat.nan, y: 200, width: 80, height: 24), in: inWindow), Visibility(listed: false))
    expectEqual(visibility(of: CGRect(x: 200, y: 200, width: CGFloat.infinity, height: 24), in: inWindow), Visibility(listed: false))
}

func testClippedNamesWhatCutTheFrame() {
    let overWindowEdge = CGRect(x: 650, y: 200, width: 100, height: 24)
    expectEqual(visibility(of: overWindowEdge, in: inWindow),
                Visibility(vis: CGRect(x: 650, y: 200, width: 50, height: 24), clipped: "window"))

    // A window half off the screen: the screen cuts what the window does not.
    let low = ClipContext(screen: screen, window: CGRect(x: 100, y: 500, width: 600, height: 500))
    let atTheEdge = CGRect(x: 200, y: 750, width: 100, height: 40)
    expectEqual(visibility(of: atTheEdge, in: low),
                Visibility(vis: CGRect(x: 200, y: 750, width: 100, height: 18), clipped: "screen"))
    // In the window, all of it below the screen: it exists, and nothing of it shows.
    let below = CGRect(x: 200, y: 800, width: 100, height: 40)
    expectEqual(visibility(of: below, in: low), Visibility(clipped: "screen"))

    // A window itself has no window to be cut by, only the screen.
    expectEqual(visibility(of: low.window, in: ClipContext(screen: screen)),
                Visibility(vis: CGRect(x: 100, y: 500, width: 600, height: 268), clipped: "screen"))
}

func testAHitOnTheElementItsInsideOrItsOutsideCoversNothing() {
    // Chains are the element first, then its parents: a label in a button in a group in a window.
    let label = ["label", "button", "group", "window"]
    let button = ["button", "group", "window"]
    let list = ["list", "window"]
    expectEqual(hitRelation(target: button, hit: button), .same)
    expectEqual(hitRelation(target: button, hit: label), .descendant, "the label inside the button")
    expectEqual(hitRelation(target: label, hit: button), .ancestor, "the button the label is in")
    expectEqual(hitRelation(target: button, hit: list), .other, "a list over the button")
    expectEqual(hitRelation(target: button, hit: ["alert", "other window"]), .other, "another window")
    // A sibling is not a relative, though they share every ancestor.
    expectEqual(hitRelation(target: button, hit: ["overlay", "group", "window"]), .other)
    expectEqual(hitRelation(target: button, hit: []), .other, "nothing was hit")
    // A chain that stops short of the target cannot show that the hit is inside it.
    expectEqual(hitRelation(target: button, hit: ["deep", "deeper"]), .other)
}

func testACovererIsNamedByItsNearestListedAncestor() {
    let listed: Set<String> = ["list", "window"]
    // The hit was a cell of the list, which the snapshot does not list: the list names it.
    expectEqual(covererIndex(hit: ["cell", "row", "list", "window"], isListed: listed.contains), 2)
    expectEqual(covererIndex(hit: ["list", "window"], isListed: listed.contains), 0)
    // Nothing of another app's alert is in this snapshot: the hit itself is named.
    expectEqual(covererIndex(hit: ["button", "alert"], isListed: listed.contains), 0)
    expect(covererIndex(hit: [String](), isListed: listed.contains) == nil, "nothing was hit")

    expectEqual(coverWhere(sameProcess: true, sameWindow: true), "window")
    expectEqual(coverWhere(sameProcess: true, sameWindow: false), "app")
    expectEqual(coverWhere(sameProcess: false, sameWindow: false), "other")
}

func testAToolbarItemBehindTheChevronIsInOverflow() {
    let toolbar = CGRect(x: 100, y: 100, width: 400, height: 40)
    let shown = CGRect(x: 120, y: 105, width: 30, height: 30)
    let pushedOut = CGRect(x: 520, y: 105, width: 30, height: 30)
    expectEqual(inOverflow(item: shown, toolbar: toolbar, hasOverflowButton: true), false)
    expectEqual(inOverflow(item: pushedOut, toolbar: toolbar, hasOverflowButton: true), true)
    expectEqual(inOverflow(item: nil, toolbar: toolbar, hasOverflowButton: true), true, "an item with no frame")
    expectEqual(inOverflow(item: CGRect(x: 0, y: 0, width: 0, height: 0), toolbar: toolbar, hasOverflowButton: true), true)
    // Without the chevron an item out of the toolbar is only hidden.
    expectEqual(inOverflow(item: pushedOut, toolbar: toolbar, hasOverflowButton: false), false)
    expectEqual(inOverflow(item: pushedOut, toolbar: nil, hasOverflowButton: true), false)
}

func testASheetIsASurfaceOfItsOwn() {
    // A sheet wider than the window it hangs from: neither it nor what is in it is cut by the
    // window.
    let sheet = CGRect(x: 50, y: 130, width: 700, height: 300)
    expectEqual(visibility(of: sheet, in: clipAround(role: "AXSheet", in: inConversation)), Visibility(vis: sheet))
    let below = clipBelow(inConversation, role: "AXSheet", frame: sheet, ref: { "unused" })
    expectEqual(below, ClipContext(screen: screen, window: sheet))
    let button = CGRect(x: 60, y: 380, width: 80, height: 24)
    expectEqual(visibility(of: button, in: below), Visibility(vis: button), "left of the window, inside the sheet")

    // A scroll area adds itself, under the ref it was given; a group changes nothing.
    expectEqual(clipBelow(inWindow, role: "AXScrollArea", frame: conversation.rect, ref: { "e20" }), inConversation)
    expectEqual(clipBelow(inWindow, role: "AXGroup", frame: conversation.rect, ref: { "unused" }), inWindow)
    // A scroll area with no frame hides nothing that can be measured.
    expectEqual(clipBelow(inWindow, role: "AXScrollArea", frame: nil, ref: { "unused" }), inWindow)
    expectEqual(clipAround(role: "AXButton", in: inConversation), inConversation)
}
