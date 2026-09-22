import CoreMedia
import CoreVideo
import Foundation
import VideoToolbox
import XCTest

@testable import Companion

/// A stream like the guest helper's (ADR 0011): solid-colour frames encoded
/// by VideoToolbox in real time without reordering, framed for the wire.
enum SyntheticScreen {
    struct Color: Equatable {
        var red: UInt8
        var green: UInt8
        var blue: UInt8
    }

    struct Encoded {
        var avcC: Data
        var samples: [VideoSample]
    }

    static func encode(width: Int, height: Int, colors: [Color]) throws -> Encoded {
        var made: VTCompressionSession?
        var status = VTCompressionSessionCreate(
            allocator: nil,
            width: Int32(width),
            height: Int32(height),
            codecType: kCMVideoCodecType_H264,
            encoderSpecification: nil,
            imageBufferAttributes: nil,
            compressedDataAllocator: nil,
            outputCallback: nil,
            refcon: nil,
            compressionSessionOut: &made
        )
        let session = try XCTUnwrap(made, "VTCompressionSessionCreate \(status)")
        defer { VTCompressionSessionInvalidate(session) }
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AllowFrameReordering, value: kCFBooleanFalse)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ProfileLevel, value: kVTProfileLevel_H264_High_AutoLevel)

        let collected = Collected()
        for (index, color) in colors.enumerated() {
            let pixels = try pixelBuffer(width: width, height: height, color: color)
            let options = index == 0 ? [kVTEncodeFrameOptionKey_ForceKeyFrame: true] as CFDictionary : nil
            status = VTCompressionSessionEncodeFrame(
                session,
                imageBuffer: pixels,
                presentationTimeStamp: CMTime(value: CMTimeValue(index), timescale: 30),
                duration: .invalid,
                frameProperties: options,
                infoFlagsOut: nil
            ) { status, _, sample in
                collected.add(status == noErr ? sample : nil)
            }
            XCTAssertEqual(status, noErr)
        }
        VTCompressionSessionCompleteFrames(session, untilPresentationTimeStamp: .invalid)

        let buffers = collected.samples
        XCTAssertEqual(buffers.count, colors.count)
        let format = try XCTUnwrap(buffers.first.flatMap(CMSampleBufferGetFormatDescription))
        let atoms = CMFormatDescriptionGetExtension(format, extensionKey: kCMFormatDescriptionExtension_SampleDescriptionExtensionAtoms)
        let avcC = try XCTUnwrap((atoms as? [String: Any])?["avcC"] as? Data)
        let samples = try buffers.map { buffer in
            let block = try XCTUnwrap(CMSampleBufferGetDataBuffer(buffer))
            var data = Data(count: CMBlockBufferGetDataLength(block))
            _ = data.withUnsafeMutableBytes { CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: $0.count, destination: $0.baseAddress!) }
            let pts = CMTimeConvertScale(buffer.presentationTimeStamp, timescale: 1_000_000, method: .default).value
            return VideoSample(keyframe: isKeyframe(buffer), pts: UInt64(pts), data: data)
        }
        return Encoded(avcC: avcC, samples: samples)
    }

    /// HELLO, FORMAT, then every sample, as the daemon sends them.
    static func wire(_ encoded: Encoded, width: Int, height: Int) -> Data {
        let hello = #"{"version":"greenroom-input 3","screen":{"width":\#(width / 2),"height":\#(height / 2)},"pixels":{"width":\#(width),"height":\#(height)}}"#
        var out = message(0x01, Data(hello.utf8))
        out += message(0x02, encoded.avcC)
        for sample in encoded.samples {
            out += message(0x03, videoPayload(sample))
        }
        return out
    }

    static func message(_ type: UInt8, _ payload: Data) -> Data {
        var out = Data([type])
        out += bigEndian(UInt32(payload.count))
        out += payload
        return out
    }

    static func videoPayload(_ sample: VideoSample) -> Data {
        var out = Data([sample.keyframe ? 1 : 0])
        out += bigEndian(sample.pts)
        out += sample.data
        return out
    }

    static func bigEndian<T: FixedWidthInteger>(_ value: T) -> Data {
        withUnsafeBytes(of: value.bigEndian) { Data($0) }
    }

    static func isKeyframe(_ buffer: CMSampleBuffer) -> Bool {
        let attachments = CMSampleBufferGetSampleAttachmentsArray(buffer, createIfNecessary: false) as? [[CFString: Any]]
        return attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool != true
    }

    /// The colour at the centre of a BGRA buffer.
    static func centre(of buffer: CVPixelBuffer) -> Color {
        CVPixelBufferLockBaseAddress(buffer, .readOnly)
        defer { CVPixelBufferUnlockBaseAddress(buffer, .readOnly) }
        let row = CVPixelBufferGetBaseAddress(buffer)!.advanced(by: CVPixelBufferGetBytesPerRow(buffer) * (CVPixelBufferGetHeight(buffer) / 2))
        let pixel = row.advanced(by: 4 * (CVPixelBufferGetWidth(buffer) / 2)).assumingMemoryBound(to: UInt8.self)
        return Color(red: pixel[2], green: pixel[1], blue: pixel[0])
    }

    static func close(_ a: Color, _ b: Color, within tolerance: Int = 24) -> Bool {
        abs(Int(a.red) - Int(b.red)) <= tolerance
            && abs(Int(a.green) - Int(b.green)) <= tolerance
            && abs(Int(a.blue) - Int(b.blue)) <= tolerance
    }

    private static func pixelBuffer(width: Int, height: Int, color: Color) throws -> CVPixelBuffer {
        var made: CVPixelBuffer?
        CVPixelBufferCreate(nil, width, height, kCVPixelFormatType_32BGRA, nil, &made)
        let buffer = try XCTUnwrap(made)
        CVPixelBufferLockBaseAddress(buffer, [])
        defer { CVPixelBufferUnlockBaseAddress(buffer, []) }
        let base = CVPixelBufferGetBaseAddress(buffer)!
        let stride = CVPixelBufferGetBytesPerRow(buffer)
        for y in 0 ..< height {
            let row = base.advanced(by: y * stride).assumingMemoryBound(to: UInt8.self)
            for x in 0 ..< width {
                row[4 * x] = color.blue
                row[4 * x + 1] = color.green
                row[4 * x + 2] = color.red
                row[4 * x + 3] = 255
            }
        }
        return buffer
    }

    private final class Collected: @unchecked Sendable {
        private let lock = NSLock()
        private var buffers: [CMSampleBuffer] = []

        func add(_ buffer: CMSampleBuffer?) {
            guard let buffer else { return }
            lock.withLock { buffers.append(buffer) }
        }

        var samples: [CMSampleBuffer] { lock.withLock { buffers } }
    }
}
