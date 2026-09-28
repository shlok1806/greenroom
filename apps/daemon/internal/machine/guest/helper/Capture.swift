// The `capture` op (daemon ADR 0006): the screen, or a region of it, through ScreenCaptureKit
// in process, encoded with ImageIO. No `screencapture` process: that is an exec per picture,
// which the agent exists to remove (daemon ADR 0005).

import CoreGraphics
import Foundation
import ImageIO
import ScreenCaptureKit

private struct CaptureArgs: Decodable {
    var format: String?
    var quality: Double?
    var maxWidth: Int?
    var rect: [Double]?
    /// An element to crop to: its visible rect grown by `margin` points (24 unless given),
    /// clipped to the screen.
    var ref: String?
    var margin: Double?
}

/// The margin around an element a capture by ref keeps unless the request says.
private let defaultCaptureMargin = 24.0

/// How an image is encoded and sized; the op's arguments after validation.
struct CaptureFormat {
    var jpeg = false
    var quality = 0.8
    var maxWidth: Int?
}

func registerCaptureOp() {
    register("capture", .capture) { call in
        let args = try call.args(CaptureArgs.self)
        var format = CaptureFormat()
        switch args.format?.lowercased() ?? "png" {
        case "png": format.jpeg = false
        case "jpeg", "jpg": format.jpeg = true
        default: throw AgentFailure("bad_request", "capture: format must be png or jpeg")
        }
        if let quality = args.quality {
            guard quality.isFinite, (0...1).contains(quality) else {
                throw AgentFailure("bad_request", "capture: quality must be between 0.0 and 1.0")
            }
            format.quality = quality
        }
        if let maxWidth = args.maxWidth {
            guard maxWidth > 0 else { throw AgentFailure("bad_request", "capture: maxWidth must be a positive number of pixels") }
            format.maxWidth = maxWidth
        }
        var region: CGRect?
        if let ref = meaningful(args.ref) {
            guard args.rect == nil else {
                throw AgentFailure("bad_request", "capture: pass rect or ref, not both")
            }
            let margin = args.margin ?? defaultCaptureMargin
            guard margin.isFinite, margin >= 0 else {
                throw AgentFailure("bad_request", "capture: margin must be zero or more points")
            }
            region = try captureRegion(of: ref, margin: CGFloat(margin), call: call)
        } else if let rect = args.rect {
            guard let r = rectArgument(rect) else {
                throw AgentFailure("bad_request", "capture: rect must be [x, y, w, h] in points with a positive width and height")
            }
            region = r
        }
        return try captureScreen(call, region: region, format: format)
    }
}

/// The region a capture by ref crops to: what shows of the element, grown by `margin` and clipped
/// to the screen. The ref is resolved on its app's read queue, in turn with that app's other
/// reads, while the capture itself stays on the capture queue.
private func captureRegion(of ref: String, margin: CGFloat, call: Call) throws -> CGRect {
    guard refNumber(ref) != nil else {
        throw AgentFailure("bad_request", "capture: \(ref) is not a ref; refs look like e17 and come from machine_snapshot or machine_find")
    }
    let key = refPid(ref, reader: call.reader).map { "pid:\($0)" } ?? "app:"
    let limit = min(call.deadline - .milliseconds(500), .now() + .seconds(5))
    let found = try onReadQueue(key, until: limit) {
        try busy("ax") { try look(ref: ref, reader: call.reader, hitTest: false, until: limit) }
    }
    guard let vis = found.shown.vis else {
        let reason = visibilityReason(offscreen: found.shown.offscreen)
        throw AgentFailure("refused", "\(targetLabel(found)) shows nothing on screen, so there is nothing to capture", detail: [
            "reason": reason, "target": found.node,
        ])
    }
    let region = vis.insetBy(dx: -margin, dy: -margin).intersection(bounds)
    guard rectShows(region) else {
        throw AgentFailure("refused", "\(targetLabel(found)) is not on the screen", detail: ["reason": "hidden", "target": found.node])
    }
    return region
}

/// Captures the main display, crops it to `region` (points; nil is the whole screen), scales
/// it down to `format.maxWidth` and attaches it to the call as its BLOB. The result says what
/// was sent: its pixel size, the pixels a point in it, and the region in points.
func captureScreen(_ call: Call, region: CGRect?, format: CaptureFormat) throws -> [String: Any] {
    try busy("capture") {
        let full = try screenshots.image(within: min(call.remaining, 15))
        try call.check()
        // Pixels a point, measured from the image rather than assumed from the display mode.
        let measured = CGFloat(full.width) / bounds.width
        var image = full
        var points = CGRect(x: 0, y: 0, width: bounds.width, height: bounds.height)
        if let region {
            guard let pixels = pixelRect(region, scale: measured, width: full.width, height: full.height),
                  let cropped = full.cropping(to: pixels)
            else {
                throw AgentFailure("bad_request", "capture: rect \(describe(region)) is not on the \(Int(bounds.width))x\(Int(bounds.height)) screen")
            }
            image = cropped
            points = CGRect(x: pixels.minX / measured, y: pixels.minY / measured,
                            width: pixels.width / measured, height: pixels.height / measured)
        }
        let size = fittedSize(width: image.width, height: image.height, maxWidth: format.maxWidth)
        if size.width != image.width {
            image = try scaled(image, width: size.width, height: size.height)
        }
        let data = try encode(image, format)
        call.blob(data, mime: format.jpeg ? "image/jpeg" : "image/png")
        return [
            "width": image.width,
            "height": image.height,
            "scale": Double(image.width) / Double(points.width),
            "rect": [points.minX, points.minY, points.width, points.height].map { Double($0) },
        ]
    }
}

