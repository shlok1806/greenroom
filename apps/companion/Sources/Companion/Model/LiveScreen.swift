import CoreMedia
import Metal
import Observation
import QuartzCore
import Synchronization
import VideoToolbox

/// The live screen route, so `LiveScreen` is testable without a daemon.
protocol ScreenSource: Sendable {
    func liveScreen(runId: String) -> AsyncThrowingStream<ScreenMessage, Error>
}

extension DaemonClient: ScreenSource {}

/// One run's live screen (ADR 0011): keeps the stream open while started,
/// decoding into `output.layer`, and reconnects with `Backoff` when it drops.
/// Frames go straight from the network task to the renderer; only phase
/// changes touch the main actor.
@Observable
@MainActor
final class LiveScreen {
    enum Phase: Equatable {
        case connecting
        case playing
        /// Why the last connection ended; the view falls back to the recording.
        case failed(String)
    }

    private(set) var phase: Phase = .connecting
    /// In pixels, for the letterbox: from HELLO, then from each FORMAT.
    private(set) var pixelSize: CGSize?
    /// The guest's screen as a picture (root ADR 0045): HELLO's points and pixels.
    private(set) var shape: ScreenShape?

    let runId: String
    @ObservationIgnored let output = VideoOutput()
    @ObservationIgnored private let source: any ScreenSource
    /// Told each shape the stream states, so the stage sizes the run's pictures by it.
    @ObservationIgnored private let onShape: (@MainActor (ScreenShape) -> Void)?
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var backoff = Backoff()

    init(runId: String, source: any ScreenSource, onShape: (@MainActor (ScreenShape) -> Void)? = nil) {
        self.runId = runId
        self.source = source
        self.onShape = onShape
    }

    var isRunning: Bool { task != nil }

    func start() {
        guard task == nil else { return }
        task = Task { [weak self] in await self?.run() }
    }

    func stop() {
        task?.cancel()
        task = nil
        output.reset(removingImage: true)
        phase = .connecting
    }

    private func run() async {
        while !Task.isCancelled {
            output.reset(removingImage: false)
            let reason: String
            do {
                try await Self.play(source.liveScreen(runId: runId), into: output) { [weak self] event in
                    self?.handle(event)
                }
                reason = "The stream ended."
            } catch {
                if Task.isCancelled || RunStore.isCancellation(error) { return }
                reason = ControlPilot.reason(error)
            }
            if Task.isCancelled { return }
            phase = .failed(reason)
            try? await Task.sleep(for: .seconds(backoff.next()))
        }
    }

    private enum Event {
        case opened(ScreenHello)
        case size(CGSize)
        case playing
    }

    /// Runs in the connection's task, so a report that was on its way when
    /// `stop()` ran finds the task cancelled and changes nothing.
    private func handle(_ event: Event) {
        guard !Task.isCancelled else { return }
        switch event {
        case .opened(let hello):
            backoff.reset()
            pixelSize = CGSize(width: hello.pixels.width, height: hello.pixels.height)
            report(ScreenShape(hello))
        case .size(let size):
            pixelSize = size
            if var known = shape, known.pixels != size {
                known.pixels = size
                report(known)
            }
        case .playing:
            phase = .playing
        }
    }

    private func report(_ new: ScreenShape) {
        guard !new.isEmpty, new != shape else { return }
        shape = new
        onShape?(new)
    }

    /// One connection, until it ends or the decoder gives up. A decoder that
    /// failed needs a keyframe, and a new connection is what asks the daemon for one.
    private nonisolated static func play(
        _ messages: AsyncThrowingStream<ScreenMessage, Error>,
        into output: VideoOutput,
        report: @escaping @MainActor @Sendable (Event) -> Void
    ) async throws {
        var format: CMVideoFormatDescription?
        var awaitingKeyframe = true
        var playing = false
        for try await message in messages {
            switch message {
            case .hello(let hello):
                await report(.opened(hello))
            case .format(let config):
                let described = try config.formatDescription()
                if let format, CMFormatDescriptionEqual(format, otherFormatDescription: described) { continue }
                if format != nil { output.reset(removingImage: false) }
                format = described
                awaitingKeyframe = true
                let size = CMVideoFormatDescriptionGetPresentationDimensions(
                    described, usePixelAspectRatio: true, useCleanAperture: true
                )
                await report(.size(size))
            case .video(let sample):
                guard let format, !(awaitingKeyframe && !sample.keyframe) else { continue }
                awaitingKeyframe = false
                guard output.enqueue(try sample.sampleBuffer(format: format)) else {
                    throw LiveScreenError.decoderFailed
                }
                if !playing {
                    playing = true
                    await report(.playing)
                }
            }
        }
    }
}

