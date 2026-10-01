import CoreGraphics

/// The guest's screen as a picture: how many pixels it has and how many points of the guest's
/// desktop they show (root ADR 0045). A 1x guest's picture has as many pixels as points; a
/// Retina guest's twice as many each way.
struct ScreenShape: Equatable, Sendable {
    var pixels: CGSize
    var points: CGSize

    init(pixels: CGSize, points: CGSize) {
        self.pixels = pixels
        self.points = points
    }

    /// The live screen's own words (HELLO).
    init(_ hello: ScreenHello) {
        pixels = CGSize(width: hello.pixels.width, height: hello.pixels.height)
        points = CGSize(width: hello.screen.width, height: hello.screen.height)
    }

    /// A recorded picture, whose point size nothing states: one pixel a point, which is what a
    /// 1x guest's frames are.
    init(picture pixels: CGSize) {
        self.pixels = pixels
        points = pixels
    }

    var isEmpty: Bool { pixels.width <= 0 || pixels.height <= 0 || points.width <= 0 || points.height <= 0 }
}

/// How large the guest's screen is drawn (root ADR 0045). Never resampled up by a fraction:
/// its largest size is one picture pixel per display pixel, or the guest's natural size (one
/// guest point per host point) when that is larger, which is then a whole multiple. Smaller
/// rooms get it smaller, scaled to fit. Sizes are whole display pixels.
enum PicturePlacement {
    /// The largest size, in points of a display with `scale` pixels a point.
    static func largest(_ shape: ScreenShape, scale: CGFloat) -> CGSize {
        let scale = max(scale, 1)
        let exact = CGSize(width: shape.pixels.width / scale, height: shape.pixels.height / scale)
        return shape.points.width > exact.width ? shape.points : exact
    }

    /// The size the picture is drawn at in `room`: its largest size, or the largest size of its
    /// shape that fits, rounded down to whole display pixels.
    static func size(_ shape: ScreenShape, in room: CGSize, scale: CGFloat) -> CGSize {
        guard !shape.isEmpty, room.width > 0, room.height > 0 else { return .zero }
        let scale = max(scale, 1)
        let top = largest(shape, scale: scale)
        if top.width <= room.width, top.height <= room.height { return top }
        let aspect = shape.pixels.width / shape.pixels.height
        var width = (min(room.width, room.height * aspect) * scale).rounded(.down)
        var height = (width / aspect).rounded()
        if height > room.height * scale {
            height = (room.height * scale).rounded(.down)
            width = (height * aspect).rounded()
        }
        return CGSize(width: width / scale, height: height / scale)
    }

    /// Whether a picture of `pixels` drawn at `drawn` points on a display of `scale` lands on
    /// whole display pixels per picture pixel (1:1 or doubled), where any filter only blurs.
    static func isWholeMultiple(_ pixels: CGSize, drawn: CGSize, scale: CGFloat) -> Bool {
        guard pixels.width > 0, pixels.height > 0 else { return false }
        let device = CGSize(width: (drawn.width * scale).rounded(), height: (drawn.height * scale).rounded())
        let k = device.width / pixels.width
        return k >= 1 && k == k.rounded() && device.height == pixels.height * k
    }
}
