import Foundation

/// One line that says what a step actually did.
///
/// A run's evidence is mostly `machine_input` and `machine_exec`, and the tool
/// name alone does not tell them apart: 136 rows reading `machine_input` are a
/// log, not a timeline. This turns a step's own input into the shortest true
/// sentence about it, so a reviewer can read the list instead of expanding it.
///
/// It is pure and it never interprets: it reads the keys the daemon's tools
/// already write and falls back to compact JSON for anything it has not met.
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
            return oneLine(compact(input))
        }
        return ""
    }

    /// What a batch of mouse and keyboard actions did, in the grain a person
    /// reads: one action is spelled out, several are counted after the first.
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

    /// Where on the guest's screen an action landed. The app speaks fractions
    /// (ADR 0009), so the evidence reads in percentages rather than in pixels
    /// it never knew.
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

    /// Newlines and runs of spaces become single spaces: a row is one line
    /// high, and a command that wraps in the shell must not make it taller.
    static func oneLine(_ text: String) -> String {
        text
            .split(whereSeparator: \.isWhitespace)
            .joined(separator: " ")
    }

    private static func compact(_ value: JSONValue) -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(value), let text = String(data: data, encoding: .utf8) else {
            return ""
        }
        return text
    }
}
