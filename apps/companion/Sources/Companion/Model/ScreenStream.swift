import CoreMedia
import Foundation

/// One message off `GET /api/runs/{id}/screen/live` (ADR 0011).
enum ScreenMessage: Equatable, Sendable {
    case hello(ScreenHello)
    case format(AVCConfig)
    case video(VideoSample)
}

/// The helper's first message: the guest display in points and in pixels.
struct ScreenHello: Codable, Hashable, Sendable {
    var version: String
    var screen: GuestScreen
    var pixels: GuestScreen
}

/// One encoded frame: AVCC data, NAL units behind length prefixes, as sent.
struct VideoSample: Equatable, Sendable {
    var keyframe: Bool
    /// Microseconds.
    var pts: UInt64
    var data: Data
}

enum ScreenStreamError: Error, LocalizedError, Equatable {
    case tooLarge(Int)
    case truncated
    case badHello(String)
    case badFormat(String)
    case badVideo(String)
    case media(String, OSStatus)

    var errorDescription: String? {
        switch self {
        case .tooLarge(let length): "a \(length) byte message"
        case .truncated: "the stream ended inside a message"
        case .badHello(let detail): "a bad HELLO: \(detail)"
        case .badFormat(let detail): "a bad FORMAT: \(detail)"
        case .badVideo(let detail): "a bad VIDEO: \(detail)"
        case .media(let call, let status): "\(call) failed (\(status))"
        }
    }
}

/// Cuts `[type u8][length u32 BE][payload]` messages out of chunks cut
/// anywhere. Types the companion has no use for (ACK, LOG, newer ones) are skipped.
struct ScreenFrameReader {
    /// Far above a 2048x1536 keyframe; a length past it means the stream is garbage.
    static let maxPayload = 16 << 20
    private static let headerSize = 5

    private var buffer = Data()

    mutating func append(_ chunk: Data) throws -> [ScreenMessage] {
        buffer.append(chunk)
        var messages: [ScreenMessage] = []
        var offset = buffer.startIndex
        while buffer.endIndex - offset >= Self.headerSize {
            let type = buffer[offset]
            let length = buffer[offset + 1 ..< offset + 5].reduce(0) { $0 << 8 | Int($1) }
            guard length <= Self.maxPayload else { throw ScreenStreamError.tooLarge(length) }
            let start = offset + Self.headerSize
            guard buffer.endIndex - start >= length else { break }
            if let message = try Self.message(type: type, payload: buffer[start ..< start + length]) {
                messages.append(message)
            }
            offset = start + length
        }
        buffer.removeSubrange(buffer.startIndex ..< offset)
        return messages
    }

    /// Throws if the stream ended partway through a message.
    func finish() throws {
        guard buffer.isEmpty else { throw ScreenStreamError.truncated }
    }

    private static func message(type: UInt8, payload: Data) throws -> ScreenMessage? {
        switch type {
        case 0x01:
            do {
                return .hello(try JSONDecoder().decode(ScreenHello.self, from: payload))
            } catch {
                throw ScreenStreamError.badHello(String(describing: error))
            }
        case 0x02:
            return .format(try AVCConfig(payload))
        case 0x03:
            guard payload.count > 9 else { throw ScreenStreamError.badVideo("\(payload.count) bytes") }
            let base = payload.startIndex
            let pts = payload[base + 1 ..< base + 9].reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
            return .video(VideoSample(keyframe: payload[base] & 1 == 1, pts: pts, data: Data(payload[(base + 9)...])))
        default:
            return nil
        }
    }
}

/// An `AVCDecoderConfigurationRecord` (ISO/IEC 14496-15 5.3.3.1): what the
/// decoder needs to read the samples that follow.
struct AVCConfig: Equatable, Sendable {
    var sps: [Data]
    var pps: [Data]
    /// Bytes in each NAL unit's length prefix.
    var nalLengthSize: Int

    init(sps: [Data], pps: [Data], nalLengthSize: Int) {
        self.sps = sps
        self.pps = pps
        self.nalLengthSize = nalLengthSize
    }

