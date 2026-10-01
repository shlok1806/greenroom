import QuartzCore
import SwiftUI

/// Shows a `LiveScreen`'s layer. The layer is placed on the rectangle `ScreenGeometry.fitted`
/// gives, so the picture and the fractions `InputSurface` sends cannot disagree about the
/// letterbox, snapped to whole display pixels (root ADR 0045).
struct LiveScreenView: NSViewRepresentable {
    let output: VideoOutput
    /// In pixels; zero until the stream says.
    var pixelSize: CGSize

    func makeNSView(context: Context) -> LiveScreenHostView {
        LiveScreenHostView(output: output)
    }

    func updateNSView(_ view: LiveScreenHostView, context: Context) {
        view.pixelSize = pixelSize
    }
}

/// Layer-hosting: the view owns its layer tree, so AppKit leaves the display layer alone.
final class LiveScreenHostView: NSView {
    let output: VideoOutput
    var displayLayer: CAMetalLayer { output.layer }

    var pixelSize: CGSize = .zero {
        didSet { if pixelSize != oldValue { needsLayout = true } }
    }

    init(output: VideoOutput) {
        self.output = output
        super.init(frame: .zero)
        layer = CALayer()
        wantsLayer = true
        layer?.addSublayer(output.layer)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    /// Same top-left origin as `ScreenGeometry` and `InputSurfaceView`.
    override var isFlipped: Bool { true }

    /// Clicks belong to the `InputSurface` above.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        needsLayout = true
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        needsLayout = true
    }

    /// The display's pixels per point: the window's, else the main screen's.
    var backingScale: CGFloat { window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 1 }

    /// Where the picture sits: the fitted rectangle with every edge on a whole display pixel,
    /// so the drawable maps onto the display one to one.
    var pictureRect: CGRect {
        let fitted = ScreenGeometry.fitted(image: pixelSize, in: bounds.size)
        let rect = fitted == .zero ? bounds : fitted
        return window == nil ? rect : backingAlignedRect(rect, options: .alignAllEdgesNearest)
    }

    override func layout() {
        super.layout()
        let rect = pictureRect
        let scale = backingScale
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        displayLayer.contentsScale = scale
        displayLayer.frame = convertToLayer(rect)
        CATransaction.commit()
        output.place(drawableSize: CGSize(width: (rect.width * scale).rounded(), height: (rect.height * scale).rounded()))
    }
}
