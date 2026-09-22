import Foundation

/// One frame off `/api/events`.
enum ServerEvent: Hashable, Sendable {
    case run(LifecycleEvent)
    /// The daemon sends the step's number; `step` is set only if a whole record arrives.
    case step(runId: String, seq: Int, step: Step?)
    case message(runId: String, message: Message)
    case frame(runId: String, frame: Frame)
}

struct LifecycleEvent: Codable, Hashable, Sendable {
    var kind: LifecycleKind
    var runId: String
    var machine: Machine?
}

extension LifecycleEvent {
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decode(.kind, or: .unknown(""))
        runId = try c.decode(.runId, or: "")
        machine = try c.decodeIfPresent(Machine.self, forKey: .machine)
    }
}

/// Cuts a byte stream into lines, keeping the empty ones.
///
/// Not `URLSession.AsyncBytes.lines`: it drops blank lines, and in SSE the
/// blank line is what ends a frame, so no event would ever dispatch.
struct SSELineSplitter {
    private var buffer: [UInt8] = []

    /// Returns a line when `byte` closes one. A trailing `\r` is left for `SSEParser`.
    mutating func consume(_ byte: UInt8) -> String? {
        guard byte == 0x0A else {
            buffer.append(byte)
            return nil
        }
        return takeLine()
    }

    /// Whatever is left when the stream ends without a final newline.
    mutating func flush() -> String? {
        buffer.isEmpty ? nil : takeLine()
    }

    private mutating func takeLine() -> String {
        defer { buffer.removeAll(keepingCapacity: true) }
        return String(decoding: buffer, as: UTF8.self)
    }
}

/// A line-at-a-time server-sent events parser.
struct SSEParser {
    private var eventName: String?
    private var data: [String] = []

    /// Returns an event when `rawLine` closed a frame.
    mutating func consume(_ rawLine: String) throws -> ServerEvent? {
        let line = rawLine.hasSuffix("\r") ? String(rawLine.dropLast()) : rawLine
        if line.isEmpty { return try dispatch() }
        if line.hasPrefix(":") { return nil }

        let field: Substring
        var value: Substring = ""
        if let colon = line.firstIndex(of: ":") {
            field = line[..<colon]
            value = line[line.index(after: colon)...]
            if value.hasPrefix(" ") { value = value.dropFirst() }
        } else {
            field = line[...]
        }

        switch field {
        case "event": eventName = String(value)
        case "data": data.append(String(value))
        default: break
        }
        return nil
    }

    private mutating func dispatch() throws -> ServerEvent? {
        defer {
            eventName = nil
            data = []
        }
        guard !data.isEmpty else { return nil }
        let payload = Data(data.joined(separator: "\n").utf8)
        let decoder = JSONDecoder.daemon()
        do {
            switch eventName {
            case "run":
                return .run(try decoder.decode(LifecycleEvent.self, from: payload))
            case "step":
                let event = try decoder.decode(StepEvent.self, from: payload)
                return .step(runId: event.runId, seq: event.seq, step: event.step)
            case "message":
                let event = try decoder.decode(MessageEvent.self, from: payload)
                return .message(runId: event.runId, message: event.message)
            case "frame":
                // The daemon flattens the frame's fields into the envelope.
                let runId = try decoder.decode(FrameEvent.self, from: payload).runId
                return .frame(runId: runId, frame: try decoder.decode(Frame.self, from: payload))
            default:
                return nil
            }
        } catch {
            throw DaemonError.badResponse("bad \(eventName ?? "unnamed") event: \(error)")
        }
    }
}

/// `step` is a number on the wire; a whole `Step` is accepted too.
private struct StepEvent: Decodable {
    var runId: String
    var seq: Int
    var step: Step?

    private enum CodingKeys: String, CodingKey {
        case runId, step
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runId = try c.decode(.runId, or: "")
        if let number = try? c.decode(Int.self, forKey: .step) {
            seq = number
        } else {
            let record = try c.decode(Step.self, forKey: .step)
            seq = record.seq
            step = record
        }
    }
}

private struct MessageEvent: Decodable {
    var runId: String
    var message: Message
}

private struct FrameEvent: Decodable {
    var runId: String

    private enum CodingKeys: String, CodingKey {
        case runId
    }

    init(from decoder: any Decoder) throws {
        runId = try decoder.container(keyedBy: CodingKeys.self).decode(.runId, or: "")
    }
}