enum LiveScreenError: Error, LocalizedError {
    case decoderFailed

    var errorDescription: String? { "The picture could not be decoded." }
}

/// The live picture's decoder and layer (root ADR 0045). VideoToolbox decodes each frame to a
/// BGRA buffer, and `ScreenRenderer` draws the newest one into `layer` at the layer's drawable
/// size: copied, pixel-repeated or Lanczos, never stretched by the compositor. Decoding and
/// drawing run on `queue`, in order, off the main thread; the host view only places the layer
/// and says what drawable size it needs (`place(drawableSize:)`).
final class VideoOutput: @unchecked Sendable {
    let layer: CAMetalLayer
    private let renderer: ScreenRenderer?
    private let queue = DispatchQueue(label: "greenroom.live-screen", qos: .userInteractive)

    /// Each reset starts an epoch; work queued before it is dropped, and a
    /// decoder failure counts only in the epoch it happened in.
    private struct State {
        var epoch = 0
        var failed = false
    }
    private let state = Mutex(State())

    /// The caller builds each sample for this call alone and never touches it again.
    private struct Handoff: @unchecked Sendable {
        let sample: CMSampleBuffer
    }

    // Only touched on `queue`.
    private var session: VTDecompressionSession?
    private var latest: CVPixelBuffer?
    private var colorSpace: CGColorSpace?
    private var drawableSize = CGSize.zero

    @MainActor
    init(renderer: ScreenRenderer? = ScreenRenderer.shared) {
        self.renderer = renderer
        layer = CAMetalLayer()
        layer.device = renderer?.device
        layer.pixelFormat = .bgra8Unorm
        // The pixel-repeat and Lanczos passes write the drawable from a shader.
        layer.framebufferOnly = false
        layer.isOpaque = false
        layer.allowsNextDrawableTimeout = true
        layer.maximumDrawableCount = 3
        // A drawable of the layer's own size maps 1:1; for the moment a resize waits for its
        // redraw, nothing is smoothed either.
        layer.magnificationFilter = .nearest
        layer.minificationFilter = .nearest
        layer.contentsGravity = .resize
    }

    /// False once the decoder has failed since the last reset. Nothing but a
    /// keyframe will restart it.
    func enqueue(_ sample: CMSampleBuffer) -> Bool {
        guard let epoch = state.withLock({ $0.failed ? nil : $0.epoch }) else { return false }
        let handoff = Handoff(sample: sample)
        queue.async { [self] in
            guard state.withLock({ $0.epoch == epoch }) else { return }
            switch decode(handoff.sample) {
            case .frame(let buffer):
                latest = buffer
                draw()
            case .none:
                break
            case .failed:
                state.withLock { if $0.epoch == epoch { $0.failed = true } }
            }
        }
        return true
    }

    /// The drawable size the layer's place on screen needs, in display pixels. The newest
    /// frame is drawn again at it, so a resize never waits for the next frame of a still screen.
    func place(drawableSize size: CGSize) {
        queue.async { [self] in
            guard size != drawableSize else { return }
            drawableSize = size
            draw()
        }
    }

    /// The picture on the layer now, for the glyph moments (ADR 0006): read on `queue`,
    /// in order with the samples, so a request made before `reset(removingImage:)` sees the
    /// frame that reset removes. Converted there too; nothing touches the main actor.
    /// `nil` while nothing has been decoded.
    func still() async -> CGImage? {
        await withCheckedContinuation { (continuation: CheckedContinuation<CGImage?, Never>) in
            queue.async { [self] in
                guard let latest else {
                    continuation.resume(returning: nil)
                    return
                }
                var image: CGImage?
                VTCreateCGImageFromCVPixelBuffer(latest, options: nil, imageOut: &image)
                continuation.resume(returning: image)
            }
        }
    }

    /// The newest decoded frame's size in pixels, read in order with the samples.
    func decodedSize() async -> CGSize? {
        await withCheckedContinuation { (continuation: CheckedContinuation<CGSize?, Never>) in
            queue.async { [self] in
                continuation.resume(returning: latest.map {
                    CGSize(width: CVPixelBufferGetWidth($0), height: CVPixelBufferGetHeight($0))
                })
            }
        }
    }

