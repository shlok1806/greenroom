import Foundation

/// A read function over bytes, as readFull reads stdin: exactly the count asked for, or nil.
func reader(_ bytes: Data) -> (Int) -> Data? {
    var offset = bytes.startIndex
    return { count in
        guard bytes.endIndex - offset >= count else {
            offset = bytes.endIndex
            return nil
        }
        defer { offset += count }
        return Data(bytes[offset..<(offset + count)])
    }
}

func testAFrameRoundTrips() {
    let payload = Data(#"{"id":7,"op":"screen"}"#.utf8)
    let bytes = encodeAgentFrame(AgentFrame.request, payload)
    expectEqual(Array(bytes.prefix(5)), [0x20, 0, 0, 0, UInt8(payload.count)], "header")
    let read = reader(bytes + encodeAgentFrame(AgentFrame.ping, Data()))
    expectEqual(try? readAgentFrame(read), AgentFrameData(type: AgentFrame.request, payload: payload), "first frame")
    expectEqual(try? readAgentFrame(read), AgentFrameData(type: AgentFrame.ping, payload: Data()), "empty payload")
    expectEqual(try? readAgentFrame(read), .some(nil), "end of stream between frames")
}

func testAFrameOverTheLimitIsRefusedBeforeItsPayload() {
    var header = Data([AgentFrame.request])
    withUnsafeBytes(of: UInt32(maxAgentPayload + 1).bigEndian) { header.append(contentsOf: $0) }
    var asked: [Int] = []
    let read: (Int) -> Data? = { count in
        asked.append(count)
        return count == 5 ? header : Data(count: count)
    }
    do {
        _ = try readAgentFrame(read)
        expect(false, "an oversize frame was read")
    } catch {
        expectEqual(error as? AgentFrameError, .tooLarge(maxAgentPayload + 1))
    }
    expectEqual(asked, [5], "only the header is read")
    // Exactly the limit is allowed.
    let limit = encodeAgentFrame(AgentFrame.response, Data(count: maxAgentPayload))
    expectEqual((try? readAgentFrame(reader(limit)))??.payload.count, maxAgentPayload, "a frame at the limit")
}

func testATruncatedFrameThrows() {
    let bytes = encodeAgentFrame(AgentFrame.request, Data("0123456789".utf8)).prefix(9)
    do {
        _ = try readAgentFrame(reader(Data(bytes)))
        expect(false, "a truncated frame was read")
    } catch {
        expectEqual(error as? AgentFrameError, .truncated)
    }
}

func testFrameTypesMatchTheADR() {
    expectEqual(
        [AgentFrame.hello, AgentFrame.request, AgentFrame.response, AgentFrame.blob, AgentFrame.cancel, AgentFrame.event,
         AgentFrame.ping, AgentFrame.pong, AgentFrame.pause, AgentFrame.resume, AgentFrame.stream],
        [0x01, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29])
    expectEqual(maxAgentPayload, 4 * 1024 * 1024, "payload limit")
    expectEqual(maxBlobChunk, 1024 * 1024, "chunk size")
    expectEqual(agentProtocol, 1, "protocol")
}

func testABlobSplitsIntoNumberedChunks() {
    let data = Data((0..<(2 * maxBlobChunk + 10)).map { UInt8(truncatingIfNeeded: $0) })
    let chunks = blobChunks(id: 0x0102_0304, data: data)
    expectEqual(chunks.count, 3, "chunks")
    expectEqual(Array(chunks[0].prefix(7)), [1, 2, 3, 4, 0, 0, 0], "first header")
    var joined = Data()
    for (i, chunk) in chunks.enumerated() {
        guard let parsed = parseBlobChunk(chunk) else {
            expect(false, "chunk \(i) does not parse")
            continue
        }
        expectEqual(parsed.id, 0x0102_0304, "id")
        expectEqual(Int(parsed.seq), i, "seq")
        expectEqual(parsed.last, i == 2, "last on chunk \(i)")
        expect(parsed.bytes.count <= maxBlobChunk, "chunk \(i) is \(parsed.bytes.count) bytes")
        joined.append(parsed.bytes)
    }
    expectEqual(joined, data, "reassembled")
}

func testABlobOfExactlyOneChunkAndAnEmptyBlob() {
    let one = blobChunks(id: 9, data: Data(count: maxBlobChunk))
    expectEqual(one.count, 1, "a full chunk is one frame")
    expectEqual(parseBlobChunk(one[0])?.last, true, "and the last")
    let empty = blobChunks(id: 9, data: Data())
    expectEqual(empty.count, 1, "an empty blob still ends")
    expectEqual(parseBlobChunk(empty[0])?.bytes, Data(), "with no bytes")
    expectEqual(parseBlobChunk(empty[0])?.last, true, "marked last")
    expect(parseBlobChunk(Data([1, 2, 3])) == nil, "a short payload is not a chunk")
}
