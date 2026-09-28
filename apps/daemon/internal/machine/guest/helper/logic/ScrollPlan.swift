// The plan of the `scroll` op (daemon ADR 0006; docs/21 section 5.3, #190): how far the content
// must move for an element to show, the wheel steps that move it, when to stop, where a wheel
// event reaches the container and no nested scroll area, and the scroll bar value of the last
// resort. Pure: the guest posts and measures (Scroll.swift), the host tests run the arithmetic.
//
// Signs: Greenroom's `by` is positive downwards (the content moves up, as a page down does).
// CGEvent's wheel is the other way round, which `wheelValue` alone knows.

import CoreGraphics
import Foundation

/// The most wheel steps one scroll posts.
let maxScrollSteps = 60

/// How far from the container's edge an element scrolled into view comes to rest, when it fits:
/// flush against the edge, a row is easily half under a header or a fade.
let intoViewMargin: CGFloat = 8

/// The smallest wheel step, so a short distance still moves a view that rounds small deltas away.
let minWheelStep: CGFloat = 40

/// How far one axis's content must move for `start...end` to lie inside `viewStart...viewEnd`,
/// positive when it must move on (down, right). An element longer than the view is brought to
/// its start, unless it already fills the view.
private func axisDistance(start: CGFloat, end: CGFloat, viewStart: CGFloat, viewEnd: CGFloat, margin: CGFloat) -> CGFloat {
    let length = end - start
    let viewLength = viewEnd - viewStart
    let room = min(margin, max(0, (viewLength - length) / 2))
    if length > viewLength - 2 * room {
        if start <= viewStart, end >= viewEnd { return 0 }
        return start - (viewStart + room)
    }
    if start < viewStart + room { return start - (viewStart + room) }
    if end > viewEnd - room { return end - (viewEnd - room) }
    return 0
}

/// How far the content must scroll for `frame` to show inside `view`: `dy` positive scrolls down,
/// `dx` positive scrolls right.
func intoViewDistance(frame: CGRect, view: CGRect, margin: CGFloat = intoViewMargin) -> CGVector {
    CGVector(
        dx: axisDistance(start: frame.minX, end: frame.maxX, viewStart: view.minX, viewEnd: view.maxX, margin: margin),
        dy: axisDistance(start: frame.minY, end: frame.maxY, viewStart: view.minY, viewEnd: view.maxY, margin: margin))
}

/// Whether `frame` shows inside `view` as far as it can: wholly, or filling the view when it is
/// longer than the view. Half a point of slack, as scroll views rest.
func insideView(_ frame: CGRect, _ view: CGRect) -> Bool {
    let d = intoViewDistance(frame: frame, view: view.insetBy(dx: -0.5, dy: -0.5), margin: 0)
    return d.dx == 0 && d.dy == 0
}

/// The next wheel step for a distance left to scroll, in Greenroom's sign: all of it when it is
/// short, else most of a view, so an element never jumps past the view in one step.
func wheelStep(distance: CGFloat, viewLength: CGFloat) -> CGFloat {
    guard distance != 0, distance.isFinite else { return 0 }
    let cap = max(minWheelStep, viewLength * 0.8)
    let size = max(1, min(abs(distance), cap).rounded())
    return distance < 0 ? -size : size
}

/// A step as CGEvent's pixel wheel reads it: the other way round, and whole pixels.
func wheelValue(_ step: CGFloat) -> Int32 {
    guard step.isFinite else { return 0 }
    return Int32(clamping: Int((-step).rounded()))
}

/// The steps of a scroll by a fixed distance, each no longer than most of a view and at most
/// `maxScrollSteps` of them (the last carries what is left over).
func relativeSteps(total: CGFloat, viewLength: CGFloat) -> [CGFloat] {
    guard total != 0, total.isFinite else { return [] }
    var steps: [CGFloat] = []
    var left = total
    while left != 0, steps.count < maxScrollSteps {
        let step = wheelStep(distance: left, viewLength: viewLength)
        if abs(left - step) < 1 || steps.count == maxScrollSteps - 1 {
            steps.append(left.rounded())
            break
        }
        steps.append(step)
        left -= step
    }
    return steps.filter { $0 != 0 }
}

