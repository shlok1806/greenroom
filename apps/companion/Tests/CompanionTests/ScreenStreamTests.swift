import CoreMedia
import VideoToolbox
import XCTest

@testable import Companion

final class ScreenFrameReaderTests: XCTestCase {
    private let hello = Data(#"{"version":"greenroom-input 3","screen":{"width":1024,"height":768},"pixels":{"width":2048,"height":1536}}"#.utf8)

    private var sample: VideoSample {
        VideoSample(keyframe: true, pts: 0x0102_0304_0506_0708, data: Data([0, 0, 0, 2, 0x65, 0x88]))
    }

    private var wire: Data {
        SyntheticScreen.message(0x01, hello)
            + SyntheticScreen.message(0x02, AVCConfigTests.record)
            + SyntheticScreen.message(0x03, SyntheticScreen.videoPayload(sample))
    }

    private var expected: [ScreenMessage] {
        [
            .hello(ScreenHello(
                version: "greenroom-input 3",
                screen: GuestScreen(width: 1024, height: 768),
                pixels: GuestScreen(width: 2048, height: 1536)
            )),
            .format(AVCConfig(sps: [Data([0x67, 0x4D, 0x00, 0x1F])], pps: [Data([0x68, 0xEE])], nalLengthSize: 4)),
            .video(sample),
        ]
    }

    func testMessagesCutAtEveryByteComeOutWhole() throws {
        var reader = ScreenFrameReader()
        var messages: [ScreenMessage] = []
        for byte in wire {
            messages += try reader.append(Data([byte]))
        }
        try reader.finish()
        XCTAssertEqual(messages, expected)
    }

    func testSeveralMessagesInOneChunkAllComeOut() throws {
        var reader = ScreenFrameReader()
        XCTAssertEqual(try reader.append(wire), expected)
        try reader.finish()
    }

    func testAChunkEndingMidHeaderKeepsTheRestForLater() throws {
        var reader = ScreenFrameReader()
        let cut = wire.count - 3
        XCTAssertEqual(try reader.append(wire.prefix(cut)).count, 2)
        XCTAssertEqual(try reader.append(wire.suffix(from: cut)), [.video(sample)])
    }

    func testTypesTheCompanionDoesNotUseAreSkipped() throws {
        var reader = ScreenFrameReader()
        let noise = SyntheticScreen.message(0x05, Data("log line".utf8))
            + SyntheticScreen.message(0x04, Data(#"{"id":7}"#.utf8))
            + SyntheticScreen.message(0x7F, Data())
        XCTAssertEqual(try reader.append(noise + wire), expected)
    }

    func testALengthPastTheLimitIsRefusedBeforeItsBodyArrives() {
        var reader = ScreenFrameReader()
        let header = Data([0x03]) + SyntheticScreen.bigEndian(UInt32(ScreenFrameReader.maxPayload + 1))
        XCTAssertThrowsError(try reader.append(header)) { error in
            XCTAssertEqual(error as? ScreenStreamError, .tooLarge(ScreenFrameReader.maxPayload + 1))
        }
    }

    func testAStreamEndingInsideAMessageIsTruncated() throws {
        var reader = ScreenFrameReader()
        _ = try reader.append(wire.dropLast())
        XCTAssertThrowsError(try reader.finish()) { error in
            XCTAssertEqual(error as? ScreenStreamError, .truncated)
        }
    }

    func testAVideoMessageWithoutASampleIsRefused() {
        var reader = ScreenFrameReader()
        let empty = SyntheticScreen.message(0x03, Data(repeating: 0, count: 9))
        XCTAssertThrowsError(try reader.append(empty)) { error in
            XCTAssertEqual(error as? ScreenStreamError, .badVideo("9 bytes"))
        }
    }

    func testAHelloThatIsNotTheContractIsRefused() {
        var reader = ScreenFrameReader()
        XCTAssertThrowsError(try reader.append(SyntheticScreen.message(0x01, Data("{}".utf8)))) { error in
            guard case ScreenStreamError.badHello = error else { return XCTFail("got \(error)") }
        }
    }

    func testTheKeyframeBitAndABigEndianTimestampAreRead() throws {
        var reader = ScreenFrameReader()
        let payload = Data([0xFE]) + SyntheticScreen.bigEndian(UInt64(1_500_000)) + Data([1, 2, 3])
        XCTAssertEqual(
            try reader.append(SyntheticScreen.message(0x03, payload)),
            [.video(VideoSample(keyframe: false, pts: 1_500_000, data: Data([1, 2, 3])))]
        )
    }
}

final class AVCConfigTests: XCTestCase {
    /// Main profile, 4-byte NAL lengths, one SPS, one PPS.
    static let record = Data([0x01, 0x4D, 0x00, 0x1F, 0xFF, 0xE1, 0x00, 0x04, 0x67, 0x4D, 0x00, 0x1F, 0x01, 0x00, 0x02, 0x68, 0xEE])

    /// What `greenroom-input --serve` sent from a 1024x768-point guest: High
    /// profile, level 5.1, and the four High-profile extension bytes at the end.
    static let guestRecord = Data([
        0x01, 0x64, 0x00, 0x33, 0xFF, 0xE1, 0x00, 0x15,
        0x27, 0x64, 0x00, 0x33, 0xAC, 0x13, 0x14, 0x10, 0x00, 0x80, 0x03, 0x06,
        0x9A, 0x81, 0x01, 0x01, 0x03, 0xC2, 0x01, 0x08, 0x42,
        0x01, 0x00, 0x04, 0x28, 0xEE, 0x3C, 0xB0,
        0xFD, 0xF8, 0xF8, 0x00,
    ])

    func testTheGuestHelpersRecordDescribesItsPixels() throws {
        let config = try AVCConfig(Self.guestRecord)
        XCTAssertEqual(config.sps.map(\.count), [21])
        XCTAssertEqual(config.pps, [Data([0x28, 0xEE, 0x3C, 0xB0])])
        XCTAssertEqual(config.nalLengthSize, 4)
        let size = CMVideoFormatDescriptionGetDimensions(try config.formatDescription())
        XCTAssertEqual(size.width, 2048)
        XCTAssertEqual(size.height, 1536)
    }

    func testARecordGivesItsParameterSetsAndLengthSize() throws {
        let config = try AVCConfig(Self.record)
        XCTAssertEqual(config.sps, [Data([0x67, 0x4D, 0x00, 0x1F])])
        XCTAssertEqual(config.pps, [Data([0x68, 0xEE])])
        XCTAssertEqual(config.nalLengthSize, 4)
    }

    func testTheLengthSizeComesFromTheLowTwoBits() throws {
        var record = Self.record
        record[4] = 0xFD
        XCTAssertEqual(try AVCConfig(record).nalLengthSize, 2)
    }

    func testBadRecordsAreRefused() {
        var version = Self.record
        version[0] = 0
        var threeByteLengths = Self.record
        threeByteLengths[4] = 0xFE
        var noPPS = Self.record.prefix(12)
        noPPS.append(0x00)
        let cases: [(String, Data)] = [
            ("version", version),
            ("3-byte lengths", threeByteLengths),
            ("no PPS", noPPS),
            ("cut short", Self.record.dropLast()),
            ("empty", Data()),
        ]
        for (name, record) in cases {
            XCTAssertThrowsError(try AVCConfig(record), name) { error in
                guard case ScreenStreamError.badFormat = error else { return XCTFail("\(name): got \(error)") }
            }
        }
    }

    func testAnEncodersRecordDescribesItsSize() throws {
        let encoded = try SyntheticScreen.encode(width: 1280, height: 960, colors: [.init(red: 0, green: 0, blue: 0)])
        let description = try AVCConfig(encoded.avcC).formatDescription()
        let size = CMVideoFormatDescriptionGetDimensions(description)
        XCTAssertEqual(size.width, 1280)
        XCTAssertEqual(size.height, 960)
        XCTAssertEqual(CMFormatDescriptionGetMediaSubType(description), kCMVideoCodecType_H264)
    }
}

final class LiveDecodeTests: XCTestCase {
    private let colors: [SyntheticScreen.Color] = [
        .init(red: 220, green: 30, blue: 30),
        .init(red: 30, green: 200, blue: 40),
        .init(red: 40, green: 60, blue: 220),
        .init(red: 240, green: 240, blue: 240),
    ]

    /// The whole companion path short of the network: encoded like the guest,
    /// framed like the daemon, cut at odd sizes, read, and decoded in hardware.
    func testASyntheticStreamDecodesFrameForFrame() throws {
        let encoded = try SyntheticScreen.encode(width: 1280, height: 960, colors: colors)
        let wire = SyntheticScreen.wire(encoded, width: 1280, height: 960)

        var reader = ScreenFrameReader()
        var messages: [ScreenMessage] = []
        let sizes = [1, 4, 5, 6, 997, 1500, 7, 64 << 10]
        var offset = 0
        var turn = 0
        while offset < wire.count {
            let end = min(offset + sizes[turn % sizes.count], wire.count)
            messages += try reader.append(wire.subdata(in: offset ..< end))
            offset = end
            turn += 1
        }
        try reader.finish()

        guard case .hello(let hello) = messages.first, case .format(let config) = messages[1] else {
            return XCTFail("got \(messages.prefix(2))")
        }
        XCTAssertEqual(hello.pixels, GuestScreen(width: 1280, height: 960))
        let format = try config.formatDescription()
        let samples = try messages.dropFirst(2).map { message in
            guard case .video(let sample) = message else { throw ScreenStreamError.badVideo("not video") }
            return try sample.sampleBuffer(format: format)
        }
        XCTAssertEqual(samples.map(SyntheticScreen.isKeyframe), encoded.samples.map(\.keyframe))
        XCTAssertTrue(encoded.samples[0].keyframe)

        let frames = try decode(samples, format: format)
        XCTAssertEqual(frames.count, colors.count)
        for (frame, color) in zip(frames, colors) {
            XCTAssertEqual(CVPixelBufferGetWidth(frame), 1280)
            XCTAssertEqual(CVPixelBufferGetHeight(frame), 960)
            let seen = SyntheticScreen.centre(of: frame)
            XCTAssertTrue(SyntheticScreen.close(seen, color), "\(seen) is not \(color)")
        }
    }

    func testASampleIsShownOnArrivalAndMarkedIfNotAKeyframe() throws {
        let format = try AVCConfig(AVCConfigTests.guestRecord).formatDescription()
        for keyframe in [true, false] {
            let buffer = try VideoSample(keyframe: keyframe, pts: 2_000_000, data: Data([0, 0, 0, 1, 0x65]))
                .sampleBuffer(format: format)
            let attachments = CMSampleBufferGetSampleAttachmentsArray(buffer, createIfNecessary: false) as? [[CFString: Any]]
            XCTAssertEqual(attachments?.first?[kCMSampleAttachmentKey_DisplayImmediately] as? Bool, true)
            XCTAssertEqual(attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool, keyframe ? nil : true)
            XCTAssertEqual(buffer.presentationTimeStamp.seconds, 2)
            XCTAssertEqual(CMSampleBufferGetTotalSampleSize(buffer), 5)
        }
    }

    private func decode(_ samples: [CMSampleBuffer], format: CMVideoFormatDescription) throws -> [CVPixelBuffer] {
        var made: VTDecompressionSession?
        let attributes = [kCVPixelBufferPixelFormatTypeKey: kCVPixelFormatType_32BGRA] as CFDictionary
        let status = VTDecompressionSessionCreate(
            allocator: nil,
            formatDescription: format,
            decoderSpecification: nil,
            imageBufferAttributes: attributes,
            outputCallback: nil,
            decompressionSessionOut: &made
        )
        let session = try XCTUnwrap(made, "VTDecompressionSessionCreate \(status)")
        defer { VTDecompressionSessionInvalidate(session) }
        let frames = Frames()
        for sample in samples {
            let status = VTDecompressionSessionDecodeFrame(session, sampleBuffer: sample, flags: [], infoFlagsOut: nil) { status, _, image, _, _ in
                XCTAssertEqual(status, noErr)
                if let image { frames.add(image) }
            }
            XCTAssertEqual(status, noErr)
        }
        VTDecompressionSessionWaitForAsynchronousFrames(session)
        return frames.all
    }

    private final class Frames: @unchecked Sendable {
        private let lock = NSLock()
        private var frames: [CVPixelBuffer] = []
        func add(_ frame: CVPixelBuffer) { lock.withLock { frames.append(frame) } }
        var all: [CVPixelBuffer] { lock.withLock { frames } }
    }
}
