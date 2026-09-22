// greenroom-input: the one thing inside a guest that moves its mouse and
// presses its keys.
//
// The daemon compiles this file inside the machine once (see input.go), then
// calls the binary with one base64-encoded JSON argument, so no text the
// person types ever passes through a shell:
//
//     greenroom-input --version
//     greenroom-input --json-base64 <base64 of {"actions":[...]}>
//     greenroom-input --serve
//
// --json-base64 writes one JSON object to stdout and exits 0, or writes an
// error object and exits 1. --serve streams the screen as H.264 and takes
// input batches on stdin until stdin closes (ADR 0011). Coordinates are pixels on the main display; the daemon turns
// the fractions the companion sends into pixels before it gets here.
//
// The events go in through CGEvent at the HID tap, which needs the
// Accessibility and PostEvent permissions. The greenroom image grants both to
// the Tart guest agent, and this binary inherits them because the agent
// starts it (docs/02-spike.md).

import CoreGraphics
import CoreMedia
import Foundation
import ScreenCaptureKit
import VideoToolbox

let version = "greenroom-input 3"

// MARK: - Wire types

struct Action: Decodable {
    var type: String
    var x: Double?
    var y: Double?
    var button: String?
    var clicks: Int?
    var deltaX: Double?
    var deltaY: Double?
    var text: String?
    var key: String?
    var mods: [String]?
    var ms: Int?
}

struct Request: Decodable {
    var actions: [Action]
}

// MARK: - Keys

/// US-ANSI virtual key codes. A shortcut has to name a key, not a character:
/// command-A is the key at code 0 with the command flag, and no amount of
/// unicode on the event will make an application see it.
let namedKeys: [String: CGKeyCode] = [
    "return": 36, "enter": 76, "tab": 48, "space": 49, "delete": 51, "backspace": 51,
    "forwarddelete": 117, "escape": 53, "esc": 53, "left": 123, "right": 124,
    "down": 125, "up": 126, "home": 115, "end": 119, "pageup": 116, "pagedown": 121,
    "capslock": 57, "help": 114,
    "f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
    "f9": 101, "f10": 109, "f11": 103, "f12": 111,
]

let characterKeys: [String: CGKeyCode] = [
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
    "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
    "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "9": 25, "7": 26, "8": 28, "0": 29,
    "=": 24, "-": 27, "]": 30, "[": 33, "'": 39, ";": 41, "\\": 42, ",": 43, "/": 44,
    ".": 47, "`": 50,
    "o": 31, "u": 32, "i": 34, "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46,
]

func keyCode(for name: String) -> CGKeyCode? {
    let lower = name.lowercased()
    if let code = namedKeys[lower] { return code }
    if let code = characterKeys[lower] { return code }
    return nil
}

func flags(_ names: [String]?) -> CGEventFlags {
    var out: CGEventFlags = []
    for raw in names ?? [] {
        switch raw.lowercased() {
        case "cmd", "command", "meta": out.insert(.maskCommand)
        case "shift": out.insert(.maskShift)
        case "alt", "option", "opt": out.insert(.maskAlternate)
        case "ctrl", "control": out.insert(.maskControl)
        case "fn", "function": out.insert(.maskSecondaryFn)
        default: break
        }
    }
    return out
}

// MARK: - Posting

let display = CGMainDisplayID()
let bounds = CGDisplayBounds(display)
let source = CGEventSource(stateID: .combinedSessionState)

/// Where the pointer is, as this process believes it. A drag is a press, some
/// moves and a release, and every one of those events has to carry a position,
/// so the last one is remembered rather than read back from the system.
var cursor = CGPoint(x: bounds.midX, y: bounds.midY)

func clamp(_ point: CGPoint) -> CGPoint {
    CGPoint(
        x: min(max(point.x, bounds.minX), bounds.maxX - 1),
        y: min(max(point.y, bounds.minY), bounds.maxY - 1)
    )
}

func point(_ action: Action) -> CGPoint {
    guard let x = action.x, let y = action.y else { return cursor }
    return clamp(CGPoint(x: x, y: y))
}

func mouseButton(_ name: String?) -> (CGMouseButton, CGEventType, CGEventType, CGEventType) {
    switch (name ?? "left").lowercased() {
    case "right": return (.right, .rightMouseDown, .rightMouseUp, .rightMouseDragged)
    case "middle", "center": return (.center, .otherMouseDown, .otherMouseUp, .otherMouseDragged)
    default: return (.left, .leftMouseDown, .leftMouseUp, .leftMouseDragged)
    }
}

/// Every event this process posts goes through here, so the HID tap is named
/// once and a caller cannot post to a different one by accident.
func post(_ event: CGEvent?) {
    event?.post(tap: .cghidEventTap)
}

func move(to target: CGPoint, dragging button: CGMouseButton?) {
    cursor = target
    let type: CGEventType
    switch button {
    case .some(.left): type = .leftMouseDragged
    case .some(.right): type = .rightMouseDragged
    case .some(.center): type = .otherMouseDragged
    default: type = .mouseMoved
    }
    post(CGEvent(
        mouseEventSource: source,
        mouseType: type,
        mouseCursorPosition: target,
        mouseButton: button ?? .left
    ))
}

