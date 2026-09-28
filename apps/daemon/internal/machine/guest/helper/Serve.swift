// The live screen, --serve (ADR 0011).

import AppKit
import CoreGraphics
import CoreMedia
import Foundation
import ScreenCaptureKit
import VideoToolbox

// MARK: - Serve (ADR 0011)

/// Every message either way is [type u8][length u32 big-endian][payload].
enum Wire {
    static let hello: UInt8 = 0x01
    static let format: UInt8 = 0x02
    static let video: UInt8 = 0x03
    static let ack: UInt8 = 0x04
    static let log: UInt8 = 0x05
    static let input: UInt8 = 0x10
    static let keyframe: UInt8 = 0x11
}

/// The encoder and input threads both write stdout; one lock keeps each
/// frame whole.
let stdoutLock = NSLock()

func writeAll(_ data: Data) {
    data.withUnsafeBytes { raw in
        guard var p = raw.baseAddress else { return }
        var left = raw.count
        while left > 0 {
            let n = write(STDOUT_FILENO, p, left)
            if n < 0 {
                if errno == EINTR { continue }
                exit(0) // EPIPE: the daemon went away and nobody is left to tell.
            }
            p += n
            left -= n
        }
    }
}

func send(_ type: UInt8, _ payload: Data) {
    var header = Data([type])
    withUnsafeBytes(of: UInt32(payload.count).bigEndian) { header.append(contentsOf: $0) }
    stdoutLock.lock()
    defer { stdoutLock.unlock() }
    writeAll(header)
    writeAll(payload)
}