    init(_ record: Data) throws {
        var cursor = ByteCursor(record)
        guard try cursor.byte() == 1 else { throw ScreenStreamError.badFormat("configuration version is not 1") }
        _ = try cursor.bytes(3) // profile, compatibility, level: all in the SPS too
        nalLengthSize = Int(try cursor.byte() & 0x03) + 1
        guard nalLengthSize != 3 else { throw ScreenStreamError.badFormat("3-byte NAL lengths") }
        sps = try cursor.parameterSets(count: Int(try cursor.byte() & 0x1F))
        pps = try cursor.parameterSets(count: Int(try cursor.byte()))
        // High profiles append chroma and bit-depth fields; the SPS already carries them.
        guard !sps.isEmpty, !pps.isEmpty else { throw ScreenStreamError.badFormat("no SPS or PPS") }
    }

    func formatDescription() throws -> CMVideoFormatDescription {
        let sets = sps + pps
        let joined = sets.reduce(into: [UInt8]()) { $0 += $1 }
        var description: CMFormatDescription?
        let status = joined.withUnsafeBufferPointer { base in
            var offset = 0
            let pointers = sets.map { set in
                defer { offset += set.count }
                return base.baseAddress! + offset
            }
            return CMVideoFormatDescriptionCreateFromH264ParameterSets(
                allocator: kCFAllocatorDefault,
                parameterSetCount: sets.count,
                parameterSetPointers: pointers,
                parameterSetSizes: sets.map(\.count),
                nalUnitHeaderLength: Int32(nalLengthSize),
                formatDescriptionOut: &description
            )
        }
        guard status == noErr, let description else {
            throw ScreenStreamError.media("CMVideoFormatDescriptionCreateFromH264ParameterSets", status)
        }
        return description
    }
}

extension VideoSample {
    /// The data goes in unchanged. Shown on arrival rather than at its
    /// timestamp; a non-keyframe is marked so the decoder knows it depends on others.
    func sampleBuffer(format: CMVideoFormatDescription) throws -> CMSampleBuffer {
        var block: CMBlockBuffer?
        var status = CMBlockBufferCreateWithMemoryBlock(
            allocator: kCFAllocatorDefault,
            memoryBlock: nil,
            blockLength: data.count,
            blockAllocator: kCFAllocatorDefault,
            customBlockSource: nil,
            offsetToData: 0,
            dataLength: data.count,
            flags: kCMBlockBufferAssureMemoryNowFlag,
            blockBufferOut: &block
        )
        guard status == noErr, let block else { throw ScreenStreamError.media("CMBlockBufferCreateWithMemoryBlock", status) }
        status = data.withUnsafeBytes { bytes in
            CMBlockBufferReplaceDataBytes(with: bytes.baseAddress!, blockBuffer: block, offsetIntoDestination: 0, dataLength: data.count)
        }
        guard status == noErr else { throw ScreenStreamError.media("CMBlockBufferReplaceDataBytes", status) }

        var timing = CMSampleTimingInfo(
            duration: .invalid,
            presentationTimeStamp: CMTime(value: CMTimeValue(clamping: pts), timescale: 1_000_000),
            decodeTimeStamp: .invalid
        )
        var size = data.count
        var sample: CMSampleBuffer?
        status = CMSampleBufferCreateReady(
            allocator: kCFAllocatorDefault,
            dataBuffer: block,
            formatDescription: format,
            sampleCount: 1,
            sampleTimingEntryCount: 1,
            sampleTimingArray: &timing,
            sampleSizeEntryCount: 1,
            sampleSizeArray: &size,
            sampleBufferOut: &sample
        )
        guard status == noErr, let sample else { throw ScreenStreamError.media("CMSampleBufferCreateReady", status) }

        let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true)! as NSArray
        let first = attachments[0] as! NSMutableDictionary
        first[kCMSampleAttachmentKey_DisplayImmediately] = true
        if !keyframe { first[kCMSampleAttachmentKey_NotSync] = true }
        return sample
    }
}

private struct ByteCursor {
    private let data: Data
    private var offset: Data.Index

    init(_ data: Data) {
        self.data = data
        offset = data.startIndex
    }

    mutating func byte() throws -> UInt8 {
        try bytes(1).first!
    }

    mutating func bytes(_ count: Int) throws -> Data {
        guard data.endIndex - offset >= count else { throw ScreenStreamError.badFormat("record ends early") }
        defer { offset += count }
        return data[offset ..< offset + count]
    }

    mutating func parameterSets(count: Int) throws -> [Data] {
        try (0 ..< count).map { _ in
            let length = try bytes(2).reduce(0) { $0 << 8 | Int($1) }
            guard length > 0 else { throw ScreenStreamError.badFormat("an empty parameter set") }
            return Data(try bytes(length))
        }
    }
}
