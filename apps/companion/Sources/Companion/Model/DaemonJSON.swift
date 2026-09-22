import Foundation

/// RFC3339. Go writes fractional seconds only when it has them, so both parse.
enum DaemonDate {
    static let withFractionalSeconds = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    static let plain = Date.ISO8601FormatStyle()

    static func parse(_ text: String) -> Date? {
        (try? withFractionalSeconds.parse(text)) ?? (try? plain.parse(text))
    }
}

extension JSONDecoder {
    /// The one decoder for daemon JSON.
    static func daemon() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let text = try container.decode(String.self)
            guard let date = DaemonDate.parse(text) else {
                throw DecodingError.dataCorruptedError(in: container, debugDescription: "not an RFC3339 time: \(text)")
            }
            return date
        }
        return decoder
    }
}

extension JSONEncoder {
    static func daemon() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(date.formatted(DaemonDate.withFractionalSeconds))
        }
        return encoder
    }
}

extension KeyedDecodingContainer {
    /// Wire types decode leniently: a missing or `null` field takes a default.
    func decode<T: Decodable>(_ key: Key, or fallback: T) throws -> T {
        try decodeIfPresent(T.self, forKey: key) ?? fallback
    }
}

extension Date {
    /// The fallback for a missing time.
    static let epoch = Date(timeIntervalSince1970: 0)
}

/// A step's tool input and output, kept as the tree that arrived.
indirect enum JSONValue: Codable, Hashable, Sendable {
    case null
    case bool(Bool)
    case int(Int)
    case double(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "not JSON")
        }
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null: try container.encodeNil()
        case .bool(let value): try container.encode(value)
        case .int(let value): try container.encode(value)
        case .double(let value): try container.encode(value)
        case .string(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        }
    }

    subscript(key: String) -> JSONValue? {
        if case .object(let fields) = self { return fields[key] }
        return nil
    }

    var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }

    var prettyPrinted: String { encoded([.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]) ?? String(describing: self) }

    var compact: String { encoded([.sortedKeys, .withoutEscapingSlashes]) ?? "" }

    private func encoded(_ formatting: JSONEncoder.OutputFormatting) -> String? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = formatting
        return (try? encoder.encode(self)).flatMap { String(data: $0, encoding: .utf8) }
    }
}
