import CoreVideo
import Metal
import XCTest

@testable import Companion

/// The live picture is drawn pixel-exact (root ADR 0045): placed at a size that needs no
/// fractional upscale, and drawn by copy, whole-pixel repeat or Lanczos, never a stretch.
final class PictureQualityTests: XCTestCase {
    private let oneX = ScreenShape(pixels: CGSize(width: 1024, height: 768), points: CGSize(width: 1024, height: 768))
    private let twoX = ScreenShape(pixels: CGSize(width: 2048, height: 1536), points: CGSize(width: 1024, height: 768))
    private let big = CGSize(width: 3000, height: 2250)

    // MARK: - Placement

    func testWithRoomTheGuestShowsAtItsSharpestSize() {
        // A 1x guest on a 1x display: one pixel each, never stretched to fill a big stage.
        XCTAssertEqual(PicturePlacement.size(oneX, in: big, scale: 1), CGSize(width: 1024, height: 768))
        // On a Retina display it is the guest's natural size, each pixel doubled.
        XCTAssertEqual(PicturePlacement.size(oneX, in: big, scale: 2), CGSize(width: 1024, height: 768))
        // A 2x guest on a Retina display: its natural size, one pixel each.
        XCTAssertEqual(PicturePlacement.size(twoX, in: big, scale: 2), CGSize(width: 1024, height: 768))
        // A 2x guest on a 1x display may grow to one picture pixel per display pixel.
        XCTAssertEqual(PicturePlacement.size(twoX, in: big, scale: 1), CGSize(width: 2048, height: 1536))
    }

    func testInLessRoomItShrinksToFitOnWholeDisplayPixels() {
        let room = CGSize(width: 490.4, height: 367.8)
        for (shape, scale) in [(oneX, 1.0), (oneX, 2.0), (twoX, 1.0), (twoX, 2.0)] {
            let size = PicturePlacement.size(shape, in: room, scale: scale)
            XCTAssertLessThanOrEqual(size.width, room.width)
            XCTAssertLessThanOrEqual(size.height, room.height)
            XCTAssertEqual((size.width * scale).rounded(), size.width * scale)
            XCTAssertEqual((size.height * scale).rounded(), size.height * scale)
            XCTAssertEqual(size.width / size.height, 4.0 / 3, accuracy: 0.01)
            XCTAssertGreaterThan(size.width, room.width - 2, "it fills the room it has")
        }
    }

    func testARoomBetweenOneToOneAndNaturalFillsIt() {
        // A 1x guest on a Retina display in a 790 point stage: smaller than its natural
        // 1024 points, larger than 512; it fills the stage, scaled with Lanczos.
        let size = PicturePlacement.size(oneX, in: CGSize(width: 790, height: 592.5), scale: 2)
        XCTAssertEqual(size.width, 790)
    }

    func testAWholeMultipleIsTold() {
        let pixels = CGSize(width: 1024, height: 768)
        XCTAssertTrue(PicturePlacement.isWholeMultiple(pixels, drawn: CGSize(width: 1024, height: 768), scale: 1))
        XCTAssertTrue(PicturePlacement.isWholeMultiple(pixels, drawn: CGSize(width: 1024, height: 768), scale: 2))
        XCTAssertFalse(PicturePlacement.isWholeMultiple(pixels, drawn: CGSize(width: 900, height: 675), scale: 1))
        XCTAssertFalse(PicturePlacement.isWholeMultiple(pixels, drawn: CGSize(width: 512, height: 384), scale: 1))
    }

    /// Clicks are fractions of the drawn picture (ADR 0009). With the picture view at its
    /// placed size, a point over a guest pixel maps to that pixel's fraction at every scale.
    func testAClickOnThePlacedPictureNamesTheGuestPixelUnderIt() throws {
        let rooms = [big, CGSize(width: 700, height: 525), CGSize(width: 333, height: 250)]
        for shape in [oneX, twoX] {
            for scale in [1.0, 2.0] {
                for room in rooms {
                    let placed = PicturePlacement.size(shape, in: room, scale: scale)
                    // The picture fills its placed view, to within a display pixel.
                    let drawn = ScreenGeometry.fitted(image: shape.pixels, in: placed)
                    XCTAssertLessThan(placed.width - drawn.width, 1 / scale + 1e-9)
                    XCTAssertLessThan(placed.height - drawn.height, 1 / scale + 1e-9)
                    // The centre of guest pixel (320, 576) of 1024x768 (scaled for 2x).
                    let pixel = CGPoint(x: 320 * shape.pixels.width / 1024 + 0.5, y: 576 * shape.pixels.height / 768 + 0.5)
                    let point = CGPoint(x: drawn.minX + pixel.x / shape.pixels.width * drawn.width,
                                        y: drawn.minY + pixel.y / shape.pixels.height * drawn.height)
                    let fraction = try XCTUnwrap(ScreenGeometry.fraction(at: point, image: shape.pixels, view: placed))
                    XCTAssertEqual(Int(fraction.x * shape.pixels.width), Int(pixel.x), "\(shape) at \(scale)x in \(room)")
                    XCTAssertEqual(Int(fraction.y * shape.pixels.height), Int(pixel.y), "\(shape) at \(scale)x in \(room)")
                }
            }
        }
    }

