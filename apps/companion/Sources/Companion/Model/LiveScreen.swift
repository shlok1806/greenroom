import AVFoundation
import Observation
import Synchronization

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

    let runId: String
    @ObservationIgnored let output = VideoOutput()
    @ObservationIgnored private let source: any ScreenSource
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var backoff = Backoff()

    init(runId: String, source: any ScreenSource) {
        self.runId = runId
        self.source = source
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
        case .size(let size):
            pixelSize = size
        case .playing:
            phase = .playing
        }
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

/// The display layer and its renderer. The renderer is not thread-safe, so
/// every call to it runs on `queue`, in order, off the main thread.
final class VideoOutput: @unchecked Sendable {
    let layer: AVSampleBufferDisplayLayer
    private let renderer: AVSampleBufferVideoRenderer
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

    @MainActor
    init() {
        layer = AVSampleBufferDisplayLayer()
        layer.videoGravity = .resizeAspect
        renderer = layer.sampleBufferRenderer
    }

    /// False once the decoder has failed since the last reset. It is flushed
    /// by then, and nothing but a keyframe will restart it.
    func enqueue(_ sample: CMSampleBuffer) -> Bool {
        guard let epoch = state.withLock({ $0.failed ? nil : $0.epoch }) else { return false }
        let handoff = Handoff(sample: sample)
        queue.async { [self] in
            let sample = handoff.sample
            guard state.withLock({ $0.epoch == epoch }) else { return }
            if renderer.status == .failed || renderer.requiresFlushToResumeDecoding {
                renderer.flush()
                state.withLock { if $0.epoch == epoch { $0.failed = true } }
                return
            }
            renderer.enqueue(sample)
        }
        return true
    }

    /// Before a new connection or format: the next sample must be a keyframe.
    func reset(removingImage: Bool) {
        state.withLock {
            $0.epoch += 1
            $0.failed = false
        }
        queue.async { [self] in
            renderer.flush(removingDisplayedImage: removingImage, completionHandler: nil)
        }
    }
}