func send(_ type: UInt8, json object: [String: Any]) {
    send(type, (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data("{}".utf8))
}

func log(_ message: String) {
    send(Wire.log, Data(message.utf8))
}

/// Reads exactly count bytes from stdin, or nil at EOF.
func readFull(_ count: Int) -> Data? {
    var data = Data(count: count)
    var got = 0
    while got < count {
        let n = data.withUnsafeMutableBytes { read(STDIN_FILENO, $0.baseAddress! + got, count - got) }
        if n < 0 && errno == EINTR { continue }
        if n <= 0 { return nil }
        got += n
    }
    return data
}

/// H.264 from captured frames. encode and forceKeyframe run on videoQueue
/// only; output runs on VideoToolbox's thread.
final class Encoder {
    private var session: VTCompressionSession?
    private var width = 0
    private var height = 0
    private var lowLatency = true
    private var fresh = true // the next frame starts a new session and must be an IDR
    private var origin: CMTime?
    private var lastPTS = CMTime(value: -1, timescale: 1000)
    private var latest: CVPixelBuffer?

    private let lock = NSLock() // guards the two below, shared with output
    private var formatDue = true
    private var lastFormat: CMFormatDescription?

    func encode(_ buffer: CVPixelBuffer, at time: CMTime, forceKeyframe: Bool = false) {
        latest = buffer
        if session == nil || CVPixelBufferGetWidth(buffer) != width || CVPixelBufferGetHeight(buffer) != height {
            open(width: CVPixelBufferGetWidth(buffer), height: CVPixelBufferGetHeight(buffer))
        }
        guard let session else { return }

        // Timestamps must rise even when a still frame is encoded again.
        if origin == nil { origin = time }
        var pts = CMTimeSubtract(time, origin!)
        if pts <= lastPTS { pts = CMTimeAdd(lastPTS, CMTime(value: 1, timescale: 1000)) }
        lastPTS = pts

        let key = forceKeyframe || fresh
        fresh = false
        let status = VTCompressionSessionEncodeFrame(
            session, imageBuffer: buffer, presentationTimeStamp: pts, duration: .invalid,
            frameProperties: key ? [kVTEncodeFrameOptionKey_ForceKeyFrame: true] as CFDictionary : nil,
            infoFlagsOut: nil, outputHandler: output)
        if status != noErr { log("encode failed: \(status)") }
    }

    /// Answers KEYFRAME. ScreenCaptureKit sends nothing while the screen is
    /// still, so the last frame is encoded again.
    func forceKeyframe() {
        lock.lock()
        formatDue = true
        lock.unlock()
        if let latest {
            encode(latest, at: CMClockGetTime(CMClockGetHostTimeClock()), forceKeyframe: true)
        }
    }

    private func open(width: Int, height: Int) {
        if let session {
            VTCompressionSessionCompleteFrames(session, untilPresentationTimeStamp: .invalid)
            VTCompressionSessionInvalidate(session)
        }
        self.width = width
        self.height = height
        fresh = true
        lock.lock()
        formatDue = true
        lock.unlock()
        // Low-latency rate control can open and then emit nothing, so it has
        // to prove itself on a frame before it is trusted.
        session = makeSession(lowLatency: lowLatency)
        if lowLatency, let candidate = session, !produces(candidate) {
            VTCompressionSessionInvalidate(candidate)
            lowLatency = false
            session = makeSession(lowLatency: false)
        }
        log("encoder \(width)x\(height): \(session == nil ? "failed" : lowLatency ? "low-latency rate control" : "hardware, real time")")
    }

    /// Encodes one blank frame and reports whether output came back. output
    /// drops it by its negative timestamp, and the next real frame is an IDR.
    private func produces(_ session: VTCompressionSession) -> Bool {
        var blank: CVPixelBuffer?
        CVPixelBufferCreate(nil, width, height, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange,
                            [kCVPixelBufferIOSurfacePropertiesKey: [:]] as CFDictionary, &blank)
        guard let blank else { return false }
        var ok = false
        let status = VTCompressionSessionEncodeFrame(
            session, imageBuffer: blank, presentationTimeStamp: CMTime(value: -1, timescale: 1000),
            duration: .invalid, frameProperties: nil, infoFlagsOut: nil
        ) { status, _, sample in ok = status == noErr && sample?.dataBuffer != nil }
        VTCompressionSessionCompleteFrames(session, untilPresentationTimeStamp: .invalid)
        return status == noErr && ok
    }

    private func makeSession(lowLatency: Bool) -> VTCompressionSession? {
        let spec: [CFString: Any] = lowLatency
            ? [kVTVideoEncoderSpecification_EnableLowLatencyRateControl: true]
            : [kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder: true]
        var session: VTCompressionSession?
        let status = VTCompressionSessionCreate(
            allocator: nil, width: Int32(width), height: Int32(height), codecType: kCMVideoCodecType_H264,
            encoderSpecification: spec as CFDictionary, imageBufferAttributes: nil,
            compressedDataAllocator: nil, outputCallback: nil, refcon: nil, compressionSessionOut: &session)
        guard status == noErr, let session else {
            log("VTCompressionSessionCreate failed: \(status)")
            return nil
        }
        let properties: [(CFString, CFTypeRef)] = [
            (kVTCompressionPropertyKey_RealTime, kCFBooleanTrue),
            (kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse),
            (kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_High_AutoLevel),
            (kVTCompressionPropertyKey_AverageBitRate, 8_000_000 as CFNumber),
            (kVTCompressionPropertyKey_ExpectedFrameRate, 60 as CFNumber),
            (kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, 2 as CFNumber),
        ]
        for (key, value) in properties {
            let status = VTSessionSetProperty(session, key: key, value: value)
            if status != noErr { log("\(key) not set: \(status)") }
        }
        VTCompressionSessionPrepareToEncodeFrames(session)
        return session
    }

    private func output(_ status: OSStatus, _: VTEncodeInfoFlags, _ sample: CMSampleBuffer?) {
        guard status == noErr, let sample, let block = sample.dataBuffer else {
            if status != noErr { log("encoder output failed: \(status)") }
            return
        }
        let pts = sample.presentationTimeStamp
        if pts < .zero { return } // the probe frame
        let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[CFString: Any]]
        let keyframe = !(attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool ?? false)

        if keyframe, let format = sample.formatDescription {
            lock.lock()
            let due = formatDue || lastFormat.map { !CMFormatDescriptionEqual($0, otherFormatDescription: format) } ?? true
            formatDue = false
            lastFormat = format
            lock.unlock()
            if due {
                let atoms = CMFormatDescriptionGetExtension(
                    format, extensionKey: kCMFormatDescriptionExtension_SampleDescriptionExtensionAtoms) as? [String: Any]
                guard let avcC = atoms?["avcC"] as? Data else {
                    log("keyframe without avcC")
                    return
                }
                send(Wire.format, avcC)
            }
        }

        let length = CMBlockBufferGetDataLength(block)
        var payload = Data(count: 9 + length)
        payload[0] = keyframe ? 1 : 0
        let micros = UInt64(max(0, CMTimeConvertScale(pts, timescale: 1_000_000, method: .roundHalfAwayFromZero).value))
        withUnsafeBytes(of: micros.bigEndian) { payload.replaceSubrange(1..<9, with: $0) }
        payload.withUnsafeMutableBytes { raw in
            _ = CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: length, destination: raw.baseAddress! + 9)
        }
        send(Wire.video, payload)
    }
}

