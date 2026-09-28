// The arithmetic of the `capture` op (daemon ADR 0006): a region in points to the captured
// image's pixels, and the size an image is scaled down to.

import CoreGraphics
import Foundation

/// A rect argument, `[x, y, w, h]` in points, or nil when it is not four finite numbers with a
/// positive width and height.
func rectArgument(_ values: [Double]) -> CGRect? {
    guard values.count == 4, values.allSatisfy(\.isFinite), values[2] > 0, values[3] > 0 else { return nil }
    return CGRect(x: values[0], y: values[1], width: values[2], height: values[3])
}

/// The pixels of an image `width` by `height` that show `points`, at `scale` pixels a point.
/// Rounded outward, so a region never loses its edge to rounding, and clipped to the image; nil
/// when nothing of it is on the image.
func pixelRect(_ points: CGRect, scale: CGFloat, width: Int, height: Int) -> CGRect? {
    guard scale > 0, width > 0, height > 0 else { return nil }
    let minX = (points.minX * scale).rounded(.down)
    let minY = (points.minY * scale).rounded(.down)
    let maxX = (points.maxX * scale).rounded(.up)
    let maxY = (points.maxY * scale).rounded(.up)
    let raw = CGRect(x: minX, y: minY, width: maxX - minX, height: maxY - minY)
    let clipped = raw.intersection(CGRect(x: 0, y: 0, width: width, height: height))
    if clipped.isNull || clipped.width < 1 || clipped.height < 1 { return nil }
    return clipped
}

/// The size an image `width` by `height` is sent at: unchanged when `maxWidth` is nil or not
/// smaller, else `maxWidth` wide with the height in proportion (never below one pixel).
func fittedSize(width: Int, height: Int, maxWidth: Int?) -> (width: Int, height: Int) {
    guard let maxWidth, maxWidth > 0, maxWidth < width, width > 0 else { return (width, height) }
    let h = (Double(height) * Double(maxWidth) / Double(width)).rounded()
    return (maxWidth, max(1, Int(h)))
}
