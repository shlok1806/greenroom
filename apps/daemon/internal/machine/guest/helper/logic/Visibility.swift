// What of an element shows (daemon ADR 0006, "Ops and results"): its visible rect, which way it
// lies when a scroll area hides it, what cut it, and whether a hit test at its center found it.
// Pure geometry over frames the guest read (Walker.swift); all rects are guest points, top-left
// origin.

import CoreGraphics
import Foundation

/// Less than a point of an element is nothing a person can see or press.
let leastVisible: CGFloat = 1

/// Whether a rect has enough area to show.
func rectShows(_ rect: CGRect) -> Bool {
    !rect.isNull && !rect.isInfinite && rect.width >= leastVisible && rect.height >= leastVisible
}

/// A frame as AX reported it, or nil when it is not a rect of finite numbers.
func finiteRect(_ rect: CGRect) -> CGRect? {
    let values = [rect.origin.x, rect.origin.y, rect.width, rect.height]
    return values.allSatisfy(\.isFinite) ? rect.standardized : nil
}

/// A scroll area above an element: its ref and its own frame, which is the part of its content
/// that shows.
struct ScrollClip: Equatable {
    var ref: String
    var rect: CGRect
}

/// Everything that can cut an element's frame.
struct ClipContext: Equatable {
    var screen: CGRect
    /// The element's window, nil for a window itself and for an element that has none.
    var window: CGRect?
    /// The scroll areas above the element, outermost first.
    var scrollers: [ScrollClip] = []

    func inside(_ scroller: ScrollClip) -> ClipContext {
        var next = self
        next.scrollers.append(scroller)
        return next
    }
}

/// Roles that are a surface of their own: what is inside one is cut by it and not by the window
/// it hangs from, since a sheet, a drawer or a popover may reach past that window's edge.
let surfaceRoles: Set<String> = ["AXWindow", "AXSheet", "AXDrawer", "AXPopover"]

/// The context an element's own frame is cut by: only the screen for a surface, else what it
/// is inside.
func clipAround(role: String, in clip: ClipContext) -> ClipContext {
    surfaceRoles.contains(role) ? ClipContext(screen: clip.screen) : clip
}

/// The context of an element's children, given the element: a surface starts over with itself
/// as the window, a scroll area adds itself, anything else changes nothing. `ref` is asked for
/// only when the element is a scroll area with a frame.
func clipBelow(_ clip: ClipContext, role: String, frame: CGRect?, ref: () -> String) -> ClipContext {
    guard let frame = frame.flatMap(finiteRect), rectShows(frame) else { return clip }
    if surfaceRoles.contains(role) {
        return ClipContext(screen: clip.screen, window: frame)
    }
    if role == "AXScrollArea" {
        return clip.inside(ScrollClip(ref: ref(), rect: frame))
    }
    return clip
}

struct Visibility: Equatable {
    /// False for an element a snapshot leaves out: one with no area, or outside its window and
    /// in no scroll area (hidden, collapsed, or laid out where nobody sees it).
    var listed = true
    /// The part of the frame that shows; nil when nothing does.
    var vis: CGRect?
    /// `above`, `below`, `left` or `right` of the scroll area that hides it.
    var offscreen: String?
    /// The scroll area the element is in: the nearest one, or the one that hides it when it is
    /// offscreen (the nearest whose view it is out of, so scrolling that one is what shows it).
    var scroller: String?
    /// What cut the frame: a scroll area's ref, `window` or `screen`.
    var clipped: String?
}

/// Which way `frame` lies from `view`, for a frame that does not show in it. Up and down win
/// over left and right: lists scroll vertically far more often, and a row that is out both ways
/// is reached by the vertical scroll first.
func offscreenDirection(of frame: CGRect, from view: CGRect) -> String {
    if frame.midY < view.minY { return "above" }
    if frame.midY > view.maxY { return "below" }
    if frame.midX < view.minX { return "left" }
    if frame.midX > view.maxX { return "right" }
    // The centre is inside but under a point of it shows: a sliver at an edge.
    let up = frame.minY - view.minY, down = view.maxY - frame.maxY
    let left = frame.minX - view.minX, right = view.maxX - frame.maxX
    let least = min(up, down, left, right)
    if least == up { return "above" }
    if least == down { return "below" }
    return least == left ? "left" : "right"
}