let videoQueue = DispatchQueue(label: "greenroom.video", qos: .userInteractive)
let encoder = Encoder()

final class Capture: NSObject, SCStreamOutput, SCStreamDelegate {
    func stream(_ stream: SCStream, didOutputSampleBuffer sample: CMSampleBuffer, of type: SCStreamOutputType) {
        // Idle frames carry no image; only complete ones are new pixels.
        guard type == .screen, sample.isValid, let buffer = sample.imageBuffer,
              let info = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let raw = info.first?[.status] as? Int, SCFrameStatus(rawValue: raw) == .complete
        else { return }
        encoder.encode(buffer, at: sample.presentationTimeStamp)
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        log("capture stopped: \(error.localizedDescription)")
        exit(1)
    }
}

let capture = Capture()
/// Held for the life of the process: a released SCStream stops capturing.
var activeStream: SCStream?

func startCapture(pixelWidth: Int, pixelHeight: Int) {
    SCShareableContent.getExcludingDesktopWindows(false, onScreenWindowsOnly: true) { content, error in
        guard let screen = content?.displays.first(where: { $0.displayID == display }) ?? content?.displays.first else {
            log("no display to capture: \(error?.localizedDescription ?? "none listed")")
            exit(1)
        }
        let config = SCStreamConfiguration()
        config.width = pixelWidth
        config.height = pixelHeight
        config.minimumFrameInterval = CMTime(value: 1, timescale: 60)
        config.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
        config.queueDepth = 3
        config.showsCursor = true
        let stream = SCStream(filter: SCContentFilter(display: screen, excludingWindows: []), configuration: config, delegate: capture)
        do {
            try stream.addStreamOutput(capture, type: .screen, sampleHandlerQueue: videoQueue)
        } catch {
            log("cannot add stream output: \(error.localizedDescription)")
            exit(1)
        }
        activeStream = stream
        stream.startCapture { error in
            if let error {
                log("cannot start capture: \(error.localizedDescription)")
                exit(1)
            }
        }
    }
}

struct Batch: Decodable {
    var id: Int?
    var actions: [Action]
}

/// Posts one INPUT batch and answers it on the input thread, so a click never
/// waits behind a frame being encoded.
func handleInput(_ payload: Data) {
    let id = (try? JSONSerialization.jsonObject(with: payload) as? [String: Any])?["id"] as? Int ?? 0
    var reply: [String: Any] = ["id": id]
    do {
        try perform(JSONDecoder().decode(Batch.self, from: payload).actions)
    } catch let failure as Failure {
        reply["error"] = failure.message
    } catch {
        reply["error"] = "\(error)"
    }
    send(Wire.ack, json: reply)
}

func readInput() {
    while let header = readFull(5) {
        let length = header[1..<5].reduce(0) { $0 << 8 | Int($1) }
        guard let payload = readFull(length) else { break }
        switch header[0] {
        case Wire.input: handleInput(payload)
        case Wire.keyframe: videoQueue.async { encoder.forceKeyframe() }
        default: log("unknown message type \(header[0])")
        }
    }
    // EOF. The stop is bounded so a wedged stream cannot keep us alive.
    let stopped = DispatchSemaphore(value: 0)
    if let stream = activeStream {
        stream.stopCapture { _ in stopped.signal() }
        _ = stopped.wait(timeout: .now() + 2)
    }
    exit(0)
}

func serve() -> Never {
    signal(SIGPIPE, SIG_IGN)
    let mode = CGDisplayCopyDisplayMode(display)
    let pixelWidth = mode?.pixelWidth ?? Int(bounds.width)
    let pixelHeight = mode?.pixelHeight ?? Int(bounds.height)
    send(Wire.hello, json: [
        "version": version,
        "screen": ["width": Int(bounds.width), "height": Int(bounds.height)],
        "pixels": ["width": pixelWidth, "height": pixelHeight],
    ])
    startCapture(pixelWidth: pixelWidth, pixelHeight: pixelHeight)
    Thread { readInput() }.start()
    dispatchMain()
}