private func describe(_ r: CGRect) -> String {
    "[\(r.minX), \(r.minY), \(r.width), \(r.height)]"
}

/// The display to capture, looked up once and again after a failure (a display that went away
/// with a resolution change fails the next capture, which then looks it up afresh).
private final class Screenshots {
    private var cached: SCDisplay?

    /// The main display at full resolution, without the pointer (as `screencapture -x` took it).
    func image(within seconds: TimeInterval) throws -> CGImage {
        let deadline = Date().addingTimeInterval(max(seconds, 0.5))
        do {
            return try capture(try shareableDisplay(until: deadline), until: deadline)
        } catch {
            cached = nil
            let firstError = error
            guard Date() < deadline else { throw firstError }
            return try capture(try shareableDisplay(until: deadline), until: deadline)
        }
    }

    private func shareableDisplay(until deadline: Date) throws -> SCDisplay {
        if let cached { return cached }
        let found = try wait(until: deadline, what: "listing the displays") { (done: @escaping (Result<SCDisplay, Error>) -> Void) in
            SCShareableContent.getExcludingDesktopWindows(false, onScreenWindowsOnly: true) { content, error in
                if let screen = content?.displays.first(where: { $0.displayID == display }) ?? content?.displays.first {
                    done(.success(screen))
                } else {
                    done(.failure(AgentFailure("capture_failed", "no display to capture: \(error?.localizedDescription ?? "none listed")")))
                }
            }
        }
        cached = found
        return found
    }

    private func capture(_ screen: SCDisplay, until deadline: Date) throws -> CGImage {
        let config = SCStreamConfiguration()
        var width = screen.width
        var height = screen.height
        if let mode = CGDisplayCopyDisplayMode(screen.displayID) {
            width = mode.pixelWidth
            height = mode.pixelHeight
        }
        config.width = width
        config.height = height
        config.showsCursor = false
        config.captureResolution = .best
        let filter = SCContentFilter(display: screen, excludingWindows: [])
        return try wait(until: deadline, what: "capturing the screen") { (done: @escaping (Result<CGImage, Error>) -> Void) in
            SCScreenshotManager.captureImage(contentFilter: filter, configuration: config) { image, error in
                if let image {
                    done(.success(image))
                } else {
                    done(.failure(AgentFailure("capture_failed", "the screen capture failed: \(error?.localizedDescription ?? "no image")")))
                }
            }
        }
    }
}

private let screenshots = Screenshots()

/// Runs a completion-handler call and waits for it until `deadline`. A handler that answers
/// later answers into nothing.
private func wait<T>(until deadline: Date, what: String, _ start: (@escaping (Result<T, Error>) -> Void) -> Void) throws -> T {
    let box = CompletionBox<T>()
    let lock = NSLock()
    let done = DispatchSemaphore(value: 0)
    start { result in
        lock.lock()
        if box.result == nil { box.result = result }
        lock.unlock()
        done.signal()
    }
    let left = max(0, deadline.timeIntervalSinceNow)
    if done.wait(timeout: .now() + left) == .timedOut {
        throw AgentFailure("capture_failed", "ScreenCaptureKit did not answer within \(Int(left.rounded())) s while \(what); the screen may be wedged")
    }
    lock.lock()
    defer { lock.unlock() }
    guard let result = box.result else { throw AgentFailure("capture_failed", "ScreenCaptureKit gave no answer while \(what)") }
    switch result {
    case let .success(value): return value
    case let .failure(error as AgentFailure): throw error
    case let .failure(error): throw AgentFailure("capture_failed", "\(what): \(error.localizedDescription)")
    }
}

/// Where a completion handler leaves its answer for `wait`.
private final class CompletionBox<T> {
    var result: Result<T, Error>?
}

private func scaled(_ image: CGImage, width: Int, height: Int) throws -> CGImage {
    let space = image.colorSpace ?? CGColorSpace(name: CGColorSpace.sRGB) ?? CGColorSpaceCreateDeviceRGB()
    guard let context = CGContext(
        data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: space,
        bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue
    ) else { throw AgentFailure("capture_failed", "cannot make a \(width)x\(height) image to scale the capture into") }
    context.interpolationQuality = .high
    context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
    guard let out = context.makeImage() else { throw AgentFailure("capture_failed", "cannot scale the capture to \(width)x\(height)") }
    return out
}

private func encode(_ image: CGImage, _ format: CaptureFormat) throws -> Data {
    let data = NSMutableData()
    let type = (format.jpeg ? "public.jpeg" : "public.png") as CFString
    guard let destination = CGImageDestinationCreateWithData(data, type, 1, nil) else {
        throw AgentFailure("capture_failed", "cannot encode the capture as \(format.jpeg ? "JPEG" : "PNG")")
    }
    let properties: [CFString: Any] = format.jpeg ? [kCGImageDestinationLossyCompressionQuality: format.quality] : [:]
    CGImageDestinationAddImage(destination, image, properties as CFDictionary)
    guard CGImageDestinationFinalize(destination) else {
        throw AgentFailure("capture_failed", "cannot encode the capture as \(format.jpeg ? "JPEG" : "PNG")")
    }
    return data as Data
}