/// How far `pages` pages scroll: a page is most of the view, so a line of the last one stays in
/// sight, as page down keeps one.
func pageDistance(pages: Double, viewLength: CGFloat) -> CGFloat {
    CGFloat(pages) * max(1, viewLength * 0.9)
}

/// The step of a scroll to the top or the bottom: long, since only the end stops it (a lazy list
/// grows its content as it goes, so no one step can say where the end is).
func edgeStep(viewLength: CGFloat, down: Bool) -> CGFloat {
    let size = max(1000, viewLength * 10)
    return down ? size : -size
}

/// Where a scroll area is, as precisely as it can be read: its scroll bars' values and the origin
/// of its content. Two equal markers read a step apart mean the step moved nothing.
struct ScrollMarker: Equatable {
    var x: Double?
    var y: Double?
    var origin: CGPoint?

    static func == (a: ScrollMarker, b: ScrollMarker) -> Bool {
        func same(_ p: Double?, _ q: Double?) -> Bool {
            switch (p, q) {
            case (nil, nil): return true
            case let (p?, q?): return abs(p - q) < 0.0005
            default: return false
            }
        }
        let origins: Bool
        switch (a.origin, b.origin) {
        case (nil, nil): origins = true
        case let (p?, q?): origins = abs(p.x - q.x) < 0.5 && abs(p.y - q.y) < 0.5
        default: origins = false
        }
        return same(a.x, b.x) && same(a.y, b.y) && origins
    }
}

/// Stops a scroll whose position has not changed for two steps: it is at an end, or the view
/// does not scroll the way it is asked.
struct ScrollProgress {
    private var last: ScrollMarker?
    private(set) var stillSteps = 0

    /// Records where the area is after a step; true once it has stayed put for two steps.
    mutating func record(_ marker: ScrollMarker) -> Bool {
        if let last, last == marker {
            stillSteps += 1
        } else {
            stillSteps = 0
        }
        last = marker
        return stillSteps >= 2
    }
}

/// Whether a scroll area can move no further the way it went.
func atEnd(_ position: ScrollPosition?, down: Bool) -> Bool {
    guard let position else { return true }
    return down ? !position.down : !position.up
}

/// Roles that take a wheel event for themselves instead of scrolling what holds them.
let wheelSwallowingRoles: Set<String> = ["AXSlider", "AXIncrementor", "AXValueIndicator"]

/// What a wheel event at a point would scroll, from the hit's chain of roles (the hit first, then
/// its parents) and where the container is in that chain.
enum WheelHit: Equatable {
    /// The container itself: the point is good.
    case container
    /// A scroll area inside the container, at this index of the chain, would take the event.
    case nested(Int)
    /// A control at this index of the chain would take it.
    case swallowed(Int)
    /// The point is not over the container at all.
    case elsewhere
}

func wheelHit(roles: [String], containerIndex: Int?) -> WheelHit {
    guard let containerIndex, containerIndex < roles.count else { return .elsewhere }
    for index in 0..<containerIndex {
        if roles[index] == "AXScrollArea" { return .nested(index) }
        if wheelSwallowingRoles.contains(roles[index]) { return .swallowed(index) }
    }
    return .container
}

/// The points of a container's visible rect a wheel event is tried at: the 3x3 grid, then the
/// 5x5 one, center first.
func wheelPoints(in rect: CGRect) -> [CGPoint] {
    var out: [CGPoint] = []
    for point in grid(rect, divisions: 3) + grid(rect, divisions: 5) where !out.contains(point) {
        out.append(point)
    }
    return out
}

/// The scroll bar value, 0 to 1, that moves the content `distance` points on from `current`,
/// given the content's and the view's length on that axis. Nil when the content fits.
func scrollBarValue(current: Double, distance: CGFloat, contentLength: CGFloat, viewLength: CGFloat) -> Double? {
    let range = contentLength - viewLength
    guard range > 0.5, current.isFinite, distance.isFinite else { return nil }
    return min(max(current + Double(distance / range), 0), 1)
}
