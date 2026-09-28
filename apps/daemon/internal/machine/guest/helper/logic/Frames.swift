// The guest agent's wire (daemon ADR 0005, "Frames"): frame types, the frame and BLOB chunk
// layouts and their limits. Pure Data work, so the host tests pin the exact bytes the daemon's
// internal/guestagent reads.

import Foundation

/// Frame types of the agent channel. The layout is the --serve one, [type u8][length u32
/// big-endian][payload], but the numbers are the agent's own (Serve.swift's `Wire` is --serve's).
enum AgentFrame {
    static let hello: UInt8 = 0x01
    static let request: UInt8 = 0x20
    static let response: UInt8 = 0x21
    static let blob: UInt8 = 0x22
    static let cancel: UInt8 = 0x23
    static let event: UInt8 = 0x24
    static let ping: UInt8 = 0x25
    static let pong: UInt8 = 0x26
    static let pause: UInt8 = 0x27
    static let resume: UInt8 = 0x28
    /// Reserved for wave 3's live video; nothing sends it in wave 1.
    static let stream: UInt8 = 0x29
}

/// The largest payload either side accepts. A longer length is a broken channel, never an
/// allocation.
let maxAgentPayload = 4 << 20

/// The most image bytes one BLOB frame carries.
let maxBlobChunk = 1 << 20

/// The version of the wire, bumped only by a breaking change (HELLO's `protocol`).
let agentProtocol = 1

struct AgentFrameData: Equatable {
    let type: UInt8
    let payload: Data
}

enum AgentFrameError: Error, Equatable {
    /// The stream ended inside a payload.
    case truncated
    /// The header announced a payload over maxAgentPayload.
    case tooLarge(Int)
}

/// The bytes of one frame.
func encodeAgentFrame(_ type: UInt8, _ payload: Data) -> Data {
    var out = Data([type])
    withUnsafeBytes(of: UInt32(payload.count).bigEndian) { out.append(contentsOf: $0) }
    out.append(payload)
    return out
}

/// Reads one frame with `read`, which returns exactly the bytes asked for or nil at the end of
/// the stream. Nil means the stream ended between frames (or inside a header, which a closed
/// pipe does the same way); a payload cut short or announced too long throws.
func readAgentFrame(_ read: (Int) -> Data?) throws -> AgentFrameData? {
    guard let header = read(5), header.count == 5 else { return nil }
    let start = header.startIndex
    let length = header[(start + 1)..<(start + 5)].reduce(0) { $0 << 8 | Int($1) }
    if length > maxAgentPayload { throw AgentFrameError.tooLarge(length) }
    guard let payload = read(length), payload.count == length else { throw AgentFrameError.truncated }
    return AgentFrameData(type: header[start], payload: payload)
}

/// Splits a request's image into BLOB payloads, `[id u32][seq u16][last u8][bytes]`, at most
/// `chunk` bytes each, numbered from 0. An empty image is one empty last chunk, so the daemon
/// always sees where the blob ends.
func blobChunks(id: UInt32, data: Data, chunk: Int = maxBlobChunk) -> [Data] {
    let size = max(1, chunk)
    var out: [Data] = []
    var offset = data.startIndex
    var seq: UInt16 = 0
    repeat {
        let end = min(offset + size, data.endIndex)
        var payload = Data()
        withUnsafeBytes(of: id.bigEndian) { payload.append(contentsOf: $0) }
        withUnsafeBytes(of: seq.bigEndian) { payload.append(contentsOf: $0) }
        payload.append(end == data.endIndex ? 1 : 0)
        payload.append(data[offset..<end])
        out.append(payload)
        offset = end
        seq &+= 1
    } while offset < data.endIndex
    return out
}

/// The header of a BLOB payload, or nil when it is shorter than one.
func parseBlobChunk(_ payload: Data) -> (id: UInt32, seq: UInt16, last: Bool, bytes: Data)? {
    guard payload.count >= 7 else { return nil }
    let s = payload.startIndex
    let id = payload[s..<(s + 4)].reduce(UInt32(0)) { $0 << 8 | UInt32($1) }
    let seq = payload[(s + 4)..<(s + 6)].reduce(UInt16(0)) { $0 << 8 | UInt16($1) }
    return (id, seq, payload[s + 6] != 0, Data(payload[(s + 7)...]))
}
