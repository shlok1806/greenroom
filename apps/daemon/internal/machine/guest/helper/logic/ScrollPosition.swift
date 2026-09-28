// Where a scroll area is (daemon ADR 0006's `scroll`): each axis a fraction 0 to 1, or nil when
// the axis does not scroll, and which ways it can still move. From the scroll bars' values when
// the area has them, else from its content's frame against its own.

import CoreGraphics
import Foundation

struct ScrollPosition: Equatable {
    var x: Double?
    var y: Double?
    var up = false
    var down = false
    var left = false
    var right = false

    /// Whether the area scrolls at all.
    var scrolls: Bool { x != nil || y != nil }
}

/// How near an end counts as the end: half a point of content, or a thousandth of the way when
/// only a scroll bar's value is known. Scroll views rest a hair off their ends.
private let restPoints: CGFloat = 0.5
private let restFraction = 0.001

/// One axis: the fraction, and whether it can move back (up, left) and on (down, right).
/// `bar` is the scroll bar's value; `viewStart` and `viewLength` are the area's own edge and
/// length on the axis, `contentStart` and `contentLength` its content's, when that is known.
private func axis(bar: Double?, viewStart: CGFloat, viewLength: CGFloat, contentStart: CGFloat?, contentLength: CGFloat?)
    -> (at: Double, back: Bool, on: Bool)? {
    var range: CGFloat?
    if let contentStart, let contentLength, contentLength.isFinite, contentStart.isFinite {
        range = contentLength - viewLength
    }
    // Content that fits does not scroll, whatever a scroll bar left over from before says.
    if let range, range <= restPoints { return nil }
    // The scroll bar knows the whole document. The content's frame may be only the rows a lazy
    // list has made so far, so it decides only when there is no bar.
    if let bar, bar.isFinite {
        let at = min(max(bar, 0), 1)
        return (at, at > restFraction, at < 1 - restFraction)
    }
    guard let range, let contentStart else { return nil }
    let offset = min(max(viewStart - contentStart, 0), range)
    return (Double(offset / range), offset > restPoints, range - offset > restPoints)
}

/// The position of a scroll area whose own frame is `view`. `content` is the frame of what it
/// scrolls (the union of its children that are not scroll bars), nil when it has none to read.
func scrollPosition(vertical: Double?, horizontal: Double?, view: CGRect, content: CGRect?) -> ScrollPosition {
    let content = content.flatMap(finiteRect)
    var position = ScrollPosition()
    if let y = axis(bar: vertical, viewStart: view.minY, viewLength: view.height,
                    contentStart: content?.minY, contentLength: content?.height) {
        position.y = y.at
        position.up = y.back
        position.down = y.on
    }
    if let x = axis(bar: horizontal, viewStart: view.minX, viewLength: view.width,
                    contentStart: content?.minX, contentLength: content?.width) {
        position.x = x.at
        position.left = x.back
        position.right = x.on
    }
    return position
}

/// The frame of a scroll area's content: the union of its children's frames.
func contentFrame(of children: [CGRect]) -> CGRect? {
    let frames = children.compactMap(finiteRect).filter { $0.width > 0 && $0.height > 0 }
    guard var all = frames.first else { return nil }
    for frame in frames.dropFirst() { all = all.union(frame) }
    return all
}
