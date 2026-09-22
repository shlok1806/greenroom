import Foundation

/// One line saying what a step did, read from the input keys the daemon's
/// tools write, falling back to compact JSON.
enum StepSummary {
    static func line(for step: Step) -> String {
        if let actions = step.input?["actions"], case .array(let list) = actions {
            return inputLine(list)
        }
        for key in ["command", "text", "dest", "path", "image", "name", "url"] {
            if let value = step.input?[key]?.stringValue, !value.isEmpty {
                return oneLine(value)
            }
        }
        if let name = step.screenshotArtifact { return name }
        if let input = step.input, case .object(let fields) = input, !fields.isEmpty {
            return oneLine(input.compact)
        }
        return ""
    }

    /// The first action spelled out, the rest counted.
    private static func inputLine(_ actions: [JSONValue]) -> String {
        guard let first = actions.first else { return "no actions" }
        let head = actionLine(first)
        guard actions.count > 1 else { return head }
        return "\(head) +\(actions.count - 1) more"
    }

    private static func actionLine(_ action: JSONValue) -> String {
        let type = action["type"]?.stringValue ?? "action"
        switch type {
        case "type":
            let text = action["text"]?.stringValue ?? ""
            return "type \(quoted(text))"
        case "key":
            let mods = modifiers(action["mods"])
            let key = action["key"]?.stringValue ?? "?"
            return "key \(mods)\(key)"
        case "scroll":
            return "scroll"
        case "sleep":
            return "sleep"
        default:
            guard let at = place(action) else { return type }
            return "\(type) \(at)"
        }
    }

    private static func modifiers(_ value: JSONValue?) -> String {
        guard case .array(let list)? = value else { return "" }
        let names = list.compactMap(\.stringValue)
        return names.isEmpty ? "" : names.joined(separator: "-") + "-"
    }

    /// Percentages, because the app only knows fractions (ADR 0009).
    private static func place(_ action: JSONValue) -> String? {
        guard let x = number(action["x"]), let y = number(action["y"]) else { return nil }
        return String(format: "%.0f%%, %.0f%%", x * 100, y * 100)
    }

    private static func number(_ value: JSONValue?) -> Double? {
        switch value {
        case .double(let value): return value
        case .int(let value): return Double(value)
        default: return nil
        }
    }

    private static func quoted(_ text: String) -> String {
        let flat = oneLine(text)
        let clipped = flat.count > 32 ? flat.prefix(32) + "…" : flat[...]
        return "\"\(clipped)\""
    }

    /// Collapses all whitespace runs to single spaces.
    static func oneLine(_ text: String) -> String {
        text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }
}
