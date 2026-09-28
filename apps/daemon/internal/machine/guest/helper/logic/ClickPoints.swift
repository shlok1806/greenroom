// Where to click an element whose center something else covers (daemon ADR 0009): points of its
// frame, nearest the center first. Pure geometry; all rects are guest points, top-left origin.

import CoreGraphics
import Foundation

/// A grid of `steps` by `steps` points inside `frame`, inset by `inset` points (so a point is
/// never on the element's edge, where a hit test may find its neighbour), ordered by distance
/// from the frame's center, the center itself first. An empty or degenerate frame gives its
/// center only.
func clickPoints(in frame: CGRect, steps: Int = 5, inset: CGFloat = 2) -> [CGPoint] {
    let center = CGPoint(x: frame.midX, y: frame.midY)
    let inner = frame.insetBy(dx: min(inset, frame.width / 4), dy: min(inset, frame.height / 4))
    guard steps > 1, !inner.isNull, inner.width > 0, inner.height > 0 else { return [center] }
    var points: [CGPoint] = [center]
    for row in 0..<steps {
        for column in 0..<steps {
            let point = CGPoint(
                x: inner.minX + inner.width * CGFloat(column) / CGFloat(steps - 1),
                y: inner.minY + inner.height * CGFloat(row) / CGFloat(steps - 1)
            )
            if point != center { points.append(point) }
        }
    }
    let distance = { (p: CGPoint) in (p.x - center.x) * (p.x - center.x) + (p.y - center.y) * (p.y - center.y) }
    // Stable, so equally near points keep reading order (top row first, left first).
    return points.enumerated().sorted { a, b in
        let (da, db) = (distance(a.element), distance(b.element))
        return da != db ? da < db : a.offset < b.offset
    }.map(\.element)
}

/// The first of `points` that `owns` says belongs to the target, or nil when none does.
func firstOwned(_ points: [CGPoint], owns: (CGPoint) -> Bool) -> CGPoint? {
    points.first(where: owns)
}
