import AppKit
import SwiftUI
import XCTest

@testable import Companion

/// Hosts a redesign view (companion ADR 0019) in a real `NSWindow` far off every display,
/// never key, so tests can lay it out, scroll it, read its accessibility and render it.
@MainActor
final class ParkedHost {
    let window: NSWindow

    init(_ view: some View, size: CGSize, dark: Bool = false) {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let window = ParkedWindow(contentRect: CGRect(origin: ParkedWindow.origin, size: size),
                                  styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
                                  backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentViewController = NSHostingController(rootView: view.ignoresSafeArea())
        window.setContentSize(size)
        window.setFrameOrigin(ParkedWindow.origin)
        window.orderFrontRegardless()
        self.window = window
    }

    func close() {
        window.orderOut(nil)
        window.close()
    }

    /// Lets SwiftUI lay out and draw.
    func settle(_ seconds: Double = 0.4) async {
        window.contentView?.layoutSubtreeIfNeeded()
        try? await Task.sleep(for: .seconds(seconds))
        window.contentView?.layoutSubtreeIfNeeded()
        window.contentView?.displayIfNeeded()
    }

    /// Every scroll view in the window, outermost first.
    var scrollViews: [NSScrollView] {
        var out: [NSScrollView] = []
        func walk(_ view: NSView) {
            if let scroll = view as? NSScrollView { out.append(scroll) }
            view.subviews.forEach(walk)
        }
        if let content = window.contentView { walk(content) }
        return out
    }

    /// The window's content drawn at 2x.
    func image() -> CGImage? {
        guard let content = window.contentView else { return nil }
        let bounds = content.bounds
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(bounds.width * 2), pixelsHigh: Int(bounds.height * 2),
                                         bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                         colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return nil }
        rep.size = bounds.size
        (window.appearance ?? NSAppearance.currentDrawing()).performAsCurrentDrawingAppearance {
            content.cacheDisplay(in: bounds, to: rep)
        }
        return rep.cgImage
    }
}

private final class ParkedWindow: NSWindow {
    static let origin = CGPoint(x: -40_000, y: -40_000)
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}
