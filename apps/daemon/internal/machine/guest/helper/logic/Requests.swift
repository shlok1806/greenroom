// Decoding the agent's requests and naming its errors (daemon ADR 0005 points 4 to 6). Pure, so
// the host tests pin what a malformed request answers.

import Foundation

/// The longest a request may run, and what one without a usable `deadlineMs` gets (the MCP cap
/// is 50 s, root ADR 0015).
let maxDeadlineMs = 45_000

/// How long after its deadline the dispatcher answers `deadline` for a handler that has not
/// answered. Under the daemon's own 2 s, so the agent's answer arrives first.
let deadlineGraceMs = 1_000

/// The error codes of the wire, and whether retrying the same request can work.
let agentErrorCodes: [String: Bool] = [
    "bad_request": false, "unknown_op": false, "stale_ref": false, "not_found": false,
    "ambiguous": false, "refused": false, "paused": false, "cancelled": false,
    "deadline": true, "not_trusted": false, "not_responding": true, "ax_error": false,
    "capture_failed": true, "internal": false,
]

func retryable(_ code: String) -> Bool {
    agentErrorCodes[code] ?? false
}

/// A request's `deadlineMs`, bounded: missing, zero or negative is the cap, and nothing is
/// longer than the cap. The daemon always sends one; the bound only keeps a bad one from making
/// the agent wait forever or refuse outright.
func clampDeadlineMs(_ ms: Int?) -> Int {
    guard let ms, ms > 0 else { return maxDeadlineMs }
    return min(ms, maxDeadlineMs)
}

/// One REQUEST, with its `args` kept as JSON for the op to decode into its own type.
struct RequestEnvelope {
    let id: Int
    let op: String
    let args: Data
    let deadlineMs: Int
    let reader: String
    let input: Bool
}

enum EnvelopeError: Error, Equatable {
    /// Not a JSON object, or no integer `id`: there is no id to answer, so the request is
    /// dropped (and logged once by the caller).
    case unanswerable(String)
    /// A request with an id that is wrong otherwise; it is answered `bad_request`.
    case bad(id: Int, message: String)
}

func parseRequest(_ payload: Data) throws -> RequestEnvelope {
    guard let object = try? JSONSerialization.jsonObject(with: payload),
          let body = object as? [String: Any]
    else { throw EnvelopeError.unanswerable("a REQUEST that is not a JSON object") }
    guard let number = body["id"] as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
          let id = Int(exactly: number.doubleValue)
    else { throw EnvelopeError.unanswerable("a REQUEST without an integer id") }
    guard let op = body["op"] as? String, !op.isEmpty else {
        throw EnvelopeError.bad(id: id, message: "the request names no op")
    }
    var args = Data("{}".utf8)
    switch body["args"] {
    case nil, is NSNull:
        break
    case let value? where value is [String: Any]:
        guard JSONSerialization.isValidJSONObject(value),
              let data = try? JSONSerialization.data(withJSONObject: value)
        else { throw EnvelopeError.bad(id: id, message: "args is not valid JSON") }
        args = data
    default:
        throw EnvelopeError.bad(id: id, message: "args must be a JSON object")
    }
    let deadline = (body["deadlineMs"] as? NSNumber).map { Int($0.doubleValue.rounded()) }
    return RequestEnvelope(
        id: id, op: op, args: args,
        deadlineMs: clampDeadlineMs(deadline),
        reader: body["reader"] as? String ?? "",
        input: (body["input"] as? NSNumber)?.boolValue ?? false
    )
}

/// What went wrong decoding an op's arguments, in words a model can fix the call from:
/// the field's path and what it should have been.
func describeDecodingError(_ error: Error) -> String {
    func path(_ keys: [CodingKey]) -> String {
        let parts = keys.map { key -> String in
            if let i = key.intValue { return "[\(i)]" }
            return "." + key.stringValue
        }
        return "args" + parts.joined()
    }
    guard let error = error as? DecodingError else { return "\(error)" }
    switch error {
    case let .typeMismatch(type, context):
        return "\(path(context.codingPath)) should be \(typeName(type))"
    case let .valueNotFound(type, context):
        return "\(path(context.codingPath)) should be \(typeName(type)), not null"
    case let .keyNotFound(key, context):
        return "\(path(context.codingPath + [key])) is required"
    case let .dataCorrupted(context):
        return "\(path(context.codingPath)): \(context.debugDescription)"
    @unknown default:
        return "\(error)"
    }
}

private func typeName(_ type: Any.Type) -> String {
    switch type {
    case is String.Type: return "a string"
    case is Bool.Type: return "true or false"
    case is Int.Type, is Double.Type, is Float.Type, is CGFloat.Type: return "a number"
    default:
        let name = "\(type)"
        if name.hasPrefix("Array") { return "a list" }
        if name.hasPrefix("Dictionary") { return "an object" }
        return "an object"
    }
}