func visibility(of frame: CGRect?, in clip: ClipContext) -> Visibility {
    guard let raw = frame, let frame = finiteRect(raw), rectShows(frame) else { return Visibility(listed: false) }
    let nearest = clip.scrollers.last?.ref

    // Out of a scroll area's view: the innermost one that hides it is the one to scroll.
    for scroller in clip.scrollers.reversed() where !rectShows(frame.intersection(scroller.rect)) {
        return Visibility(offscreen: offscreenDirection(of: frame, from: scroller.rect), scroller: scroller.ref)
    }

    var vis = frame
    var clipped: String?
    func narrow(by rect: CGRect, named name: String) -> Bool {
        let rest = vis.intersection(rect)
        if !rectShows(rest) {
            clipped = clipped ?? name
            return false
        }
        if clipped == nil, rest != vis { clipped = name }
        vis = rest
        return true
    }
    for scroller in clip.scrollers.reversed() where !narrow(by: scroller.rect, named: scroller.ref) {
        // In each scroll area's view but in none of their common part.
        return Visibility(scroller: nearest, clipped: clipped)
    }
    if let window = clip.window, !narrow(by: window, named: "window") {
        // Outside its window: hidden, unless a scroll area holds it (its content may be laid
        // out past the window's edge, and scrolling can bring it in).
        return clip.scrollers.isEmpty ? Visibility(listed: false) : Visibility(scroller: nearest, clipped: clipped)
    }
    if !narrow(by: clip.screen, named: "screen") {
        return Visibility(scroller: nearest, clipped: clipped)
    }
    return Visibility(vis: vis, scroller: nearest, clipped: clipped)
}

// MARK: - Covered

/// What a hit test at an element's point found, relative to the element.
enum HitRelation: Equatable {
    /// The element itself.
    case same
    /// Something inside the element: a press there is a press on the element.
    case descendant
    /// Something the element is inside: the element takes no events of its own (a label in a
    /// button), which covers nothing.
    case ancestor
    /// Anything else: it covers the element at that point.
    case other
}

/// Compares two parent chains, each the element first and then its parent, its parent's
/// parent, and so on. Chains may stop short of the root (they are bounded); a relation they
/// cannot show is `other`.
func hitRelation<Handle: Hashable>(target: [Handle], hit: [Handle]) -> HitRelation {
    guard let element = target.first, let found = hit.first else { return .other }
    if element == found { return .same }
    if hit.dropFirst().contains(element) { return .descendant }
    if target.dropFirst().contains(found) { return .ancestor }
    return .other
}

/// The element a coverer is named by: the nearest of the hit and its ancestors that the
/// snapshot lists, so the name is one the reader can see in the tree, else the hit itself.
func covererIndex<Handle: Hashable>(hit: [Handle], isListed: (Handle) -> Bool) -> Int? {
    if hit.isEmpty { return nil }
    return hit.firstIndex(where: isListed) ?? 0
}

/// Where a coverer sits relative to what it covers.
func coverWhere(sameProcess: Bool, sameWindow: Bool) -> String {
    if !sameProcess { return "other" }
    return sameWindow ? "window" : "app"
}

// MARK: - Toolbar overflow (daemon ADR 0006, M15)

/// Whether a toolbar item sits behind the overflow chevron: its toolbar has an overflow button
/// and nothing of the item shows inside the toolbar.
func inOverflow(item: CGRect?, toolbar: CGRect?, hasOverflowButton: Bool) -> Bool {
    guard hasOverflowButton, let toolbar = toolbar.flatMap(finiteRect) else { return false }
    guard let item = item.flatMap(finiteRect) else { return true }
    return !rectShows(item.intersection(toolbar))
}