    /// Before a new connection or format: the next sample must be a keyframe.
    func reset(removingImage: Bool) {
        state.withLock {
            $0.epoch += 1
            $0.failed = false
        }
        guard removingImage else { return }
        queue.async { [self] in
            latest = nil
            // A stopped screen holds no decoder; the next start makes one on its keyframe.
            invalidateSession()
            clear()
        }
    }

    // VideoToolbox asks for an explicit invalidate before the last release; nothing else holds
    // `self` here, so touching `session` off `queue` is safe.
    deinit { invalidateSession() }

    private func invalidateSession() {
        guard let session else { return }
        VTDecompressionSessionInvalidate(session)
        self.session = nil
    }

    private enum Decoded {
        case frame(CVPixelBuffer)
        case none // dropped by the decoder, which is not a failure
        case failed
    }

    /// Collects the synchronous decode's output from VideoToolbox's callback.
    private final class Box: @unchecked Sendable {
        var image: CVImageBuffer?
        var status: OSStatus = noErr
    }

    private func decode(_ sample: CMSampleBuffer) -> Decoded {
        guard let format = sample.formatDescription else { return .failed }
        if let session, !VTDecompressionSessionCanAcceptFormatDescription(session, formatDescription: format) {
            invalidateSession()
        }
        if session == nil { session = makeSession(format) }
        guard let session else { return .failed }
        let box = Box()
        let status = VTDecompressionSessionDecodeFrame(session, sampleBuffer: sample, flags: [], infoFlagsOut: nil) {
            status, _, image, _, _ in
            box.status = status
            box.image = image
        }
        VTDecompressionSessionWaitForAsynchronousFrames(session)
        guard status == noErr, box.status == noErr else {
            // A session that failed (the GPU went away, a bad frame) is made again on the
            // next keyframe, which the reconnect asks for.
            invalidateSession()
            return .failed
        }
        return box.image.map(Decoded.frame) ?? .none
    }

    private func makeSession(_ format: CMVideoFormatDescription) -> VTDecompressionSession? {
        let attributes: [CFString: Any] = [
            kCVPixelBufferPixelFormatTypeKey: kCVPixelFormatType_32BGRA,
            kCVPixelBufferMetalCompatibilityKey: true,
            kCVPixelBufferIOSurfacePropertiesKey: [CFString: Any]() as CFDictionary,
        ]
        var made: VTDecompressionSession?
        let status = VTDecompressionSessionCreate(
            allocator: nil, formatDescription: format, decoderSpecification: nil,
            imageBufferAttributes: attributes as CFDictionary, outputCallback: nil, decompressionSessionOut: &made
        )
        guard status == noErr, let made else { return nil }
        VTSessionSetProperty(made, key: kVTDecompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        return made
    }

    /// Draws the newest frame at the drawable size, in the frame's own colour space.
    private func draw() {
        guard let renderer, let latest, drawableSize.width >= 1, drawableSize.height >= 1,
              let frame = renderer.texture(latest) else { return }
        let space = CVImageBufferCreateColorSpaceFromAttachments(
            CVBufferCopyAttachments(latest, .shouldPropagate) ?? [:] as CFDictionary
        )?.takeRetainedValue() ?? CGColorSpace(name: CGColorSpace.sRGB)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        if layer.drawableSize != drawableSize { layer.drawableSize = drawableSize }
        if space != colorSpace {
            colorSpace = space
            layer.colorspace = space
        }
        CATransaction.commit()
        guard let drawable = layer.nextDrawable(), let buffer = renderer.commandBuffer() else { return }
        renderer.encode(frame.texture, into: drawable.texture, on: buffer)
        frame.hold(buffer)
        buffer.present(drawable)
        buffer.commit()
    }

    /// Presents an empty drawable: the picture is gone.
    private func clear() {
        guard let renderer, drawableSize.width >= 1, drawableSize.height >= 1 else { return }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        if layer.drawableSize != drawableSize { layer.drawableSize = drawableSize }
        CATransaction.commit()
        guard let drawable = layer.nextDrawable(), let buffer = renderer.commandBuffer() else { return }
        let pass = MTLRenderPassDescriptor()
        pass.colorAttachments[0].texture = drawable.texture
        pass.colorAttachments[0].loadAction = .clear
        pass.colorAttachments[0].clearColor = MTLClearColor(red: 0, green: 0, blue: 0, alpha: 0)
        pass.colorAttachments[0].storeAction = .store
        buffer.makeRenderCommandEncoder(descriptor: pass)?.endEncoding()
        buffer.present(drawable)
        buffer.commit()
    }
}