func mouse(_ type: CGEventType, at target: CGPoint, button: CGMouseButton, clickState: Int) {
    cursor = target
    guard let event = CGEvent(
        mouseEventSource: source,
        mouseType: type,
        mouseCursorPosition: target,
        mouseButton: button
    ) else { return }
    event.setIntegerValueField(.mouseEventClickState, value: Int64(clickState))
    post(event)
}

/// A double click is one click event with clickState 2, not two clicks: an
/// application reads the field rather than timing the presses.
func click(at target: CGPoint, button: CGMouseButton, down: CGEventType, up: CGEventType, times: Int) {
    for n in 1...max(times, 1) {
        mouse(down, at: target, button: button, clickState: n)
        mouse(up, at: target, button: button, clickState: n)
    }
}

/// Which button, if any, is held. Held state is what turns a move into a drag.
var held: CGMouseButton?

func type(text: String) {
    // One event per character, with the character set as the event's unicode
    // string. This types what the person typed without caring which keyboard
    // layout the guest has.
    //
    // A one-millisecond gap follows both the key-down and the key-up: posted
    // back to back with no gap at all, a whole string arrives at the window
    // server faster than a freshly-focused app's run loop can drain its event
    // queue, and it silently drops everything after the first character or
    // two -- confirmed end to end (e2e_input_test.go) by typing into Terminal
    // right after opening it: only the first couple of characters landed and
    // the rest, including the trailing return, never did. The delay costs
    // nothing a person would notice and it is what makes every character
    // actually arrive.
    for character in text {
        let units = Array(String(character).utf16)
        guard let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false)
        else { continue }
        units.withUnsafeBufferPointer { buffer in
            guard let base = buffer.baseAddress else { return }
            down.keyboardSetUnicodeString(stringLength: units.count, unicodeString: base)
            up.keyboardSetUnicodeString(stringLength: units.count, unicodeString: base)
        }
        post(down)
        usleep(1_000)
        post(up)
        usleep(1_000)
    }
}

func press(key name: String, mods: [String]?) throws {
    guard let code = keyCode(for: name) else {
        throw Failure("unknown key \(name)")
    }
    let modifiers = flags(mods)
    guard let down = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: true),
          let up = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: false)
    else { throw Failure("cannot build a key event for \(name)") }
    down.flags = modifiers
    up.flags = modifiers
    post(down)
    post(up)
}

func scroll(deltaX: Double, deltaY: Double) {
    guard let event = CGEvent(
        scrollWheelEvent2Source: source,
        units: .pixel,
        wheelCount: 2,
        wheel1: Int32(deltaY.rounded()),
        wheel2: Int32(deltaX.rounded()),
        wheel3: 0
    ) else { return }
    // A scroll goes to whatever is under the pointer, so it carries the
    // position the caller last moved to.
    event.location = cursor
    post(event)
}

// MARK: - Running

struct Failure: Error {
    let message: String
    init(_ message: String) { self.message = message }
}

func run(_ action: Action) throws {
    switch action.type.lowercased() {
    case "move":
        move(to: point(action), dragging: held)
    case "click":
        let (button, down, up, _) = mouseButton(action.button)
        click(at: point(action), button: button, down: down, up: up, times: action.clicks ?? 1)
    case "down":
        let (button, down, _, _) = mouseButton(action.button)
        // The click count comes from the window the person clicked in, so a
        // double click here is a double click there.
        mouse(down, at: point(action), button: button, clickState: max(action.clicks ?? 1, 1))
        held = button
    case "up":
        let (button, _, up, _) = mouseButton(action.button)
        mouse(up, at: point(action), button: button, clickState: max(action.clicks ?? 1, 1))
        held = nil
    case "scroll":
        if action.x != nil { move(to: point(action), dragging: held) }
        scroll(deltaX: action.deltaX ?? 0, deltaY: action.deltaY ?? 0)
    case "type":
        type(text: action.text ?? "")
    case "key":
        try press(key: action.key ?? "", mods: action.mods)
    case "sleep":
        usleep(UInt32(max(0, min(action.ms ?? 0, 5000)) * 1000))
    default:
        throw Failure("unknown action \(action.type)")
    }
}

func emit(_ object: [String: Any], to handle: FileHandle) {
    let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data("{}".utf8)
    handle.write(data)
    handle.write(Data("\n".utf8))
}

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
        for action in try JSONDecoder().decode(Batch.self, from: payload).actions {
            try run(action)
        }
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

let arguments = Array(CommandLine.arguments.dropFirst())
if arguments.first == "--version" {
    print(version)
    exit(0)
}
if arguments.first == "--serve" {
    serve()
}

guard arguments.count == 2, arguments[0] == "--json-base64",
      let payload = Data(base64Encoded: arguments[1])
else {
    emit(["error": "usage: greenroom-input --json-base64 <base64 json>"], to: FileHandle.standardError)
    exit(1)
}

do {
    let request = try JSONDecoder().decode(Request.self, from: payload)
    for action in request.actions {
        try run(action)
    }
    // A release that is never posted leaves the guest with a stuck button, so
    // a batch that ends mid-drag is the caller's business, not a leak here:
    // the daemon always sends the release in the same batch or a later one.
    emit([
        "ok": true,
        "actions": request.actions.count,
        "screen": ["width": Int(bounds.width), "height": Int(bounds.height)],
    ], to: FileHandle.standardOutput)
} catch let failure as Failure {
    emit(["error": failure.message], to: FileHandle.standardError)
    exit(1)
} catch {
    emit(["error": "\(error)"], to: FileHandle.standardError)
    exit(1)
}
