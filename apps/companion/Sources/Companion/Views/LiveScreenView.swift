import AVFoundation
import SwiftUI

/// Shows a `LiveScreen`'s display layer. The layer is placed on exactly the
/// rectangle `ScreenGeometry.fitted` gives, so the picture and the fractions
/// `InputSurface` sends cannot disagree about the letterbox.
struct LiveScreenView: NSViewRepresentable {
    let layer: AVSampleBufferDisplayLayer
    /// In pixels; zero until the stream says.
    var pixelSize: CGSize

    func makeNSView(context: Context) -> LiveScreenHostView {
        LiveScreenHostView(displayLayer: layer)
    }

    func updateNSView(_ view: LiveScreenHostView, context: Context) {
        view.pixelSize = pixelSize
    }
}

/// Layer-hosting: the view owns its layer tree, so AppKit leaves the display layer alone.
final class LiveScreenHostView: NSView {
    let displayLayer: AVSampleBufferDisplayLayer

    var pixelSize: CGSize = .zero {
        didSet { if pixelSize != oldValue { needsLayout = true } }
    }

    init(displayLayer: AVSampleBufferDisplayLayer) {
        self.displayLayer = displayLayer
        super.init(frame: .zero)
        layer = CALayer()
        wantsLayer = true
        layer?.addSublayer(displayLayer)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    /// Same top-left origin as `ScreenGeometry` and `InputSurfaceView`.
    override var isFlipped: Bool { true }

    /// Clicks belong to the `InputSurface` above.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func layout() {
        super.layout()
        let fitted = ScreenGeometry.fitted(image: pixelSize, in: bounds.size)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        displayLayer.frame = convertToLayer(fitted == .zero ? bounds : fitted)
        CATransaction.commit()
    }
}
