import CoreGraphics
import CoreVideo
import Metal
import MetalPerformanceShaders

/// How a decoded frame becomes a drawable's pixels (root ADR 0045): copied at its own size,
/// each pixel repeated at a whole multiple of it, and Lanczos otherwise. Never a bilinear
/// stretch, which is what blurred small text.
enum ScreenFilter: Equatable, Sendable {
    case copy
    case repeatPixels(Int)
    case lanczos

    static func of(source: (width: Int, height: Int), drawable: (width: Int, height: Int)) -> ScreenFilter {
        if source == drawable { return .copy }
        if source.width > 0, source.height > 0,
           drawable.width % source.width == 0, drawable.height % source.height == 0,
           drawable.width / source.width == drawable.height / source.height {
            return .repeatPixels(drawable.width / source.width)
        }
        return .lanczos
    }
}

/// Draws frames with Metal: a BGRA pixel buffer onto a texture of any size, by `ScreenFilter`.
/// One per process; Metal objects are thread-safe, and each call gets its own command buffer.
final class ScreenRenderer: @unchecked Sendable {
    let device: MTLDevice
    private let queue: MTLCommandQueue
    private let cache: CVMetalTextureCache
    private let repeatPixels: MTLComputePipelineState
    private let lanczos: MPSImageLanczosScale

    /// `nil` on a Mac without Metal, where nothing can show the live screen.
    static let shared = ScreenRenderer()

    private static let source = """
    #include <metal_stdlib>
    using namespace metal;
    kernel void repeatPixels(texture2d<half, access::read> src [[texture(0)]],
                             texture2d<half, access::write> dst [[texture(1)]],
                             constant uint &factor [[buffer(0)]],
                             uint2 at [[thread_position_in_grid]]) {
        if (at.x >= dst.get_width() || at.y >= dst.get_height()) return;
        dst.write(src.read(at / factor), at);
    }
    """

    init?(device: MTLDevice? = MTLCreateSystemDefaultDevice()) {
        guard let device, let queue = device.makeCommandQueue() else { return nil }
        var cache: CVMetalTextureCache?
        guard CVMetalTextureCacheCreate(nil, nil, device, nil, &cache) == kCVReturnSuccess, let cache,
              let library = try? device.makeLibrary(source: Self.source, options: nil),
              let function = library.makeFunction(name: "repeatPixels"),
              let pipeline = try? device.makeComputePipelineState(function: function) else { return nil }
        self.device = device
        self.queue = queue
        self.cache = cache
        repeatPixels = pipeline
        lanczos = MPSImageLanczosScale(device: device)
    }

    /// A texture over a pixel buffer's own memory, no copy, valid while `owner` lives: keep it
    /// until the GPU is done (`hold(_:)`).
    /// Immutable, and only released by the completion handler.
    struct FrameTexture: @unchecked Sendable {
        let texture: MTLTexture
        let owner: CVMetalTexture

        /// Keeps the texture alive until `buffer` completes.
        func hold(_ buffer: MTLCommandBuffer) {
            buffer.addCompletedHandler { [self] _ in withExtendedLifetime(self) {} }
        }
    }

    /// The buffer must be BGRA and Metal compatible, as `VideoOutput` decodes them.
    func texture(_ buffer: CVPixelBuffer) -> FrameTexture? {
        var made: CVMetalTexture?
        let status = CVMetalTextureCacheCreateTextureFromImage(
            nil, cache, buffer, nil, .bgra8Unorm,
            CVPixelBufferGetWidth(buffer), CVPixelBufferGetHeight(buffer), 0, &made
        )
        guard status == kCVReturnSuccess, let made, let texture = CVMetalTextureGetTexture(made) else { return nil }
        return FrameTexture(texture: texture, owner: made)
    }

    func commandBuffer() -> MTLCommandBuffer? { queue.makeCommandBuffer() }

    /// Encodes `source` drawn over all of `target`. The target needs `.shaderWrite` unless the
    /// sizes match. Returns the filter used.
    @discardableResult
    func encode(_ source: MTLTexture, into target: MTLTexture, on buffer: MTLCommandBuffer) -> ScreenFilter {
        let filter = ScreenFilter.of(source: (source.width, source.height), drawable: (target.width, target.height))
        switch filter {
        case .copy:
            guard let blit = buffer.makeBlitCommandEncoder() else { break }
            blit.copy(from: source, to: target)
            blit.endEncoding()
        case .repeatPixels(let factor):
            guard let compute = buffer.makeComputeCommandEncoder() else { break }
            compute.setComputePipelineState(repeatPixels)
            compute.setTexture(source, index: 0)
            compute.setTexture(target, index: 1)
            var factor = UInt32(factor)
            compute.setBytes(&factor, length: MemoryLayout<UInt32>.size, index: 0)
            let width = repeatPixels.threadExecutionWidth
            let group = MTLSize(width: width, height: max(1, repeatPixels.maxTotalThreadsPerThreadgroup / width), depth: 1)
            compute.dispatchThreads(MTLSize(width: target.width, height: target.height, depth: 1), threadsPerThreadgroup: group)
            compute.endEncoding()
        case .lanczos:
            lanczos.encode(commandBuffer: buffer, sourceTexture: source, destinationTexture: target)
        }
        return filter
    }
}