    /// A run's live screen says its shape; a recorded frame stands in until it does, and never
    /// replaces it after.
    @MainActor
    func testTheLiveScreensShapeWinsOverARecordedFrames() {
        let store = RunStore()
        let frame = ScreenShape(picture: CGSize(width: 1280, height: 960))
        store.noteScreen(frame, runId: "r", live: false)
        XCTAssertEqual(store.screenShapes["r"], frame)
        store.noteScreen(ScreenShape(picture: CGSize(width: 800, height: 600)), runId: "r", live: false)
        XCTAssertEqual(store.screenShapes["r"], frame, "the first frame's shape stands")
        store.noteScreen(twoX, runId: "r", live: true)
        XCTAssertEqual(store.screenShapes["r"], twoX)
        store.noteScreen(frame, runId: "r", live: false)
        XCTAssertEqual(store.screenShapes["r"], twoX, "a frame never replaces the live screen's word")
        store.noteScreen(oneX, runId: "r", live: true)
        XCTAssertEqual(store.screenShapes["r"], oneX, "a new live screen does")
    }

    // MARK: - Filters

    func testTheFilterFollowsTheSizes() {
        XCTAssertEqual(ScreenFilter.of(source: (1024, 768), drawable: (1024, 768)), .copy)
        XCTAssertEqual(ScreenFilter.of(source: (1024, 768), drawable: (2048, 1536)), .repeatPixels(2))
        XCTAssertEqual(ScreenFilter.of(source: (1024, 768), drawable: (1580, 1185)), .lanczos)
        XCTAssertEqual(ScreenFilter.of(source: (2048, 1536), drawable: (1024, 768)), .lanczos)
        XCTAssertEqual(ScreenFilter.of(source: (1024, 768), drawable: (2048, 1535)), .lanczos)
    }

    func testCopyAndRepeatReproduceEveryPixelExactly() throws {
        let renderer = try XCTUnwrap(ScreenRenderer.shared, "no Metal device")
        let source = try checkerboard(width: 37, height: 23)
        let frame = try XCTUnwrap(renderer.texture(source))

        let same = try draw(renderer, frame, width: 37, height: 23)
        XCTAssertEqual(same.filter, .copy)
        for y in 0..<23 { for x in 0..<37 { XCTAssertEqual(same.pixel(x, y), expected(x, y)) } }

        let doubled = try draw(renderer, frame, width: 74, height: 46)
        XCTAssertEqual(doubled.filter, .repeatPixels(2))
        for y in 0..<46 { for x in 0..<74 { XCTAssertEqual(doubled.pixel(x, y), expected(x / 2, y / 2)) } }
    }

    func testOtherSizesAreScaledWithLanczos() throws {
        let renderer = try XCTUnwrap(ScreenRenderer.shared, "no Metal device")
        let frame = try XCTUnwrap(renderer.texture(try checkerboard(width: 64, height: 48)))
        let down = try draw(renderer, frame, width: 40, height: 30)
        XCTAssertEqual(down.filter, .lanczos)
        // A 1 pixel checkerboard averages to grey, never to aliased black and white bands.
        let middle = down.pixel(20, 15)
        XCTAssertGreaterThan(middle, 60)
        XCTAssertLessThan(middle, 200)
    }

    // MARK: - Helpers

    private func expected(_ x: Int, _ y: Int) -> UInt8 { (x + y) % 2 == 0 ? 0 : 255 }

    private func checkerboard(width: Int, height: Int) throws -> CVPixelBuffer {
        var made: CVPixelBuffer?
        let attributes: [CFString: Any] = [
            kCVPixelBufferMetalCompatibilityKey: true,
            kCVPixelBufferIOSurfacePropertiesKey: [CFString: Any]() as CFDictionary,
        ]
        CVPixelBufferCreate(nil, width, height, kCVPixelFormatType_32BGRA, attributes as CFDictionary, &made)
        let buffer = try XCTUnwrap(made)
        CVPixelBufferLockBaseAddress(buffer, [])
        let base = CVPixelBufferGetBaseAddress(buffer)!.assumingMemoryBound(to: UInt8.self)
        let row = CVPixelBufferGetBytesPerRow(buffer)
        for y in 0..<height {
            for x in 0..<width {
                let v = expected(x, y)
                let p = base + y * row + x * 4
                p[0] = v; p[1] = v; p[2] = v; p[3] = 255
            }
        }
        CVPixelBufferUnlockBaseAddress(buffer, [])
        return buffer
    }

    private struct Drawn {
        var filter: ScreenFilter
        var bytes: [UInt8]
        var width: Int

        /// The blue channel, as grey.
        func pixel(_ x: Int, _ y: Int) -> UInt8 { bytes[(y * width + x) * 4] }
    }

    private func draw(_ renderer: ScreenRenderer, _ frame: ScreenRenderer.FrameTexture, width: Int, height: Int) throws -> Drawn {
        let descriptor = MTLTextureDescriptor.texture2DDescriptor(pixelFormat: .bgra8Unorm, width: width, height: height, mipmapped: false)
        descriptor.usage = [.shaderRead, .shaderWrite, .renderTarget]
        descriptor.storageMode = .shared
        let target = try XCTUnwrap(renderer.device.makeTexture(descriptor: descriptor))
        let buffer = try XCTUnwrap(renderer.commandBuffer())
        let filter = renderer.encode(frame.texture, into: target, on: buffer)
        frame.hold(buffer)
        buffer.commit()
        buffer.waitUntilCompleted()
        XCTAssertNil(buffer.error)
        var bytes = [UInt8](repeating: 0, count: width * height * 4)
        target.getBytes(&bytes, bytesPerRow: width * 4, from: MTLRegionMake2D(0, 0, width, height), mipmapLevel: 0)
        return Drawn(filter: filter, bytes: bytes, width: width)
    }
}
